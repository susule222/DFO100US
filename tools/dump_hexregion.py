# -*- coding: utf-8 -*-
"""Dump candidate key-string regions of the US100 client (read-only)."""
import pathlib
import re
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

pe = PE(default_exe())
data = bytes(pe.data)
for start, length in ((0x7b96f00, 0x200), (0x7b97000, 0x120), (0x7dcc180, 0x100)):
    chunk = data[start:start + length]
    print('== file %#x' % start)
    for off in range(0, len(chunk), 32):
        row = chunk[off:off + 32]
        printable = ''.join(chr(b) if 32 <= b < 127 else '.' for b in row)
        print('  %#x  %-64s  %s' % (start + off, row.hex(), printable))

print()
print('== all 64-char hex strings in .rdata-like range ==')
for m in re.finditer(rb'[0-9A-Fa-f]{64}', data):
    off = m.start()
    if 0x7b00000 <= off <= 0x7e00000:
        ctx = data[max(0, off - 24):off + 72]
        print('  %#x: %s' % (off, ctx.decode('ascii', 'replace').replace('\x00', '.')))
pe.close()
