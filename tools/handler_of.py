# -*- coding: utf-8 -*-
"""Print the recovered handler address(es) for one or more command ids, so a
packet's real layout can be recovered statically from the client's own parser.

Usage: python tools/handler_of.py 27 28 29 37 45 39 117
"""
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
ROWS = json.loads((ROOT / 'runtime' / 'us100_registrations.json').read_text(encoding='utf-8'))
WANT = [int(a, 0) for a in sys.argv[1:]]

by_id = {}
for r in ROWS:
    try:
        by_id.setdefault(int(r['id']), []).append(r)
    except (KeyError, TypeError, ValueError):
        continue

for want in WANT:
    rows = by_id.get(want, [])
    if not rows:
        print('id %d: no registration' % want)
        continue
    for r in rows:
        print('id %-6d handler=%s call_va=%s src=%s' % (want, r.get('handler'), r.get('call_va'), r.get('id_src')))
