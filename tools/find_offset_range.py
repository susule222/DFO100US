# -*- coding: utf-8 -*-
"""Find instructions whose displacement falls inside a struct-offset range.

tools/find_offset_refs.py looks for one exact displacement; recovering a whole
struct needs the range, because a field map is exactly "which offsets are touched".
This scans .text once for the instruction prefixes that can carry a displacement
and keeps any whose value lies in [start, end).

Bounded: one pass over .text with a byte-prefix filter, hits capped.

Usage: python tools/find_offset_range.py 0x7f2 0x81f [max_per_offset]
"""
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64

START = int(sys.argv[1], 0)
END = int(sys.argv[2], 0)
CAP = int(sys.argv[3]) if len(sys.argv) > 3 else 4

pe = PE(default_exe())
base = pe.base
md = Cs(CS_ARCH_X86, CS_MODE_64)

# Every form that can encode a disp32: lea/mov/cmp/add/... with ModRM mod=10.
PREFIXES = [bytes([p0, p1, modrm]) for p0, p1 in (
    (0x48, 0x8d),  # lea r64
    (0x4c, 0x8d),  # lea r64 (extended)
    (0x48, 0x8b),  # mov r64
    (0x4c, 0x8b),
    (0x48, 0x89),  # mov r/m64, r64
    (0x4c, 0x89),
    (0x48, 0x39),  # cmp
    (0x48, 0x3b),
    (0x48, 0x03),  # add
    (0x0f, 0x11),  # movups/movss stores
    (0x0f, 0x10),  # movups loads
    (0x80, 0x3d),  # cmp byte [rip+disp], imm8  (handled separately, disp at +2)
) for modrm in range(0x80, 0xc0)]

hits = {}
text_va = text_ra = text_rs = None
for name, va, vsize, ra, rsize in pe.sections:
    if name == '.text':
        text_va, text_ra, text_rs = va, ra, rsize

CHUNK = 64 * 1024
for offset in range(0, text_rs, CHUNK):
    body = pe.read(text_va + offset, min(CHUNK, text_rs - offset))
    for prefix in PREFIXES:
        start = 0
        while True:
            k = body.find(prefix, start)
            if k < 0:
                break
            start = k + 1
            if k + 7 > len(body):
                continue
            disp = struct.unpack_from('<i', body, k + 3)[0]
            if not (START <= disp < END):
                continue
            va = base + text_va + offset + k
            insns = list(md.disasm(body[k:k + 12], va))
            if not insns:
                continue
            ins = insns[0]
            hits.setdefault(disp, []).append('%010x %s %s' % (va - base, ins.mnemonic, ins.op_str))

print('offsets touched in %#x..%#x: %d' % (START, END, len(hits)))
for disp in sorted(hits):
    rows = hits[disp]
    print('  +%#05x  %d reference(s)' % (disp, len(rows)))
    for row in rows[:CAP]:
        print('        %s' % row)
    if len(rows) > CAP:
        print('        ... %d more' % (len(rows) - CAP))
pe.close()
