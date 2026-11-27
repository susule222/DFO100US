# -*- coding: utf-8 -*-
"""Derive the character pointer chain: global -> actor -> stat source -> values.

Classic base-plus-offset chain, derived at runtime:
  1. hook the settlement entry 0x143f03330 (rcx = actor) and the int getter
     0x143ed8e50 (rcx = stat source) during a normal entry;
  2. after entry, scan the actor object's first 0x2000 bytes for qwords equal
     to the stat source -> the actor->src offset(s);
  3. scan the module's .data (rw) for qwords equal to the actor -> static
     global roots (file offsets into the unpacked PE);
  4. dump the src's indexed map (root [map+8], node key@+0x1c value@+0x20)
     and the secure slot array src+0x304..+0x350, decoded.

Spawn-based (hooks must exist before entry); ENTER is posted adaptively.

Usage: python tools/probe_chain.py [seconds]
"""
import ctypes
import json
import pathlib
import sys
import threading
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
READY = ROOT / 'runtime' / 'interactive' / 'ready.json'
import pathlib
sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import default_exe
CLIENT_EXE = default_exe()
CLIENT_CWD = r'D:\DNF\client\US100'
LOG = ROOT / 'runtime' / 'chain.log'
RUN = int(sys.argv[1], 0) if len(sys.argv) > 1 else 120
ENTERED = [False]
DEADLINE = [0.0]

JS = r'''
var base = Process.getModuleByName('DFO.exe').base;
var KEY = 0x1F2A025C;
var actor = null;
var src = null;
var settleCount = 0;

Interceptor.attach(base.add(0x3f03330), {
    onEnter: function (args) {
        settleCount++;
        if (actor === null) { actor = args[0]; send('SETTLE actor=' + actor); }
    }
});
Interceptor.attach(base.add(0x3ed8e50), {
    onEnter: function (args) {
        var idx = args[1].toInt32();
        if (src === null && idx >= 0 && idx <= 200) { src = args[0]; send('GETTER src=' + src + ' idx=' + idx); }
    }
});

function decode(p) { return (p.readU32() ^ KEY) - 4; }

function walk(node, depth, out) {
    if (depth > 24 || node.isNull()) { return; }
    if (node.add(0x19).readU8() !== 0) { return; }
    walk(node.readPointer(), depth + 1, out);
    out.push({ key: node.add(0x1c).readU32(), val: decode(node.add(0x20)), node: node });
    walk(node.add(0x10).readPointer(), depth + 1, out);
}

function derive() {
    if (actor === null || src === null) { send('CHAIN ABORT actor=' + actor + ' src=' + src); return; }
    // 1) actor -> src offsets
    var found = [];
    for (var off = 0; off < 0x2000; off += 8) {
        try {
            if (actor.add(off).readPointer().equals(src)) { found.push(off); }
        } catch (e) {}
    }
    send('actor->src offsets: ' + JSON.stringify(found));
    // also one level deeper: fields pointing to objects that point to src
    // 2) static globals pointing at actor (.data rw region)
    var data = Process.findRangeByAddress(base.add(0x9500000));
    var globs = [];
    if (data) {
        var lo = base.add(0x9475000), hi = base.add(0x9475000 + 0x174ac50);
        for (var p = lo; p.compare(hi) < 0; p = p.add(0x1000)) {
            try {
                var chunk = p.readByteArray(0x1000);
                var dv = new DataView(chunk);
                for (var i = 0; i + 8 <= 0x1000; i += 8) {
                    var lo32 = dv.getUint32(i, true), hi32 = dv.getUint32(i + 4, true);
                    if (hi32 === 0x1 || (hi32 & 0xffff) === 0) { continue; }
                    var v = ptr(uint64(hi32, lo32));
                    if (v.equals(actor)) { globs.push(p.add(i).sub(base).toInt32() >>> 0); }
                }
            } catch (e) {}
        }
    }
    send('globals->actor rvas: ' + JSON.stringify(globs.slice(0, 24)));
    // 3) map dump
    var off1430 = src.add(0x1430).readU64();
    var moff = off1430 !== 0 ? 0x1428 : 0x1330;
    var map = src.add(moff).readPointer();
    send('src=' + src + ' mapOff=+' + moff.toString(16) + ' map=' + map);
    if (!map.isNull()) {
        var root = map.add(0x8).readPointer();
        var entries = [];
        try { walk(root, 0, entries); } catch (e) { send('walk err ' + e); }
        entries.sort(function (a, b) { return a.key - b.key; });
        entries.forEach(function (e) { send('K=' + e.key + ' V=' + e.val); });
        send('map entries=' + entries.length);
    }
    // 4) slot array
    var slots = [];
    for (var s = 0x304; s <= 0x354; s += 8) {
        slots.push('+' + s.toString(16) + '=' + decode(src.add(s)));
    }
    send('slots: ' + slots.join(' '));
    send('CHAIN DONE');
}

var chainIv = setInterval(function () {
    if (actor !== null && src !== null) {
        clearInterval(chainIv);
        setTimeout(derive, 4000);   // let entry finish settling first
    }
}, 2000);
'''


def post_enter():
    user32 = ctypes.WinDLL('user32', use_last_error=True)
    hwnd = user32.FindWindowW(None, 'Dungeon Fighter Online')
    if not hwnd:
        return False
    user32.PostMessageW(hwnd, 0x100, 0x0D, 0)
    time.sleep(0.08)
    user32.PostMessageW(hwnd, 0x101, 0x0D, 0)
    return True


def key_thread():
    while not ENTERED[0] and time.time() < DEADLINE[0]:
        post_enter()
        for _ in range(12):
            if ENTERED[0]:
                break
            time.sleep(1)


def main():
    import frida
    state = json.loads(READY.read_text(encoding='utf-8'))
    host, port = state['address'].split(':')
    payload = '13?%s?%s?probe?00000000000000000000000000000000?0?0?30?0?0?0' % (host, port)
    device = frida.get_local_device()
    pid = None
    for proc in device.enumerate_processes():
        if proc.name.lower() == 'dfo.exe':
            pid = proc.pid
            break
    if pid is None:
        raise SystemExit('DFO.exe is not running')
    print('attaching to pid %d' % pid, flush=True)
    session = device.attach(pid)
    script = session.create_script(JS)
    handle = open(LOG, 'w', encoding='utf-8')

    def on_message(message, data):
        if message.get('type') == 'send':
            line = message.get('payload')
            if line.startswith('SETTLE'):
                ENTERED[0] = True
            handle.write('%s\n' % line)
            handle.flush()
            print(line, flush=True)
        elif message.get('type') == 'error':
            print('ERROR %s' % message.get('description'), flush=True)

    script.on('message', on_message)
    script.load()
    DEADLINE[0] = time.time() + RUN - 30
    threading.Thread(target=key_thread, daemon=True).start()
    time.sleep(RUN)
    handle.close()
    print('done; log %s' % LOG, flush=True)
    return 0


if __name__ == '__main__':
    sys.exit(main())
