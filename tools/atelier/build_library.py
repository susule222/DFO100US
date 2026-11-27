"""Build a read-only searchable UI catalog and reward-only server projections."""
import argparse
import gzip
import json
from pathlib import Path
import sqlite3
import sys
import uuid

sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
import atelier_runtime as rt
import core
from epic_data import TYPES,RARITIES

SLOTS={'[hat avatar]':'cap','[coat avatar]':'coat','[pants avatar]':'pants','[shoes avatar]':'shoes','[hair avatar]':'hair',
       '[breast avatar]':'neck','[face avatar]':'face','[waist avatar]':'belt','[skin avatar]':'skin',
       '[weapon avatar]':'weapon','[aurora avatar]':'aura'}
SLOT_ZH=dict(zip('skin shoes pants coat belt neck face hair cap weapon aura'.split(),
                 '肤色 鞋子 下装 上衣 腰部 胸部 脸部 头发 帽子 武器装扮 光环'.split()))
def dump(obj):return json.dumps(obj,ensure_ascii=False,separators=(',',':'))


def translations(group):
    names={}
    for folder in ('localize-us100-audit/us-tables','localize-us100-candidate/replacement-tables'):
        p=rt.ROOT/'runtime'/folder/'string'/(group+'.uv.str')
        if not p.exists():continue
        with p.open(encoding='utf-16-le',errors='replace') as f:
            for line in f:
                k,_,v=line.rstrip().partition('>')
                if k and v and ('name' in k.lower()):names.setdefault(k,[]).append(v)
    return names


def valid_gear(r,wear):
    f=r.get('Fields',{});d=f.get('[durability]',[]);rar=f.get('[rarity]',[])
    return (r['Kind'] in wear and len(f.get('[attach type]',[]))==1 and len(rar)==1 and rar[0]['type']==0 and rar[0]['value']>=0
            and ((len(d)==1 and d[0]['type']==0 and 0<=d[0]['value']<=65535) or (not d and r['Kind'] in
              ('[amulet]','[wrist]','[ring]','[support]','[magic stone]','[earring]','[title name]'))))


PATH_CHAR_JOB = {
    'swordman': '[swordman]', 'fighter': '[fighter]', 'gunner': '[gunner]',
    'mage': '[mage]', 'priest': '[priest]', 'thief': '[thief]',
    'knight': '[knight]', 'demonic_lancer': '[demonic lancer]', 'gunblader': '[gun blader]',
}


def _infer_job_from_path(path):
    parts = path.lower().replace('\\', '/').split('/')
    if len(parts) >= 4 and parts[0] == 'equipment' and parts[1] == 'character':
        char_dir = parts[2]
        if char_dir in PATH_CHAR_JOB:
            is_at = any(p.startswith('at_') for p in parts[3:])
            if is_at and char_dir in ('swordman', 'fighter', 'gunner', 'mage', 'priest'):
                return {f'[at {char_dir}]'}
            base = PATH_CHAR_JOB[char_dir]
            if base == '[swordman]':
                return {'[swordman]', '[demonic swordman]'}
            if base == '[mage]':
                return {'[mage]', '[creator mage]'}
            return {base}
    return set()


