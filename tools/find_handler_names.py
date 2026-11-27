# -*- coding: utf-8 -*-
"""Find all CNRDUserInfoPacketHandler log strings and their xrefs, to name
each NOTI2 parser function."""
import pathlib
import re
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

pe = PE(default_exe())
data = bytes(pe.data)
base = pe.base

# Gather all strings mentioning the handler class.
texts = {}
for m in re.finditer(rb'CNRDUserInfoPacketHandler[^\0]{0,120}', data):
    off = m.start()
    s = m.group().decode('ascii', 'replace')
    texts.setdefault(s, []).append(off)
for s, offs in texts.items():
    print('%d hit(s): %r' % (len(offs), s))
    for off in offs[:4]:
        print('    file %#x' % off)

# Xref each string back into code.
sva = ra = rs = None
for name, vs2, vsz, r2, rsz in pe.sections:
    if name == '.text':
        sva, ra, rs = vs2, r2, rsz
blob = data[ra:ra + rs]
print()
for s, offs in texts.items():
    for off in offs[:1]:
        rva = None
        for name, vs2, vsz, r2, rsz in pe.sections:
            if r2 <= off < r2 + rsz:
                rva = vs2 + (off - r2)
                break
        if rva is None:
            continue
        disp = rva - 7
        needle = struct.pack('<i', disp)
        refs = []
        start = 0
        while True:
            k = blob.find(needle, start)
            if k < 0:
                break
            start = k + 1
            if k + 7 <= len(blob):
                refs.append(base + sva + k)
        print('%-60s xrefs: %s' % (s[:60], [hex(r) for r in refs[:5]]))
pe.close()
