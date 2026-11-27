# -*- coding: utf-8 -*-
"""Find callers of the US100 stream-XOR function and the key literal they pass.

The stream function contains the 0x339E9711 immediate at file 0x58e4191 /
0x5941891 (two inlined copies). Walk back from each call site to recover the
literal address loaded into the key register.
"""
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_IMM, CS_OP_MEM
from capstone.x86 import X86_REG_RIP

pe = PE(default_exe())
data = bytes(pe.data)
base = pe.base
sva = None
ra = None
rs = None
for name, vs2, vsize, r2, rsize in pe.sections:
    if name == '.text':
        sva, ra, rs = vs2, r2, rsize
print('.text rva %#x file %#x size %#x' % (sva, ra, rs))

# Function starts containing the seed immediate.
seed_file = 0x58E4191
seed_rva = sva + (seed_file - ra)
func = pe.function(seed_rva)
print('function containing the seed immediate: rva %#x..%#x' % (func[0], func[1]))
func_va = base + func[0]
print('function VA %#x' % func_va)

# Find direct calls to func_va.
hits = []
blob = data[ra:ra + rs]
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
    if va + 5 + rel == func_va:
        hits.append(va)
print('direct call sites: %d' % len(hits))
for va in hits[:20]:
    print('  call at %#x' % va)

md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True
for va in hits[:20]:
    begin, end, _ = pe.function(va - base)
    lo = max(begin, va - base - 0x80)
    print('--- caller %#x in function %#x' % (va, base + begin))
    for ins in md.disasm(pe.read(lo, va - base - lo), base + lo):
        note = ''
        for op in ins.operands:
            if op.type == CS_OP_MEM and op.mem.base == X86_REG_RIP:
                ref = ins.address + ins.size + op.mem.disp
                note += ' [va=%#x]' % ref
                txt = None
                try:
                    off = ref - base
                    raw = data[off:off + 64]
                    end0 = raw.find(b'\0')
                    if end0 > 0:
                        cand = raw[:end0]
                        if all(32 <= b < 127 for b in cand):
                            txt = cand.decode()
                except Exception:
                    pass
                if txt:
                    note += ' %r' % txt
        print('   %08x %-20s %-8s %s%s' % (ins.address - base, ins.bytes.hex(), ins.mnemonic, ins.op_str, note))
pe.close()
