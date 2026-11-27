# -*- coding: utf-8 -*-
"""Why did the gateway's automatic room start pick the wrong dungeon?

Read-only. Prints, for one profession/advancement pair, the starting route the
tutorial flow table names, whether that id exists in the tutorial dungeon
catalog, and the level gates of both that dungeon and the lowest ordinary
catalog dungeon (the fallback the auto start lands on). Bounded: three config
files, no database, no client.
"""
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
CFG = ROOT / 'server-us100' / 'configs' / 'us100'
TUT = ROOT / 'server-us100' / 'runtime' / 'us100_tutorial'
PROFESSION = int(sys.argv[1]) if len(sys.argv) > 1 else 0
ADVANCEMENT = int(sys.argv[2]) if len(sys.argv) > 2 else 0


def load(path):
    return json.loads(path.read_text(encoding='utf-8'))


def as_map(node):
    if isinstance(node, dict):
        return {int(k): v for k, v in node.items()}
    return {int(v.get('id', v.get('ID', 0))): v for v in node}


characters = load(CFG / 'characters.generated.json')
professions = as_map(characters['professions'])
entry = professions.get(PROFESSION)
job = entry.get('job') if entry else None
print('profession %d -> job %s' % (PROFESSION, job))

routes = load(TUT / 'routes.json')
route_map = routes.get('routes')
if isinstance(route_map, dict):
    route_map = list(route_map.values())
matched = []
for r in route_map or []:
    if r.get('job') == job and int(r.get('advancement') or 0) == ADVANCEMENT:
        matched.append(r)
print('routes: %d entries, %d match job=%s advancement=%d' % (
    len(route_map or []), len(matched), job, ADVANCEMENT))
for r in matched:
    print('  route dungeon=%s town=%s area=%s position=%s' % (
        r.get('dungeon'), r.get('town'), r.get('area'), r.get('position')))

tutorial = load(TUT / 'dungeons.json')
td = as_map(tutorial.get('dungeons') or tutorial.get('Dungeons'))
tids = sorted(td)
print('tutorial dungeons: %s' % tids)

catalog = load(CFG / 'dungeons.generated.json')
cd = as_map(catalog.get('dungeons') or catalog.get('Dungeons'))
cids = sorted(cd)
print('catalog dungeons: %d, lowest five %s' % (len(cids), cids[:5]))


def describe(label, d):
    if d is None:
        print('  %s missing' % label)
        return
    print('  %s id=%s tutorial=%s minLevel=%s mazes=%s' % (
        label, d.get('id', d.get('ID')), d.get('tutorial', d.get('Tutorial')),
        d.get('minimumLevel', d.get('MinimumLevel')),
        [m.get('index', m.get('Index')) for m in (d.get('mazes') or d.get('Mazes') or [])]))


for r in matched:
    rid = int(r.get('dungeon') or 0)
    print('route dungeon %d:' % rid)
    describe('  tutorial', td.get(rid))
    describe('  catalog ', cd.get(rid))
for cid in cids[:3]:
    describe('catalog fallback', cd[cid])
