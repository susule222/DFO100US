# -*- coding: utf-8 -*-
"""Disassemble an RVA range of the client with capstone, annotating calls.

Read-only and bounded: the range is capped at 8 KiB so a wrong address cannot
pull a megabyte into memory. Call targets go through .pdata, so a call into a
real function shows its entry (fn) or its offset inside it (fn+..), and a
rip-relative load that points at an encrypted string is decoded with the
client's own literal scheme. That is what makes "does this value index a table
or land in a store" readable at a glance.

Usage: python tools/dis_range.py <rva> [length] [--fn]
       --fn disassembles the whole .pdata function containing <rva>
"""
import sys
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_IMM, CS_OP_MEM, CS_OP_REG

MAX_LEN = 8192

pe = PE(default_exe())
base = pe.base
# The packed image's .text is also captured from a running client. The file at
# rest is readable too, but it is worth having both: the dump is what a probe
# actually executes, so a mismatch here would mean the file is not the code.
DUMP = pathlib.Path(__file__).resolve().parent.parent / 'runtime' / 'dump'
_dump = None
for cand in sorted(DUMP.glob('*-0x140001000-r-x.bin'), reverse=True) if DUMP.is_dir() else []:
    _dump = cand
    break
_dump_map = None
_dump_base = 0x140001000
if _dump is not None:
    import mmap
    _fh = open(_dump, 'rb')
    _dump_map = mmap.mmap(_fh.fileno(), 0, access=mmap.ACCESS_READ)


def read_rva(rva, size, from_dump=False):
    """Read code at an absolute virtual address (image base included)."""
    if from_dump:
        if _dump_map is None:
            return b''
        off = rva - _dump_base
        if 0 <= off < len(_dump_map):
            return bytes(_dump_map[off:off + size])
        return b''
    if rva >= base:
        rva -= base
    return pe.read(rva, size)


rva = int(sys.argv[1], 0)
length = MAX_LEN
for a in sys.argv[2:]:
    if a.isdigit() or a.startswith('0x'):
        length = int(a, 0)
if '--fn' in sys.argv:
    start, end, _ = pe.function(rva - base)
    rva, length = base + start, min(end - start, MAX_LEN)
length = max(1, min(length, MAX_LEN))

md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True


def addr_of(ins, rva_here, disp):
    return (rva_here + ins.size + disp) & 0xFFFFFFFFFFFFFFFF


print('range %#x..%#x (image base %#x%s)'
      % (rva, rva + length, base, ' runtime dump' if '--dump' in sys.argv else ''))
for ins in md.disasm(read_rva(rva, length, '--dump' in sys.argv), rva):
    here = ins.address
    note = ''
    for op in ins.operands:
        if op.type == CS_OP_MEM and op.mem.base == 0 and op.mem.index == 0:
            note += ' [abs %#x]' % (op.mem.disp & 0xFFFFFFFFFFFFFFFF)
        elif op.type == CS_OP_MEM and ins.reg_name(op.mem.base) == 'rip':
            tgt = addr_of(ins, here, op.mem.disp)
            note += ' [rip %#x]' % tgt
            if ins.mnemonic in ('lea', 'mov'):
                lit = pe.literal(tgt - base)
                if lit:
                    note += ' lit=%r' % lit[:60]
        elif op.type == CS_OP_IMM and ins.mnemonic in ('call', 'jmp'):
            tgt = op.imm & 0xFFFFFFFFFFFFFFFF
            try:
                fs, fe, _ = pe.function(tgt - base)
            except Exception:
                fs = tgt
            where = 'fn' if fs == tgt - base else 'fn+%#x' % (tgt - base - fs)
            note += ' -> %#x (%s)' % (tgt, where)
    print('%08x  %-8s %s%s' % (here, ins.mnemonic, ins.op_str, note))
