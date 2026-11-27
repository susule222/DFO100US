# -*- coding: utf-8 -*-
"""Walk the status panel's child controls and find the row that shows a value.

The panel draws its rows every frame from live data, so the current fame value
is rendered somewhere in the control tree — but not necessarily inline: DNF
text controls often keep a pointer to a separately allocated string object.
This walks the tree, scans each control for the value in every representation
(u32/u16, ascii/utf-16, with and without thousands separators), and also
follows each heap pointer inside the control to scan the target object.

Usage: python tools/walk_children2.py <window> <value> [depth]
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
function reps(v) {
    var list = [];
    list.push({name: 'u32', b: [v & 0xFF, (v >>> 8) & 0xFF, (v >>> 16) & 0xFF, (v >>> 24) & 0xFF]});
    list.push({name: 'u16', b: [v & 0xFF, (v >>> 8) & 0xFF]});
    var dec = String(v);
    var withComma = dec.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
    function str(s) {
        var a = []; for (var i = 0; i < s.length; i++) { a.push(s.charCodeAt(i)); }
        return a;
    }
    list.push({name: 'ascii', b: str(dec)});
    list.push({name: 'ascii,', b: str(withComma)});
    var w = []; for (var i = 0; i < dec.length; i++) { w.push(dec.charCodeAt(i)); w.push(0); }
    var wc = []; for (var i = 0; i < withComma.length; i++) { wc.push(withComma.charCodeAt(i)); wc.push(0); }
    list.push({name: 'utf16', b: w});
    list.push({name: 'utf16,', b: wc});
    return list;
}
function scanBuf(buf, base) {
    var b = new Uint8Array(buf);
    var hits = [];
    var patterns = reps(VALUE);
    for (var p = 0; p < patterns.length; p++) {
        var pat = patterns[p].b;
        for (var off = 0; off + pat.length <= b.length; off++) {
            var ok = true;
            for (var k = 0; k < pat.length; k++) { if (b[off + k] !== pat[k]) { ok = false; break; } }
            if (ok) { hits.push(patterns[p].name + '@+' + off.toString(16)); }
        }
    }
    return hits;
}
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
        var sz = 0x800;
        var buf = obj.readByteArray(sz);
        hits = scanBuf(buf, obj);
    } catch (err) {}
    // Follow heap pointers inside the control: text buffers are often separate
    // allocations referenced from +0x1f0-style fields.
    var viaptr = [];
    try {
        var raw = obj.readByteArray(0x200);
        for (var off = 0; off + 8 <= 0x200; off += 8) {
            var p = obj.add(off).readPointer();
            if (p.isNull()) { continue; }
            if (p.compare(ptr(0x10000)) < 0 || p.compare(ptr(0x7fff00000000)) > 0) { continue; }
            try {
                var tbuf = p.readByteArray(0x200);
                var thits = scanBuf(tbuf, p);
                for (var t = 0; t < thits.length; t++) {
                    viaptr.push('+' + off.toString(16) + '->' + p + ':' + thits[t]);
                }
            } catch (err) {}
        }
    } catch (err) {}
    var all = hits.concat(viaptr);
    if (all.length) {
        send(label + ' ' + obj + ' ' + info + ' HITS ' + all.join(','));
    }
    var offs = [];
    for (var o = 0x18; o <= 0x200; o += 8) { offs.push(o); }
    for (var i = 0; i < offs.length; i++) {
        var q;
        try { q = obj.add(offs[i]).readPointer(); } catch (err) { continue; }
        if (q.isNull()) { continue; }
        if (q.compare(ptr(0x10000)) < 0 || q.compare(ptr(0x7fff00000000)) > 0) { continue; }
        try {
            var qv = q.readPointer();
            if (!Process.findModuleByAddress(qv)) { continue; }
            scan(q, label + '/+' + offs[i].toString(16), level + 1);
        } catch (err) {}
    }
}
scan(ROOT, 'window', 0);
send('walk done');
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
    time.sleep(10)
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
