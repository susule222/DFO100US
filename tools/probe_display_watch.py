# -*- coding: utf-8 -*-
"""Locate the status-panel display struct and its writer, by value + guard page.

All server-side injection points failed to move the panel display (secure slot
writes, indexed-getter overrides 0..127), so the display must read a plain
struct. Its known stable contents while in town with the panel open:
Physical Atk = 6 and Magical Atk = 6 adjacent u32s, Ind. Atk = 44 nearby,
Strength..Spirit = 0, HP/MP = 0.

Step 1: scan readable heap for u32 pair (6,6) with u32 44 within +64 bytes.
Step 2: arm a PAGE_GUARD on the candidate page; the exception handler logs the
writer's RIP (from the exception context), unprotects so the write completes,
and re-arms a few times. Distinct writer RIPs = the display refresh code.

Reads nothing back into the game; the guard only delays writes by microseconds.

Usage: python tools/probe_display_watch.py [seconds]
"""
import pathlib
import struct
import sys
import threading
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
LOG = ROOT / 'runtime' / 'display_watch.log'
RUN = int(sys.argv[1], 0) if len(sys.argv) > 1 else 25

JS = r'''
var mod = Process.findModuleByName('DFO.exe');
var hits = [];
var writers = {};
var armed = null;
var armCount = 0;

function vpfn() {
    var kb = Process.getModuleByName('kernelbase.dll');
    var addr = kb.getExportByName('VirtualProtect');
    if (!addr && kb.findExportByName) { addr = kb.findExportByName('VirtualProtect'); }
    return new NativeFunction(addr, 'int', ['pointer', 'uint64', 'uint32', 'pointer']);
}

function arm() {
    if (hits.length === 0 || armCount >= 6) { return; }
    var h = null;
    for (var i = 0; i < hits.length; i++) {
        if (hits[i].addr.toString(16).indexOf('e8381') >= 0) { h = hits[i]; break; }
    }
    if (h === null) { h = hits[armCount % hits.length]; }
    var page = h.addr.and(ptr(~4095));
    armed = page;
    armCount++;
    try {
        var oldp = Memory.alloc(4);
        vpfn()(page, 4096, 0x102 /* PAGE_READONLY|PAGE_GUARD */, oldp);
        send('armed guard on ' + page + ' (cand ' + h.addr + ')');
    } catch (e) { send('arm failed: ' + e); }
}

function arm_all() {
    var pages = {};
    var n = 0;
    for (var i = 0; i < hits.length && n < 6; i++) {
        var page = hits[i].addr.and(ptr(~4095)).toString();
        if (pages[page]) { continue; }
        pages[page] = true;
        n++;
        try {
            var oldp = Memory.alloc(4);
            vpfn()(ptr(page), 4096, 0x102, oldp);
            send('armed guard on ' + page);
        } catch (e) { send('arm failed: ' + e); }
    }
    armCount = n;
}

Process.setExceptionHandler(function (details) {
    if (details.type === 'guard-page') {
        var rip = details.context.pc;
        var rva = rip.sub(mod.base).toInt32() >>> 0;
        var op = details.memory ? details.memory.address : null;
        if (!writers[rva]) {
            writers[rva] = 0;
            send('ACCESS rva=' + rva.toString(16) + ' rip=' + rip + ' target=' + op);
        }
        writers[rva]++;
        // Unprotect exactly the trapped page so the access completes; do not
        // re-arm it (one trap per page names the code without trap loops).
        if (op !== null) {
            try {
                var oldp = Memory.alloc(4);
                vpfn()(op.and(ptr(~4095)), 4096, 0x04, oldp);
            } catch (e) {}
        }
        return true;
    }
    return false;
});

// Deferred: a synchronous multi-range scan would block script.load.
setTimeout(function () {
    // A previous run may have left a guard page protected; restore it so the
    // scan can read it again.
    try {
        var oldp = Memory.alloc(4);
        vpfn()(ptr('0xe8381000'), 4096, 0x04, oldp);
    } catch (e) {}
    var ranges = Process.enumerateRanges('rw-').filter(function (r) {
        return r.size >= 64 && r.size <= 16 * 1024 * 1024;
    });
    send('scanning ' + ranges.length + ' rw- ranges');
    var done = 0;
    function step(i) {
        if (i >= ranges.length) {
            send('candidates: ' + hits.length);
            hits.slice(0, 12).forEach(function (h) {
                send('CAND ' + h.addr + ' off44=+' + h.off44 + ' mid=' + JSON.stringify(h.zeros));
            });
            if (hits.length === 0) { send('NO CANDIDATES'); }
            setTimeout(arm_all, 300);
            return;
        }
        var r = ranges[i];
        try {
            Memory.scanSync(r.base, r.size, '06 00 00 00 06 00 00 00').forEach(function (m) {
                try {
                    var u = new Uint32Array(m.address.readByteArray(96));
                    for (var k = 2; k < 16; k++) {
                        if (u[k] === 44) {
                            hits.push({ addr: m.address, off44: k * 4, zeros: [u[2],u[3],u[4],u[5]] });
                            break;
                        }
                    }
                } catch (e) {}
            });
        } catch (e) {}
        done++;
        if (done % 64 === 0) { send('scan progress ' + done + '/' + ranges.length + ' hits=' + hits.length); }
        setTimeout(function () { step(i + 1); }, 0);
    }
    step(0);
}, 100);

setInterval(function () {
    send('HEARTBEAT writers=' + JSON.stringify(writers) + ' arms=' + armCount + ' hits=' + hits.length);
}, 5000);
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
        raise SystemExit('DFO.exe is not running (open the panel first)')
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
            handle.write('ERROR %s\n' % message.get('description'))

    script.on('message', on_message)
    script.load()
    # The panel caches its text at open/close, so the display struct is only
    # touched then. Toggle the panel while the guard page is armed: the
    # refresh's reads (and any writes) trap and name the panel code.
    import ctypes
    from ctypes import wintypes
    user32 = ctypes.WinDLL('user32', use_last_error=True)

    def post_a():
        hwnd = user32.FindWindowW(None, 'Dungeon Fighter Online')
        if hwnd:
            user32.PostMessageW(hwnd, 0x100, 0x41, 0)
            time.sleep(0.08)
            user32.PostMessageW(hwnd, 0x101, 0x41, 0)
            return True
        return False

    def key_schedule():
        time.sleep(24)
        post_a()
        print('A posted (toggle 1)', flush=True)
        time.sleep(3)
        post_a()
        print('A posted (toggle 2)', flush=True)
        time.sleep(3)
        post_a()
        print('A posted (toggle 3)', flush=True)

    threading.Thread(target=key_schedule, daemon=True).start()
    time.sleep(RUN)
    handle.close()
    print('done; log at %s' % LOG, flush=True)
    return 0


if __name__ == '__main__':
    sys.exit(main())
