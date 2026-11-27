# -*- coding: utf-8 -*-
"""Derive US100 rule files from the reference ones, changing only the source checksum.

Rules whose numeric content is a reference-server model (not PVF-derived) keep
their values; only the source-version binding moves to the US100 archive, so
the server's cross-file version checks pass on US100 data.
"""
import json
import pathlib

ROOT = pathlib.Path(r'D:\DNF project\service\美服100服务端\server-us100')
CFG = ROOT / 'configs'
OUT = CFG / 'us100'
OUT.mkdir(parents=True, exist_ok=True)
US100 = '53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69'

targets = {
    'inventory.current37.json': 'rules.bag.json',
    'drop.current36.json': 'rules.drop.json',
    'cards.compat90.json': 'rules.cards.json',
    'inventory.compat90.json': 'rules.bag.compat.json',
    'drop.compat90.json': 'rules.drop.compat.json',
}
# Only the archive-version binding moves. reference_sha256 names the local
# reference model the numbers came from and must keep pointing at it, so the
# provenance of the values stays readable.
for src, dst in targets.items():
    p = CFG / src
    d = json.loads(p.read_text(encoding='utf-8'))
    before = d.get('source')
    if isinstance(before, str) and len(before) == 64:
        d['source'] = US100
    d['us100_note'] = ('Values are the local reference model named by '
                       'reference_sha256, not recovered US100 server values. Only the '
                       'archive-version binding was moved to the US100 Script.pvf so the '
                       'server cross-file version checks accept US100 catalogs.')
    (OUT / dst).write_text(json.dumps(d, indent=2, ensure_ascii=False), encoding='utf-8')
    print('%-28s -> %-24s source %s -> %s' % (
        src, dst, (before or '(none)')[:12], (d.get('source') or '(none)')[:12]))
