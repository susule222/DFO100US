# -*- coding: utf-8 -*-
"""Verify runtime/dump/DFO_unpacked.exe: parse it with the project PE reader,
check headers/sections, and disassemble two known sites (panel getter call at
RVA 0x3c00f85, encoder at 0x4f1f570) — output must match what the live-memory
disassembly showed.

Usage: python tools/verify_unpacked.py
"""
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE

from capstone import Cs, CS_ARCH_X86, CS_MODE_64

PATH = pathlib.Path(__file__).resolve().parent.parent / 'runtime' / 'dump' / 'DFO_unpacked.exe'

pe = PE(str(PATH))
print('base=%#x sections:' % pe.base)
for name, va, vs, ra, rs in pe.sections:
    print('  %-8s VA=%#09x VSize=%#09x Raw=%#08x@%#08x' % (name, va, vs, rs, ra))
print('pdata rva=%#x size=%#x (entries=%d)' % (pe.pdata, pe.pdata_size, pe.pdata_size // 12))

md = Cs(CS_ARCH_X86, CS_MODE_64)
for rva, label, n in ((0x3c00f6a, 'panel getter (decode call site follows)', 12),
                      (0x4f1f570, 'secure encoder entry', 8)):
    code = pe.read(rva, n * 8)
    print('\n== %#x %s ==' % (pe.base + rva, label))
    for ins in md.disasm(code, pe.base + rva):
        print('  %08x  %-10s %s' % (ins.address - pe.base, ins.mnemonic, ins.op_str))
        n -= 1
        if n <= 0:
            break
