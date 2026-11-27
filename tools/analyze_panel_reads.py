# -*- coding: utf-8 -*-
"""Summarize runtime/panel_reads.log: distinct decoder reads per stat object,
grouped by offset, with their callers and observed decoded values. The slot
region (0x1080..0x1200) is highlighted — those are the panel's sources.

Usage: python tools/analyze_panel_reads.py [logfile]
"""
import collections
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
LOG = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else ROOT / 'runtime' / 'panel_reads.log'

dec_re = re.compile(r'DEC off=([0-9a-f]+) caller=([0-9a-f]+) raw=(-?\d+) decoded=(-?\d+) obj=(\S+)')

per_obj = collections.defaultdict(lambda: collections.defaultdict(list))  # obj -> off -> [(caller, decoded)]
hearts = []
ctors = []
for line in LOG.read_text(encoding='utf-8').splitlines():
    m = dec_re.match(line)
    if m:
        off, caller, raw, dec, obj = m.group(1), m.group(2), int(m.group(3)), int(m.group(4)), m.group(5)
        entry = (caller, dec)
        if entry not in per_obj[obj][int(off, 16)]:
            per_obj[obj][int(off, 16)].append(entry)
    elif line.startswith('CTOR'):
        ctors.append(line.split('statobj=')[1])
    elif line.startswith('HEARTBEAT'):
        hearts.append(line)

print('ctors: %s' % ', '.join(ctors))
for h in hearts[-3:]:
    print(h)

for obj, offs in per_obj.items():
    total = sum(len(v) for v in offs.values())
    print('\n=== obj %s: %d distinct offsets, %d distinct (caller,off,val) reads ===' % (obj, len(offs), total))
    for off in sorted(offs):
        reads = offs[off]
        callers = sorted(set(c for c, _ in reads))
        vals = sorted(set(v for _, v in reads))
        mark = '  <== SLOT REGION' if 0x1080 <= off <= 0x1200 else ''
        vshow = vals[:8]
        print('  off %#06x  callers[%d]: %s  vals(%d): %s%s' %
              (off, len(callers), ','.join(callers[:6]), len(vals), vshow, mark))
