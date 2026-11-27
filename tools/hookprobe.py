# -*- coding: utf-8 -*-
"""Hook arbitrary client addresses for a few seconds and log their arguments.

The dungeon/entry probe carries a fixed hook list and owns the client it spawned,
so stopping it to change that list costs a client restart. This is the lighter
tool for a single question: attach to the client that is already running, hook
the addresses given on the command line, log each call's arguments (and return
values), and detach. Nothing is written to the target and the client keeps
running.

The output is capped: these addresses may sit on hot paths, and an unbounded log
is worse than a short one.

Usage: python tools/hookprobe.py 0x14325b720:mode0-callee [seconds] [--cap 200]
       python tools/hookprobe.py --spawn 0xADDR:label ...      (spawn instead)
"""
import pathlib
import subprocess
import sys
import time

ROOT = pathlib.Path(r'D:\DNF project\service\美服100服务端')

hooks = []
seconds = 30
cap = 200
spawn = '--spawn' in sys.argv
raw = [a for a in sys.argv[1:] if not a.startswith('--')]
i = 0
while i < len(raw):
    a = raw[i]
    if ':' in a:
        addr, label = a.split(':', 1)
        hooks.append((int(addr, 0), label))
        i += 1
        continue
    if a.isdigit():
        seconds = int(a)
    i += 1
for j, a in enumerate(sys.argv):
    if a == '--cap' and j + 1 < len(sys.argv):
        cap = int(sys.argv[j + 1], 0)

if not hooks:
    print(__doc__)
    raise SystemExit(2)

entries = ',\n'.join(
    '    [%d, %r]' % (addr - 0x140000000 if addr >= 0x140000000 else addr, label)
    for addr, label in hooks)
# Optional edx filter: hooks fire only when the second argument equals it. The
# UI event dispatcher takes the event number in edx, so one hook with a filter
# names the caller of a specific event among its six thousand call sites.
edx_filter = 'null'
for j, a in enumerate(sys.argv):
    if a == '--edx' and j + 1 < len(sys.argv):
        edx_filter = sys.argv[j + 1]
JS = r'''
var exe = Process.findModuleByName('DFO.exe');
if (exe === null) { send('DFO.exe not found'); } else {
    var hooks = [
%s
    ];
    var COUNTS = {};
    var SENT = 0;
    var CAP = %d;
    var BT = %s;
    var EDX = %s;
    // Remember each hook's last-seen edx so onLeave can stay quiet for the
    // filtered-out calls while still reporting the return of the ones that
    // matched: with a filter, the return value is usually the point of the hook
    // (for a lookup, it is the found object).
    var EDXLAST = {};
    hooks.forEach(function (pair) {
        try {
            Interceptor.attach(exe.base.add(pair[0]), {
                onEnter: function (args) {
                    var name = pair[1];
                    EDXLAST[name] = args[1].toInt32();
                    if (EDX !== null && EDXLAST[name] !== EDX) { return; }
                    COUNTS[name] = (COUNTS[name] || 0) + 1;
                    if (SENT >= CAP) { return; }
                    SENT = SENT + 1;
                    var line = name + ' #' + COUNTS[name] + ' rcx=' + args[0] + ' rdx=' + args[1]
                         + ' r8=' + args[2] + ' r9=' + args[3];
                    if (BT) {
                        // The caller chain is often the point of the hook: for a
                        // hash-table getter, the frame above names the UI code
                        // that asked for the value. Bounded to 10 DFO frames.
                        try {
                            var bt = Thread.backtrace(this.context, Backtracer.FUZZY);
                            var frames = [];
                            for (var i = 0; i < bt.length && frames.length < 10; i++) {
                                var m = Process.findModuleByAddress(bt[i]);
                                if (!m || m.name.indexOf('DFO') !== 0) { continue; }
                                frames.push('+' + (bt[i].sub(m.base).toInt32() >>> 0).toString(16));
                            }
                            line = line + ' stack=' + frames.join(' ');
                        } catch (err) {
                            line = line + ' stack=failed';
                        }
                    }
                    send(line);
                },
                onLeave: function (ret) {
                    var name = pair[1];
                    if (EDX !== null && EDXLAST[name] !== EDX) { return; }
                    if (SENT >= CAP) { return; }
                    SENT = SENT + 1;
                    send(name + ' LEAVE ret=' + ret);
                }
            });
        } catch (err) {
            send('hook failed for ' + pair[1] + ' at ' + pair[0] + ': ' + err);
        }
    });
    send('adhoc probe installed (' + hooks.length + ' hooks, cap ' + CAP + ')');
}
''' % (entries, cap, 'true' if '--bt' in sys.argv else 'false', edx_filter)

import pathlib
sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import default_exe
CLIENT_EXE = default_exe()
CLIENT_CWD = r'D:\DNF\client\US100'
READY = ROOT / 'runtime' / 'interactive' / 'ready.json'


def main():
    import frida
    device = frida.get_local_device()
    if spawn:
        import json
        state = json.loads(READY.read_text(encoding='utf-8'))
        host, _, port = state['address'].rpartition(':')
        payload = '13?%s?%s?probe?00000000000000000000000000000000?0?0?30?0?0?0' % (host, port)
        pid = device.spawn([CLIENT_EXE, payload], cwd=CLIENT_CWD)
        session = device.attach(pid)
        spawned = True
    else:
        out = subprocess.run(['tasklist', '/FI', 'IMAGENAME eq DFO.exe', '/NH'],
                             capture_output=True, text=True, errors='replace')
        pid = None
        for line in out.stdout.splitlines():
            parts = line.split()
            if len(parts) >= 2 and parts[0].lower() == 'dfo.exe':
                pid = int(parts[1])
                break
        if pid is None:
            raise SystemExit('DFO.exe is not running')
        session = device.attach(pid)
        spawned = False
    log = open(ROOT / 'runtime' / ('adhoc_%s.log' % time.strftime('%H%M%S')), 'w', encoding='utf-8')
    script = session.create_script(JS)

    def on_message(message, data):
        if message.get('type') == 'send':
            line = '%s %s' % (time.strftime('%H:%M:%S'), message.get('payload'))
            log.write(line + '\n')
            log.flush()
            print(line, flush=True)
        elif message.get('type') == 'error':
            print('ERROR %s' % message.get('description'), flush=True)

    script.on('message', on_message)
    script.load()
    if spawned:
        device.resume(pid)
    print('hooked %d addresses on pid=%d for %ds' % (len(hooks), pid, seconds), flush=True)
    try:
        time.sleep(seconds)
    except KeyboardInterrupt:
        pass
    finally:
        try:
            script.unload()
        except Exception:
            pass
        try:
            session.detach()
        except Exception:
            pass
        log.close()


if __name__ == '__main__':
    main()
