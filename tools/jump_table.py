# -*- coding: utf-8 -*-
"""Decode a jump table of the client's UI event switch.

The status panel's window function (0x1447b5c80, 107 KB) begins with
switch(word at [input+1]) over up to 258 cases, via the jump table at image
offset 0x47cfb68 (entries are 4-byte image-relative offsets added to the image
base). Naming the case that writes the panel's stat fields needs the table
decoded: for each event number it prints the case address, and optionally the
range of instructions until the next case.

Read-only. Usage: python tools/jump_table.py <table_rva> <count> [--base_fn RVA]
"""
import pathlib
import struct
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / 'tools'))
from inspect_pe import PE, default_exe  # noqa: E402

pe = PE(default_exe())
base = pe.base

args = [a for a in sys.argv[1:] if not a.startswith('--')]
table_rva = int(args[0], 0)
count = int(args[1], 0)
count = max(1, min(count, 1024))

# The table's entries were seen referenced as [rdi + rax*4 + 0x47cfb68] where
# rdi = image base, i.e. plain 4-byte image-relative offsets.
blob = pe.read(table_rva, count * 4)
if len(blob) < count * 4:
    raise SystemExit('table read short: %d bytes' % len(blob))

cases = []
for i in range(count):
    rel = struct.unpack_from('<I', blob, i * 4)[0]
    cases.append((i, rel))

prev = None
for i, rel in cases:
    print('event %3d (0x%02x) -> case at %#x' % (i, i, base + rel))
pe.close()
