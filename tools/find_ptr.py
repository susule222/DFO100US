# -*- coding: utf-8 -*-
"""Find every place in the client image that embeds an absolute pointer to a
function or a global, which is how dispatch tables reference handlers.

Usage: python tools/find_ptr.py <va> [max_hits]
"""
import mmap
import struct
import sys

import pathlib
sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import default_exe
EXE = default_exe()
VA = int(sys.argv[1], 0)
MAX = int(sys.argv[2], 0) if len(sys.argv) > 2 else 40

needle = struct.pack('<Q', VA & 0xFFFFFFFFFFFFFFFF)
with open(EXE, 'rb') as f:
    image = mmap.mmap(f.fileno(), 0, access=mmap.ACCESS_READ)

hits = []
at = 0
while True:
    k = image.find(needle, at)
    if k < 0:
        break
    hits.append(k)
    at = k + 1
    if len(hits) >= MAX:
        break
print('pointer %#x found at %d file offsets (first %d)' % (VA, len(hits), MAX))
# Name the section each hit falls in via the PE header.
pe_off = struct.unpack_from('<I', image, 0x3C)[0]
nsect = struct.unpack_from('<H', image, pe_off + 6)[0]
opt = pe_off + 24
magic = struct.unpack_from('<H', image, opt)[0]
optsize = struct.unpack_from('<H', image, pe_off + 20)[0]
sections = []
for i in range(nsect):
    start = opt + optsize + i * 40
    name = bytes(image[start:start + 8]).rstrip(b'\0').decode('ascii', 'replace')
    vs, va, rs, ra = struct.unpack_from('<IIII', image, start + 8)
    sections.append((name, ra, rs, va))
for k in hits:
    where = '?'
    rva = None
    for name, ra, rs, va in sections:
        if ra <= k < ra + rs:
            where = '%s+%#x' % (name, k - ra)
            rva = va + k - ra
            break
    print('  %#010x  %-24s rva=%s' % (k, where, hex(rva) if rva is not None else '?'))
