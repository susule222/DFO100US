# -*- coding: utf-8 -*-
"""List every ASCII hex string >= 16 chars in the US100 image with its RVA.

Read-only. Groups by section so loader constants can be spotted.
"""
import pathlib
import re
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE, default_exe

pe = PE(default_exe())
data = bytes(pe.data)


def section_of(off):
    for name, sva, vs, ra, rs in pe.sections:
        if ra <= off < ra + rs:
            return name, sva + (off - ra)
    return None, None


groups = {}
for m in re.finditer(rb'[0-9A-Fa-f]{16,}', data):
    off = m.start()
    sec, rva = section_of(off)
    if sec is None:
        continue
    txt = m.group().decode()
    groups.setdefault(sec, []).append((rva, len(txt), txt))

for sec, rows in groups.items():
    print('== section %s: %d hex strings' % (sec, len(rows)))
    for rva, n, txt in rows:
        # Skip the literal-obfuscation noise: printable-key candidates are
        # usually prefixed by a nearby length. Report only even-length spans.
        if n % 8 == 0 and 16 <= n <= 80:
            print('   rva %#010x len %-3d %s' % (rva, n, txt if n <= 40 else txt[:32] + '...'))
pe.close()
