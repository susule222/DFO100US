# -*- coding: utf-8 -*-
"""Summarise a world catalog: top-level keys and dungeon area counts."""
import json
import pathlib
import sys

p = pathlib.Path(sys.argv[1])
d = json.loads(p.read_text(encoding='utf-8'))
print('file:', p.name)
print('top keys:', list(d.keys()))
for k, v in d.items():
    if isinstance(v, list):
        print('  %-24s list len=%d' % (k, len(v)))
        if v and isinstance(v[0], dict):
            print('      sample keys:', list(v[0].keys())[:12])
    elif isinstance(v, dict):
        print('  %-24s dict len=%d' % (k, len(v)))
        ks = list(v.keys())[:6]
        print('      sample keys:', ks)
    else:
        print('  %-24s %r' % (k, v))
