# -*- coding: utf-8 -*-
"""List the call sites inside one function, annotated with their arguments.

The interesting question about a parser is usually "who does it call and with
what", so this walks the .pdata function containing <rva> and prints each call
with the preceding few instructions (the argument setup) — enough to see which
struct offset or stack value is handed over. Bounded: one function, capped at
64 KiB and at <max> printed instructions per call.

Usage: python tools/fn_calls.py <rva> [--target 0xADDR] [--ctx 4]
"""
import sys
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_IMM

pe = PE(default_exe())
base = pe.base

rva = int(sys.argv[1], 0)
target = None
ctx = 4
for i, a in enumerate(sys.argv):
    if a == '--target' and i + 1 < len(sys.argv):
        target = int(sys.argv[i + 1], 0)
    if a == '--ctx' and i + 1 < len(sys.argv):
        ctx = int(sys.argv[i + 1], 0)

start, end, _ = pe.function(rva - base)
size = min(end - start, 65536)
print('function %#x..%#x (%d bytes)' % (base + start, base + end, end - start))

md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True
window = []
for ins in md.disasm(pe.read(start, size), base + start):
    here = ins.address
    if ins.mnemonic == 'call' and ins.operands and ins.operands[0].type == CS_OP_IMM:
        tgt = ins.operands[0].imm & 0xFFFFFFFFFFFFFFFF
        if target is None or tgt == target:
            print('--- call at %#x -> %#x' % (here, tgt))
            for w in window[-ctx:]:
                print('      %08x  %-8s %s' % (w[0], w[1], w[2]))
    window.append((here, ins.mnemonic, ins.op_str))
    if len(window) > 64:
        window.pop(0)