def build_from_jsonl(raw_path=None, pvf_meta=None, activate=True):
    generation=uuid.uuid4().hex;dest=rt.DATA/generation;dest.mkdir(parents=True)
    baseline=rt.inputs();cfg=core.CFG
    gear=core.read(cfg/'equipment.generated.json');loot=core.read(cfg/'loot.generated.json')
    bag=core.read(cfg/'rules.bag.json');wear=core.read(cfg/'rules.wear.json')['slots']
    groups=set(bag['slots'])|set(bag.get('slot_aliases',{}))
    old=core.catalog();basegear={r['ID'] for r in gear['rows']};baseitems={int(i) for i in loot['items']}
    raw=Path(raw_path) if raw_path else (rt.ROOT/'runtime'/'atelier-library.jsonl.gz')
    with gzip.open(raw,'rt',encoding='utf8') as f:
        header=json.loads(next(f))
        # Equipment and stackables have independent namespaces; never grant ambiguous new IDs.
        stackids={r['ID'] for line in f if (r:=json.loads(line))['Group']=='stackable'}
    db=sqlite3.connect(dest/'items.sqlite')
    db.executescript("""
    PRAGMA journal_mode=OFF;
    CREATE TABLE item(g TEXT,id INTEGER,name TEXT,alias TEXT,path TEXT,cat TEXT,rar TEXT,jobs TEXT,img TEXT,frame INTEGER,
                      grantmode INTEGER,rank INTEGER,slot TEXT,visual TEXT, PRIMARY KEY(g,id));
    CREATE TABLE wardrobe(job TEXT,mode TEXT,slot TEXT,g TEXT,id INTEGER);
    CREATE TABLE meta(k TEXT PRIMARY KEY,v TEXT);
    """)
    db.execute('insert into meta values (?,?)',('poses',dump(header['poses'])))
    db.execute('insert into meta values (?,?)',('source',header['source']))
    if pvf_meta:
        db.execute('insert into meta values (?,?)',('pvf_meta',dump(pvf_meta)))
    pkg_map={};avatars={};extra=0;ns=0;counts={}
    with (dest/'equipment.json').open('w',encoding='utf8') as out,gzip.open(raw,'rt',encoding='utf8') as src:
        next(src);out.write('{"source":'+dump(gear['source'])+',"rows":[')
        for i,r in enumerate(gear.pop('rows')):out.write((',' if i else '')+dump(r))
        current=None;names={}
        for line in src:
            r=json.loads(line);g,i,k,path=r['Group'],r['ID'],r['Kind'],r['Path']
            if current!=g:names=translations(g);current=g
            ref=r['Name'];key=ref.split('::')[-1].removesuffix('>')
            vals=list(names.get(key,[]))
            live_name=(r.get('DisplayName') or '').strip()
            if live_name:
                if live_name not in vals:
                    vals.append(live_name)
                name=live_name
            else:
                name=vals[-1] if vals else (ref if ref and not ref.startswith('<') else Path(path).stem)
            slot=SLOTS.get(k,'');jobs=list(dict.fromkeys(r.get('Jobs',[])))
            cat=('时装-'+SLOT_ZH[slot] if slot else TYPES.get(k.strip('[]').replace(' ','_'),k.strip('[]') or '未解析')) if g=='equipment' else {
                '[material]':'材料','[quest]':'任务道具','[throw]':'投掷物','[waste]':'消耗品'}.get(k,k.strip('[]') or '未解析')
            mode=int(i in old and old[i][1]==path)
            if not mode and i not in stackids and g=='equipment' and i not in basegear and valid_gear(r,wear):
                out.write(','+dump(dict(ID=i,Path=path,SHA256=r['SHA'],Fields=r['Fields'],reward_only=True)));extra+=1;mode=2
            if not mode and g=='stackable' and k in groups and not r.get('Error') and i not in basegear and i>0 and r.get('Limit',0)>=0:
                if i not in baseitems:
                    loot['items'][str(i)]=dict(id=i,kind='stackable',stackable_type=k,stack_limit=r.get('Limit',0),
                        reward_only=True,Weight=0,Rarity=max(0,r['Rarity']),Script=dict(path=path,sha256=r['SHA'],cells=[]))
                    ns+=1
                mode=2
            visual=None
            if slot:
                layers=r.get('Layers',[]);aura=r.get('Aura',[]);aura_anim=r.get('AuraAnim',[])
                has_vis=bool(layers or aura)
                visual=dump([layers,aura,aura_anim]) if aura_anim else dump([layers,aura])
                typed={l['Job'] for l in layers if l['Job'] in header['poses']}
                fits=set(jobs) or {'[all]'}
                if typed and '[all]' in fits:fits=typed
                if slot!='aura' and '[all]' in fits:
                    inferred=_infer_job_from_path(path)
                    if inferred:fits=inferred
                avatars[i]=(slot,fits,has_vis)
                db.executemany('insert into wardrobe values(?,?,?,?,?)',[(j,'single',slot,g,i) for j in fits])
            if g=='stackable' and (r.get('Display') or r.get('Booster') or r.get('Package')):
                pkg_map[i]=(r.get('Display') or [], r.get('Booster') or [], r.get('Package') or [], r.get('SuitableJobs') or [])
            rarity=RARITIES.get(r['Rarity'],'未标注')
            rank=(0 if mode==1 else 10 if mode==2 else 20)+{'神话':0,'史诗':1,'传说':2,'神器':3,'稀有':4}.get(rarity,5)
            db.execute('insert into item values (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',(g,i,name,' '.join(dict.fromkeys(vals)),path,cat,rarity,
                      '|'.join(jobs),r['Img'],r['Frame'],mode,rank,slot,visual))
            counts[g]=counts.get(g,0)+1
        out.write(']}')

    def pick_visual_slots(member_ids):
        """Select at most one visual avatar per slot from member_ids, ignoring blank clone placeholders."""
        by_slot={}
        for mid in member_ids:
            info=avatars.get(mid)
            if not info:continue
            sl,fits,has_vis=info
            if not has_vis:continue
            if sl not in by_slot:
                by_slot[sl]=(mid,fits)
        return by_slot

    def resolve_set(pid, depth=0):
        entry=pkg_map.get(pid)
        if not entry:return {},[]
        display,booster,package,suitable=entry
        # 1. Prefer Neople's canonical [cs display only] preview list when it has >=2 real visual avatars
        by_slot=pick_visual_slots(display)
        if len(by_slot)>=2:
            return by_slot,suitable
        # 2. Next prefer [booster info] -> [avatar] (look boxes)
        by_slot=pick_visual_slots(booster)
        if len(by_slot)>=2:
            return by_slot,suitable
        # 3. Direct [package data] real visual avatars + 1-level expansion of nested look boxes
        by_slot=pick_visual_slots(package)
        if depth<2:
            for mid in package:
                if mid in pkg_map and mid!=pid:
                    sub_slots,sub_suit=resolve_set(mid,depth+1)
                    for sl,val in sub_slots.items():
                        if sl not in by_slot:
                            by_slot[sl]=val
                    if not suitable and sub_suit:
                        suitable=sub_suit
        return by_slot,suitable

    sets=0
    for pid in pkg_map:
        by_slot,suitable=resolve_set(pid,0)
        if len(by_slot)<2:continue
        compatible=set(header['poses'])
        suit_valid={j for j in suitable if j in header['poses']}
        if suit_valid:
            compatible.intersection_update(suit_valid)
        for sl,(aid,jobs) in by_slot.items():
            if jobs and '[all]' not in jobs:
                compatible.intersection_update(jobs)
        if not compatible:continue
        ordered_ids=[aid for aid,_ in by_slot.values()]
        db.execute("update item set visual=? where g='stackable' and id=?",(dump(ordered_ids),pid))
        db.executemany('insert into wardrobe values(?,?,?,?,?)',[(j,'set','','stackable',pid) for j in compatible]);sets+=1
    db.executescript('CREATE INDEX page ON item(rank,id,g);CREATE INDEX byid ON item(id,grantmode);CREATE INDEX category ON item(cat,rank,id);CREATE INDEX outfit ON wardrobe(job,mode,slot,id);ANALYZE;')
    db.commit();db.close()
    (dest/'loot.json').write_text(dump(loot),encoding='utf8')
    if rt.inputs()!=baseline:raise RuntimeError('基础目录在导入期间发生变化，未切换')
    meta=dict(generation=generation,source=header['source'],inputs=baseline,counts=counts,sets=sets,extra_gear=extra,extra_stackable=ns)
    if pvf_meta:
        meta['pvf']=dict(pvf_meta)
    if activate:
        core.atomic(rt.DATA/'current.json',meta)
    return meta


def main():
    ap=argparse.ArgumentParser()
    ap.add_argument('--raw',default='')
    ap.add_argument('--pvf-path',default='')
    ap.add_argument('--pvf-fingerprint',default='')
    args=ap.parse_args()
    pvf_meta=None
    if args.pvf_path:
        p=Path(args.pvf_path)
        st=p.stat() if p.is_file() else None
        pvf_meta=dict(path=str(p),size=st.st_size if st else 0,mtime_ns=st.st_mtime_ns if st else 0,
                      fingerprint=args.pvf_fingerprint or '')
    meta=build_from_jsonl(args.raw or None, pvf_meta=pvf_meta, activate=True)
    print(meta)


if __name__=='__main__':main()
