# -*- coding: utf-8 -*-
"""Decode obfuscated string literals referenced by the US100 PVF loader."""
import sys
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

pe = PE(default_exe())
for rva in (0x7D97BD0, 0x7D97C48, 0x7D97C48 - 0x78, 0x7D97BD0 - 0x60):
    text = pe.literal(rva)
    print('literal rva %#x -> %r' % (rva, text))

# Also dump a window of candidate literal starts near those addresses.
print()
for base_rva in (0x7D97BD0, 0x7D97C48):
    print('== window around %#x ==' % base_rva)
    for delta in range(-0x40, 0x20, 4):
        rva = base_rva + delta
        try:
            t = pe.literal(rva)
        except Exception:
            t = None
        if t:
            print('   %#x (%+#x): %r' % (rva, delta, t))
pe.close()
