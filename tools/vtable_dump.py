# -*- coding: utf-8 -*-
"""Dump the pointer run around an .rdata address (vtable reconstruction).

Walks backwards/forwards from the hit while the qwords look like image
pointers, prints the run, then tries to resolve RTTI: the qword before the
run start is the Complete Object Locator, whose type descriptor carries the
class name string.

Usage: python tools/vtable_dump.py <rva_or_va> [max_entries]
"""
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

BASE = 0x140000000
pe = PE(default_exe())

arg = int(sys.argv[1], 0)
rva = arg - BASE if arg >= BASE else arg
maxn = int(sys.argv[2]) if len(sys.argv) > 2 else 80


def q(at_rva):
    return struct.unpack('<Q', pe.read(at_rva, 8))[0]


def looks_ptr(v):
    return BASE <= v < BASE + 0x8000000


# walk back to the run start
start = rva
while start - 8 >= 0 and looks_ptr(q(start - 8)):
    start -= 8
    if (rva - start) // 8 > maxn:
        break
# COL is the qword before the first entry (if the run start is a vtable start)
col = q(start - 8) if start >= 8 else 0
print('run starts at rva %#x (va %#x); qword before = %#x' % (start, BASE + start, col))

# RTTI: COL -> type descriptor (offset 0x0c in COL image) -> name at td+0x10
if looks_ptr(col):
    col_rva = col - BASE
    try:
        td = struct.unpack('<I', pe.read(col_rva + 0x0c, 4))[0]  # RVA of type descriptor
        name = pe.read(td + 0x10, 128)
        name = name.split(b'\x00')[0]
        print('RTTI class: %s' % name.decode('latin1'))
    except Exception as e:
        print('RTTI resolve failed: %r' % e)

n = 0
r = start
while n < maxn and looks_ptr(q(r)):
    mark = '  <== HIT' if r == rva else ''
    print('  [%2d] va %#x -> %#x%s' % (n, BASE + r, q(r), mark))
    r += 8
    n += 1
