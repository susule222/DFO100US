# -*- coding: utf-8 -*-
"""Distinct-caller census of the secure-field decoder, unfiltered by object.

The panel-read capture showed the entry stat object is NOT the panel's source
(only 7 offsets ever decoded from it). The panel must read another secure
object; the way to find it is from the code side: hook decoder 0x144f1f2a0 and
record every DISTINCT caller RVA once (first hit carries a sample pointer +
value). Pressing A mid-run opens the status panel, so callers that first
appear after the keypress are the panel's own readers — and their sample
pointer names the object the panel actually reads.

Attaches to the RUNNING client (spawned by probe_panel_spawn.py and left in
town). Exits without detaching so the client survives for the user.

Usage: python tools/probe_decode_callers.py [seconds]
"""
import ctypes
import json
import pathlib
import sys
import threading
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
LOG = ROOT / 'runtime' / 'decode_callers.log'

RUN = int(sys.argv[1], 0) if len(sys.argv) > 1 else 45

JS = r'''
var base = Process.getModuleByName('DFO.exe').base;
var seen = {};
var total = 0;
Interceptor.attach(base.add(0x4f1f2a0), {
    onEnter: function (args) {
        total++;
        var caller = this.returnAddress.sub(base).toInt32() >>> 0;
        if (!seen[caller]) {
            seen[caller] = true;
            var raw = -1;
            try { raw = args[0].readU32(); } catch (e) {}
            var dec = ((raw ^ 0x1f2a025c) - 4) | 0;
            send('CALLER ' + caller.toString(16) + ' sample_ptr=' + args[0] +
                 ' raw=' + raw + ' decoded=' + dec + ' total=' + total);
        }
    }
});
setInterval(function () {
    send('HEARTBEAT total=' + total + ' distinct=' + Object.keys(seen).length);
}, 5000);
send('decoder caller census installed');
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
    # ONE A press at t+10s: open the status panel and leave it open.
    time.sleep(10)
    for _ in range(6):
        if post_key(0x41):
            print('A posted (panel open) at t+10s', flush=True)
            return
        time.sleep(2)
    print('game window not found; no key posted', flush=True)


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
    print('attaching to pid %d' % pid, flush=True)
    session = device.attach(pid)
    script = session.create_script(JS)
    handle = open(LOG, 'w', encoding='utf-8')
    marks = []

    def on_message(message, data):
        if message.get('type') == 'send':
            line = message.get('payload')
            handle.write('%s\n' % line)
            handle.flush()
            if line.startswith('CALLER'):
                marks.append(line)
        elif message.get('type') == 'error':
            handle.write('ERROR %s\n' % message.get('description'))
            handle.flush()

    script.on('message', on_message)
    script.load()
    threading.Thread(target=key_thread, daemon=True).start()
    time.sleep(RUN)
    handle.close()
    # Exit WITHOUT unload/detach: this client build dies when hooks are torn
    # down; the python process ending leaves the session's server side inert.
    print('distinct callers captured: %d; log at %s' % (len(marks), LOG), flush=True)
    return 0


if __name__ == '__main__':
    sys.exit(main())
