# -*- coding: utf-8 -*-
"""Show the entry-payload decisions of the newest run.

Reads the interactive run's events and prints only the events that say which
mode-1 / mode-6 payload the server resolved and sent, which is what the flag
overlap used to hide.

Usage: python tools/show_entry_mode.py [events.jsonl]
"""
import json
import pathlib
import sys

PATH = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else pathlib.Path(
    r'D:\DNF project\service\美服100服务端\runtime\interactive\events.jsonl')

interesting = ('entry_addition_mode', 'entry_stat_error', 'entry_addition_error',
               'entry_basic_probe_sent', 'entry_addition_sent', 'entry_stat_probe_sent')

rows = []
for line in PATH.read_text(encoding='utf-8', errors='replace').splitlines():
    line = line.strip()
    if not line:
        continue
    try:
        event = json.loads(line)
    except ValueError:
        continue
    if event.get('kind') in interesting:
        rows.append(event)

print('%s: %d entry events' % (PATH.name, len(rows)))
for event in rows:
    payload = event.get('plain_hex', '')
    shown = '%s... (%d bytes)' % (payload[:32], len(payload) // 2) if payload else ''
    extras = {k: v for k, v in event.items() if k not in ('kind', 'time', 'plain_hex', 'id')}
    print('  %-24s %s %s' % (event.get('kind'), shown, extras if extras else ''))
