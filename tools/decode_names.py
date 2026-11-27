# -*- coding: utf-8 -*-
"""Decode the encoded literals near 0x147326a30 and find all handler names."""
import sys
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

pe = PE(default_exe())
base = pe.base

# The annotated string at 0x147326a30 is an ENCODED literal. Decode it and
# nearby literals to name the functions around the NOTI2 parser.
for rva in (0x7326a30, 0x7326a00, 0x73269d0, 0x7326658, 0x7326660, 0x7326a70, 0x7326a90):
    t = pe.literal(rva)
    print('%#x -> %r' % (rva, t))
pe.close()
