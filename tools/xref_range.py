# -*- coding: utf-8 -*-
"""Find every instruction that references a range of addresses, and show the
containing function.

Generalises tools/xref_globals.py: that one looks for a few fixed globals with a
handful of instruction prefixes, which misses operand-size variants (`cmp byte
[rip+..]`, `movups [rip+..]`, xmm stores). This walks .text once with capstone and
checks the actual RIP-relative operand of every instruction, so a byte test and a
16-byte store are found the same way a `lea` is.

Usage: python tools/xref_range.py 0x149477580 0x149477598 [max_functions]
"""
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_MEM
from capstone.x86 import X86_REG_RIP

START = int(sys.argv[1], 0)
END = int(sys.argv[2], 0)
MAX_FUNCS = int(sys.argv[3]) if len(sys.argv) > 3 else 12

pe = PE(default_exe())
base = pe.base
text_va = text_ra = text_rs = None
for name, va, vsize, ra, rsize in pe.sections:
    if name == '.text':
        text_va, text_ra, text_rs = va, ra, rsize

md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True
md.skipdata = True

# The PE reader caps a single read at 64 KiB, so .text is walked in chunks with a
# 16-byte overlap: an instruction that starts near a boundary is decoded in full
# and only hits whose instruction *starts* inside the chunk are kept.
CHUNK = 64 * 1024
OVERLAP = 16
hits = []
chunks = 0
for offset in range(0, text_rs, CHUNK):
    span = min(CHUNK + OVERLAP, text_rs - offset)
    # The reader's own cap is 64 KiB, so the overlap is fetched separately and
    # appended: one read can never exceed the cap.
    body = pe.read(text_va + offset, min(CHUNK, text_rs - offset))
    if span > len(body):
        body += pe.read(text_va + offset + len(body), span - len(body))
    view = body
    limit = offset + CHUNK
    for ins in md.disasm(view, base + text_va + offset):
        start = ins.address - base - text_va
        if start >= limit:
            break
        # skipdata emits placeholder "data" instructions whose operands cannot be
        # inspected; they carry no reference anyway.
        if ins.id == 0 or ins.mnemonic == '.byte':
            continue
        for op in ins.operands:
            if op.type == CS_OP_MEM and op.mem.base == X86_REG_RIP:
                target = ins.address + ins.size + op.mem.disp
                if START <= target < END:
                    hits.append((ins.address - base, target, ins.mnemonic, ins.op_str,
                                 target - START))
    chunks += 1
    if chunks % 200 == 0:
        print('  scanned %.1f MB, hits so far %d' % (offset / 1024 / 1024, len(hits)), flush=True)

print('references to %#x..%#x: %d' % (START, END, len(hits)))
for va, target, mnemonic, op_str, offset in hits:
    print('  %010x  %-10s %-34s -> block+%d' % (va, mnemonic, op_str, offset))

shown = set()
print()
for va, target, _m, _o, _off in hits:
    begin, end, _ = pe.function(va)
    if begin in shown:
        continue
    shown.add(begin)
    if len(shown) > MAX_FUNCS:
        break
    print('=== function %#x (first reference at %#x) ===' % (base + begin, base + va))
    count = 0
    for ins in md.disasm(pe.read(begin, min(end - begin, 0x300)), base + begin):
        note = ''
        for op in ins.operands:
            if op.type == CS_OP_MEM and op.mem.base == X86_REG_RIP:
                ref = ins.address + ins.size + op.mem.disp
                if START <= ref < END:
                    note = '   <-- block+%d' % (ref - START)
                else:
                    note = ' [va=%#x]' % ref
        print('  %08x %-24s %-9s %s%s' % (ins.address - base, ins.bytes.hex(),
                                          ins.mnemonic, ins.op_str, note))
        count += 1
        if count > 40:
            print('  ...')
            break
pe.close()
