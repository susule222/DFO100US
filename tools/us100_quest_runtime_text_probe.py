"""Read-only check for the exact quest UI labels in an existing US100 process."""
import argparse
from collections import defaultdict
import ctypes as c
from ctypes import wintypes as w
from pathlib import Path


LABELS = ("[Main Quests]", "[Other Quests]", "Act Completed", "<Complete condition>",
          "View Optimal Level quests only", " In progress", "Lv. %d Quest",
          "Hendon Myre", "Grand Flores", "主线任务", "完成条件", "传说中的白化变异哥布林")
CLIENT = Path("D:/DNF/client/100US/DFO/DFO.exe")


class MemoryRange(c.Structure):
    _fields_ = [("base", c.c_void_p), ("allocation", c.c_void_p), ("allocation_flags", w.DWORD),
                ("partition", w.WORD), ("size", c.c_size_t), ("state", w.DWORD),
                ("protect", w.DWORD), ("kind", w.DWORD)]


def scan(pid):
    k = c.WinDLL("kernel32", use_last_error=True)
    k.OpenProcess.argtypes = (w.DWORD, w.BOOL, w.DWORD)
    k.OpenProcess.restype = w.HANDLE
    k.CloseHandle.argtypes = (w.HANDLE,)
    k.QueryFullProcessImageNameW.argtypes = (w.HANDLE, w.DWORD, w.LPWSTR, c.POINTER(w.DWORD))
    k.VirtualQueryEx.argtypes = (w.HANDLE, c.c_void_p, c.POINTER(MemoryRange), c.c_size_t)
    k.VirtualQueryEx.restype = c.c_size_t
    k.ReadProcessMemory.argtypes = (w.HANDLE, c.c_void_p, c.c_void_p, c.c_size_t, c.POINTER(c.c_size_t))
    h = k.OpenProcess(0x1010, False, pid)
    if not h:
        raise c.WinError(c.get_last_error())
    patterns = defaultdict(list)
    for value in LABELS:
        for enc in ("utf-8", "utf-16-le"):
            patterns[enc].append((value, value.encode(enc)))
    try:
        name, size = c.create_unicode_buffer(4096), w.DWORD(4096)
        if not k.QueryFullProcessImageNameW(h, 0, name, c.byref(size)) or Path(name.value) != CLIENT:
            raise ValueError("not the running US100 client")
        chunk = 4 << 20
        buf, got, m = c.create_string_buffer(chunk), c.c_size_t(), MemoryRange()
        address = 0
        matches = defaultdict(list)
        while k.VirtualQueryEx(h, address, c.byref(m), c.sizeof(m)):
            base = m.base or 0
            address = base + m.size
            if not m.size or address <= base:
                break
            if m.state != 0x1000 or m.protect & 0x100 or m.protect & 0xff not in (2, 4, 8):
                continue
            for pos in range(base, address, chunk - 128):
                if not k.ReadProcessMemory(h, pos, buf, min(chunk, address-pos), c.byref(got)):
                    continue
                data = buf[:got.value]
                for enc, rows in patterns.items():
                    for label, needle in rows:
                        if (found := data.find(needle)) >= 0 and len(matches[label, enc]) < 12:
                            matches[label, enc].append((hex(pos + found), hex(base), hex(m.kind)))
        for label, enc in ((s, e) for s in LABELS for e in patterns):
            if matches[label, enc]:
                print(repr(label), enc, matches[label, enc])
    finally:
        k.CloseHandle(h)


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--pid", type=int, required=True)
    scan(p.parse_args().pid)
