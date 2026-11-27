# -*- coding: utf-8 -*-
"""Find every call site of a function and disassemble the caller context."""
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64

pe = PE(default_exe())
base = pe.base
sva = ra = rs = None
for name, vs2, vsz, r2, rsz in pe.sections:
    if name == '.text':
        sva, ra, rs = vs2, r2, rsz
blob = bytes(pe.data[ra:ra + rs])

target = int(sys.argv[1], 0)  # RVA of the function
target_va = base + target
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
    if va + 5 + rel == target_va:
        hits.append(va)
print('%d call sites of %#x' % (len(hits), target_va))
md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True
for va in hits:
    begin, end, _ = pe.function(va - base)
    lo = max(begin, va - base - 0x80)
    print('--- call at %#x in fn %#x..%#x' % (va, base + begin, base + end))
    for ins in md.disasm(pe.read(lo, va - base - lo + 5), base + lo):
        print('   %08x %-20s %-7s %s' % (ins.address - base, ins.bytes.hex(), ins.mnemonic, ins.op_str))
pe.close()
