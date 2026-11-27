# -*- coding: utf-8 -*-
"""Locate the packet-registration init function and where its manager object
comes from, so the runtime id->handler map can be dumped from that global.

Usage: python tools/find_registration_func.py
"""
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_IMM, CS_OP_MEM
from capstone.x86 import X86_REG_RIP

EXE = default_exe()
CALLS = [0x14352b1ae, 0x14352ac15, 0x143515af6, 0x143515a30]

pe = PE(EXE)
base = pe.base
md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True

for call in CALLS:
    s, e, _ = pe.function(call - base)
    print('call %#x is in func %#x..%#x (%d bytes)' % (call, s, e, e - s))
    blob = pe.read(s, min(e - s, 0x900))
    for ins in md.disasm(blob, s):
        rva = ins.address - base
        note = ''
        for op in ins.operands:
            if op.type == CS_OP_MEM and op.mem.base == X86_REG_RIP:
                note = ' [global %#x]' % (ins.address + ins.size + op.mem.disp)
        if ins.mnemonic in ('mov', 'lea', 'call') and (
                'rbx' in ins.op_str or 'global' in note or 'rip' in ins.op_str):
            if rva < s + 0x120:
                print('   %08x %-8s %s%s' % (rva, ins.mnemonic, ins.op_str, note))
    print()
pe.close()
