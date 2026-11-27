# -*- coding: utf-8 -*-
"""Full unpack of the running Themida-packed DFO.exe into a canonical PE file.

The shipped file is packed; the running process carries the unpacked image.
This tool attaches READ-ONLY, reads the whole module image (base..base+size)
in bounded chunks (unreadable pages become zeros, noted in the log), then
rebuilds a file whose sections sit at their memory RVAs:

  - first SizeOfHeaders bytes: the in-memory headers;
  - every section header rewritten: PointerToRawData = VirtualAddress,
    SizeOfRawData = VirtualSize rounded up to FileAlignment;
  - SectionAlignment == FileAlignment == 0x1000 in the rebuilt file, so the
    file offset of every byte equals its RVA — all existing RVA-based tools
    (dis_range, callers_of, find_ptr, encode_census...) run on it directly.

The import table stays as the live process resolved it (absolute addresses of
this boot's DLLs): fine for analysis, not for re-running. Nothing is written
back into the client; the session detaches without unloading hooks (there are
none), which this client tolerates.

Usage: python tools/dump_unpacked_pe.py [output.exe] [--max-mb 400]
"""
import json
import pathlib
import struct
import subprocess
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUTDIR = ROOT / 'runtime' / 'dump'

JS = r'''
rpc.exports = {
    info: function () {
        var mod = Process.findModuleByName('DFO.exe');
        if (mod === null) { return null; }
        return { base: mod.base.toString(), size: mod.size, path: mod.path };
    },
    read: function (address, size) {
        return ptr(address).readByteArray(size);
    }
};
'''


def read_chunk(script, addr, size):
    try:
        data = script.exports_sync.read(addr, size)
        return data if data else None
    except Exception:
        return None


def main():
    import frida
    out = OUTDIR / 'DFO_unpacked.exe'
    max_mb = 400
    argv = sys.argv[1:]
    if argv and not argv[0].startswith('--'):
        out = pathlib.Path(argv[0])
    if '--max-mb' in argv:
        max_mb = int(argv[argv.index('--max-mb') + 1])

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
    script.load()
    info = script.exports_sync.info()
    if not info:
        raise SystemExit('DFO.exe module not found')
    base = int(info['base'], 16)
    size = int(info['size'])
    print('module base=%#x size=%#x (%.1f MB)' % (base, size, size / 1048576), flush=True)
    if size > max_mb * 1024 * 1024:
        raise SystemExit('image exceeds --max-mb budget')

    # 1) flat read of the whole image, chunked; failures -> zero pages
    CHUNK = 1024 * 1024
    image = bytearray()
    failed = []
    t0 = time.time()
    off = 0
    while off < size:
        want = min(CHUNK, size - off)
        data = read_chunk(script, base + off, want)
        if data is None or len(data) == 0:
            # retry in 4K pages to salvage what is readable
            page = 4096
            sub = off
            end = off + want
            while sub < end:
                pw = min(page, end - sub)
                pd = read_chunk(script, base + sub, pw)
                if pd and len(pd) == pw:
                    image.extend(pd)
                else:
                    image.extend(b'\x00' * pw)
                    failed.append((sub, pw))
                sub += pw
        else:
            image.extend(data)
            if len(data) < want:  # short read: pad and note
                image.extend(b'\x00' * (want - len(data)))
                failed.append((off + len(data), want - len(data)))
        off += want
        if off % (16 * 1024 * 1024) == 0:
            print('  %.1f MB read (%.0fs)' % (off / 1048576, time.time() - t0), flush=True)
    print('read done in %.0fs; %d failed chunk(s)' % (time.time() - t0, len(failed)), flush=True)

    # 2) parse in-memory headers
    e_lfanew = struct.unpack_from('<I', image, 0x3c)[0]
    sig = image[e_lfanew:e_lfanew + 4]
    if sig != b'PE\x00\x00':
        raise SystemExit('in-memory PE signature missing (e_lfanew=%#x)' % e_lfanew)
    coff = e_lfanew + 4
    nsec = struct.unpack_from('<H', image, coff + 2)[0]
    opt_size = struct.unpack_from('<H', image, coff + 16)[0]
    opt = coff + 20
    magic = struct.unpack_from('<H', image, opt)[0]
    if magic != 0x20b:
        raise SystemExit('not a PE32+ optional header (magic=%#x)' % magic)
    size_of_headers = struct.unpack_from('<I', image, opt + 60)[0]
    file_align = struct.unpack_from('<I', image, opt + 36)[0]
    sect_align = struct.unpack_from('<I', image, opt + 32)[0]
    print('sections=%d SizeOfHeaders=%#x FileAlignment=%#x SectionAlignment=%#x'
          % (nsec, size_of_headers, file_align, sect_align), flush=True)
    sec_tab = opt + opt_size
    sections = []
    for i in range(nsec):
        at = sec_tab + i * 40
        name = bytes(image[at:at + 8]).rstrip(b'\x00').decode('latin1')
        vsize, va, rawsize, rawptr = struct.unpack_from('<IIII', image, at + 8)
        chars = struct.unpack_from('<I', image, at + 36)[0]
        sections.append([name, vsize, va, rawsize, rawptr, chars, at])
        print('  %-8s VA=%#09x VSize=%#09x Raw=%#08x@%#08x chars=%#x'
              % (name, va, vsize, rawsize, rawptr, chars), flush=True)

    # 3) rebuild: file offset == RVA (align everything to 0x1000)
    ALIGN = 0x1000
    if file_align != ALIGN or sect_align != ALIGN:
        print('note: forcing FileAlignment=SectionAlignment=0x1000 in the rebuilt file')
        struct.pack_into('<I', image, opt + 32, ALIGN)
        struct.pack_into('<I', image, opt + 36, ALIGN)
        struct.pack_into('<I', image, opt + 60, ALIGN)  # SizeOfHeaders = one page
        size_of_headers = ALIGN
    outblob = bytearray(image[:size_of_headers])
    outblob.extend(b'\x00' * (ALIGN - len(outblob)))
    for name, vsize, va, rawsize, rawptr, chars, at in sections:
        end = min(va + max(vsize, rawsize), size)
        body = bytes(image[va:end])
        body += b'\x00' * ((-len(body)) % ALIGN)
        struct.pack_into('<I', image, at + 8, vsize)
        struct.pack_into('<I', image, at + 16, len(body))       # SizeOfRawData
        struct.pack_into('<I', image, at + 20, len(outblob))   # PointerToRawData
        # re-copy the patched header into outblob later; record for now
        outblob.extend(body)
        print('  -> %-8s file [%#x..%#x)' % (name, len(outblob) - len(body), len(outblob)), flush=True)
    outblob[:size_of_headers] = image[:size_of_headers]  # headers with patched section table

    OUTDIR.mkdir(parents=True, exist_ok=True)
    tmp = out.with_suffix('.tmp')
    tmp.write_bytes(bytes(outblob))
    tmp.replace(out)
    meta = {
        'source_pid': pids[0], 'module_base': info['base'], 'module_size': size,
        'failed_chunks': [{'off': o, 'size': s} for o, s in failed],
        'sections': [{'name': s[0], 'va': s[2], 'vsize': s[1]} for s in sections],
        'built': time.strftime('%Y-%m-%d %H:%M:%S'),
    }
    (OUTDIR / 'DFO_unpacked.meta.json').write_text(json.dumps(meta, indent=2), encoding='utf-8')
    print('wrote %s (%.1f MB)' % (out, len(outblob) / 1048576), flush=True)
    try:
        session.detach()
    except Exception:
        pass
    return 0


if __name__ == '__main__':
    sys.exit(main())
