# -*- coding: utf-8 -*-
"""Compare the runtime memory dump against the on-disk DFO.exe section by
section. Identifies which sections Themida changes at runtime, especially
whether .text is swapped (which would invalidate static disassembly)."""
import hashlib
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE

DUMP = pathlib.Path(r'C:\Users\Li_P\AppData\Local\Temp\DFO_memory_dump.bin')
EXE = pathlib.Path(r'D:\DNF\client\US100\DFO.exe')


def sha(b):
    return hashlib.sha256(b).hexdigest()[:16]


def main():
    pe = PE(str(EXE))
    dump = DUMP.read_bytes()
    print('dump size: %d, file size: %d' % (len(dump), EXE.stat().st_size))
    for name, sva, vs, ra, rs in pe.sections:
        file_bytes = bytes(pe.data[ra:ra + rs])
        # Runtime image is aligned: bytes at RVA sva, size = max(vs, rs)
        rva_end = sva + max(vs, rs)
        mem_bytes = dump[sva:rva_end] if rva_end <= len(dump) else dump[sva:]
        mem_rs = mem_bytes[:rs]
        same = mem_rs == file_bytes
        # Also compare virtual-size rounded region
        print('%-8s rva=%#x vs=%#x rs=%#x  raw-equal=%s  file=%s  mem=%s' % (
            name, sva, vs, rs, same, sha(file_bytes), sha(mem_rs)))
        if not same and len(file_bytes) == len(mem_rs):
            # Find first differing offset
            for i in range(min(len(file_bytes), len(mem_rs))):
                if file_bytes[i] != mem_rs[i]:
                    print('    first diff at +%#x (rva %#x): file=%02x mem=%02x' % (
                        i, sva + i, file_bytes[i], mem_rs[i]))
                    break
    pe.close()


if __name__ == '__main__':
    main()
