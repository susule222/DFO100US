# -*- coding: utf-8 -*-
"""Print the client's PE sections and probe whether an RVA is readable.

Read-only. Usage: python tools/pe_info.py [rva ...]
"""
import sys
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

CLIENT = default_exe()
pe = PE(CLIENT)
print('base %#x image_size %#x' % (pe.base, pe.image_size))
for name, va, vs, ra, rs in pe.sections:
    print('%-8s va=%#010x vsz=%#010x raw=%#010x rawsz=%#010x %s'
          % (name, va, vs, ra, rs, 'MAPPED' if vs and rs and vs == rs else ''))
for a in [int(x, 0) for x in sys.argv[1:]]:
    d = pe.read(a, 16)
    print('%#x -> %d bytes %s' % (a, len(d), d.hex()))
