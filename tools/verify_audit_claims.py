# -*- coding: utf-8 -*-
"""Verify two audit claims: the bag-rules note, and binary vs source timestamps."""
import datetime
import json
import pathlib

ROOT = pathlib.Path(r'D:\DNF project\service\美服100服务端')

rules = ROOT / 'server-us100' / 'configs' / 'us100' / 'rules.bag.json'
data = json.loads(rules.read_text(encoding='utf-8'))
note = data.get('us100_note') or data.get('note')
print('rules.bag note:')
print(' ', note)
print()

FILES = [
    'server-us100/bin/wireprobe-us100.exe',
    'server-us100/cmd/wireprobe/main.go',
    'server-us100/cmd/wireprobe/dungeon_flow.go',
    'server-us100/internal/game/protocol/dungeon.go',
    'tools/us100_interactive.py',
]
for rel in FILES:
    path = ROOT / rel
    if path.exists():
        stamp = datetime.datetime.fromtimestamp(path.stat().st_mtime).strftime('%Y-%m-%d %H:%M:%S')
        print('%-56s %s' % (rel, stamp))
    else:
        print('%-56s MISSING' % rel)
