# -*- coding: utf-8 -*-
"""Find every writer of the panel object's secure-field array (+0x30c, stride 8).

The panel getters (0x143c00f85 family) decode fields at obj+0x30c+8k with the
guard/repair pattern. This scan enumerates EVERY direct call to the encode
writer 0x144f1f570 in .text, disassembles a small window before each call and
records how rdx (the field pointer) was formed. Call sites whose rdx is
`lea rdx,[reg+disp]` with disp in the panel array's window name the functions
that fill the panel — the US100 counterpart of 115's expansion.

Usage: python tools/find_field_writers.py [lo_disp hi_disp]
"""
import collections
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_MEM, CS_OP_REG

TARGET = 0x144f1f570
BASE = 0x140000000
LO = int(sys.argv[1], 0) if len(sys.argv) > 1 else 0x2c0
HI = int(sys.argv[2], 0) if len(sys.argv) > 2 else 0x3a0
WINDOW = 40  # bytes before the call to look for the rdx setup

pe = PE(default_exe())

# .pdata: function bounds, for naming the writer function of each call site.
bounds = []
n = pe.pdata_size // 12
for i in range(n):
    begin, end, _ = struct.unpack('<III', pe.read(pe.pdata + i * 12, 12))
    if end > begin and end - begin < 0x200000:
        bounds.append((begin, end))
bounds.sort()

import bisect
starts = [b for b, _ in bounds]


def fn_of(rva):
    i = bisect.bisect_right(starts, rva) - 1
    if i >= 0 and bounds[i][0] <= rva < bounds[i][1]:
        return BASE + bounds[i][0]
    return None


# Collect every E8 call to the encoder across .text (byte scan, chunked).
text = None
for name, va, vs, ra, rs in pe.sections:
    if name == '.text':
        text = (va, vs, ra, rs)
tva, tvs, tra, trs = text
sites = []
CH = 0x10000  # inspect_pe caps reads at 64 KiB
for off in range(0, tvs, CH):
    chunk = pe.read(tva + off, min(CH, tvs - off))
    at = 0
    while True:
        k = chunk.find(b'\xe8', at)
        if k < 0 or k + 5 > len(chunk):
            break
        at = k + 1
        rel = struct.unpack_from('<i', chunk, k + 1)[0]
        site_rva = tva + off + k
        if BASE + site_rva + 5 + rel == TARGET:
            sites.append(site_rva)
print('encoder call sites in .text: %d' % len(sites))

md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True

hits = collections.defaultdict(list)  # disp -> [(site_va, fn_va, op_str)]
disp_hist = collections.Counter()
for site in sites:
    start = site - WINDOW
    code = pe.read(start, WINDOW)
    last_rdx = None
    for ins in md.disasm(code, BASE + start):
        if ins.address >= BASE + site:
            break
        if ins.operands:
            d = ins.operands[0]
            if d.type == CS_OP_REG and ins.reg_name(d.reg) in ('rdx', 'edx', 'dx', 'dl'):
                disp = None
                if ins.mnemonic == 'lea' and len(ins.operands) > 1 and ins.operands[1].type == CS_OP_MEM:
                    disp = ins.operands[1].mem.disp
                last_rdx = (ins.mnemonic, ins.op_str, disp)
    if last_rdx and last_rdx[2] is not None:
        disp_hist[last_rdx[2]] += 1
        if LO <= last_rdx[2] <= HI:
            hits[last_rdx[2]].append((BASE + site, fn_of(site), last_rdx[1]))

print('\nfield-ptr displacements in window %#x..%#x:' % (LO, HI))
for disp in sorted(hits):
    rows = hits[disp]
    fns = sorted(set(f for _, f, _ in rows if f))
    print('  +%-8s %d site(s)  functions: %s' % ('%#x' % disp, len(rows), ', '.join('%#x' % f for f in fns)))

print('\ntop displacements overall (any range):')
for disp, cnt in disp_hist.most_common(20):
    print('  +%-10s %d' % ('%#x' % disp, cnt))
