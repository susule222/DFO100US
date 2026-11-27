# -*- coding: utf-8 -*-
"""Show which functions embed a 32-bit immediate, filtered by the .pdata
functions that contain them, with context.

find_immediate.py answers "where does the constant appear"; this adds the two
questions that actually matter when hunting a registration: what the
surrounding instructions do, and whether the function also calls a given
target (the register call).

Usage: python tools/imm_context.py <value> [--call 0xTARGET] [--max 20]
"""
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_IMM

pe = PE(default_exe())
value = int(sys.argv[1], 0)
call_target = None
maxshow = 20
for i, a in enumerate(sys.argv):
    if a == '--call' and i + 1 < len(sys.argv):
        call_target = int(sys.argv[i + 1], 0)
    if a == '--max' and i + 1 < len(sys.argv):
        maxshow = int(sys.argv[i + 1], 0)

needle = struct.pack('<I', value)
md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True

hits = 0
step = 0x10000
for base_rva in range(0, 0x649f000, step):
    blob = pe.read(base_rva, min(step, 0x649f000 - base_rva))
    at = 0
    while hits < maxshow:
        k = blob.find(needle, at)
        if k < 0:
            break
        at = k + 1
        rva = base_rva + k
        begin, end, _ = pe.function(rva)
        # Context: 24 bytes before and after the immediate.
        lo = max(begin, rva - 24)
        ctx = pe.read(lo, min(64, end - lo))
        has_call = False
        for ins in md.disasm(ctx, 0x140000000 + lo):
            if ins.mnemonic == 'call' and ins.operands and ins.operands[0].type == CS_OP_IMM:
                if call_target is None or ins.operands[0].imm == call_target:
                    has_call = True
        if call_target is not None and not has_call:
            continue
        hits += 1
        print('imm %#x at rva %08x in func %08x (size %d)' % (value, rva, begin, end - begin))
        for ins in md.disasm(ctx, 0x140000000 + lo):
            mark = ' <<<' if (ins.address - 0x140000000) <= rva < (ins.address - 0x140000000 + ins.size) else ''
            print('   %08x  %-8s %s%s' % (ins.address - 0x140000000, ins.mnemonic, ins.op_str, mark))
        print()
pe.close()
