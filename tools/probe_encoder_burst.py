# -*- coding: utf-8 -*-
"""Name the US100 expansion function by its write pattern, live.

The 115 client expands the packed 91-byte record into 40 secure slots (8-byte
stride) with one encoder call per field. The US100 counterpart was never
found statically because a loop writer shows only one or two call sites in the
encode census. This probe hooks the encoder itself (0x144f1f570, value in rcx,
field ptr in rdx) plus the stat object's constructor (0x143be60f0, this=rcx)
and the settlement entry (0x143f03330), then watches a real character entry:
every encoder call whose field pointer lands inside a constructed stat object
is logged as object-relative offset + caller RVA. The expansion shows up as a
burst of ~40 calls from ONE caller covering offsets ~0x1094..0x11cc with
stride 8 — and if no burst appears, that proves the client never expands
anything on this path and the server must write the slots through another
channel.

Spawn-based (hooks must exist before the client's own init), server must be
up without a client. ENTER is posted at t+40s and t+52s to walk the spawned
client from character select into the game.

Usage: python tools/probe_encoder_burst.py [seconds]
"""
import ctypes
import json
import pathlib
import sys
import threading
import time
from ctypes import wintypes

ROOT = pathlib.Path(__file__).resolve().parent.parent
READY = ROOT / 'runtime' / 'interactive' / 'ready.json'
import pathlib
sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import default_exe
CLIENT_EXE = default_exe()
CLIENT_CWD = r'D:\DNF\client\US100'
LOG = ROOT / 'runtime' / 'encoder_burst.log'

RUN = int(sys.argv[1], 0) if len(sys.argv) > 1 else 150
MAX_LINES = 20000

JS = r'''
var base = Process.getModuleByName('DFO.exe').base;
var maxLines = %d;
var lines = 0;
var objects = [];   // [{lo, hi, tag}]

function log(s) {
    if (lines < maxLines) { lines++; send(s); }
}

// stat object constructor: this = rcx
Interceptor.attach(base.add(0x3be60f0), {
    onEnter: function (args) {
        var lo = args[0];
        objects.push({lo: lo, hi: lo.add(0x23a8), tag: 'ctor'});
        log('CTOR statobj=' + lo);
    }
});

// settlement entry: rcx=actor, rdx=flag
Interceptor.attach(base.add(0x3f03330), {
    onEnter: function (args) {
        log('SETTLE actor=' + args[0] + ' flag=' + args[1].toInt32());
    }
});

// attach helper on the settlement chain
Interceptor.attach(base.add(0x3f0c440), {
    onEnter: function (args) {
        log('ATTACH a0=' + args[0] + ' a1=' + args[1]);
    }
});

// encode writer: rcx=value_ptr, rdx=field_ptr
Interceptor.attach(base.add(0x4f1f570), {
    onEnter: function (args) {
        var f = args[1];
        for (var i = 0; i < objects.length; i++) {
            if (f.compare(objects[i].lo) >= 0 && f.compare(objects[i].hi) < 0) {
                var off = f.sub(objects[i].lo).toInt32() >>> 0;
                var val = -1;
                try { val = args[0].readU32(); } catch (e) {}
                var caller = this.returnAddress.sub(base).toInt32() >>> 0;
                log('ENC obj=' + objects[i].lo + ' off=' + off.toString(16) +
                    ' val=' + val + ' caller=' + caller.toString(16));
                return;
            }
        }
    }
});
send('hooks installed');
''' % MAX_LINES


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
    for delay in (40, 52):
        time.sleep(delay if delay == 40 else 12)
        for attempt in range(6):
            if post_enter():
                print('ENTER posted at t+%ds' % delay, flush=True)
                break
            time.sleep(2)


def main():
    import frida
    state = json.loads(READY.read_text(encoding='utf-8'))
    host, port = state['address'].split(':')
    payload = '13?%s?%s?probe?00000000000000000000000000000000?0?0?30?0?0?0' % (host, port)
    device = frida.get_local_device()
    print('spawning client (payload %s)' % payload, flush=True)
    pid = device.spawn([CLIENT_EXE, payload], cwd=CLIENT_CWD)
    session = device.attach(pid)
    script = session.create_script(JS)
    handle = open(LOG, 'w', encoding='utf-8')
    count = [0]

    def on_message(message, data):
        if message.get('type') == 'send':
            count[0] += 1
            handle.write('%s\n' % message.get('payload'))
            handle.flush()
        elif message.get('type') == 'error':
            handle.write('ERROR %s\n' % message.get('description'))
            handle.flush()

    script.on('message', on_message)
    script.load()
    device.resume(pid)
    print('spawned pid=%d, capturing %ds to %s' % (pid, RUN, LOG), flush=True)
    t = threading.Thread(target=key_thread, daemon=True)
    t.start()
    time.sleep(RUN)
    try:
        script.unload()
    except Exception:
        pass
    try:
        session.detach()
    except Exception:
        pass
    handle.close()
    print('lines captured: %d' % count[0], flush=True)
    print('client pid %d left running' % pid, flush=True)
    return 0


if __name__ == '__main__':
    sys.exit(main())
