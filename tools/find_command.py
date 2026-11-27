# -*- coding: utf-8 -*-
"""Search the recovered US100 command registrations for handler names matching a
substring, to locate the packet that carries a given feature (stats, HP, ...).

Usage: python tools/find_command.py stat [hp] [attributes]
"""
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
ROWS = json.loads((ROOT / 'runtime' / 'us100_registrations.json').read_text(encoding='utf-8'))
NEEDLES = [a.lower() for a in sys.argv[1:]] or ['stat']

print('rows: %d' % len(ROWS))
print('sample row:', json.dumps(ROWS[0], ensure_ascii=False)[:300])
print()
for r in ROWS:
    text = json.dumps(r, ensure_ascii=False).lower()
    if any(n in text for n in NEEDLES):
        print(json.dumps(r, ensure_ascii=False)[:260])
