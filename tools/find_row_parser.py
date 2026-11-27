# -*- coding: utf-8 -*-
"""Find where the US100 client receives the roster packet and feeds its parser.

The 2.38 build calls row parser 0x14563e280 from packet handler 0x145250f00.
In 2.24 the roster row data lives in the same network packet, so finding the
parser means the row layout can be recovered statically.
"""
import pathlib
import re
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_MEM
from capstone.x86 import X86_REG_RIP

pe = PE(default_exe())
data = bytes(pe.data)
base = pe.base

# The protobuf helpers seen in the roster handler: every one is called with a
# stream-cursor global; this build's roster row reader must consume the same
# cursor sequence the 2.38 row parser consumed.
helpers = {
    'read_u8': 0x144F2BA70,
    'read_u16': 0x144F2C840,
    'read_u32': 0x144F2BC20,
    'utf8_narrow_to_wide': 0x1400F9680,
}

sva = ra = rs = None
for name, vs2, vsz, r2, rsz in pe.sections:
    if name == '.text':
        sva, ra, rs = vs2, r2, rsz
blob = data[ra:ra + rs]


def call_sites(target):
    hits = []
    start = 0
    while True:
        k = blob.find(b'\xe8', start)
        if k < 0:
            break
        start = k + 1
        if k + 5 > len(blob):
            continue
        rel = struct.unpack_from('<i', blob, k + 1)[0]
        va = base + sva + k
        if va + 5 + rel == target:
            hits.append(va)
    return hits


md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True
for label, target in helpers.items():
    hits = call_sites(target)
    print('%s %#x called from %d sites' % (label, target, len(hits)))
    # Rank functions by how many distinct helpers they call in a window.
    funcs = {}
    for va in hits:
        begin, end, _ = pe.function(va - base)
        funcs.setdefault(base + begin, 0)
        funcs[base + begin] += 1
    for fn, n in sorted(funcs.items(), key=lambda kv: -kv[1])[:8]:
        print('   fn %#x has %d calls to this helper' % (fn, n))
pe.close()
