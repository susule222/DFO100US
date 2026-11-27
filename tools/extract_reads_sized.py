# -*- coding: utf-8 -*-
"""Extract a parser's wire-read sequence with sizes and cumulative offsets.

Tracks the last edx value (read count) before each packet-reader call, so raw
reads get their real size, and reports the byte offset each read lands on if
the flow were linear. Branch targets that skip data are reported inline, so
the reader can tell where the linear assumption breaks.

Read-only. Usage: python tools/extract_reads_sized.py <rva> <length>
"""
import sys
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_IMM, CS_OP_REG

pe = PE(default_exe())
base = pe.base
md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True

READERS = {
    0x144F2BA70: ('u8', 1),
    0x144F2C840: ('u16', 2),
    0x144F2BC20: ('u32', 4),
    0x144F2BC60: ('raw', None),
    0x144F176C0: ('STRING', None),
}
# Loops and calls that consume their own reads; listed so the linear offset
# counter can be annotated instead of silently drifting.
BRANCH_NOTE = ('jmp', 'je', 'jne', 'jae', 'jb', 'jg', 'jl', 'jle', 'jge', 'ja', 'jb')

start = int(sys.argv[1], 0)
length = int(sys.argv[2], 0)
offset = 0
last_edx = None
for ins in md.disasm(pe.read(start, length), base + start):
    here = ins.address - base
    m = ins.mnemonic
    if m == 'mov' and len(ins.operands) == 2 and ins.operands[0].type == CS_OP_REG \
            and ins.operands[0].reg == md.reg_name if False else False:
        pass
    # track edx = imm / edx = reg+imm
    if m == 'mov' and ins.op_str.startswith('edx, '):
        try:
            last_edx = int(ins.op_str.split(', ')[1], 0)
        except ValueError:
            last_edx = None
    elif m == 'lea' and ins.op_str.startswith('edx, '):
        rhs = ins.op_str.split(', ')[1]
        if '+' in rhs:
            try:
                last_edx = int(rhs.split('+')[-1].rstrip(']'), 0)
            except ValueError:
                last_edx = None
        else:
            last_edx = None
    elif m == 'xor' and ins.op_str == 'edx, edx':
        last_edx = 0
    if m == 'call' and len(ins.operands) == 1 and ins.operands[0].type == CS_OP_IMM:
        tgt = ins.operands[0].imm
        if tgt in READERS:
            kind, size = READERS[tgt]
            if size is None:
                size = last_edx
            shown = size if size is not None else '?'
            print('%08x  off=%-5d %-6s size=%s' % (here, offset, kind, shown))
            if isinstance(size, int):
                offset += size
    elif m in BRANCH_NOTE and 'rip' not in ins.op_str and len(ins.op_str) > 0 and ins.op_str.startswith('0x'):
        print('%08x  (branch -> %s; linear offsets after this point are conditional)' % (here, ins.op_str))
pe.close()
