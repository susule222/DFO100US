"""Build a read-only searchable UI catalog and reward-only server projections."""
import argparse
from collections import defaultdict
import gzip
import json
from pathlib import Path
import re
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
    pkg_map={};pkg_meta={};avatars={};extra=0;ns=0;counts={}
    partset_names=header.get('partsets') or {}
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
            rarity_num=r['Rarity']
            rarity=RARITIES.get(rarity_num,'未标注')
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
                avatars[i]=dict(
                    slot=slot, fits=fits, has_vis=has_vis,
                    rarity_num=rarity_num, rarity=rarity, name=name,
                    img=r['Img'], frame=r['Frame'], path=path,
                    part_sets=r.get('Set') or [],
                    effect_set=int(r.get('EffectSet') or 0),
                    set_name=(r.get('SetName') or '').strip(),
                    color=int(r.get('Color', -1)),
                )
                db.executemany('insert into wardrobe values(?,?,?,?,?)',[(j,'single',slot,g,i) for j in fits])
            if g=='stackable' and (r.get('Display') or r.get('Booster') or r.get('Package')):
                pkg_map[i]=(r.get('Display') or [], r.get('Booster') or [], r.get('Package') or [], r.get('SuitableJobs') or [])
                pkg_meta[i]=(name, rarity_num)
            rank=(0 if mode==1 else 10 if mode==2 else 20)+{'神话':0,'史诗':1,'传说':2,'神器':3,'稀有':4,'高级':5}.get(rarity,6)
            db.execute('insert into item values (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',(g,i,name,' '.join(dict.fromkeys(vals)),path,cat,rarity,
                      '|'.join(jobs),r['Img'],r['Frame'],mode,rank,slot,visual))
            counts[g]=counts.get(g,0)+1
        out.write(']}')

    def pick_visual_slots(member_ids):
        """Select at most one visual avatar per slot from member_ids, ignoring blank clone placeholders."""
        by_slot={}
        for mid in member_ids:
            info=avatars.get(mid)
            if not info or not info['has_vis']:continue
            sl=info['slot']
            if sl not in by_slot:
                by_slot[sl]=(mid,info['fits'])
        return by_slot

    def resolve_set(pid, depth=0):
        entry=pkg_map.get(pid)
        if not entry:return {},[]
        display,booster,package,suitable=entry
        by_slot=pick_visual_slots(display)
        if len(by_slot)>=2:
            return by_slot,suitable
        by_slot=pick_visual_slots(booster)
        if len(by_slot)>=2:
            return by_slot,suitable
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

    BODY_SLOTS = ('cap','hair','face','neck','coat','belt','pants','shoes')
    COLOR_LABELS = {0: 'A型', 1: 'B型', 2: 'C型', 3: 'D型', 4: 'E型', 5: 'F型'}
    SLOT_SUFFIX_RE = re.compile(
        r'(?:\s*[\[（(][^\]）)]*[\]）)]\s*|\s*[A-F]型|\s*GM\b|'
        r'(?:装扮|时装)?(?:帽子|头饰|头盔|发卡|发带|头发|发型|长发|短发|马尾|脸部|面具|面纱|眼镜|耳环|胸部|披风|羽翼|翅膀|围巾|上衣|战袍|胸甲|战甲|盔甲|外套|长袍|夹克|礼服|腰部|腰带|下装|长裤|短裤|护腿|裙子|鞋子|战靴|短靴|长靴|皮肤))$'
    )

    def is_placeholder_name(nm: str) -> bool:
        s = (nm or '').strip()
        return (not s) or s in ('测试', 'test', 'TEST', '未命名') or s.isdigit() or bool(re.fullmatch(r'[a-z]{2,8}_\d+', s.lower()))

    def outfit_fp(slot_to_id: dict):
        body = tuple(sorted((sl, aid) for sl, aid in slot_to_id.items() if sl in BODY_SLOTS))
        if len(body) >= 2:
            return body
        return tuple(sorted(slot_to_id.items()))

    def derive_set_rarity(aids, fallback_num=-1):
        nums = [avatars[a]['rarity_num'] for a in aids if a in avatars and avatars[a]['rarity_num'] >= 0]
        rnum = max(nums) if nums else fallback_num
        return rnum, RARITIES.get(rnum, '高级')

    def clean_piece_prefix(nm: str) -> str:
        s = (nm or '').strip()
        for _ in range(3):
            ns = SLOT_SUFFIX_RE.sub('', s).strip(' -·_')
            if ns == s or len(ns) < 2:
                break
            s = ns
        return s

    def common_set_name(aids, fallback_label):
        real_names = [avatars[a]['name'] for a in aids if a in avatars and not is_placeholder_name(avatars[a]['name'])]
        if not real_names:
            return fallback_label
        # Try longest common prefix of cleaned names
        cleaned = [clean_piece_prefix(n) for n in real_names]
        cleaned = [c for c in cleaned if len(c) >= 2]
        if len(cleaned) >= 2:
            prefix = cleaned[0]
            for c in cleaned[1:]:
                k = 0
                while k < len(prefix) and k < len(c) and prefix[k] == c[k]:
                    k += 1
                prefix = prefix[:k]
            prefix = prefix.strip(' -·_[]()（）')
            if len(prefix) >= 2:
                return prefix + ' 套装'
        # Fallback: coat or cap cleaned name
        return cleaned[0] + ' 套装' if cleaned else fallback_label

    # 1. Resolve all Cash Packages (stackable sets) and deduplicate identical outfits per job
    # Keep per (job, fp) -> (score, group, item_id, ordered_ids, rarity_zh, slot_to_id)
    chosen_sets = {}
    pkg_ids_used = {}
    for pid in pkg_map:
        by_slot, suitable = resolve_set(pid, 0)
        if len(by_slot) < 2:
            continue
        compatible = set(header['poses'])
        suit_valid = {j for j in suitable if j in header['poses']}
        if suit_valid:
            compatible.intersection_update(suit_valid)
        for sl, (aid, jobs) in by_slot.items():
            if jobs and '[all]' not in jobs:
                compatible.intersection_update(jobs)
        if not compatible:
            continue
        slot_to_id = {sl: aid for sl, (aid, _) in by_slot.items()}
        ordered_ids = list(slot_to_id.values())
        pname, prar = pkg_meta.get(pid, ('', -1))
        rnum, rzh = derive_set_rarity(ordered_ids, prar)
        fp = outfit_fp(slot_to_id)
        score = (0 if is_placeholder_name(pname) else 1, len(slot_to_id), -pid)
        for j in compatible:
            key = (j, fp)
            prev = chosen_sets.get(key)
            if prev is None or score > prev[0]:
                chosen_sets[key] = (score, 'stackable', pid, ordered_ids, rzh, slot_to_id)
        pkg_ids_used[pid] = (ordered_ids, rzh)

    # 2. Compose single-piece avatar sets per job (PartSet / SetName / EffectSet / Rare Sky Sets)
    # Index visual avatars by job for fast grouping
    job_avatars = defaultdict(list)
    all_jobs = list(header['poses'].keys())
    for aid, info in avatars.items():
        if not info['has_vis'] or info['slot'] in ('weapon', 'aura'):
            continue
        targets = all_jobs if '[all]' in info['fits'] else [j for j in info['fits'] if j in header['poses']]
        for j in targets:
            job_avatars[j].append((aid, info))

    composed_rows = []
    next_comp_id = 900000001
    for j, entries in job_avatars.items():
        # Group into candidate sets
        groups = defaultdict(list)
        ps_skins = defaultdict(list)
        for aid, info in entries:
            sl = info['slot']
            c = info['color'] if info['color'] >= 0 else 0
            ps_list = [p for p in info['part_sets'] if p not in (0, 2, 3)]
            if sl == 'skin':
                for ps in ps_list:
                    ps_skins[ps].append(aid)
                continue
            placed = False
            # Priority 1: Specific [part set index]
            for ps in ps_list:
                groups[('ps', ps, c)].append((aid, info))
                placed = True
            if placed:
                continue
            # Priority 2: Explicit [set name]
            if info['set_name']:
                groups[('sn', info['set_name'], c)].append((aid, info))
                continue
            # Priority 3: Rare/Unique Sky Set with [effect part set index]
            if info['effect_set'] > 0:
                groups[('eps', info['effect_set'], c)].append((aid, info))
                continue
            # Priority 4: Classic Rare/Unique Sky Sets grouped by cleaned name prefix + color
            if info['rarity_num'] >= 2 and not is_placeholder_name(info['name']):
                pref = clean_piece_prefix(info['name'])
                if len(pref) >= 2:
                    groups[('rn', info['rarity_num'], pref, c)].append((aid, info))

        # Track color counts per base key so we only add [A型/B型] suffix when a set has multiple colors
        base_colors = defaultdict(set)
        for gkey in groups:
            base_colors[gkey[:-1]].add(gkey[-1])

        for gkey, members in groups.items():
            by_slot = {}
            # Sort members so real Chinese names & lower IDs win per slot
            members.sort(key=lambda x: (is_placeholder_name(x[1]['name']), x[0]))
            for aid, info in members:
                sl = info['slot']
                if sl not in by_slot:
                    by_slot[sl] = aid
            min_slots = 3 if gkey[0] in ('ps', 'sn', 'eps') else 4
            if len(by_slot) < min_slots:
                continue
            # Attach matching skin for specific part_set if available
            if gkey[0] == 'ps' and gkey[1] in ps_skins and 'skin' not in by_slot:
                skins = ps_skins[gkey[1]]
                c_idx = gkey[2]
                by_slot['skin'] = skins[c_idx] if 0 <= c_idx < len(skins) else skins[0]

            fp = outfit_fp(by_slot)
            ordered_ids = [by_slot[s] for s in (*BODY_SLOTS, 'skin') if s in by_slot]
            rnum, rzh = derive_set_rarity(ordered_ids, 1)

            # Build readable set name
            color_tag = ''
            if len(base_colors[gkey[:-1]]) > 1 and gkey[-1] in COLOR_LABELS:
                color_tag = f' [{COLOR_LABELS[gkey[-1]]}]'
            if gkey[0] == 'ps':
                base_name = partset_names.get(str(gkey[1]), '').strip()
                if not base_name:
                    base_name = common_set_name(ordered_ids, f'组合套装 #{gkey[1]}')
            elif gkey[0] == 'sn':
                base_name = gkey[1]
            elif gkey[0] == 'eps':
                base_name = common_set_name(ordered_ids, f'稀有装扮套装 #{gkey[1]}')
            else:
                base_name = gkey[2] + ' 套装'
            full_name = base_name + color_tag

            # Deduplicate with Package Sets: if same outfit already exists, keep the better-named one
            key = (j, fp)
            prev = chosen_sets.get(key)
            if prev is not None:
                prev_score, prev_g, prev_id, _pids, _prar, _pslots = prev
                # Keep package set unless it had a placeholder name ("测试") and composed set has a real name
                if prev_score[0] == 1 or is_placeholder_name(full_name):
                    continue
                # Upgrade placeholder package entry with this cleanly named composed set
            comp_id = next_comp_id
            next_comp_id += 1
            icon_aid = by_slot.get('coat') or by_slot.get('cap') or ordered_ids[0]
            icon_info = avatars[icon_aid]
            score = (0 if is_placeholder_name(full_name) else 1, len(by_slot), -comp_id)
            chosen_sets[key] = (score, 'composed_set', comp_id, ordered_ids, rzh, by_slot)
            composed_rows.append((
                'composed_set', comp_id, full_name, full_name,
                f'composed_set/{gkey[0]}_{comp_id}', '组合套装', rzh,
                j, icon_info['img'], icon_info['frame'],
                0, 20 + {'神话':0,'史诗':1,'传说':2,'神器':3,'稀有':4,'高级':5}.get(rzh, 6),
                '', dump(ordered_ids),
            ))

    # 3. Persist deduplicated Package Sets + Composed Sets into sqlite
    active_comp_ids = {cid for _sc, grp, cid, *_ in chosen_sets.values() if grp == 'composed_set'}
    wardrobe_set_rows = []
    for (j, _fp), (_sc, grp, sid, ordered_ids, rzh, _slots) in chosen_sets.items():
        wardrobe_set_rows.append((j, 'set', '', grp, sid))

    # Populate visual + derived rarity on every resolved cash package so direct ID lookups still work,
    # while wardrobe only exposes the deduplicated set list.
    for pid, (ordered_ids, rzh) in pkg_ids_used.items():
        db.execute("update item set visual=?, rar=? where g='stackable' and id=?", (dump(ordered_ids), rzh, pid))
    if composed_rows:
        db.executemany(
            'insert into item values (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',
            [row for row in composed_rows if row[1] in active_comp_ids],
        )
    db.executemany('insert into wardrobe values(?,?,?,?,?)', wardrobe_set_rows)
    sets = len({(grp, sid) for _j, _m, _sl, grp, sid in wardrobe_set_rows})
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
