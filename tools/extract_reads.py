# -*- coding: utf-8 -*-
"""Extract the ordered wire-read sequence of a parser function.

Walks the function's disassembly and lists, in order, every call to the
packet-read helpers (read_u8/u16/u32/raw), so the exact field layout the
client expects is recovered statically. Read-only.
"""
import sys
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_IMM

pe = PE(default_exe())
base = pe.base
md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True

READERS = {
    0x144F2BA70: 'u8',
    0x144F2C840: 'u16',
    0x144F2BC20: 'u32',
    0x144F2BC60: 'raw',   # raw read: edx = count
    0x144F176C0: 'STRING',  # string reader: u32 length + bytes
    0x144F1F570: 'sizefix',  # size-field adjust (no cursor advance)
    0x144F1F2D0: 'sizebyte',  # byte-size adjust
}

start = int(sys.argv[1], 0)
length = int(sys.argv[2], 0)
offset = 0
for ins in md.disasm(pe.read(start, length), base + start):
    here = ins.address - base
    if ins.mnemonic == 'call' and len(ins.operands) == 1 and ins.operands[0].type == CS_OP_IMM:
        tgt = ins.operands[0].imm
        if tgt in READERS:
            # Recover the edx argument (count) for raw reads.
            arg = ''
            if READERS[tgt] == 'raw':
                arg = '?'
            print('%08x  %-7s %s' % (here, READERS[tgt], arg))
pe.close()
