# -*- coding: utf-8 -*-
"""One-shot attach: identify the memory range that holds the panel's slot
object (sample pointers from runtime/decode_callers.log), then leave.

Exits WITHOUT detaching so the client survives.

Usage: python tools/probe_panel_object.py [ptr_hex ...]
"""
import pathlib
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent

PTRS = [int(a, 0) for a in sys.argv[1:]] or [0xdf513424, 0xdf51342c, 0xdf513434, 0xdf51344c, 0xdf513130]

JS = r'''
var ptrs = %s;
ptrs.forEach(function (p) {
    var a = ptr(p);
    var r = Process.findRangeByAddress(a);
    if (r) {
        send('ptr=' + a + ' range base=' + r.base + ' size=' + r.size +
             ' prot=' + r.protection + ' file=' + (r.file ? r.file.path : 'heap/anon'));
    } else {
        send('ptr=' + a + ' no range');
    }
});
// Dump 0x60 bytes around the first slot pointer and 0x40 before it, to see
// whether neighbours look like encoded slots (guard pairs).
var first = ptr(ptrs[0]);
try {
    var before = first.sub(0x40).readByteArray(0x40);
    var at = first.readByteArray(0x60);
    send('BEFORE', before);
    send('AT', at);
} catch (e) { send('dump failed: ' + e); }
send('done');
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
    print('attaching to pid %d' % pid, flush=True)
    session = device.attach(pid)
    script = session.create_script(JS % PTRS)
    done = [False]

    def on_message(message, data):
        if message.get('type') == 'send':
            p = message.get('payload')
            if data:
                hexs = data.hex()
                print('%s %s' % (p, hexs[:320]), flush=True)
            else:
                print(p, flush=True)
            if p == 'done':
                done[0] = True
        elif message.get('type') == 'error':
            print('ERROR %s' % message.get('description'), flush=True)
            done[0] = True

    script.on('message', on_message)
    script.load()
    for _ in range(50):
        if done[0]:
            break
        time.sleep(0.1)
    print('leaving session attached (client stays alive)', flush=True)
    return 0


if __name__ == '__main__':
    sys.exit(main())
