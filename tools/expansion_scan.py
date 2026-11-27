# -*- coding: utf-8 -*-
"""Find the US100 expansion: which encode-heavy function writes the 40-slot
stat region (+0x1094..+0x11cc, 8-byte stride) of the 9128-byte stat object.

For each candidate function (.pdata-bounded), disassemble with capstone and
record, for every direct call to the encode writer 0x144f1f570, the last
instruction that set rdx (the field pointer). A `lea rdx,[reg+disp]` with
disp inside the slot region is the expansion's fingerprint; the 115
counterpart writes 40 such fields with 8-byte stride.

Usage: python tools/expansion_scan.py [cand,cand,...]
"""
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_IMM, CS_OP_MEM, CS_OP_REG

TARGET = 0x144f1f570          # encode writer: (value_ptr=rcx, field_ptr=rdx)
LO, HI = 0x0f00, 0x1300       # slot region window (region itself: 0x1094..0x11cc)
BASE = 0x140000000

DEFAULT_CANDS = [0x143c36bc0, 0x14434f750, 0x1440648c0, 0x140083620]

pe = PE(default_exe())

# .pdata lookup: function bounds by entry RVA.
bounds = {}
n = pe.pdata_size // 12
for i in range(n):
    begin, end, _ = struct.unpack('<III', pe.read(pe.pdata + i * 12, 12))
    if end > begin and end - begin < 0x200000:
        bounds[begin] = end


def analyze(va):
    rva = va - BASE
    end = bounds.get(rva)
    if end is None:
        # not a function start; find the containing entry
        for b, e in bounds.items():
            if b <= rva < e:
                rva, end = b, e
                break
        else:
            print('%#x: not in .pdata' % va)
            return
    size = end - rva
    code = b''
    for off in range(rva, end, 0x10000):
        code += pe.read(off, min(0x10000, end - off))
    md = Cs(CS_ARCH_X86, CS_MODE_64)
    md.detail = True
    last_rdx = None       # (mnemonic, op_str, disp or None)
    calls = []            # (call_rva, rdx description)
    slot_disps = []
    for ins in md.disasm(code, BASE + rva):
        if ins.mnemonic == 'call' and ins.operands and ins.operands[0].type == CS_OP_IMM:
            if ins.operands[0].imm == TARGET:
                calls.append((ins.address, last_rdx))
                if last_rdx and last_rdx[2] is not None and LO <= last_rdx[2] <= HI:
                    slot_disps.append((ins.address, last_rdx[2]))
            last_rdx = None
            continue
        # Track writes to rdx (lea/mov/add). Only lea with a memory operand
        # gives a usable displacement.
        writes_rdx = False
        disp = None
        if ins.operands:
            d = ins.operands[0]
            if d.type == CS_OP_REG and ins.reg_name(d.reg) in ('rdx', 'edx', 'dx', 'dl'):
                writes_rdx = True
                if ins.mnemonic == 'lea' and len(ins.operands) > 1:
                    s = ins.operands[1]
                    if s.type == CS_OP_MEM:
                        disp = s.mem.disp
        if writes_rdx:
            last_rdx = (ins.mnemonic, ins.op_str, disp)
    in_region = [d for _, d in slot_disps]
    distinct = sorted(set(in_region))
    print('%#x (size %#x): %d encode calls, %d with slot-region field ptr, %d distinct disp' %
          (va, size, len(calls), len(slot_disps), len(distinct)))
    if distinct:
        print('   disps: %s' % ', '.join('%#x' % d for d in distinct[:50]))
        strides = set(b - a for a, b in zip(distinct, distinct[1:]))
        print('   strides: %s' % sorted(strides)[:10])
    # show a few sample call sites with their rdx sources
    for addr, desc in calls[:6]:
        print('   call at %#x  rdx<-%s' % (addr, desc))


cands = DEFAULT_CANDS
if len(sys.argv) > 1:
    cands = [int(x, 0) for x in sys.argv[1].split(',')]
for va in cands:
    analyze(va)
    print()
