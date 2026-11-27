# -*- coding: utf-8 -*-
"""Find code referencing the sk.dat key globals and disassemble the loader."""
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_MEM
from capstone.x86 import X86_REG_RIP

TARGETS = [0x149481190, 0x149481198, 0x149481188, 0x1494811A0]

pe = PE(default_exe())
data = bytes(pe.data)
base = pe.base
sva = ra = rs = None
for name, vs2, vsize, r2, rsize in pe.sections:
    if name == '.text':
        sva, ra, rs = vs2, r2, rsize

blob = data[ra:ra + rs]
md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True

hits = []
prefixes = (b'\x48\x8d\x05', b'\x48\x8d\x0d', b'\x48\x8d\x15', b'\x4c\x8d\x05',
            b'\x4c\x8d\x0d', b'\x4c\x8d\x15', b'\x48\x8b\x05', b'\x48\x8b\x0d',
            b'\x48\x8b\x15', b'\x4c\x8b\x05', b'\x48\x89\x05', b'\x48\x89\x0d')
for pfx in prefixes:
    start = 0
    while True:
        k = blob.find(pfx, start)
        if k < 0:
            break
        start = k + 1
        if k + 7 > len(blob):
            continue
        disp = struct.unpack_from('<i', blob, k + 3)[0]
        va = base + sva + k
        tgt = va + 7 + disp
        if tgt in TARGETS:
            hits.append((va, tgt))
print('globals references: %d' % len(hits))
for va, tgt in sorted(hits):
    print('  %#x -> %#x' % (va, tgt))

shown = set()
for va, tgt in sorted(hits):
    begin, end, _ = pe.function(va - base)
    if begin in shown:
        continue
    shown.add(begin)
    print('=== function %#x (ref to %#x at %#x) ===' % (base + begin, tgt, va))
    n = 0
    for ins in md.disasm(pe.read(begin, min(end - begin, 0x800)), base + begin):
        note = ''
        for op in ins.operands:
            if op.type == CS_OP_MEM and op.mem.base == X86_REG_RIP:
                ref = ins.address + ins.size + op.mem.disp
                note += ' [va=%#x]' % ref
        print('  %08x %-24s %-8s %s%s' % (ins.address - base, ins.bytes.hex(), ins.mnemonic, ins.op_str, note))
        n += 1
        if n > 60:
            print('  ...')
            break
pe.close()
