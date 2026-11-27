# -*- coding: utf-8 -*-
"""Find instructions whose displacement matches a target struct offset.

Scans .text for 4-byte little-endian displacements and reports the decoded
instruction plus its containing function, so readers of a struct field can be
located from its offset alone.

Bounded: one pass over .text, hits capped.
Usage: python tools/find_offset_refs.py 0x7f2 [max_hits]
"""
import sys
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_MEM

pe = PE(default_exe())
base = pe.base
disp = int(sys.argv[1], 0)
MAX = int(sys.argv[2]) if len(sys.argv) > 2 else 25
needle = disp.to_bytes(4, 'little')

text_va, text_rs, text_ra = None, None, None
for name, va, vs, ra, rs in pe.sections:
    if name == '.text':
        text_va, text_rs, text_ra = va, rs, ra
blob = bytes(pe.data[text_ra:text_ra + text_rs])
print('disp=%#x in .text (%d bytes)' % (disp, len(blob)))

md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True
hits = 0
start = 0
while hits < MAX:
    k = blob.find(needle, start)
    if k < 0:
        break
    start = k + 1
    # Decode an instruction ending right after the displacement.
    for back in range(2, 10):
        off = k - back
        if off < 0:
            continue
        ins_list = list(md.disasm(blob[off:k + 4], base + text_va + off))
        if not ins_list:
            continue
        ins = ins_list[0]
        if ins.address + ins.size != base + text_va + k + 4:
            continue
        # Only report real struct accesses: a memory operand using this disp.
        ok = any(op.type == CS_OP_MEM and op.mem.disp == disp for op in ins.operands)
        if ok:
            begin, end, _ = pe.function(ins.address - base)
            print('  %08x  %-8s %-28s func %08x' % (
                ins.address - base, ins.mnemonic, ins.op_str, begin))
            hits += 1
        break
print('hits=%d' % hits)
pe.close()
