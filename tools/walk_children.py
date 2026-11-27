# -*- coding: utf-8 -*-
"""Walk the status panel's child-control tree and find controls by text.

The panel redraws its rows from data every frame, so the row text is inside the
control buffers right now: fame shows 36352 (last injection) while HP shows
"0/0". Walking the child list of window 0xec90cb80 and scanning each control
for the fame value locates the fame row's control; its sibling of the same
class is the HP row, and the class's per-frame update virtual is the data-read
chain we need.

Read-only. Usage: python tools/walk_children.py <window> <value> [depth]
"""
import pathlib
import subprocess
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
args = [a for a in sys.argv[1:] if not a.startswith('--')]
window = int(args[0], 0)
value = int(args[1], 0)
depth = int(args[2], 0) if len(args) > 2 else 3

JS = r'''
var ROOT = ptr(%d);
var VALUE = %d;
var DEPTH = %d;
var seen = {};
function scan(obj, label, level) {
    if (level > DEPTH) { return; }
    var key = obj.toString();
    if (seen[key]) { return; }
    seen[key] = 1;
    var info = '';
    try {
        var vt = obj.readPointer();
        var vm = Process.findModuleByAddress(vt);
        info = 'vt=' + vt + (vm ? (' DFO+0x' + vt.sub(vm.base).toString(16)) : '');
    } catch (err) { info = 'unreadable'; }
    var hits = [];
    try {
        // Scan the control's first 0x600 bytes for the value as u32 and as the
        // decimal digit string (ascii and utf-16), which is what a text
        // control's buffer holds.
        var sz = 0x600;
        var buf = obj.readByteArray(sz);
        var b = new Uint8Array(buf);
        var v = [VALUE & 0xFF, (VALUE >>> 8) & 0xFF, (VALUE >>> 16) & 0xFF, (VALUE >>> 24) & 0xFF];
        var dec = String(VALUE);
        var bytes = [];
        for (var i = 0; i < dec.length; i++) { bytes.push(dec.charCodeAt(i)); }
        for (var off = 0; off + 4 <= sz; off++) {
            if (b[off] === v[0] && b[off+1] === v[1] && b[off+2] === v[2] && b[off+3] === v[3]) {
                hits.push('u32@+' + off.toString(16));
            }
        }
        for (var off = 0; off + bytes.length <= sz; off++) {
            var ok = true;
            for (var k = 0; k < bytes.length; k++) { if (b[off+k] !== bytes[k]) { ok = false; } }
            if (ok) { hits.push('ascii@+' + off.toString(16)); }
        }
    } catch (err) {}
    send(label + ' ' + obj + ' ' + info + ' hits=' + (hits.length ? hits.join(',') : 'none'));
    // Recurse into plausible child pointers at common offsets. The window's
    // child-tree root is at +0xa8 (0x1448dcbd0 reads [window+0xa8]), controls
    // chain through +0x18..+0x40, so the sweep goes to +0x200.
    var offs = [];
    for (var o = 0x18; o <= 0x200; o += 8) { offs.push(o); }
    for (var i = 0; i < offs.length; i++) {
        var p;
        try { p = obj.add(offs[i]).readPointer(); } catch (err) { continue; }
        if (p.isNull()) { continue; }
        // Heap objects live above 0x10000 and below the top of user space
        // (0x7fffffffffff); the earlier 0x40000000 ceiling wrongly rejected
        // this client's 0xEDxxxxxx heap.
        if (p.compare(ptr(0x10000)) < 0 || p.compare(ptr(0x7fff00000000)) > 0) { continue; }
        try {
            var q = p.readPointer();
            var m = Process.findModuleByAddress(q);
            if (!m) { continue; }
            scan(p, label + '/+' + offs[i].toString(16), level + 1);
        } catch (err) {}
    }
}
scan(ROOT, 'window', 0);
'''

import frida  # noqa: E402


def main():
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
    session = frida.get_local_device().attach(pid)
    script = session.create_script(JS % (window, value, depth))

    def on_message(m, d):
        if m.get('type') == 'send':
            print(m.get('payload'), flush=True)
        elif m.get('type') == 'error':
            print('ERROR ' + m.get('description'), flush=True)

    script.on('message', on_message)
    script.load()
    time.sleep(6)
    try:
        script.unload()
    except Exception:
        pass
    try:
        session.detach()
    except Exception:
        pass


if __name__ == '__main__':
    main()
