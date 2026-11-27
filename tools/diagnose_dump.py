"""Bounded minidump reader. Seeks into dumps; never loads full process memory.

Usage: python diagnose_dump.py <dump> [<dump> ...]
The stack output is a list of pointer candidates, not a claimed unwound stack.
"""
from __future__ import annotations

import argparse
import bisect
import json
import pathlib
import struct
import sys


class Dump:
    def __init__(self, path):
        self.path = pathlib.Path(path)
        self.f = self.path.open("rb")
        self.size = self.path.stat().st_size
        signature, version, count, directory, _, self.timestamp, self.flags = self.unpack("<IIIIIIQ", 0)
        if signature != 0x504D444D or count > 256:
            raise ValueError("Not a supported minidump")
        self.streams = {}
        for i in range(count):
            kind, size, rva = self.unpack("<III", directory + i * 12)
            self.streams[kind] = (size, rva)
        self.modules = []
        if 4 in self.streams:
            _, pos = self.streams[4]
            count, = self.unpack("<I", pos)
            if count > 10000:
                raise ValueError("Invalid module count")
            for i in range(count):
                base, size, _, timestamp, name_rva = self.unpack("<QIIII", pos + 4 + i * 108)
                length, = self.unpack("<I", name_rva)
                name = self.read(name_rva + 4, min(length, 4096)).decode("utf-16-le", "replace")
                self.modules.append((base, base + size, name, timestamp))
        self.modules.sort()
        self.ranges = []
        if 9 in self.streams:
            _, pos = self.streams[9]
            count, cursor = self.unpack("<QQ", pos)
            if count > 1000000:
                raise ValueError("Invalid memory64 count")
            for i in range(count):
                start, length = self.unpack("<QQ", pos + 16 + 16 * i)
                self.ranges.append((start, start + length, cursor))
                cursor += length
        elif 5 in self.streams:
            _, pos = self.streams[5]
            count, = self.unpack("<I", pos)
            if count > 1000000:
                raise ValueError("Invalid memory count")
            for i in range(count):
                start, length, rva = self.unpack("<QII", pos + 4 + 16 * i)
                self.ranges.append((start, start + length, rva))
        self.ranges.sort()
        self.starts = [row[0] for row in self.ranges]

    def read(self, offset, size):
        if size < 0 or size > 65536 or offset < 0 or offset + size > self.size:
            raise ValueError(f"Invalid/big read offset={offset:#x} size={size}")
        self.f.seek(offset)
        raw = self.f.read(size)
        if len(raw) != size:
            raise ValueError("Truncated dump")
        return raw

    def unpack(self, fmt, offset):
        return struct.unpack(fmt, self.read(offset, struct.calcsize(fmt)))

    def memory(self, address, size):
        idx = bisect.bisect_right(self.starts, address) - 1
        if idx < 0:
            return b""
        start, end, rva = self.ranges[idx]
        if not start <= address < end:
            return b""
        return self.read(rva + address - start, min(size, end - address, 65536))

    def location(self, address):
        for start, end, path, timestamp in self.modules:
            if start <= address < end:
                return f"{pathlib.PureWindowsPath(path).name}+0x{address - start:x}"
        return hex(address)

    def unwind(self, registers):
        """Unwind ordinary AMD64 frames using each image's .pdata/.xdata.

        Stops on unsupported metadata rather than inventing a stack frame.
        """
        from inspect_pe import PE
        order = "rax rcx rdx rbx rsp rbp rsi rdi r8 r9 r10 r11 r12 r13 r14 r15".split()
        images = {}
        regs = dict(registers)
        frames = []
        def qword(address):
            data = self.memory(address, 8)
            if len(data) != 8:
                raise ValueError(f"Stack memory absent at {address:#x}")
            return struct.unpack("<Q", data)[0]
        def apply(info, image, control_offset, depth=0):
            if depth > 8:
                raise ValueError("Invalid chained unwind")
            raw = image.read(info, 4)
            version_flags, prologue, count, frame = raw
            if version_flags & 7 not in (1, 2):
                raise ValueError("Unsupported unwind version")
            codes = image.read(info + 4, ((count + 1) & ~1) * 2 + 12)
            i = 0
            while i < count:
                offset, packed = codes[2*i:2*i+2]
                op, value = packed & 15, packed >> 4
                slots = 1
                if op in (4, 8) or op == 1 and value == 0:
                    slots = 2
                elif op in (5, 9) or op == 1 and value == 1:
                    slots = 3
                if control_offset < prologue and offset > control_offset:
                    i += slots
                    continue
                if op == 0:
                    regs[order[value]] = qword(regs["rsp"])
                    regs["rsp"] += 8
                elif op == 1:
                    size = struct.unpack_from("<H" if value == 0 else "<I", codes, 2*i + 2)[0]
                    regs["rsp"] += size * 8 if value == 0 else size
                elif op == 2:
                    regs["rsp"] += value * 8 + 8
                elif op == 3:
                    regs["rsp"] = regs[order[frame & 15]] - (frame >> 4) * 16
                elif op in (4, 5):
                    size = struct.unpack_from("<H" if op == 4 else "<I", codes, 2*i + 2)[0]
                    regs[order[value]] = qword(regs["rsp"] + (size * 8 if op == 4 else size))
                elif op in (8, 9):
                    pass  # XMM values are not needed for stack unwinding.
                else:
                    raise ValueError(f"Unsupported unwind opcode {op}")
                i += slots
            if version_flags >> 3 & 4:
                begin, end, chained = struct.unpack_from("<III", codes, ((count + 1) & ~1) * 2)
                apply(chained, image, end - begin, depth + 1)
        try:
            for _ in range(32):
                rip, sp = regs["rip"], regs["rsp"]
                if rip == 0:
                    break
                frames.append({"location": self.location(rip), "rsp": hex(sp)})
                mod = next((m for m in self.modules if m[0] <= rip < m[1]), None)
                if mod is None:
                    break
                base, _, path, _ = mod
                image = images.get(path)
                if image is None:
                    image = images[path] = PE(path)
                begin, end, info = image.function(rip - base)
                if info:
                    apply(info, image, rip - base - begin)
                regs["rip"] = qword(regs["rsp"])
                regs["rsp"] += 8
                if regs["rsp"] <= sp:
                    raise ValueError("Non-increasing stack pointer")
        except (ValueError, OSError, struct.error, IndexError) as exc:
            frames.append({"stopped": str(exc)})
        finally:
            for image in images.values():
                image.close()
        return frames

    def report(self):
        result = {"dump": str(self.path), "file_mib": round(self.size / 1048576, 2),
                  "timestamp": self.timestamp, "flags": hex(self.flags)}
        if 6 not in self.streams:
            return result
        _, pos = self.streams[6]
        thread, _, code, flags, record, address, nparams, _ = self.unpack("<IIIIQQII", pos)
        params = list(self.unpack("<15Q", pos + 40))[:nparams]
        context_size, context_rva = self.unpack("<II", pos + 160)
        context = self.read(context_rva, min(context_size, 4096))
        names = "rax rcx rdx rbx rsp rbp rsi rdi r8 r9 r10 r11 r12 r13 r14 r15 rip".split()
        regs = {name: struct.unpack_from("<Q", context, 0x78 + 8 * i)[0] for i, name in enumerate(names)}
        result.update(thread=thread, exception=hex(code), location=self.location(address),
                      parameters=[hex(x) for x in params], registers={k: hex(v) for k, v in regs.items()})
        result["unwound_stack"] = self.unwind(regs)
        stack = self.memory(regs["rsp"], 4096)
        pointers = []
        for i in range(0, len(stack) - 7, 8):
            value, = struct.unpack_from("<Q", stack, i)
            loc = self.location(value)
            if "+0x" in loc:
                pointers.append({"rsp_offset": hex(i), "target": loc})
        result["stack_pointer_candidates"] = pointers[:60]
        result["register_memory"] = {k: self.memory(v, 64).hex(" ") for k, v in regs.items()
                                     if k in ("rcx", "rdx", "rax", "rbx", "rsi", "rdi", "rbp")}
        try:
            from capstone import Cs, CS_ARCH_X86, CS_MODE_64
            dis = Cs(CS_ARCH_X86, CS_MODE_64)
            raw = self.memory(regs["rip"], 128)
            result["fault_instructions"] = [f"{self.location(i.address)}: {i.mnemonic} {i.op_str}"
                                            for i in dis.disasm(raw, regs["rip"])]
        except ImportError:
            pass
        result["module_count"] = len(self.modules)
        result["client_modules"] = [{"path": p, "base": hex(a), "size": b - a, "timestamp": t}
                                    for a, b, p, t in self.modules
                                    if pathlib.PureWindowsPath(p).name.lower() in ("dfo.exe", "ngclient64.aes")]
        return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("dumps", nargs="+")
    parser.add_argument("--out", type=pathlib.Path)
    args = parser.parse_args()
    results = []
    for path in args.dumps:
        dump = Dump(path)
        try:
            results.append(dump.report())
        finally:
            dump.f.close()
    text = json.dumps(results, ensure_ascii=False, indent=2)
    if args.out:
        args.out.write_text(text, encoding="utf-8")
    print(text)


if __name__ == "__main__":
    if sys.stdout is not None:
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    main()
