# -*- coding: utf-8 -*-
"""Timeline probe: mode-1 apply vs stat settlement vs panel writers.

flag13 reached the wire (block[13]=1 confirmed in entry_addition_sent) but the
settlement entry 0x143f03330 still saw flag=0. Hypothesis: the client settles
the actor BEFORE it applies our USERINFO mode-1 row, so the flag bits set by
the apply consumers (0x143f0b990 for block[12], 0x143f0b930 for block[13])
arrive too late — and nothing re-settles, so the panel writers (vtable[45]
wrappers 0x142eb4ba0/0x142ebb1f0) never run.

This probe logs a strict timeline of:
  PARSE   mode-1 row parser entry       0x1438068a0
  F12/F13 block flag consumers          0x143f0b990 / 0x143f0b930
  SETTLE  stat settlement entry         0x143f03330 (rcx=actor, rdx=flag)
  WRAP1/2 panel writer wrappers         0x142eb4ba0 / 0x142ebb1f0
  WRITER  panel slot writers            0x142eb48d0 / 0x142ebaf70
plus the stat ctor. Sequence numbers make the order unambiguous.

Usage: python tools/probe_apply_order.py [seconds]
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
LOG = ROOT / 'runtime' / 'apply_order.log'

RUN = int(sys.argv[1], 0) if len(sys.argv) > 1 else 170
ENTERED = [False]
DEADLINE = [0.0]

JS = r'''
var base = Process.getModuleByName('DFO.exe').base;
var seq = 0;
function t(tag, extra) { seq++; send('[' + seq + '] ' + tag + (extra ? ' ' + extra : '')); }

Interceptor.attach(base.add(0x38068a0), {
    onEnter: function (args) { t('PARSE mode1 row', 'a0=' + args[0] + ' a1=' + args[1]); }
});
Interceptor.attach(base.add(0x3f0b990), {
    onEnter: function (args) { t('F12 consumer', 'a0=' + args[0] + ' val=' + args[1].toInt32()); }
});
Interceptor.attach(base.add(0x3f0b930), {
    onEnter: function (args) { t('F13 consumer', 'a0=' + args[0] + ' val=' + args[1].toInt32()); }
});
Interceptor.attach(base.add(0x3f03330), {
    onEnter: function (args) { t('SETTLE', 'actor=' + args[0] + ' flag=' + args[1].toInt32()); }
});
[0x2eb4ba0, 0x2ebb1f0].forEach(function (off) {
    Interceptor.attach(base.add(off), {
        onEnter: function (args) {
            t('WRAP ' + off.toString(16), 'obj=' + args[0] +
              ' caller=' + this.returnAddress.sub(base).toString(16));
        }
    });
});
[0x2eb48d0, 0x2ebaf70].forEach(function (off) {
    Interceptor.attach(base.add(off), {
        onEnter: function (args) { t('WRITER ' + off.toString(16), 'obj=' + args[0]); }
    });
});
Interceptor.attach(base.add(0x3be60f0), {
    onEnter: function (args) { t('CTOR statobj=' + args[0]); }
});
send('timeline hooks installed');
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
    while not ENTERED[0] and time.time() < DEADLINE[0]:
        if post_key(0x0D):
            print('ENTER posted at %s' % time.strftime('%H:%M:%S'), flush=True)
        for _ in range(12):
            if ENTERED[0]:
                break
            time.sleep(1)
    if ENTERED[0]:
        time.sleep(6)
        post_key(0x41)
        print('A posted (panel open)', flush=True)


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
            line = message.get('payload')
            if 'SETTLE' in line or 'PARSE' in line:
                ENTERED[0] = True
            handle.write('%s\n' % line)
            handle.flush()
        elif message.get('type') == 'error':
            handle.write('ERROR %s\n' % message.get('description'))
            handle.flush()

    script.on('message', on_message)
    script.load()
    device.resume(pid)
    print('spawned pid=%d, capturing %ds to %s' % (pid, RUN, LOG), flush=True)
    DEADLINE[0] = time.time() + RUN - 15
    threading.Thread(target=key_thread, daemon=True).start()
    time.sleep(RUN)
    handle.close()
    print('lines captured: %d' % count[0], flush=True)
    return 0


if __name__ == '__main__':
    sys.exit(main())
