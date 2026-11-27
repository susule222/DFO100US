# -*- coding: utf-8 -*-
"""Count direct calls to the encode helper (0x144f1f570) per .pdata function.

The stat expansion writes ~40 secure slots, so the function that calls the
encode helper that many times is the expansion. Walk every function, count the
E8-rel32 calls, print the top.

Usage: python tools/encode_census.py [min_calls]
"""
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

MIN = int(sys.argv[1]) if len(sys.argv) > 1 else 2

pe = PE(default_exe())
base = pe.base
target = 0x144f1f570

text_va = text_ra = text_rs = None
for name, va, vs, ra, rs in pe.sections:
    if name == '.text':
        text_va, text_ra, text_rs = va, ra, rs

counts = {}
pdata = pe.pdata
n = pe.pdata_size // 12
for i in range(n):
    begin, end, _ = struct.unpack('<III', pe.read(pdata + i * 12, 12))
    if end <= begin or end - begin > 0x200000:
        continue
    code = b''
    for off in range(begin, min(end, begin + 0x200000), 0x10000):
        code += pe.read(off, min(0x10000, end - off))
    found = 0
    at = 0
    while True:
        k = code.find(b'\xe8', at)
        if k < 0:
            break
        at = k + 1
        if k + 5 > len(code):
            break
        rel = struct.unpack_from('<i', code, k + 1)[0]
        dst = base + begin + k + 5 + rel
        if dst == target:
            found += 1
    if found >= MIN:
        counts[begin] = found

rows = sorted(((n, b) for b, n in counts.items()), reverse=True)
print('functions with >= %d encode calls: %d' % (MIN, len(rows)))
for n, b in rows[:25]:
    print('  %4d calls  function %#x' % (n, base + b))
