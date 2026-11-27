# -*- coding: utf-8 -*-
"""Full static layout report for one or more client commands.

For every id it prints:
  * every recovered handler address,
  * the handler's wire-read sequence (kind, size, cumulative offset),
  * every debug literal the handler loads (the names things are logged with),
which together give the packet's real field order and, usually, its meaning.

Usage: python tools/packet_layout.py 15 16 27 28 29 [window_hex]
"""
import json
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_IMM, CS_OP_MEM
from capstone.x86 import X86_REG_RIP

ROOT = pathlib.Path(__file__).resolve().parent.parent
EXE = default_exe()
WINDOW = int(sys.argv[-1], 0) if sys.argv[-1].startswith('0x') else 0x400
IDS = [int(a, 0) for a in sys.argv[1:] if not a.startswith('0x')]

READERS = {
    0x144F2BA70: ('u8', 1),
    0x144F2C840: ('u16', 2),
    0x144F2BC20: ('u32', 4),
    0x144F2BC60: ('raw', None),
    0x144F176C0: ('string', None),
}
LITERAL_GETTER = 0x144F16830

rows = json.loads((ROOT / 'runtime' / 'us100_registrations.json').read_text(encoding='utf-8'))
by_id = {}
for r in rows:
    try:
        by_id.setdefault(int(r['id']), []).append(r)
    except (KeyError, TypeError, ValueError):
        continue

pe = PE(EXE)
base = pe.base
md = Cs(CS_ARCH_X86, CS_MODE_64)
md.detail = True


def report(pid):
    print('=== id %d ===' % pid)
    entries = by_id.get(pid, [])
    if not entries:
        print('  (no registration)')
        return
    for r in entries:
        handler = int(r['handler'], 0)
        rva = handler - base
        print('  handler %#x (rva %#x, src %s)' % (handler, rva, r.get('id_src')))
        blob = pe.read(rva, WINDOW)
        offset = 0
        last_edx = None
        pending_literal = False
        for ins in md.disasm(blob, handler):
            m = ins.mnemonic
            if m == 'mov' and ins.op_str.startswith('edx, '):
                try:
                    last_edx = int(ins.op_str.split(', ')[1], 0)
                except ValueError:
                    last_edx = None
            elif m == 'lea' and ins.op_str.startswith('edx, '):
                rhs = ins.op_str.split(', ')[1]
                try:
                    last_edx = int(rhs.split('+')[-1].rstrip(']'), 0) if '+' in rhs else None
                except ValueError:
                    last_edx = None
            elif m == 'xor' and ins.op_str == 'edx, edx':
                last_edx = 0
            if m == 'lea' and ins.op_str.startswith('rcx, '):
                for op in ins.operands:
                    if op.type == CS_OP_MEM and op.mem.base == X86_REG_RIP:
                        text = pe.literal(ins.address + ins.size + op.mem.disp - base)
                        if text:
                            print('    literal: %r' % text[:90])
                            break
            if m == 'call' and len(ins.operands) == 1 and ins.operands[0].type == CS_OP_IMM:
                tgt = ins.operands[0].imm
                if tgt in READERS:
                    kind, size = READERS[tgt]
                    if size is None:
                        size = last_edx
                    print('    read %-6s off=%-5d size=%s' % (kind, offset, size))
                    if isinstance(size, int):
                        offset += size
    print()


for pid in IDS:
    report(pid)
pe.close()
