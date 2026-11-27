# -*- coding: utf-8 -*-
"""Print the cells of one section tag from an exported .tokens.json."""
import json
import pathlib
import sys

p = pathlib.Path(sys.argv[1])
tag = sys.argv[2]
limit = int(sys.argv[3]) if len(sys.argv) > 3 else 60
cells = json.loads(p.read_text(encoding='utf-8'))
active = False
shown = 0
for i, c in enumerate(cells):
    if c.get('type') == 3:
        active = (c.get('text') == tag)
        if active:
            print('[%s] starts at index %d' % (tag, i))
        continue
    if not active:
        continue
    print('  %-6s %s' % (c.get('type'), c.get('value')))
    shown += 1
    if shown >= limit:
        print('  ...')
        break
