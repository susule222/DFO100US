# -*- coding: utf-8 -*-
"""Find a byte pattern in the client's writable memory, natively.

The whole-process scanners this project already has walk memory one byte at a
time from JavaScript, which is fine for a small heap and not fine for a client
that has been running for an hour: the script load times out. Memory.scanSync is
frida's own scanner (native, no JS loop), so the same question — where did the
value I just sent land — is answered in a second and without heating the machine.

Ranges are enumerated, filtered to private writable memory (ranges holding
mapped files and the image itself are skipped by default) and capped.

Usage: python tools/scanptr.py <pattern> [--max 40] [--all]
   eg: python tools/scanptr.py "01 00 11 11 01 00 11 11"
       python tools/scanptr.py 777 --u32
"""
import pathlib
import struct
import subprocess
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent


def to_pattern(text):
    """Accept either a hex byte pattern or a decimal/hex number."""
    stripped = text.replace(' ', '')
    if all(c in '0123456789abcdefABCDEF' for c in stripped) and len(stripped) % 2 == 0 and len(stripped) > 2:
        if ' ' in text or len(stripped) > 8 or not text.strip().isdigit():
            return ' '.join(stripped[i:i + 2] for i in range(0, len(stripped), 2))
    value = int(text, 0)
    return ' '.join('%02x' % b for b in struct.pack('<I', value & 0xFFFFFFFF))


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        return 2
    pattern = to_pattern(sys.argv[1])
    maximum = 40
    include_files = '--all' in sys.argv
    for i, a in enumerate(sys.argv):
        if a == '--max' and i + 1 < len(sys.argv):
            maximum = int(sys.argv[i + 1], 0)

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

    js = r'''
    var pattern = %s;
    var maximum = %d;
    var includeFiles = %s;
    // The scan must not run while the script is loading: it takes longer than
    // the attach timeout, so it is deferred and reports through send().
    var hits = [];
    var ranges;
    var scanned = 0;
    var chunk = 32 * 1024 * 1024;
    var skippedFile = 0;
    var errors = 0;
    var firstError = '';
    var anon = 0;
    setTimeout(function () {
    try {
        ranges = Process.enumerateRanges(includeFiles ? 'r--' : 'rw-');
    } catch (e) {
        send('enumerate failed: ' + e);
        ranges = [];
    }
    send('scan started: ' + ranges.length + ' ranges');
    for (var i = 0; i < ranges.length && hits.length < maximum; i++) {
        var r = ranges[i];
        // Every rw- range in this client reports as file-backed (it maps large
        // ImagePacks), so the file flag cannot be used to skip: scan them all.
        if (includeFiles === false && r.file !== null) { skippedFile++; }
        anon++;
        for (var off = 0; off < r.size && hits.length < maximum; off += chunk) {
            var size = Math.min(chunk, r.size - off);
            var found;
            try {
                found = Memory.scanSync(r.base.add(off), size, pattern);
            } catch (e) {
                errors++;
                if (firstError === '') { firstError = String(e); }
                continue;
            }
            scanned += size;
            for (var k = 0; k < found.length && hits.length < maximum; k++) {
                hits.push(found[k].address.toString() + ' in range ' + r.base + '+0x' + off.toString(16) + ' size=0x' + r.size.toString(16));
            }
        }
    }
    send('ranges ' + ranges.length + ' file-backed ' + skippedFile + ' anonymous ' + anon
         + ' scan errors ' + errors + (firstError ? (' first error: ' + firstError) : ''));
    send('scanned ' + scanned + ' bytes in ' + ranges.length + ' ranges, hits ' + hits.length);
    for (var h = 0; h < hits.length; h++) { send(hits[h]); }
    }, 0);
    ''' % ('"%s"' % pattern, maximum, 'true' if include_files else 'false')

    import frida
    device = frida.get_local_device()
    session = device.attach(pid)
    script = session.create_script(js)

    def on_message(message, data):
        if message.get('type') == 'send':
            print(message.get('payload'), flush=True)
        elif message.get('type') == 'error':
            print('ERROR %s' % message.get('description'), flush=True)

    script.on('message', on_message)
    script.load()
    time.sleep(90)
    try:
        script.unload()
    except Exception:
        pass
    try:
        session.detach()
    except Exception:
        pass
    return 0


if __name__ == '__main__':
    sys.exit(main())
