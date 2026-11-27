"""Build the compact offline name index from the project's US string tables."""
import sys
from pathlib import Path

sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
from core import ROOT, atomic, catalog


def build():
    result={}
    for kind,table in [('装备','equipment'),('材料/道具','stackable')]:
        wanted={}
        for ident,(category,_,ref) in catalog().items():
            if category==kind and '::' in ref:
                wanted.setdefault(ref.split('::',1)[1].rstrip('>'),[]).append(ident)
        for directory in ('localize-us100-audit/us-tables','localize-us100-candidate/replacement-tables'):
            path=ROOT/'runtime'/directory/'string'/(table+'.uv.str')
            if not path.exists(): continue
            with path.open(encoding='utf-16-le',errors='replace') as f:
                for line in f:
                    k,_,v=line.rstrip().partition('>')
                    if k not in wanted or not v: continue
                    for ident in wanted[k]:
                        names=result.setdefault(ident,[])
                        if v not in names: names.append(v)
    atomic(Path(__file__).with_name('names.json'),result)
    print('Indexed names:',len(result))


if __name__=='__main__': build()
