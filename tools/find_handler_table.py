# -*- coding: utf-8 -*-
"""Locate the US100 client's incoming-command handler table.

The frida dispatcher tracer already resolved handler function pointers for a
few ids. If those pointers live in a contiguous array indexed by command id,
every id's handler (and therefore its expected packet layout) can be read
statically without running the client again.

Usage:
  python find_handler_table.py <DFO.exe> <id=va> [<id=va> ...]
"""
import collections
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE


def main():
    exe = pathlib.Path(sys.argv[1])
    known = {}
    for raw in sys.argv[2:]:
        i, v = raw.split('=')
        known[int(i, 0)] = int(v, 0)
    pe = PE(str(exe))
    base = pe.base
    print('image base %#x, sections %d' % (base, len(pe.sections)))

    # Map each known handler VA to its file offsets.
    hits = {}
    for cid, va in sorted(known.items()):
        needle = struct.pack('<Q', va)
        offs = []
        for name, sva, vs, ra, rs in pe.sections:
            if not rs:
                continue
            blob = bytes(pe.data[ra:ra + rs])
            start = 0
            while True:
                k = blob.find(needle, start)
                if k < 0:
                    break
                offs.append(ra + k)
                start = k + 1
        hits[cid] = offs
        print('id %-6d va %#x -> %d hit(s) %s' % (cid, va, len(offs), [hex(o) for o in offs[:6]]))

    # A table indexed by id puts id's pointer at table_file_offset + id*8.
    votes = collections.Counter()
    for cid, offs in hits.items():
        for o in offs:
            votes[(o - cid * 8) // 8 * 8 + 8] += 0  # placeholder, replaced below
    # candidate table start = hit_offset - id*8 (aligned to 8)
    cand = collections.Counter()
    for cid, offs in hits.items():
        for o in offs:
            cand[o - cid * 8] += 1
    print('candidate table starts (>=2 ids agreeing):')
    for start, n in cand.most_common(6):
        if n >= 2:
            print('  file %#x rva %#x  agreement %d/%d' % (start, start, n, len(known)))
    if not cand:
        pe.close()
        return
    start, n = cand.most_common(1)[0]
    # Report entries around the known ids plus a readable dump of the table.
    print('table file offset %#x' % start)
    entries = {}
    for cid in range(0, 4096):
        data = bytes(pe.data[start + cid * 8:start + cid * 8 + 8])
        if len(data) < 8:
            break
        v = struct.unpack('<Q', data)[0]
        if v:
            entries[cid] = v
    print('non-null entries in first 4096 ids: %d' % len(entries))
    for cid, v in sorted(entries.items()):
        mark = ' KNOWN' if cid in known else ''
        print('  id=%-6d -> %#x (rva %#x)%s' % (cid, v, v - base, mark))
    pe.close()


if __name__ == '__main__':
    main()
