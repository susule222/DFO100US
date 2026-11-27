# -*- coding: utf-8 -*-
"""Force the re-settlement the client never schedules on its own.

Timeline captures proved: the entry settlement (0x143f03330) fires while the
client processes the mode-0 actor row — BEFORE the mode-1 row is applied — and
nothing ever settles again, so the panel writers (vtable[45] wrappers
0x142eb4ba0/0x142ebb1f0 -> slot writers 0x142eb48d0/0x142ebaf70) never run and
the panel slot array (+0x304..+0x334) stays zero: the 0/0 panel.

This probe patches the flow at runtime the way the 115 client behaves
("record arrives -> slots written"): when the mode-1 apply consumer for block
byte 13 (0x143f0b930) returns — the actor's flag bits are now set — it calls
the settlement itself: settle(actor, 1). If the writers then fire and fill the
slots, the same three instructions become a permanent ngs_stub patch and the
panel lights from server data.

Watched alongside: SETTLE entries (flag value), WRAP/WRITER hits, and distinct
indexed-getter results after the force (to see recomputed values).

Usage: python tools/probe_forcesettle.py [seconds]
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
LOG = ROOT / 'runtime' / 'forcesettle.log'

RUN = int(sys.argv[1], 0) if len(sys.argv) > 1 else 170
APPLIED = [False]
DEADLINE = [0.0]

JS = r'''
var base = Process.getModuleByName('DFO.exe').base;
var seq = 0;
function t(tag, extra) { seq++; send('[' + seq + '] ' + tag + (extra ? ' ' + extra : '')); }

var settle = new NativeFunction(base.add(0x3f03330), 'void', ['pointer', 'int']);
var forced = false;
var forcing = false;

Interceptor.attach(base.add(0x3be60f0), {
    onEnter: function (args) { t('CTOR statobj=' + args[0]); }
});
Interceptor.attach(base.add(0x38068a0), {
    onEnter: function (args) { t('PARSE mode1 row', 'a0=' + args[0]); }
});
Interceptor.attach(base.add(0x3f03330), {
    onEnter: function (args) {
        t('SETTLE', 'actor=' + args[0] + ' flag=' + args[1].toInt32() +
          (forcing ? ' (FORCED)' : ''));
    }
});

// mode-1 apply consumer for block byte 13: rcx = actor. After it returns the
// actor carries the applied flag bits — force the settlement right here.
Interceptor.attach(base.add(0x3f0b930), {
    onEnter: function (args) {
        this.actor = args[0];
        t('F13 consumer', 'actor=' + args[0]);
    },
    onLeave: function () {
        if (forced || forcing) { return; }
        forced = true;
        forcing = true;
        t('FORCE settle(actor=' + this.actor + ', flag=1)');
        try {
            settle(this.actor, 1);
            t('FORCE returned');
        } catch (e) {
            t('FORCE failed: ' + e);
        }
        forcing = false;
    }
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

// distinct indexed-getter results (value changes re-log once)
var seen = {};
Interceptor.attach(base.add(0x3ed8e50), {
    onEnter: function (args) { this.idx = args[1].toInt32(); this.caller = this.returnAddress.sub(base).toInt32() >>> 0; },
    onLeave: function (retval) {
        var v = retval.toInt32();
        var k = 'i' + this.idx + ':' + this.caller + ':' + v;
        if (!seen[k] && Object.keys(seen).length < 3000) {
            seen[k] = true;
            t('INT idx=' + this.idx + ' val=' + v + ' caller=' + this.caller.toString(16));
        }
    }
});
[0x3edf9e0, 0x3edafc0].forEach(function (off) {
    Interceptor.attach(base.add(off), {
        onEnter: function (args) { this.idx = args[1].toInt32(); this.caller = this.returnAddress.sub(base).toInt32() >>> 0; },
        onLeave: function () {
            var f = 0;
            try { f = this.context.xmm0.readFloat(); } catch (e) {}
            var fi = Math.round(f * 1000) / 1000;
            var k = 'f' + this.idx + ':' + this.caller + ':' + fi;
            if (!seen[k] && Object.keys(seen).length < 3000) {
                seen[k] = true;
                t('FLT idx=' + this.idx + ' val=' + fi + ' caller=' + this.caller.toString(16));
            }
        }
    });
});
send('forcesettle hooks installed');
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
    # ENTER until the mode-1 apply (F13) says we are in; then A for the panel.
    while not APPLIED[0] and time.time() < DEADLINE[0]:
        if post_key(0x0D):
            print('ENTER posted at %s' % time.strftime('%H:%M:%S'), flush=True)
        for _ in range(12):
            if APPLIED[0]:
                break
            time.sleep(1)
    if APPLIED[0]:
        time.sleep(8)
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
            if 'F13 consumer' in line:
                APPLIED[0] = True
            handle.write('%s\n' % line)
            handle.flush()
        elif message.get('type') == 'error':
            handle.write('ERROR %s\n' % message.get('description'))
            handle.flush()

    script.on('message', on_message)
    script.load()
    device.resume(pid)
    print('spawned pid=%d, capturing %ds to %s' % (pid, RUN, LOG), flush=True)
    DEADLINE[0] = time.time() + RUN - 20
    threading.Thread(target=key_thread, daemon=True).start()
    time.sleep(RUN)
    handle.close()
    print('lines captured: %d' % count[0], flush=True)
    print('client pid %d left running (session NOT detached)' % pid, flush=True)
    return 0


if __name__ == '__main__':
    sys.exit(main())
