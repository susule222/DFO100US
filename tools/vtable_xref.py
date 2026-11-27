# -*- coding: utf-8 -*-
"""Resolve RTTI class name for a vtable (COL pointer) and scan .text for
virtual calls at a given vtable offset.

Usage: python tools/vtable_xref.py <col_va> [vt_offset_hex]
"""
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

BASE = 0x140000000
pe = PE(default_exe())

col = int(sys.argv[1], 0)
vtoff = int(sys.argv[2], 0) if len(sys.argv) > 2 else 0x168

col_rva = col - BASE
td = struct.unpack('<I', pe.read(col_rva + 0x0c, 4))[0]
name = pe.read(td + 0x10, 160).split(b'\x00')[0]
print('RTTI class: %s' % name.decode('latin1'))

# scan .text for ff 9? <vtoff disp32> (call qword [reg+disp32])
text = None
for sname, va, vs, ra, rs in pe.sections:
    if sname == '.text':
        text = (va, vs)
tva, tvs = text
pat_tail = struct.pack('<I', vtoff)
hits = []
CH = 0x10000
for off in range(0, tvs, CH):
    chunk = pe.read(tva + off, min(CH, tvs - off))
    at = 0
    while True:
        k = chunk.find(pat_tail, at)
        if k < 0 or k + 4 > len(chunk):
            break
        at = k + 1
        # disp32 at k; the two bytes before should be ff 90..97 (call [reg+disp32])
        if k >= 2 and chunk[k - 2] == 0xFF and 0x90 <= chunk[k - 1] <= 0x97:
            hits.append(tva + off + k - 2)
print('call [reg+%#x] sites: %d' % (vtoff, len(hits)))

# name the containing functions
bounds = []
n = pe.pdata_size // 12
for i in range(n):
    begin, end, _ = struct.unpack('<III', pe.read(pe.pdata + i * 12, 12))
    if end > begin and end - begin < 0x200000:
        bounds.append((begin, end))
bounds.sort()
import bisect
starts = [b for b, _ in bounds]
seen = {}
for h in hits:
    i = bisect.bisect_right(starts, h) - 1
    f = BASE + bounds[i][0] if i >= 0 and bounds[i][0] <= h < bounds[i][1] else None
    seen.setdefault(f, []).append(BASE + h)
for f, sites in sorted(seen.items(), key=lambda kv: kv[0] or 0):
    print('  fn %#x: %s' % (f or 0, ', '.join('%#x' % s for s in sites[:6])))
