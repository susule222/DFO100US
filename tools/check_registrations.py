# -*- coding: utf-8 -*-
"""Compare the client's runtime registrations with the ids the gateway sends.

Usage: python tools/check_registrations.py 15 16 27 28 29 30 37 39 45 46 217
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
LOG = ROOT / 'runtime' / 'registrations_probe.log'

text = LOG.read_text(encoding='utf-8', errors='replace')
pairs = [(int(a), b) for a, b in re.findall(r'register id=(\d+) handler=(\S+)', text)]
mapping = {}
for pid, handler in pairs:
    mapping.setdefault(pid, handler)
print('runtime registrations: %d distinct ids (from %d lines)' % (len(mapping), len(pairs)))
for pid in [int(a) for a in sys.argv[1:]]:
    print('id %-5d -> %s' % (pid, mapping.get(pid, 'NOT registered at startup')))
print()
print('ids in the 10..40 range: %s' % sorted(i for i in mapping if 10 <= i <= 40))
