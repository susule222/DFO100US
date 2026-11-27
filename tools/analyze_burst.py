# -*- coding: utf-8 -*-
"""Analyze runtime/encoder_burst.log: group encoder calls by caller RVA,
split around the SETTLE marker (the entry's stat settlement), and surface
the writes that carry real values into the 40-slot stat region.

Usage: python tools/analyze_burst.py [logfile]
"""
import collections
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
LOG = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else ROOT / 'runtime' / 'encoder_burst.log'

enc_re = re.compile(r'ENC obj=(\S+) off=([0-9a-f]+) val=(-?\d+) caller=([0-9a-f]+)')

lines = LOG.read_text(encoding='utf-8').splitlines()
settle_idx = [i for i, l in enumerate(lines) if l.startswith('SETTLE')]
print('total lines %d, SETTLE markers at %s' % (len(lines), settle_idx[:10]))

events = []  # (line_no, obj, off, val, caller)
ctors = []   # (line_no, obj)
for i, l in enumerate(lines):
    m = enc_re.match(l)
    if m:
        events.append((i, m.group(1), int(m.group(2), 16), int(m.group(3)), m.group(4)))
    elif l.startswith('CTOR'):
        ctors.append((i, l.split('statobj=')[1]))

print('CTOR count %d, ENC count %d' % (len(ctors), len(events)))

first_settle = settle_idx[0] if settle_idx else len(lines)

# Group by caller: pre/post settle counts, offset span, sample nonzero values.
groups = collections.defaultdict(lambda: {'pre': 0, 'post': 0, 'offs': set(), 'nz': [], 'objs': set()})
for i, obj, off, val, caller in events:
    g = groups[caller]
    g['pre' if i < first_settle else 'post'] += 1
    g['offs'].add(off)
    g['objs'].add(obj)
    if val not in (0, -1) and len(g['nz']) < 12:
        g['nz'].append((off, val, obj))

print('\n%-12s %6s %6s %6s  %s' % ('caller', 'pre', 'post', '#offs', 'offset span'))
for caller, g in sorted(groups.items(), key=lambda kv: -(kv[1]['pre'] + kv[1]['post'])):
    offs = sorted(g['offs'])
    span = '%#x..%#x' % (offs[0], offs[-1]) if offs else '-'
    print('%-12s %6d %6d %6d  %s' % (caller, g['pre'], g['post'], len(offs), span))

# The slot region of interest (115 layout: 0x1094..0x11cc).
LO, HI = 0x1094, 0x11cc
print('\n--- callers touching slot region %#x..%#x ---' % (LO, HI))
for caller, g in sorted(groups.items()):
    inregion = sorted(o for o in g['offs'] if LO <= o <= HI)
    if inregion:
        nz = [t for t in g['nz'] if LO <= t[0] <= HI]
        print('caller %s: %d region offsets, pre=%d post=%d' % (caller, len(inregion), g['pre'], g['post']))
        print('   offsets: %s' % ', '.join('%#x' % o for o in inregion))
        if nz:
            print('   nonzero writes: %s' % ', '.join('off=%#x val=%d obj=%s' % t for t in nz))

# Post-settle writes per object: which stat object got the entry values?
print('\n--- post-SETTLE ENC writes (first 60) ---')
post = [e for e in events if e[0] > first_settle]
for i, obj, off, val, caller in post[:60]:
    print('line %6d obj=%s off=%#x val=%d caller=%s' % (i, obj, off, val, caller))
print('post-SETTLE total: %d' % len(post))
