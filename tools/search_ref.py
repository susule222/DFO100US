# -*- coding: utf-8 -*-
"""Search the 115us docs/code for roster row / character list layout evidence."""
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
terms = re.compile(r'nameless|roster|row parser|CharacterList|列表|名单|row', re.I)
for p in sorted(root.rglob('*')):
    if p.suffix not in ('.md', '.go', '.py', '.txt'):
        continue
    try:
        text = p.read_text(encoding='utf-8', errors='replace')
    except Exception:
        continue
    hits = []
    for i, line in enumerate(text.splitlines(), 1):
        if terms.search(line) and len(line) > 12:
            hits.append((i, line.strip()[:200]))
    if hits:
        print('== %s' % p)
        for i, line in hits[:6]:
            print('  %d: %s' % (i, line))
