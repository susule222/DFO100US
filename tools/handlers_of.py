# -*- coding: utf-8 -*-
"""Print the client's registered handler addresses for a set of command ids,
as RVAs relative to the image base (0x140000000).

Usage: python tools/handlers_of.py 3 16 28 29 30 37
"""
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
TABLE = ROOT / 'runtime' / 'us100_registrations.json'
BASE = 0x140000000

wanted = [int(a, 0) for a in sys.argv[1:]] or [3, 16, 28, 29, 30, 37]
rows = json.loads(TABLE.read_text(encoding='utf-8'))
by_id = {}
for row in rows:
    if isinstance(row, dict) and row.get('id') is not None and row.get('handler'):
        by_id.setdefault(int(row['id']), row)

for cid in wanted:
    row = by_id.get(cid)
    if row is None:
        print('%-4d  not registered' % cid)
        continue
    handler = int(str(row['handler']), 0)
    print('%-4d  handler=%#x  rva=%#x  (id_src=%s)' % (cid, handler, handler - BASE, row.get('id_src', '')))
