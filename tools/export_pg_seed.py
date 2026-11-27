# -*- coding: utf-8 -*-
"""Export every public table of the project's PostgreSQL into the db100 seed.

One-shot migration helper for REBUILD-100US.md milestone 3: the borrowed
PostgreSQL still owns the live rows, and db100 (server-us100/internal/db100)
boots an empty single-file database. This dumps each table as a list of JSON
rows into server-us100/runtime/us100_seed.json; db100.ImportSeed fills gaps
from it on first run (existing rows always win), so re-running the import is
safe and re-running the export just refreshes the snapshot.

Connection settings come from the same local.json the server uses, and the
password is passed via PGPASSWORD, never on the command line.

Usage: python tools/export_pg_seed.py [output.json]
"""
import base64
import json
import os
import pathlib
import re
import subprocess
import sys
import urllib.parse
from datetime import datetime

ROOT = pathlib.Path(__file__).resolve().parent.parent
CONFIG = ROOT / 'server-us100' / 'runtime' / 'storage' / 'local.json'
DEFAULT_OUT = ROOT / 'server-us100' / 'runtime' / 'us100_seed.json'

TABLES = [
    'accounts',
    'characters',
    'character_world',
    'character_quests',
    'character_quest_rewards',
    'character_quest_repairs',
    'character_fatigue',
    'character_fatigue_rooms',
    'character_map_clears',
    'character_birth',
    'character_tutorial_flags',
    'character_vaults',
    'account_currency',
    'admin_grants',
    'character_events',
]


TS_RE = re.compile(r'^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}')


def normalize(value):
    """Convert PG-only text forms into the forms db100 storage expects.

    - bytea arrives as "\\x<hex>" text; Go stores []byte as base64.
    - timestamptz arrives as PG text; Go parses RFC 3339. Pure dates (the
      fatigue `day` column) are left alone — the server compares those as
      "YYYY-MM-DD" strings and rollover logic depends on that exact shape.
    """
    if isinstance(value, str):
        if value.startswith('\\x'):
            try:
                return base64.b64encode(bytes.fromhex(value[2:])).decode()
            except ValueError:
                return value
        if TS_RE.match(value):
            try:
                return datetime.fromisoformat(value).isoformat()
            except ValueError:
                return value
    return value


def normalize_row(row):
    return {k: normalize(v) for k, v in row.items()}


def psql_json(cfg, query):
    dsn = urllib.parse.urlparse(cfg['postgres_dsn'])
    env = dict(os.environ)
    env['PGPASSWORD'] = dsn.password or ''
    binary = pathlib.Path(cfg.get('postgres_bin', r'D:\115us\115us\tools\pg\pgsql\bin')) / 'psql.exe'
    result = subprocess.run(
        [str(binary), '-h', dsn.hostname or '127.0.0.1', '-p', str(dsn.port or 5432),
         '-U', dsn.username or '', '-d', (dsn.path or '/').lstrip('/'),
         '-A', '-t', '-c', query],
        capture_output=True, text=True, env=env, errors='replace')
    if result.returncode != 0:
        raise SystemExit('psql failed: %s' % (result.stderr or '').strip())
    rows = []
    for line in (result.stdout or '').splitlines():
        line = line.strip()
        if line:
            rows.append(json.loads(line))
    return rows


def main():
    out = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else DEFAULT_OUT
    cfg = json.loads(CONFIG.read_text(encoding='utf-8'))
    seed = {}
    for table in TABLES:
        rows = [normalize_row(r) for r in psql_json(cfg, 'select row_to_json(t) from %s t' % table)]
        seed[table] = rows
        print('%-28s %d rows' % (table, len(rows)))
    tmp = out.with_suffix('.tmp')
    tmp.write_text(json.dumps(seed, ensure_ascii=False, separators=(',', ':')), encoding='utf-8')
    os.replace(tmp, out)
    print('\nwrote %s (%d bytes)' % (out, out.stat().st_size))
    return 0


if __name__ == '__main__':
    sys.exit(main())
