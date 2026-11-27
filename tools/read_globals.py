# -*- coding: utf-8 -*-
"""Read the global pointer table around the sk.dat key globals (read-only)."""
import struct
import sys
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

pe = PE(default_exe())
data = bytes(pe.data)


def rva_of(off):
    for name, sva, vs, ra, rs in pe.sections:
        if ra <= off < ra + rs:
            return sva + (off - ra)
    return None


def str_at(va, limit=200):
    off = va - pe.base
    if not (0 <= off < len(data)):
        return None
    blob = data[off:off + limit]
    end = blob.find(b'\0')
    if end <= 0:
        return None
    try:
        return blob[:end].decode('ascii')
    except UnicodeDecodeError:
        return None


base_off = 0x9481190
print('globals file %#x rva %#x' % (base_off, rva_of(base_off) or 0))
for i in range(-6, 10):
    off = base_off + i * 8
    if off + 8 > len(data):
        break
    val = struct.unpack_from('<Q', data, off)[0]
    note = ''
    s = str_at(val) if val >= pe.base else None
    if s:
        note = ' -> %r' % (s[:60])
    print('  [%+#06x] file %#x = %#018x%s' % (i * 8, off, val, note))
pe.close()
