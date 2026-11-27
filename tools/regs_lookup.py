# -*- coding: utf-8 -*-
"""Look up command ids in the recovered US100 registration table."""
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
rows = json.loads(path.read_text(encoding='utf-8'))
by_id = {}
for r in rows:
    if r['id'] is not None and r['id'] not in by_id:
        by_id[r['id']] = r
missing = []
for raw in sys.argv[2:]:
    want = int(raw, 0)
    r = by_id.get(want)
    if r:
        print('id=%-6d handler=%-14s call=%s' % (want, r['handler'], r['call_va']))
    else:
        print('id=%-6d MISSING' % want)
        missing.append(want)
print('total ids in table:', len(by_id))
