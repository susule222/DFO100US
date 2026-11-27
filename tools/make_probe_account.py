# -*- coding: utf-8 -*-
"""Create a clean probe account with two characters inside the US100 catalog.

Reads the storage DSN without printing secrets. Idempotent per name.
"""
import json
import pathlib
import subprocess
import sys

ROOT = pathlib.Path(r'D:\DNF project\service\美服100服务端\server-us100')
STORAGE = ROOT / 'runtime' / 'storage' / 'local.json'
CATALOG = ROOT / 'configs' / 'us100' / 'characters.generated.json'
ACCOUNT = 'probe-us100'
ROLES = [('us100a', 0), ('us100b', 3)]

cfg = json.loads(STORAGE.read_text(encoding='utf-8'))
dsn = cfg['postgres_dsn']
catalog = json.loads(CATALOG.read_text(encoding='utf-8'))
profs = catalog['professions']
checksum = catalog['source']['checksum']


def psql(sql):
    r = subprocess.run(['E:/BaiduNetdiskDownload/115us/tools/pg/pgsql/bin/psql.exe', dsn, '-Atc', sql],
                       capture_output=True, text=True)
    if r.returncode != 0:
        raise SystemExit('psql: ' + r.stderr.strip()[:200])
    return r.stdout.strip()


account = psql("SELECT id FROM accounts WHERE username='%s'" % ACCOUNT)
if not account:
    account = psql("INSERT INTO accounts(username,development_only) VALUES('%s',true) RETURNING id" % ACCOUNT)
print('account %s id=%s' % (ACCOUNT, account))

existing = psql("SELECT name,wire_id FROM characters WHERE account_id=%s AND deleted_at IS NULL" % account)
seen = {}
for line in existing.splitlines():
    if '|' in line:
        n, w = line.split('|', 1)
        seen[n] = w

for name, pid in ROLES:
    if name in seen:
        print('exists: %s (wire %s)' % (name, seen[name]))
        continue
    prof = profs[str(pid)]
    state = {
        'level': 1,
        'attributes': prof.get('initial_attributes') or {},
        'initial_skill_cells': prof.get('initial_skill_cells') or [],
        'source_path': prof.get('path') or '',
        'source_sha256': prof.get('raw_sha256') or '',
        'advancement': 0,
        'equipment_pending': True,
    }
    state_json = json.dumps(state).replace("'", "''")
    sql = ("INSERT INTO characters(account_id,wire_id,name,profession,create_request,config_version,state) "
           "SELECT %s, coalesce(max(wire_id),0)+1, '%s', %d, '\\x', '%s', '%s'::jsonb FROM characters WHERE account_id=%s RETURNING wire_id" % (
               account, name, pid, checksum, state_json, account))
    wire = psql(sql)
    print('created %s profession=%d wire=%s' % (name, pid, wire))

print('roster:')
print(psql("SELECT wire_id,name,profession FROM characters WHERE account_id=%s AND deleted_at IS NULL ORDER BY wire_id" % account))
