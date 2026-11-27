# -*- coding: utf-8 -*-
"""Print a slice of an exported token array with raw values."""
import json
import pathlib
import sys

p = pathlib.Path(sys.argv[1])
start = int(sys.argv[2])
end = int(sys.argv[3])
cells = json.loads(p.read_text(encoding='utf-8'))
for i in range(max(0, start), min(len(cells), end)):
    c = cells[i]
    print('  [%3d] type=%s value=%-16s hex=%s' % (
        i, c.get('type'), c.get('value'), format(c.get('value', 0) & 0xFFFFFFFF, '08x')))
