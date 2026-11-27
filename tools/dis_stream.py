# -*- coding: utf-8 -*-
"""Disassemble the US100 PVF stream-XOR function to recover its exact formula."""
import sys
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64

pe = PE(default_exe())
base = pe.base
for rva, length in ((0x58E4160, 0x180),):
    print('== function rva %#x ==' % rva)
    for ins in Cs(CS_ARCH_X86, CS_MODE_64).disasm(pe.read(rva, length), base + rva):
        print('  %08x %-22s %-8s %s' % (ins.address - base, ins.bytes.hex(), ins.mnemonic, ins.op_str))
pe.close()
