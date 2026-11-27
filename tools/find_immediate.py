# -*- coding: utf-8 -*-
"""Find functions in a code range that embed a 32-bit immediate, so a packet
builder can be located from a constant it writes (for example the dungeon id a
client puts into its select request).

Usage: python tools/find_immediate.py 0xc49 [start_hex] [end_hex]
"""
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

ROOT = pathlib.Path(__file__).resolve().parent.parent
EXE = default_exe()
VALUE = int(sys.argv[1], 0)
START = int(sys.argv[2], 0) if len(sys.argv) > 2 else 0x3000000
END = int(sys.argv[3], 0) if len(sys.argv) > 3 else 0x3600000

pe = PE(EXE)
needle = struct.pack('<I', VALUE)
print('searching %#x..%#x for %#x' % (START, END, VALUE))
hits = 0
step = 0x10000
for base_rva in range(START, END, step):
    blob = pe.read(base_rva, min(step, END - base_rva))
    at = 0
    while True:
        k = blob.find(needle, at)
        if k < 0:
            break
        at = k + 1
        rva = base_rva + k
        begin, end, _ = pe.function(rva)
        print('  %08x  in func %08x (size %d)' % (rva, begin, end - begin))
        hits += 1
        if hits >= 40:
            break
    if hits >= 40:
        break
print('hits=%d' % hits)
pe.close()
