# -*- coding: utf-8 -*-
"""List functions near the registration insert that use the FNV-1a constants, to
find the map lookup used by the packet dispatch.

Usage: python tools/find_lookup_near.py [center_hex] [radius_hex]
"""
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE

EXE = default_exe()
CENTER = int(sys.argv[1], 0) if len(sys.argv) > 1 else 0x3a4f640
RADIUS = int(sys.argv[2], 0) if len(sys.argv) > 2 else 0x20000

pe = PE(EXE)
basis = struct.pack('<Q', 0xcbf29ce484222325)
start = CENTER - RADIUS
found = {}
step = 0x10000
for off in range(0, 2 * RADIUS, step):
    blob = pe.read(start + off, min(step, 2 * RADIUS - off))
    at = 0
    while True:
        k = blob.find(basis, at)
        if k < 0:
            break
        at = k + 1
        rva = start + off + k
        begin, end, _ = pe.function(rva)
        found.setdefault((begin, end), 0)
        found[(begin, end)] += 1
for (begin, end), n in sorted(found.items()):
    print('func %08x..%08x size=%d fnv_uses=%d' % (begin, end, end - begin, n))
print('functions: %d (range %#x..%#x)' % (len(found), start, CENTER + RADIUS))
pe.close()
