#!/bin/sh
""":"
exec python3 "$0" "$@"
":"""
"""Recreate the old test copies from their byte-identical preserved originals."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil


def sha(path):
    with path.open('rb') as f:
        return hashlib.file_digest(f,'sha256').hexdigest()


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--only',help='restrict restore to the exact run directory name')
    p.add_argument('--probe',action='store_true',help='restore a test copy under this receipt directory')
    a=p.parse_args(); evidence=Path(__file__).resolve().parent
    m=json.loads((evidence/'MOVED.json').read_text(encoding='utf-8-sig'))
    root=Path(m['root']).resolve()
    if root!=evidence.parent or root.name!='runtime':raise ValueError('unexpected receipt root')
    selected=[e for e in m['moved'] if not a.only or e['run']==a.only]
    if not selected:raise ValueError('no matching moved items')
    plan=[]
    for e in selected:
        old=Path(e['path']).resolve(); source=Path(e['retained']).resolve()
        if (old.parent!=root/e['run'] or source.parent!=old.parent or old.name not in
            ('rollback-test.zip','rollback-test.pvf','rollback-test.dat','rollback-test.NPK')
            or not source.name.startswith('before-') or sha(source)!=e['sha256']):
            raise ValueError('unexpected or changed restoration source')
        dest=evidence/'restore-probe'/e['run']/old.name if a.probe else old
        target_root=evidence if a.probe else root
        if not dest.resolve().is_relative_to(target_root) or dest.exists():raise ValueError(f'refuse overwrite: {dest}')
        plan.append((source,dest,e['sha256']))
    for source,dst,h in plan:
        dst.parent.mkdir(parents=True,exist_ok=True)
        with source.open('rb') as i,dst.open('xb') as o:shutil.copyfileobj(i,o,4<<20)
        if sha(dst)!=h:raise ValueError('restored test copy hash differs')
        print(f'{dst}: {h}')


if __name__=='__main__':main()
