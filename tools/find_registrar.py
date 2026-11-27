# -*- coding: utf-8 -*-
"""Find the US100 client's command-handler registration sites.

Known handler function addresses come from the frida dispatcher tracer at
runtime. The registration call sites pass them as `lea r8, [rip+disp]`, so
scanning .text for rip-relative leas whose target equals a known handler
locates the registrars and the id constants next to them.

Usage:
  python find_registrar.py <DFO.exe> <id>=<handler_va> [...]
"""
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE


def scan_lea_targets(pe, targets, window=0x120):
    """Yield (file_offset, va, target) for lea reg,[rip+d] hitting targets."""
    hits = []
    for name, sva, vs, ra, rs in pe.sections:
        if name != '.text':
            continue
        blob = bytes(pe.data[ra:ra + rs])
        for code in (b'\x4c\x8d\x05', b'\x4c\x8d\x0d', b'\x4c\x8d\x15',
                     b'\x48\x8d\x05', b'\x48\x8d\x0d', b'\x48\x8d\x15'):
            start = 0
            while True:
                k = blob.find(code, start)
                if k < 0:
                    break
                start = k + 1
                if k + 7 > len(blob):
                    continue
                disp = struct.unpack_from('<i', blob, k + 3)[0]
                va = pe.base + sva + k
                target = va + 7 + disp
                if target in targets:
                    hits.append((ra + k, va, target))
    return hits


def disasm_window(pe, rva, before=0x60, after=0x40):
    from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_MEM
    from capstone.x86 import X86_REG_RIP
    md = Cs(CS_ARCH_X86, CS_MODE_64)
    md.detail = True
    begin, end, unwind = pe.function(rva)
    lo = max(begin, rva - before)
    data = pe.read(lo, min(end - lo, before + after))
    out = []
    for ins in md.disasm(data, pe.base + lo):
        here = ins.address - pe.base
        if here > rva + after:
            break
        note = ''
        for op in ins.operands:
            if op.type == CS_OP_MEM and op.mem.base == X86_REG_RIP:
                ref = ins.address + ins.size + op.mem.disp - pe.base
                note += ' [rva=%#x]' % ref
        mark = '  <== here' if here <= rva < here + ins.size else ''
        out.append('%08x %-22s %-8s %s%s%s' % (here, ins.bytes.hex(), ins.mnemonic, ins.op_str, note, mark))
    return out


def main():
    exe = sys.argv[1]
    known = {}
    for raw in sys.argv[2:]:
        i, v = raw.split('=')
        known[int(i, 0)] = int(v, 0)
    pe = PE(exe)
    targets = set(known.values())
    print('scanning .text for lea to %d known handler(s)...' % len(targets))
    hits = scan_lea_targets(pe, targets)
    print('hits: %d' % len(hits))
    sites = []
    for off, va, target in hits:
        rid = [k for k, v in known.items() if v == target]
        print('--- hit va %#x -> %#x (known id %s)' % (va, target, rid))
        for line in disasm_window(pe, va - pe.base):
            print('   ', line)
        sites.append((va, target))
    pe.close()


if __name__ == '__main__':
    main()
