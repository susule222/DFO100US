# -*- coding: utf-8 -*-
"""Find code that stores/loads the stat source's table members +0x1330/+0x1428.

The int getter (0x143ed8e50) picks table [src+0x1428] when [src+0x1430]!=0,
else [src+0x1330]. Whoever WRITES those members owns the panel's data supply.
Byte-scan .text for the disp32 patterns and disassemble a window around each
hit, keeping only real [reg+disp] operand uses, grouped by kind (store to the
member itself / read of the member / use as base+disp).

Usage: python tools/find_table_writers.py
"""
import collections
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_MEM

BASE = 0x140000000
DISPS = {0x1330: 'base_table', 0x1428: 'mod_table', 0x1430: 'mod_flag'}
WINDOW = 24

pe = PE(default_exe())
text = None
for name, va, vs, ra, rs in pe.sections:
    if name == '.text':
        text = (va, vs)
tva, tvs = text

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


md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True

hits = collections.defaultdict(list)
for disp, label in DISPS.items():
    pat = struct.pack('<I', disp)
    CH = 0x10000
    for off in range(0, tvs, CH):
        chunk = pe.read(tva + off, min(CH, tvs - off))
        at = 0
        while True:
            k = chunk.find(pat, at)
            if k < 0:
                break
            at = k + 1
            # the disp32 must end at or before the chunk end; window disasm
            site = tva + off + k
            start = max(tva, site - WINDOW)
            code = pe.read(start, min(WINDOW + 8, tvs - start))
            for ins in md.disasm(code, BASE + start):
                found = False
                for opd in ins.operands:
                    if opd.type == CS_OP_MEM and opd.mem.disp == disp:
                        found = True
                if not found:
                    continue
                is_store = (ins.mnemonic.startswith('mov') or ins.mnemonic.startswith('lea')) and \
                           ins.operands[0].type == CS_OP_MEM and ins.operands[0].mem.disp == disp
                is_lea = ins.mnemonic == 'lea' and ins.operands[1].type == CS_OP_MEM and ins.operands[1].mem.disp == disp
                if is_store:
                    kind = 'STORE'
                elif is_lea:
                    kind = 'LEA'   # address taken: map object passed somewhere
                else:
                    continue       # plain reads: the getters, already known
                f = fn_of(ins.address - BASE)
                hits[(label, kind)].append((ins.address, f, ins.mnemonic, ins.op_str))
print('functions storing / taking address of the table members:')
for (label, kind), rows in hits.items():
    uniq = sorted(set((f or 0, a, m, o) for a, f, m, o in rows))
    print('\n== %s %s (%d sites) ==' % (label, kind, len(rows)))
    shown = collections.Counter()
    for f, a, m, o in uniq:
        if shown[f] < 2:
            print('  %#x in fn %#x: %s %s' % (a, f or 0, m, o))
            shown[f] += 1
