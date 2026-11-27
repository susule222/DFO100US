"""Localize remaining 100US equipment-tooltip labels on top of the installed checkout build."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
from tempfile import TemporaryDirectory
from zipfile import ZipFile

from us100_shop_checkout import archive, member_hash
from us100_status_dstr import ACT, CLEAR_FONTS, CLIENT, EXE, REL, GameFonts, core, extract, key_pair, pack, replace, unpack


ROOT=Path(__file__).resolve().parents[1]; RT=ROOT/'runtime'
CHECKOUT=RT/'us100-shop-checkout-r2'
BASE_INNER=CHECKOUT/'Script.inner.pvf'
INNER_HASH='5381042bb82dab7403d85006f0a5517b8099f5600f08d771304875d6bc1afd01'
BASE={'dstr.dat':'d960047de1633b1b106ff41b4f490430d15643c49f087a25f178bb94891f5a49',
      'Script.pvf':'21dadda93d5614180446ae83ddaedfcc6c15cea6c903597d87b29d7ba8982627'}
LIVE={n:CLIENT/n for n in BASE}
ATLAS=CLIENT/'ImagePacks2'/'sprite_interface2_univ_coupon.NPK'
ENTRY='string/equipment.uv.str'
TABLE_HASH='1f00d9d612c5ad399075e79d0f1d51fec0bcba0804cd991ceb688d95330b2ce5'
CATALOG_HASH='28669c04034c09b5083cfe6215fe7e8fa640aa99b0e5104899f05bc98e762052'
TABLE_SOURCE=RT/'us100-tooltip-cleanup-audit'/'00-equipment.uv.str.bin'
INSPECTOR=RT/'localize-us100-audit/pvfinspect-localization.exe'
EDITOR=RT/'localize-us100-audit/pvflocalize.exe'
EQUIPMENT_IDS=('100050911','100090550','100070725','100080748','100060784')
ITEM_KEYS={f'{p}_explain_{i}' for p in ('basic','detail') for i in EQUIPMENT_IDS}
OLD='恢复自身HP最大值'; NEW='恢复自身最大生命值'
GOLD=re.compile(r'\s*(?:%[0-9]*[sdui]\s*)?gold\s*',re.I)
SENTINELS={'60860':'神话','100002441':'未附魔','60938':'详情（%s）','23065':'商城结算','33141':'支付','100087494':'全部分类'}
LABELS={
    '100089003':('STR %+.*f','力量 %+.*f'),'100089004':('INT %+.*f','智力 %+.*f'),
    '100089005':('VIT %+.*f','体力 %+.*f'),'100089006':('SPR %+.*f','精神 %+.*f'),
    '100089008':('Total Growth Option Levels','成长属性等级总和'),
    '100001024':('STR','力量'),'100001025':('INT','智力'),'100001194':('STR','力量'),
    '100030829':('STR','力量'),'100030830':('INT','智力'),'100030832':('SPR','精神'),
    '1099':('Hit Rate %+.*f%%%%','命中率 %+.*f%%%%'), '36458':('Hit Rate %d%%%%','命中率 %d%%%%'),
    '100003150':('Hit Rate +%d%%%%','命中率 +%d%%%%'), '75917':('Hit Rate : +30%%%%','命中率：+30%%%%'),
    '100088589':('Enchantment','附魔'),
} | {i:('Enchant','附魔') for i in ('21271','22410','44131','69215','100008027','100088608')}
LABELS.update({i:('Hit Rate','命中率') for i in ('4096','6124','100039','100030824')})


def sha(b):return hashlib.sha256(b).hexdigest()


def make_rows(txt):
    coins={}
    for line in txt.decode('utf-8').splitlines():
        k,_,v=line.partition('>')
        if k.isdigit() and GOLD.fullmatch(v):coins[k]=(v,re.sub('gold','金币',v,flags=re.I))
    if len(coins)!=48:raise ValueError('unexpected exact currency display strings')
    return coins|LABELS


def table_edit(raw,old,new):
    seen={}; text=raw.decode('utf-16-le'); lines=text.split('\r\n')
    for i,l in enumerate(lines):
        key,sep,value=l.partition('>')
        if key not in ITEM_KEYS:continue
        if not sep or key in seen or value.count(old)!=1 or new in value:
            raise ValueError(f'unexpected equipment description {key}')
        v=value.replace(old,new,1); seen[key]=v; lines[i]=key+'>'+v
    if seen.keys()!=ITEM_KEYS:raise ValueError('equipment variant descriptions incomplete')
    return '\r\n'.join(lines).encode('utf-16-le'),seen


def identities(path):
    with ZipFile(path) as z:
        if sorted(z.namelist())!=sorted(LIVE):raise ValueError('bundle entries differ')
        return {n:member_hash(z,n) for n in LIVE}


def extract_src(path,dst):
    with ZipFile(path) as z:
        if sorted(z.namelist())!=sorted(LIVE):raise ValueError('bundle entries differ')
        for n in LIVE:
            with z.open(n) as f,(dst/n).open('xb') as g:shutil.copyfileobj(f,g,4<<20)
    return {n:dst/n for n in LIVE}


def prepare_pvf(out,run):
    source=TABLE_SOURCE.read_bytes()
    if sha(source)!=TABLE_HASH or core.digest(BASE_INNER)!=INNER_HASH:raise ValueError('shop PVF base identity changed')
    chosen,shown=table_edit(source,OLD,NEW)
    if table_edit(chosen,NEW,OLD)[0]!=source:raise ValueError('equipment table not byte-reversible')
    d=out/'replacement'; t=d/ENTRY;t.parent.mkdir(parents=True);t.write_bytes(chosen)
    i=out/'Script.inner.pvf'; w=out/'Script.pvf'
    run('APPLY PVF',EDITOR,'-source',BASE_INNER,'-dir',d,'-output',i)
    run('WRAP PVF',sys.executable,Path(__file__).with_name('rewrap_us100_pvf.py'),'--original',CLIENT/'Script.pvf-1',
        '--baseline',RT/'pvf_source/Script.inner.pvf','--candidate',i,'--output',w)
    return w,i,chosen,shown


def inspect(path,manifest):
    ids=identities(path); m=core.read(manifest)
    with ZipFile(path) as z:
        keys=key_pair(CLIENT/'DFO.exe'); blob=z.read('dstr.dat'); plain=unpack(blob,keys)
    if pack(plain,keys)!=blob:raise ValueError('DSTR round trip differed')
    v=extract(plain,m['rows'].keys()|SENTINELS.keys())
    if any(v[k]!=s for k,s in SENTINELS.items()):raise ValueError('previous releases were not retained')
    print(json.dumps({'assets':ids,'labels':v,'equipment':m['equipment'][ids['Script.pvf']]},ensure_ascii=False,sort_keys=True))


def build(out):
    out=out.resolve()
    if out.is_relative_to(CLIENT) or any(core.digest(p)!=BASE[n] for n,p in LIVE.items()):raise ValueError('unknown live baseline')
    out.mkdir(parents=True); before={n:out/('before-'+n) for n in LIVE}; new={n:out/n for n in LIVE}
    for n,p in LIVE.items():core.file_copy(p,before[n])
    z0,z1,d,r=(out/n for n in ('before-UI.zip','UI.zip','UI.diff.json','ROLLBACK.sh'))
    archive(z0,before); log=['Scope: exact remaining tooltip format strings and HP phrasing in ten description entries.']
    def run(label,*cl):
        cmd=list(map(str,cl)); p=subprocess.run(cmd,capture_output=True,encoding='utf-8',env={**os.environ,'PYTHONIOENCODING':'utf-8'})
        log.extend([label,'argv='+json.dumps(cmd,ensure_ascii=False),f'exit={p.returncode}',
                    'stdout:',p.stdout,'stderr:',p.stderr]); p.check_returncode(); return p.stdout
    try:
        keys=key_pair(CLIENT/'DFO.exe'); base=unpack(before['dstr.dat'].read_bytes(),keys); rows=make_rows(base)
        zh=replace(base,rows)
        if replace(zh,{k:(z,e) for k,(e,z) in rows.items()})!=base:raise ValueError('DSTR not byte-reversible')
        new['dstr.dat'].write_bytes(pack(zh,keys))
        wrapped,inner,expected,items=prepare_pvf(out,run)
        reopened=out/'reopened';run('REOPEN PVF',INSPECTOR,'-source',inner,'-files',ENTRY+',etc/(r)cerashop.etc','-output',reopened)
        records=core.read(reopened/'exports.json')
        if (records[0]['entry']['archive_path']!=ENTRY or (reopened/records[0]['raw_file']).read_bytes()!=expected or
            records[1]['entry']['archive_path']!='etc/(r)cerashop.etc' or records[1]['raw_sha256']!=CATALOG_HASH):
            raise ValueError('modified native table or preserved checkout catalog differs')
        archive(z1,new)
        core.atomic(d,{'before':core.digest(z0),'after':core.digest(z1),'rows':rows,
                       'pvf':{'entry':ENTRY,'old':OLD,'new':NEW,'keys':sorted(ITEM_KEYS)},
                       'equipment':{BASE['Script.pvf']:{k:v.replace(NEW,OLD) for k,v in items.items()},core.digest(wrapped):items}})
        r.write_text((CHECKOUT/'ROLLBACK.sh').read_text(encoding='utf-8'),encoding='utf-8',newline='\n');r.chmod(0o755)
        copy=out/'rollback-test.zip';core.file_copy(z1,copy);py=sys.executable;code=Path(__file__).resolve()
        b=run('BASELINE',py,code,'inspect','--file',z0,'--diff',d)
        a=run('MODIFIED',py,code,'inspect','--file',z1,'--diff',d)
        run('ROLLBACK.sh',py,r,copy)
        if b==a or b!=run('ROLLBACK',py,code,'inspect','--file',copy,'--diff',d) or core.digest(copy)!=core.digest(z0):
            raise ValueError('rollback behavior changed')
        replay=out/'reapplied-UI.zip';run('REAPPLY',py,code,'apply','--file',copy,'--diff',d,'--out',replay)
        log+=['Restored SHA-256: '+core.digest(copy),'Reapplied SHA-256: '+core.digest(replay),
              'Unchanged checkout catalog: '+CATALOG_HASH]
    finally:(out/'VERIFICATION.txt').write_text('\n'.join(log)+'\n',encoding='utf-8')
    for p in (z1,d,out/'VERIFICATION.txt',r):print(p,core.digest(p))


def apply(base,delta,out):
    m=core.read(delta)
    if core.digest(base)!=m['before'] or out.resolve().is_relative_to(CLIENT):raise ValueError('wrong patch base')
    with TemporaryDirectory(dir=out.parent) as x:
        temp=Path(x); src=extract_src(base,temp)
        if core.digest(src['Script.pvf'])!=BASE['Script.pvf']:raise ValueError('wrong pristine candidate')
        keys=key_pair(CLIENT/'DFO.exe'); raw=unpack(src['dstr.dat'].read_bytes(),keys); rows={k:tuple(v) for k,v in m['rows'].items()}
        if rows!=make_rows(raw) or m['pvf']!={'entry':ENTRY,'old':OLD,'new':NEW,'keys':sorted(ITEM_KEYS)}:raise ValueError('unexpected patch scope')
        src['dstr.dat'].write_bytes(pack(replace(raw,rows),keys))
        stage=temp/'out'; stage.mkdir();src['Script.pvf']=prepare_pvf(stage,lambda label,*cmd:subprocess.run(list(map(str,cmd)),check=True))[0]
        archive(out,src)
    if core.digest(out)!=m['after']:raise ValueError('reapplied archive sha differs')


def deploy(out,rollback=False):
    out=out.resolve(); d=core.read(out/'UI.diff.json'); a,b=out/'before-UI.zip',out/'UI.zip'
    if out.is_relative_to(CLIENT) or (core.digest(a),core.digest(b))!=(d['before'],d['after']) or core.digest(out/'reapplied-UI.zip')!=d['after']:
        raise ValueError('unverified release bundle')
    left,right=({n:out/('before-'+n) for n in LIVE},{n:out/n for n in LIVE}); old,new=(right,left) if rollback else (left,right)
    expected,wanted=(identities(b),identities(a)) if rollback else (identities(a),identities(b))
    fonts=GameFonts(CLIENT/'Fonts')
    with fonts.locked():
        assets={p:core.digest(p) for p in (ATLAS,CLIENT/REL,CLIENT/'DFO.exe')}
        if (core.client_up() or fonts.hashes()!=CLEAR_FONTS or assets[CLIENT/'DFO.exe']!=EXE or assets[CLIENT/REL]!=ACT
            or any((core.digest(old[n]),core.digest(new[n]),core.digest(p))!=(expected[n],wanted[n],expected[n]) for n,p in LIVE.items())):
            raise ValueError('client process or baseline changed')
        done=[]
        try:
            for n,p in LIVE.items():
                if core.client_up() or core.digest(p)!=expected[n]:raise ValueError('client reopened')
                core.file_copy(new[n],p);done.append(n)
                if core.digest(p)!=wanted[n]:raise ValueError('installed hash mismatch')
            if any(core.digest(p)!=v for p,v in assets.items()):raise ValueError('unrelated assets changed')
        except Exception:
            if not core.client_up():
                for n in reversed(done):core.file_copy(old[n],LIVE[n])
            raise
        info={'utc':datetime.now(timezone.utc).isoformat(),'live':{str(p):core.digest(p) for p in LIVE.values()},
              'preserved':{str(p):v for p,v in assets.items()},'rollback':rollback,'game_checked':False}
        core.atomic(out/'deployment.json',info);print(json.dumps(info,ensure_ascii=False))


if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('action',choices=('build','inspect','apply','install','rollback'))
    for k in ('file','diff','out'):p.add_argument('--'+k,type=Path)
    a=p.parse_args()
    if a.action=='build':build(a.out)
    elif a.action=='inspect':inspect(a.file,a.diff)
    elif a.action=='apply':apply(a.file,a.diff,a.out)
    else:deploy(a.out,a.action=='rollback')
