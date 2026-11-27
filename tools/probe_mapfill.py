# -*- coding: utf-8 -*-
"""Dump and marker-override the stat source's indexed map (the 115-style feed).

The panel display inlines the map lookup, so function hooks cannot move it;
the data itself can. Node layout (from the getter disassembly):
  left@+0x00  right@+0x10  isnil@+0x19  key@+0x1c  value@+0x20 (secure slot)
  guard@+0x24 ; map object at [src+0x1330] (or [src+0x1428] when [src+0x1430]!=0),
  root at [map+0x8]. Secure pair: slot=(v+4)^0x1F2A025C, guard=v+slot+0xC4.

Phase 1: hook the int getter briefly to learn the live src pointers.
Phase 2: walk each src's tree, log every (key, decoded value).
Phase 3: overwrite keys 0..40 with 3000+key (distinct markers), leave the
client running; a screenshot then maps display fields to keys.

Usage: python tools/probe_mapfill.py [seconds]
"""
import pathlib
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
LOG = ROOT / 'runtime' / 'mapfill.log'
RUN = int(sys.argv[1], 0) if len(sys.argv) > 1 else 30

JS = r'''
var base = Process.getModuleByName('DFO.exe').base;
var KEY = 0x1F2A025C;
var srcs = {};
var hook = Interceptor.attach(base.add(0x3ed8e50), {
    onEnter: function (args) {
        var idx = args[1].toInt32();
        if (idx >= 0 && idx <= 200) { srcs[args[0].toString()] = args[0]; }
    }
});

function decode(p) { return (p.readU32() ^ KEY) - 4; }

function walk(map, node, depth, out) {
    if (depth > 24 || node.isNull()) { return; }
    if (node.add(0x19).readU8() !== 0) { return; }   // nil sentinel
    walk(map, node.readPointer(), depth + 1, out);    // left @+0
    out.push({ node: node, key: node.add(0x1c).readU32(), val: decode(node.add(0x20)) });
    walk(map, node.add(0x10).readPointer(), depth + 1, out);  // right
}

setTimeout(function () {
    hook.detach();
    var list = Object.keys(srcs).map(function (k) { return srcs[k]; });
    send('srcs captured: ' + list.length);
    list.slice(0, 6).forEach(function (src) {
        var off = src.add(0x1430).readU64() !== 0 ? 0x1428 : 0x1330;
        var map = src.add(off).readPointer();
        if (map.isNull()) { send('src ' + src + ': null map'); return; }
        var root = map.add(0x8).readPointer();
        var out = [];
        try { walk(map, root, 0, out); } catch (e) { send('walk failed ' + e); }
        send('src ' + src + ' map+' + off.toString(16) + ' entries=' + out.length);
        out.sort(function (a, b) { return a.key - b.key; });
        out.forEach(function (e) {
            send('  K=' + e.key + ' V=' + e.val + ' node=' + e.node);
        });
        // Phase 3: marker override on the first src with a non-trivial table
        if (out.length > 5) {
            out.forEach(function (e) {
                if (e.key <= 40) {
                    var v = 3000 + e.key;
                    var slot = (v + 4) ^ KEY;
                    e.node.add(0x20).writeU32(slot);
                    e.node.add(0x24).writeU32((v + slot + 0xC4) | 0);
                }
            });
            send('markers 3000+key written on src ' + src);
        }
    });
    send('MAPDUMP DONE');
}, 6000);
'''


def main():
    import frida
    device = frida.get_local_device()
    pid = None
    for proc in device.enumerate_processes():
        if proc.name.lower() == 'dfo.exe':
            pid = proc.pid
            break
    if pid is None:
        raise SystemExit('DFO.exe is not running')
    session = device.attach(pid)
    script = session.create_script(JS)
    handle = open(LOG, 'w', encoding='utf-8')

    def on_message(message, data):
        if message.get('type') == 'send':
            handle.write('%s\n' % message.get('payload'))
            handle.flush()
            print(message.get('payload'), flush=True)
        elif message.get('type') == 'error':
            print('ERROR %s' % message.get('description'), flush=True)

    script.on('message', on_message)
    script.load()

    # Toggle the panel during the capture window so refresh code calls the
    # getter and reveals the live stat-source pointers.
    import ctypes
    import threading
    user32 = ctypes.WinDLL('user32', use_last_error=True)

    def post_a():
        hwnd = user32.FindWindowW(None, 'Dungeon Fighter Online')
        if hwnd:
            user32.PostMessageW(hwnd, 0x100, 0x41, 0)
            time.sleep(0.08)
            user32.PostMessageW(hwnd, 0x101, 0x41, 0)

    def toggles():
        for at in (1.0, 1.5):
            time.sleep(at)
            post_a()

    threading.Thread(target=toggles, daemon=True).start()
    time.sleep(RUN)
    handle.close()
    try:
        session.detach()
    except Exception:
        pass
    print('done; client left running with markers; log at %s' % LOG, flush=True)
    return 0


if __name__ == '__main__':
    sys.exit(main())
