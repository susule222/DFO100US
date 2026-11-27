# -*- coding: utf-8 -*-
"""Recover the US100 client's full command-handler registration table.

Approach: the registrars found by find_registrar.py are called once per
command with (rcx=manager, edx=id, r8=handler, r9=0). Scanning every call to
them and decoding the preceding instructions yields id -> handler for the
whole build, statically and without running the client.

Usage:
  python scan_registrations.py <DFO.exe> <registrar_va> [...] [-o out.json]
"""
import argparse
import collections
import json
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_IMM, CS_OP_MEM, CS_OP_REG
from capstone.x86 import X86_REG_RIP

REG64 = {}


def text_section(pe):
    for name, sva, vs, ra, rs in pe.sections:
        if name == '.text':
            return sva, ra, rs
    raise SystemExit('no .text')


def find_call_sites(pe, targets):
    sva, ra, rs = text_section(pe)
    blob = bytes(pe.data[ra:ra + rs])
    sites = collections.defaultdict(list)
    start = 0
    while True:
        k = blob.find(b'\xe8', start)
        if k < 0:
            break
        start = k + 1
        if k + 5 > len(blob):
            continue
        rel = struct.unpack_from('<i', blob, k + 1)[0]
        va = pe.base + sva + k
        tgt = va + 5 + rel
        if tgt in targets:
            sites[tgt].append(va)
    return sites


def reg_write_value(ins, reg_state):
    """Return the immediate the instruction writes to a 32/64-bit reg, if known."""
    if len(ins.operands) != 2:
        return None
    dst, src = ins.operands
    if dst.type != CS_OP_REG:
        return None
    name = ins.op_str.split(',')[0].strip()
    if dst.size == 4:
        name = name.replace('r8d', 'r8').replace('r9d', 'r9').replace('r10d', 'r10')
        name = name.replace('edx', 'rdx').replace('ecx', 'rcx')
    if ins.mnemonic == 'mov' and src.type == CS_OP_IMM:
        reg_state[name] = src.imm & 0xffffffff
        return src.imm
    if ins.mnemonic == 'xor' and src.type == CS_OP_REG and ins.op_str.split(',')[0].strip() == ins.op_str.split(',')[1].strip():
        reg_state[name] = 0
        return 0
    if ins.mnemonic == 'lea' and src.type == CS_OP_MEM and src.mem.base == X86_REG_RIP:
        reg_state[name] = ins.address + ins.size + src.mem.disp
        return reg_state[name]
    if ins.mnemonic == 'mov' and src.type == CS_OP_REG:
        s = ins.op_str.split(',')[1].strip()
        s = s.replace('r8d', 'r8').replace('r9d', 'r9').replace('r10d', 'r10')
        s = s.replace('edx', 'rdx').replace('ecx', 'rcx')
        if s in reg_state:
            reg_state[name] = reg_state[s]
            return reg_state[name]
    return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('exe')
    ap.add_argument('registrars', nargs='+', type=lambda s: int(s, 0))
    ap.add_argument('-o', '--out', default='')
    args = ap.parse_args()

    pe = PE(args.exe)
    targets = set(args.registrars)
    print('scanning for calls to %s' % [hex(t) for t in sorted(targets)])
    sites = find_call_sites(pe, targets)
    total = sum(len(v) for v in sites.values())
    print('call sites: %d %s' % (total, {hex(k): len(v) for k, v in sites.items()}))

    md = Cs(CS_ARCH_X86, CS_MODE_64)
    md.detail = True

    by_fn = collections.defaultdict(list)
    for tgt, vas in sites.items():
        for va in vas:
            begin, end, _ = pe.function(va - pe.base)
            by_fn[(begin, end)].append(va)

    rows = []
    for (begin, end), calls in sorted(by_fn.items()):
        calls = sorted(set(calls))
        want = set(calls)
        data = pe.read(begin, min(end - begin, 4 << 20))
        reg_state = {}
        last = None  # (mnemonic, dst_name, src_text)
        for ins in md.disasm(data, pe.base + begin):
            op = ins.op_str
            if ins.mnemonic in ('mov', 'lea', 'xor'):
                reg_write_value(ins, reg_state)
            if ('edx' in op or 'r8' in op or 'rcx' in op or 'r9' in op) and ins.mnemonic in ('mov', 'lea', 'xor'):
                last = ins
            if ins.mnemonic == 'call' and ins.address in want:
                id_val = None
                id_src = ''
                # Walk back the last few instructions we recorded is fragile;
                # instead re-read from the register snapshot by re-decoding a
                # short window ending at the call.
                win = []
                lo = max(begin, ins.address - pe.base - 0x60)
                for w in md.disasm(pe.read(lo, ins.address - pe.base - lo), pe.base + lo):
                    win.append(w)
                for w in reversed(win):
                    if w.mnemonic in ('mov', 'lea') and w.op_str.split(',')[0].strip() == 'edx':
                        if w.operands[1].type == CS_OP_IMM:
                            id_val = w.operands[1].imm & 0xffff
                            id_src = 'imm'
                        elif w.operands[1].type == CS_OP_MEM:
                            op2 = w.operands[1]
                            base = w.reg_name(op2.mem.base) if op2.mem.base else ''
                            idx = w.reg_name(op2.mem.index) if op2.mem.index else ''
                            key = (base or idx).replace('d', '') if (base or idx) else ''
                            key = {'r9': 'r9', 'r8': 'r8', 'rcx': 'rcx', 'rax': 'rax'}.get(key, key)
                            if key in reg_state:
                                id_val = (reg_state[key] + op2.mem.disp) & 0xffff
                                id_src = 'reg+disp'
                            else:
                                id_src = 'unknown reg %s disp %#x' % (key, op2.mem.disp)
                        break
                handler = None
                for w in reversed(win):
                    if w.mnemonic == 'lea' and len(w.operands) == 2 and w.op_str.startswith('r8,'):
                        op2 = w.operands[1]
                        if op2.type == CS_OP_MEM and op2.mem.base == X86_REG_RIP:
                            handler = w.address + w.size + op2.mem.disp
                            break
                        if op2.type == CS_OP_IMM:
                            handler = op2.imm
                            break
                rows.append({'id': id_val, 'id_src': id_src, 'handler': hex(handler) if handler else None,
                             'call_va': hex(ins.address)})
        if len(rows) > 100000:
            break

    seen = {}
    for r in rows:
        if r['id'] is not None and r['id'] not in seen:
            seen[r['id']] = r
    print('call sites decoded: %d, distinct ids: %d' % (len(rows), len(seen)))
    for k in sorted(seen):
        r = seen[k]
        print('  id=%-6d handler=%s (%s)' % (k, r['handler'], r['id_src']))
    unknown = [r for r in rows if r['id'] is None]
    print('undecoded id count: %d' % len(unknown))
    if args.out:
        pathlib.Path(args.out).write_text(json.dumps(sorted(rows, key=lambda r: (r['id'] is None, r['id'] or 0)), indent=2), encoding='utf-8')
        print('wrote', args.out)
    pe.close()


if __name__ == '__main__':
    main()
