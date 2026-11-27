# -*- coding: utf-8 -*-
"""Find rip-relative references to given VAs in a PE's executable sections.

Read-only. Reports the referencing instruction and the containing function, so
a loader/decoder can be located from a data address.

Usage: python xref.py <exe> <va> [<va> ...]
"""
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE

from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_MEM
from capstone.x86 import X86_REG_RIP

PREFIXES = (b'\x48\x8d\x05', b'\x48\x8d\x0d', b'\x48\x8d\x15', b'\x48\x8d\x1d',
            b'\x48\x8d\x35', b'\x48\x8d\x3d', b'\x4c\x8d\x05', b'\x4c\x8d\x0d',
            b'\x4c\x8d\x15', b'\x4c\x8d\x1d', b'\x4c\x8d\x35', b'\x4c\x8d\x3d',
            b'\x48\x8b\x05', b'\x48\x8b\x0d', b'\x48\x8b\x15', b'\x4c\x8b\x05')


def main():
    pe = PE(sys.argv[1])
    targets = {int(a, 0): a for a in sys.argv[2:]}
    print('targets:', [hex(t) for t in targets])
    for name, sva, vs, ra, rs in pe.sections:
        if name != '.text':
            continue
        blob = bytes(pe.data[ra:ra + rs])
        for pfx in PREFIXES:
            start = 0
            while True:
                k = blob.find(pfx, start)
                if k < 0:
                    break
                start = k + 1
                if k + 7 > len(blob):
                    continue
                disp = struct.unpack_from('<i', blob, k + 3)[0]
                va = pe.base + sva + k
                tgt = va + 7 + disp
                if tgt in targets:
                    begin, end, _ = pe.function(va - pe.base)
                    print('  ref at %#x (func %#x) -> %#x  bytes=%s' % (
                        va, pe.base + begin, tgt, blob[k:k + 7].hex()))
    pe.close()


if __name__ == '__main__':
    main()
