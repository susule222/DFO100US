"""Look for exact known localization buffers in a running US100 client, read-only."""
import argparse
import ctypes as c
from ctypes import wintypes as w
import hashlib
import json
from pathlib import Path

from us100_quest_runtime_text_probe import CLIENT, MemoryRange


def pattern_rows(original, trial, dstr):
    patterns = {}
    for label, path in (("original_table", original), ("trial_table", trial)):
        u16 = path.read_bytes().removeprefix(b"\xff\xfe")
        for enc, raw in (("utf16", u16), ("utf8", u16.decode("utf-16-le").encode("utf-8"))):
            # Three distant anchors distinguish a whole plaintext table from an individual UI label.
            for i, at in enumerate((4096, len(raw)//2, len(raw)-1024)):
                patterns[f"{label}_{enc}_{i}"] = (raw[at:at+96], at, raw)
    ciphertext = dstr.read_bytes()
    for i, at in enumerate((0, len(ciphertext)//2, len(ciphertext)-128)):
        patterns[f"original_dstr_binary_{i}"] = (ciphertext[at:at+96], at, ciphertext)
    for word in ("dstr.dat", "dnf.uv.str", "[Main Quests]", "[\u4e3b\u7ebf\u4efb\u52a1]",
                 "<Complete condition>", "<\u5b8c\u6210\u6761\u4ef6>", "Lv. %d Quest", "%d \u7ea7\u4efb\u52a1",
                 "The following characters can be"):
        for enc in ("utf-8", "utf-16-le"):
            patterns[f"{word} ({enc})"] = (word.encode(enc), None, None)
    return patterns


def scan(pid, patterns):
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
    try:
        exe, cap = c.create_unicode_buffer(4096), w.DWORD(4096)
        if not k.QueryFullProcessImageNameW(h, 0, exe, c.byref(cap)) or Path(exe.value) != CLIENT:
            raise ValueError("not the exact running 100US client")
        read_size = 4 << 20
        buf, got, bounds = c.create_string_buffer(read_size), c.c_size_t(), MemoryRange()
        found = {name: [] for name in patterns}
        seen = set()
        address = 0
        while k.VirtualQueryEx(h, address, c.byref(bounds), c.sizeof(bounds)):
            base = bounds.base or 0
            address = base + bounds.size
            if address <= base:
                break
            if bounds.state != 0x1000 or bounds.protect & 0x100 or bounds.protect & 0xff not in (2,4,8):
                continue
            for pos in range(base,address,read_size-128):
                if not k.ReadProcessMemory(h,pos,buf,min(read_size,address-pos),c.byref(got)):
                    continue
                data = buf[:got.value]
                for label,(needle,at,raw) in patterns.items():
                    if len(found[label]) >= 4:
                        continue
                    off = data.find(needle)
                    if off < 0:
                        continue
                    hit = pos + off
                    if (label,hit) in seen:
                        continue
                    seen.add((label,hit))
                    row = {"addr":hex(hit)}
                    if at is not None and hit >= at:
                        exact = c.create_string_buffer(len(raw))
                        if k.ReadProcessMemory(h,hit-at,exact,len(raw),c.byref(got)) and got.value==len(raw):
                            if exact.raw == raw:
                                row["whole_buffer_equal_sha256"] = hashlib.sha256(raw).hexdigest()
                    found[label].append(row)
        return {name: hits for name,hits in found.items() if hits}
    finally:
        k.CloseHandle(h)


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--pid", type=int, required=True)
    for option in ("original", "trial", "dstr"):
        p.add_argument("--"+option, type=Path, required=True)
    a = p.parse_args()
    print(json.dumps(scan(a.pid,pattern_rows(a.original,a.trial,a.dstr)),indent=2))
