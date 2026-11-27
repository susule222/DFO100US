"""Small, mmap-backed PE/disassembly helper for this client's crash analysis."""
from __future__ import annotations

import argparse
import mmap
import os
import pathlib
import struct
import sys

# The at-rest client may be absent (it was removed from D:\DNF\client\US100 on
# 2026-09-23); the unpacked memory image rebuilt by tools/dump_unpacked_pe.py is
# the fallback for all static analysis. DFO_EXE overrides both.
CLIENT_EXE = r'D:\DNF\client\US100\DFO.exe'
UNPACKED_EXE = str(pathlib.Path(__file__).resolve().parent.parent
                   / 'runtime' / 'dump' / 'DFO_unpacked.exe')


def default_exe() -> str:
    env = os.environ.get('DFO_EXE')
    if env and pathlib.Path(env).is_file():
        return env
    if pathlib.Path(CLIENT_EXE).is_file():
        return CLIENT_EXE
    return UNPACKED_EXE


class PE:
    def __init__(self, path):
        self.f = open(path, "rb")
        self.data = mmap.mmap(self.f.fileno(), 0, access=mmap.ACCESS_READ)
        pe, = struct.unpack_from("<I", self.data, 0x3C)
        machine, nsect = struct.unpack_from("<HH", self.data, pe + 4)
        self.machine = machine
        self.timestamp, = struct.unpack_from("<I", self.data, pe + 8)
        opt = pe + 24
        magic, = struct.unpack_from("<H", self.data, opt)
        self.base, = struct.unpack_from("<Q" if magic == 0x20B else "<I", self.data, opt + (24 if magic == 0x20B else 28))
        self.image_size, = struct.unpack_from("<I", self.data, opt + 56)
        dd = opt + (112 if magic == 0x20B else 96)
        self.pdata, self.pdata_size = struct.unpack_from("<II", self.data, dd + 24)
        optsize, = struct.unpack_from("<H", self.data, pe + 20)
        self.sections = []
        for i in range(nsect):
            start = opt + optsize + i * 40
            name = bytes(self.data[start:start + 8]).rstrip(b"\0").decode("ascii", "replace")
            vs, va, rs, ra = struct.unpack_from("<IIII", self.data, start + 8)
            self.sections.append((name, va, vs, ra, rs))

    def close(self):
        self.data.close()
        self.f.close()

    def read(self, rva, size):
        if not 0 <= size <= 65536:
            raise ValueError("PE read must be <=64 KiB")
        for name, va, vs, ra, rs in self.sections:
            if va <= rva < va + rs:
                off = ra + rva - va
                return bytes(self.data[off:off + min(size, va + rs - rva)])
        return b""

    def function(self, rva):
        lo, hi = 0, self.pdata_size // 12
        while lo < hi:
            mid = (lo + hi) // 2
            start, end, unwind = struct.unpack("<III", self.read(self.pdata + mid * 12, 12))
            if start <= rva:
                lo = mid + 1
            else:
                hi = mid
        if lo:
            row = struct.unpack("<III", self.read(self.pdata + (lo - 1) * 12, 12))
            if row[0] <= rva < row[1]:
                return row
        return (rva, rva + 128, 0)

    def literal(self, rva):
        h = self.read(rva, 4)
        if len(h) != 4:
            return ""
        for width in (1, 2):
            try:
                x = h[1] & 0xFE
                n = (((x << 7) | x) ^ int.from_bytes(h[2:], "little")) * width
                if not 0 < n <= 4096:
                    continue
                raw = bytearray(self.read(rva + 4, n))
                if len(raw) != n:
                    continue
                key = x | 0x9A714CA0
                for i in range(0, n // 4 * 4, 4):
                    v = int.from_bytes(raw[i:i + 4], "little") ^ key
                    raw[i:i + 4] = v.to_bytes(4, "little")
                    key = (key * 0x1003F + v) & 0xFFFFFFFF
                for i in range(n // 4 * 4, n):
                    v = raw[i] ^ (key & 255)
                    raw[i] = v
                    key = (key * 0x107 + v) & 0xFFFFFFFF
                text = bytes(raw).decode("utf-8" if width == 1 else "utf-16-le").rstrip("\0")
                if text and sum(c.isprintable() or c in "\t\r\n" for c in text) / len(text) > 0.98:
                    return text[:400]
            except (ValueError, UnicodeError):
                pass
        return ""


def main():
    from capstone import Cs, CS_ARCH_X86, CS_MODE_64, CS_OP_MEM
    from capstone.x86 import X86_REG_RIP
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("exe")
    parser.add_argument("rvas", nargs="+")
    parser.add_argument("--window", type=lambda s: int(s, 0), default=256)
    parser.add_argument("--full", action="store_true")
    args = parser.parse_args()
    pe = PE(args.exe)
    md = Cs(CS_ARCH_X86, CS_MODE_64)
    md.detail = True
    try:
        for target in args.rvas:
            rva = int(target, 0)
            begin, end, unwind = pe.function(rva)
            print(f"RVA {rva:#x} function {begin:#x}..{end:#x} unwind={unwind:#x}")
            data = pe.read(begin, min(end - begin, 65536))
            for ins in md.disasm(data, pe.base + begin):
                here = ins.address - pe.base
                if not args.full and not rva - args.window <= here <= rva + args.window:
                    continue
                note = ""
                for op in ins.operands:
                    if op.type == CS_OP_MEM and op.mem.base == X86_REG_RIP:
                        ref = ins.address + ins.size + op.mem.disp - pe.base
                        note += f" [rva={ref:#x}]"
                        if ins.mnemonic == "lea":
                            text = pe.literal(ref)
                            if text:
                                note += " " + repr(text)
                print(f"{here:08x} {ins.bytes.hex():24s} {ins.mnemonic:8s} {ins.op_str}{note}")
    finally:
        pe.close()


if __name__ == "__main__":
    if sys.stdout is not None:
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    main()
