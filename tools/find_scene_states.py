# -*- coding: utf-8 -*-
"""Find every recorded scene-state report (command 217) and its second byte.

The dungeon selection screen was observed once, and this project's own notes
record the client's scene report as 0x02 in town, 0x1b while the selection screen
is open and 0x64 after the actor moved into the transition. So the run where the
screen appeared is the run whose 217 frame carries 0x1b — and that run's log then
says exactly which packets were sent. Read-only.

Usage: python tools/find_scene_states.py
"""
import json
import pathlib

ROOT = pathlib.Path(__file__).resolve().parent.parent
PATTERNS = ['runtime/**/*.jsonl']

rows = []
for pattern in PATTERNS:
    for path in sorted(ROOT.glob(pattern)):
        if not path.is_file():
            continue
        try:
            text = path.read_text(encoding='utf-8', errors='replace')
        except OSError:
            continue
        for index, line in enumerate(text.splitlines()):
            line = line.strip()
            # Cheap pre-filter only; the real test is the parsed id below. (An
            # earlier version filtered on '"217"', which never matches because
            # JSON encodes an integer id without quotes.)
            if not line or '217' not in line:
                continue
            try:
                event = json.loads(line)
            except ValueError:
                continue
            body = event.get('plain_hex') or ''
            if event.get('kind') != 'client_frame' or event.get('id') != 217 or len(body) < 4:
                continue
            second = int(body[2:4], 16)
            rows.append((path.relative_to(ROOT), index, second, body, event.get('time', '')))

print('records: %d' % len(rows))
for rel, index, second, body, when in rows:
    marker = '  <-- selection screen open' if second == 0x1b else ''
    print('  %-52s line=%-4d state=0x%02x  %s  %s%s' % (rel, index, second, when, body, marker))

hits = [r for r in rows if r[2] == 0x1b]
if not hits:
    print('\nno 0x1b scene state in any retained log')
else:
    print('\nruns whose client reported the selection screen open: %d' % len(hits))
