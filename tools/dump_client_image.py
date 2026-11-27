# -*- coding: utf-8 -*-
"""Read-only image dump of the running client, for offline analysis.

Why: the shipped DFO.exe is Themida-packed, and this project's rule is that the
original client file is never modified. Reading the *running* process gives the
already-unpacked image without touching anything on disk, and it also answers a
question the file cannot: does the in-memory code differ from the file's, i.e. is
the file packed at rest in the sections this project analyses?

Method: attach (read only — deliberately no code patching, which is the thing this
client reverts when it is done after start), enumerate the main module's readable
committed ranges, read them in bounded chunks through a single RPC export, and
write one file per range plus a JSON index. Nothing is written back, and the
client keeps running.

Usage: python tools/dump_client_image.py [--max-mb 400]
"""
import json
import pathlib
import subprocess
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / 'runtime' / 'dump'
MAX_MB = 400
CHUNK = 4 * 1024 * 1024
for i, arg in enumerate(sys.argv):
    if arg == '--max-mb' and i + 1 < len(sys.argv):
        MAX_MB = int(sys.argv[i + 1])

JS = r'''
// frida 17 moved the read APIs onto the pointer itself: Memory only exposes
// alloc/dup/patchCode/scan here, so the read goes through NativePointer.
rpc.exports = {
    read: function (address, size) {
        return ptr(address).readByteArray(size);
    }
};
var mod = Process.findModuleByName('DFO.exe');
if (mod === null) {
    send({ kind: 'error', text: 'DFO.exe module not found' });
} else {
    var ranges = Process.enumerateRanges('r--').filter(function (r) {
        return r.base.compare(mod.base) >= 0 && r.base.add(r.size).compare(mod.base.add(mod.size)) <= 0;
    });
    send({ kind: 'module', base: mod.base.toString(), size: mod.size, path: mod.path, ranges: ranges.length });
    ranges.forEach(function (r) {
        send({ kind: 'range', base: r.base.toString(), size: r.size, protection: r.protection });
    });
}
'''


def main():
    import frida
    listing = subprocess.run(['tasklist', '/FI', 'IMAGENAME eq DFO.exe', '/NH'],
                             capture_output=True, text=True, errors='replace')
    pids = []
    for line in (listing.stdout or '').splitlines():
        parts = line.split()
        if len(parts) >= 2 and parts[0].lower() == 'dfo.exe':
            try:
                pids.append(int(parts[1]))
            except ValueError:
                pass
    if not pids:
        raise SystemExit('DFO.exe is not running')

    device = frida.get_local_device()
    session = device.attach(pids[0])
    script = session.create_script(JS)
    info = {}

    def on_message(message, data):
        if message.get('type') == 'send':
            payload = message.get('payload') or {}
            if payload.get('kind') == 'module':
                info['module'] = payload
                print('module base=%s size=%#x readable ranges=%d' % (
                    payload['base'], payload['size'], payload['ranges']))
            elif payload.get('kind') == 'range':
                info.setdefault('ranges', []).append(payload)
            elif payload.get('kind') == 'error':
                print('script error: %s' % payload.get('text'))
        elif message.get('type') == 'error':
            print('script exception: %s' % message.get('description'))

    script.on('message', on_message)
    script.load()
    time.sleep(2)
    if 'module' not in info:
        raise SystemExit('no module information; nothing dumped')

    OUT.mkdir(parents=True, exist_ok=True)
    stamp = time.strftime('%Y%m%d-%H%M%S')
    budget = MAX_MB * 1024 * 1024
    total = 0
    written = []
    for entry in sorted(info.get('ranges', []), key=lambda r: -int(r['size'])):
        size = int(entry['size'])
        base = int(entry['base'], 16)
        if size <= 0 or total + size > budget:
            continue
        chunks = []
        offset = 0
        while offset < size:
            want = min(CHUNK, size - offset)
            try:
                piece = script.exports_sync.read(base + offset, want)
            except Exception as exc:
                print('  read failed at %#x: %s' % (base + offset, exc))
                break
            if not piece:
                break
            chunks.append(piece)
            offset += len(piece)
        data = b''.join(chunks)
        if not data:
            continue
        path = OUT / ('%s-%#x-%s.bin' % (stamp, base, entry['protection']))
        path.write_bytes(data)
        written.append({'base': entry['base'], 'size': len(data),
                        'protection': entry['protection'], 'file': path.name})
        total += len(data)
        print('  %-34s %8.1f MB' % (path.name, len(data) / 1024 / 1024))

    (OUT / ('%s-index.json' % stamp)).write_text(
        json.dumps({'module': info.get('module'), 'ranges': written, 'total_bytes': total},
                   indent=2, ensure_ascii=False), encoding='utf-8')
    print('total %.1f MB -> %s' % (total / 1024 / 1024, OUT))
    try:
        session.detach()
    except Exception:
        pass


if __name__ == '__main__':
    main()
