# -*- coding: utf-8 -*-
"""Spawn-based capture of the panel READ side (secure-field decoder callers).

The attach variant died with the client (this packed build exits when a frida
session detaches), so this probe owns the whole lifecycle: spawn the client
against the running server, hook the stat-object constructor (0x143be60f0) to
learn every live stat object, hook the decoder (0x144f1f2a0) and log DISTINCT
(caller RVA, object offset, decoded value) triples only. ENTER walks the
client into the game; A opens the status panel so the panel's own reads fire.

The distinct set answers the M7 question from the read side: exactly which
slots the HP orb / status panel read, and which code reads them — the writers
of those slots (or their absence) is what the server must reproduce.

Usage: python tools/probe_panel_spawn.py [seconds]
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
LOG = ROOT / 'runtime' / 'panel_reads.log'

RUN = int(sys.argv[1], 0) if len(sys.argv) > 1 else 130

JS = r'''
var base = Process.getModuleByName('DFO.exe').base;
function rva(a) { return a.sub(base).toInt32() >>> 0; }
var objects = [];
var seen = {};
var decCount = 0;

Interceptor.attach(base.add(0x3be60f0), {
    onEnter: function (args) {
        var lo = args[0];
        objects.push([lo, lo.add(0x23a8)]);
        send('CTOR statobj=' + lo);
    }
});

Interceptor.attach(base.add(0x4f1f2a0), {
    onEnter: function (args) {
        decCount++;
        var p = args[0];
        for (var i = 0; i < objects.length; i++) {
            if (p.compare(objects[i][0]) >= 0 && p.compare(objects[i][1]) < 0) {
                var off = p.sub(objects[i][0]).toInt32() >>> 0;
                var caller = rva(this.returnAddress);
                var raw = -1;
                try { raw = p.readU32(); } catch (e) {}
                var dec = ((raw ^ 0x1f2a025c) - 4) | 0;
                // Distinct per (caller, offset, decoded value): value changes
                // matter (a slot going 0 -> real), but per-frame repeats don't.
                var key = caller + ':' + off + ':' + dec;
                if (!seen[key]) {
                    seen[key] = true;
                    send('DEC off=' + off.toString(16) + ' caller=' + caller.toString(16) +
                         ' raw=' + raw + ' decoded=' + dec + ' obj=' + objects[i][0]);
                }
                return;
            }
        }
    }
});
setInterval(function () {
    send('HEARTBEAT decodes=' + decCount + ' distinct=' + Object.keys(seen).length +
         ' objects=' + objects.length);
}, 10000);
send('hooks installed (ctor + decoder)');
'''


def post_key(vk):
    user32 = ctypes.WinDLL('user32', use_last_error=True)
    hwnd = user32.FindWindowW(None, 'Dungeon Fighter Online')
    if not hwnd:
        return False
    user32.PostMessageW(hwnd, 0x100, vk, 0)
    time.sleep(0.08)
    user32.PostMessageW(hwnd, 0x101, vk, 0)
    return True


def key_thread():
    # ENTER (0x0D) twice to select+enter, then A (0x41) to open the stat panel.
    plan = [(40, 0x0D), (52, 0x0D), (70, 0x41), (76, 0x41)]
    last = 0
    for at, vk in plan:
        time.sleep(max(0, at - last))
        last = at
        for _ in range(6):
            if post_key(vk):
                print('key %#x posted at t+%ds' % (vk, at), flush=True)
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
    threading.Thread(target=key_thread, daemon=True).start()
    time.sleep(RUN)
    # NOTE: unloading/detaching kills this client build; leave the session and
    # just stop the capture. The client stays in game for the user.
    handle.close()
    print('lines captured: %d' % count[0], flush=True)
    print('client pid %d left running (session intentionally NOT detached)' % pid, flush=True)
    return 0


if __name__ == '__main__':
    sys.exit(main())
