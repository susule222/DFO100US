# -*- coding: utf-8 -*-
"""Dump one client_frame's plaintext from events.jsonl, with u16/u32 views."""
import json
import pathlib
import struct
import sys

path = pathlib.Path(sys.argv[1])
want = int(sys.argv[2])
maxn = int(sys.argv[3]) if len(sys.argv) > 3 else 1

shown = 0
for line in path.read_text(encoding='utf-8', errors='replace').splitlines():
    line = line.strip()
    if not line:
        continue
    e = json.loads(line)
    if e.get('kind') != 'client_frame' or e.get('id') != want:
        continue
    shown += 1
    if shown > maxn:
        break
    ph = e.get('plain_hex') or ''
    p = bytes.fromhex(ph)
    print('== frame #%d at %s bytes=%d plain=%d' % (shown, e.get('time', '')[11:23], e.get('bytes', -1), len(p)))
    for off in range(0, min(len(p), 256), 16):
        chunk = p[off:off + 16]
        u16 = ' '.join('%5d' % v for v in struct.unpack('<%dH' % (len(chunk) // 2), chunk[:len(chunk) // 2 * 2]))
        print('  %04x  %-47s | %s' % (off, chunk.hex(), u16))
    if len(p) > 256:
        print('  ... (%d bytes total)' % len(p))
