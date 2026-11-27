# -*- coding: utf-8 -*-
"""Print a slice of the probe's embedded JavaScript, to see the exact text frida reads."""
import pathlib
import sys

path = pathlib.Path(__file__).resolve().parent / 'probe_dungeon_handlers.py'
text = path.read_text(encoding='utf-8')
marker = "'" * 3
start = text.index(marker) + 3
end = text.index(marker, start)
js = text[start:end]
lines = js.splitlines()

first = int(sys.argv[1]) if len(sys.argv) > 1 else 60
last = int(sys.argv[2]) if len(sys.argv) > 2 else 80
print('js lines: %d' % len(lines))
for i in range(first - 1, min(last, len(lines))):
    print('%3d| %s' % (i + 1, lines[i]))
