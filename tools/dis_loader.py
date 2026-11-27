# -*- coding: utf-8 -*-
"""Full disassembly of the US100 PVF loader function with literal annotations."""
import sys
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_MEM
from capstone.x86 import X86_REG_RIP

pe = PE(default_exe())
base = pe.base
md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True

RVA = int(sys.argv[1], 0)
LEN = int(sys.argv[2], 0) if len(sys.argv) > 2 else 0x600
for ins in md.disasm(pe.read(RVA, LEN), base + RVA):
    note = ''
    for op in ins.operands:
        if op.type == CS_OP_MEM and op.mem.base == X86_REG_RIP:
            ref = ins.address + ins.size + op.mem.disp
            note += ' [va=%#x]' % ref
            if ins.mnemonic == 'lea':
                t = pe.literal(ref - base) if ref - base >= 0 else ''
                if t:
                    note += ' %r' % t[:48]
    print('%08x %-22s %-8s %s%s' % (ins.address - base, ins.bytes.hex(), ins.mnemonic, ins.op_str, note))
pe.close()
