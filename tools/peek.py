# -*- coding: utf-8 -*-
"""Read or write a few bytes in the running client's memory.

The client cannot be inspected statically (its .text is restored at run time and
its heap is where the interesting state lives), and a hook log only shows the
values that passed through the hooked call. This is the small missing piece:
give it an address and a length and get the bytes, so a value the log reported
can be checked in place afterwards — for example whether a stat object still
holds what its setter was called with.

Writing is the other half of the same question: when a display shows 0 for a
field the server believes it filled, writing a distinctive number into the
candidate location says whether that location is what the display reads. The
writer is deliberate and explicit (--write), never a side effect of reading.

Read-only unless --write is given; the client keeps running either way.

Usage: python tools/peek.py <address> [size] [--write hexbytes]
"""
import pathlib
import subprocess
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
args = sys.argv[1:]
if not args:
    print(__doc__)
    raise SystemExit(2)
addr = int(args[0], 0)
size = int(args[1], 0) if len(args) > 1 and not args[1].startswith('--') else 32
count = 1
write_hex = None
find = None
for i, a in enumerate(args):
    if a == '--count' and i + 1 < len(args):
        count = int(args[i + 1], 0)
    if a == '--write' and i + 1 < len(args):
        write_hex = args[i + 1].replace(' ', '')
    if a == '--find' and i + 1 < len(args):
        find = [int(x, 0) for x in args[i + 1].split(',')]
size = max(1, min(size, 65536))

JS = r'''
var addr = %d;
var size = %d;
var count = %d;
var writeHex = %s;
var find = %s;
function hexOf(bytes, n) {
    var view = new Uint8Array(bytes);
    var out = [];
    for (var k = 0; k < n; k++) { out.push(('0' + view[k].toString(16)).slice(-2)); }
    return out.join(' ');
}
if (writeHex !== null) {
    var raw = [];
    for (var i = 0; i < writeHex.length; i += 2) { raw.push(parseInt(writeHex.substr(i, 2), 16)); }
    try {
        ptr(addr).writeByteArray(raw);
        send('wrote ' + raw.length + ' bytes: ' + hexOf(new Uint8Array(raw), raw.length));
    } catch (err) {
        send('write failed: ' + err);
    }
}
// Scan a region for the numbers a display shows. A panel that reads a structure
// holds that structure's values, so finding the values it displays locates the
// structure — which is what a "the panel shows 0 for everything the server
// fills" dead end needs.
if (find !== null) {
    try {
        var buf = ptr(addr).readByteArray(size);
        var b = new Uint8Array(buf);
        var hits = [];
        for (var off = 0; off + 4 <= size; off += 1) {
            for (var f = 0; f < find.length; f++) {
                var want = find[f];
                var v = b[off] | (b[off + 1] << 8) | (b[off + 2] << 16) | (b[off + 3] << 24);
                if ((v >>> 0) === (want >>> 0)) { hits.push('+' + off.toString(16) + '=u32 ' + want); }
                var sv = (v | 0);
                if (sv === want && want < 0) { hits.push('+' + off.toString(16) + '=i32 ' + want); }
            }
            if (hits.length > 60) { break; }
        }
        var u16hits = [];
        for (var off2 = 0; off2 + 2 <= size; off2 += 2) {
            var w = b[off2] | (b[off2 + 1] << 8);
            for (var f2 = 0; f2 < find.length; f2++) {
                if (w === find[f2] || (w | 0) === find[f2]) {
                    u16hits.push('+' + off2.toString(16) + '=' + find[f2]);
                }
            }
            if (u16hits.length > 60) { break; }
        }
        send('scan size=' + size + ' u32 hits: ' + (hits.length ? hits.join(' ') : 'none'));
        send('scan size=' + size + ' u16 hits: ' + (u16hits.length ? u16hits.join(' ') : 'none'));
    } catch (err) {
        send('scan failed: ' + err);
    }
}
for (var i = 0; i < count; i++) {
    var out;
    try {
        var bytes = ptr(addr).readByteArray(size);
        var view = new Uint8Array(bytes);
        var u32 = [];
        for (var k = 0; k + 4 <= size; k += 4) {
            var v = view[k] | (view[k + 1] << 8) | (view[k + 2] << 16) | (view[k + 3] << 24);
            u32.push((v >>> 0).toString());
        }
        out = 'addr=' + ptr(addr) + ' hex=' + hexOf(bytes, size) + ' u32=' + u32.join(',');
    } catch (err) {
        out = 'addr=' + ptr(addr) + ' read failed: ' + err;
    }
    send(out);
    if (i + 1 < count) { Thread.sleep(0.5); }
}
''' % (addr, size, count, ('"%s"' % write_hex) if write_hex else 'null',
       ('[%s]' % ','.join(str(v) for v in find)) if find else 'null')


def main():
    import frida
    listing = subprocess.run(['tasklist', '/FI', 'IMAGENAME eq DFO.exe', '/NH'],
                             capture_output=True, text=True, errors='replace')
    pid = None
    for line in (listing.stdout or '').splitlines():
        parts = line.split()
        if len(parts) >= 2 and parts[0].lower() == 'dfo.exe':
            pid = int(parts[1])
            break
    if pid is None:
        raise SystemExit('DFO.exe is not running')
    device = frida.get_local_device()
    session = device.attach(pid)
    script = session.create_script(JS)

    def on_message(message, data):
        if message.get('type') == 'send':
            print(message.get('payload'), flush=True)
        elif message.get('type') == 'error':
            print('ERROR %s' % message.get('description'), flush=True)

    script.on('message', on_message)
    script.load()
    time.sleep(0.6 + 0.6 * count)
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
