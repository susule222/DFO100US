# -*- coding: utf-8 -*-
"""Which stat slots does the US100 panel actually READ, and from what code?

The entry capture (runtime/encoder_burst.log) proved the stat object's slot
region only ever receives defaults (40 / -1 / 100 / 1.0f) — no expansion of a
server record like 115 has. So the panel's source fields must be identified
from the read side: hook the secure-field decoder 0x144f1f2a0 ((x^0x1f2a025c)-4)
and record DISTINCT (caller RVA, object offset) pairs only — the per-frame
refresh calls it thousands of times, but the distinct set is small and names
every field the UI reads, with the code that reads it.

Attaches to the already-running client (the game window is in town). Stat
objects are re-discovered here by treating any decode target inside the heap
object captured from the constructor hook — since attach cannot replay the
ctor, the object list is seeded from the last CTOR line of encoder_burst.log
(the entry actor's stat object stays alive while in game).

Usage: python tools/probe_panel_reads.py [seconds] [statobj_hex]
"""
import json
import pathlib
import re
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
LOG = ROOT / 'runtime' / 'panel_reads.log'
BURST = ROOT / 'runtime' / 'encoder_burst.log'

RUN = int(sys.argv[1], 0) if len(sys.argv) > 1 else 45

JS = r'''
var base = Process.getModuleByName('DFO.exe').base;
function rva(a) { return a.sub(base).toInt32() >>> 0; }
var objs = %s;   // [[loStr, hiStr]] as hex strings
var seen = {};
var count = 0;
Interceptor.attach(base.add(0x4f1f2a0), {
    onEnter: function (args) {
        count++;
        var p = args[0];
        for (var i = 0; i < objs.length; i++) {
            var lo = ptr(objs[i][0]), hi = ptr(objs[i][1]);
            if (p.compare(lo) >= 0 && p.compare(hi) < 0) {
                var off = p.sub(lo).toInt32() >>> 0;
                var caller = rva(this.returnAddress);
                var key = caller + ':' + off;
                if (!seen[key]) {
                    seen[key] = true;
                    var raw = -1, dec = -1;
                    try { raw = p.readU32(); dec = ((raw ^ 0x1f2a025c) - 4) | 0; } catch (e) {}
                    send('DEC off=' + off.toString(16) + ' caller=' + caller.toString(16) +
                         ' raw=' + raw + ' decoded=' + dec);
                }
                return;
            }
        }
    }
});
setInterval(function () { send('HEARTBEAT decodes=' + count + ' distinct=' + Object.keys(seen).length); }, 5000);
send('decode hook installed on ' + objs.length + ' objects');
'''


def main():
    import frida
    # Seed the object list: every CTOR object from the burst log, each 0x23a8 long.
    objs = []
    if len(sys.argv) > 2:
        lo = int(sys.argv[2], 0)
        objs = [[hex(lo), hex(lo + 0x23a8)]]
    else:
        for line in BURST.read_text(encoding='utf-8').splitlines():
            m = re.match(r'CTOR statobj=(\S+)', line)
            if m:
                lo = int(m.group(1), 16)
                objs.append([hex(lo), hex(lo + 0x23a8)])
    if not objs:
        raise SystemExit('no stat objects found; pass one explicitly')
    print('watching %d stat objects' % len(objs), flush=True)

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
    script = session.create_script(JS % json.dumps(objs))
    handle = open(LOG, 'w', encoding='utf-8')

    def on_message(message, data):
        if message.get('type') == 'send':
            handle.write('%s\n' % message.get('payload'))
            handle.flush()
            print(message.get('payload'), flush=True)
        elif message.get('type') == 'error':
            handle.write('ERROR %s\n' % message.get('description'))
            handle.flush()
            print('ERROR %s' % message.get('description'), flush=True)

    script.on('message', on_message)
    script.load()
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
    print('done; log at %s' % LOG, flush=True)
    return 0


if __name__ == '__main__':
    sys.exit(main())
