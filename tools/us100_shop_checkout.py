"""Reversible 100US checkout localization (display labels, sprites and one offer title)."""
import argparse
import base64
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
from tempfile import TemporaryDirectory
from zipfile import ZipFile, ZipInfo

from us100_checkout_atlas import BASE as NPK_BASE, NAME, build as build_atlas
from us100_shop_load_atlas import verify_spans
from us100_status_dstr import ACT, CLEAR_FONTS, CLIENT, EQUIP_PVF, EXE, REL, GameFonts, core, extract, key_pair, pack, replace, unpack


ROOT=Path(__file__).resolve().parents[1]
RUNTIME=ROOT/'runtime'
AUDIT=RUNTIME/'us100-shop-payment-audit'
BASE_INNER=RUNTIME/'us100-equipfilter-pvf-r1/Script.inner.pvf'
BASE_INNER_HASH='bb60cc55c64f7d2b9539b035468985d13b8b92f09234f737350d5d0d74e2406e'
PVF_INSPECT=RUNTIME/'localize-us100-audit/pvfinspect-localization.exe'
DSTR_BASE='7e3e9ffbb06a01097ad79eff6c394416a1d34ac095bacd8ab838eaeba7199d99'
LIVE={'dstr.dat':CLIENT/'dstr.dat', NAME:CLIENT/'ImagePacks2'/NAME, 'Script.pvf':CLIENT/'Script.pvf'}
EXPECTED={'dstr.dat':DSTR_BASE, NAME:NPK_BASE, 'Script.pvf':EQUIP_PVF}
ENTRY='etc/(r)cerashop.etc'
OLD='The Neo: Awakened Ones Package'; NEW='《新：觉醒者》礼包'
OFFER_PREFIX='3400149 590005784 0 0 3980 0 0 '
TITLE_CELL=52844*5
ROWS={
    '23065':('Item Shop Transaction Window','商城结算'),
    '23066':('Please double check the items for purchase, and then press Buy\\nto complete the transaction.',
             '请核对要购买的商品。\\n点击「购买」完成交易。'),
    '23067':('Would you like to purchase the following items?','确定购买以下商品吗？'),
    '23071':('In possession','持有'), '23072':('Pay','应付'), '23073':('Remaining','余额'),
    '33136':('All Payment Methods','全部支付方式'), '33137':('Cera','点券'),
    '33138':('Gold Coin','金币'), '33141':(' Payment','支付'),
    '35330':('Rebated Mileage Points','返还商城积分'),
    '51355':('Discounted','优惠'), '51356':('Apply','使用'),
    '51357':('Error: Cannot apply coupon to any applicable item.','当前商品无法使用优惠券。'),
    '51358':('Error: Target item is not applicable with coupon usage.','所选商品不适用这张优惠券。'),
    '182':('%d CERA','%d 点券'), '248':('%s CERA','%s 点券'), '252':('%d CERA','%d 点券'),
    '871':('%d CERA','%d 点券'), '910':(' %d CERA',' %d 点券'), '914':(' %d CERA',' %d 点券'),
    '35266':('%s CERA','%s 点券'), '37008':('%s CERA','%s 点券'), '400001639':('%s CERA','%s 点券'),
}
SENTINELS={'23068':' 点券','39024':'地下城','60860':'神话','100087494':'全部分类','501031101':'鬼剑士'}


def archive(path, files):
    with ZipFile(path,'x') as z:
        for n,p in sorted(files.items()):
            with z.open(ZipInfo(n),'w') as dst, p.open('rb') as src:shutil.copyfileobj(src,dst,4<<20)


def member_hash(z, n):
    with z.open(n) as f:return hashlib.file_digest(f,'sha256').hexdigest()


def identities(path):
    with ZipFile(path) as z:
        if sorted(z.namelist())!=sorted(LIVE):raise ValueError('bundle inventory differs')
        return {n:member_hash(z,n) for n in LIVE}


def extract_files(path, target):
    with ZipFile(path) as z:
        if sorted(z.namelist())!=sorted(LIVE):raise ValueError('bundle inventory differs')
        for n in LIVE:
            with z.open(n) as src, (target/n).open('xb') as w:shutil.copyfileobj(src,w,4<<20)
    return {n:target/n for n in LIVE}


def inspect(path, receipt):
    m=core.read(receipt); ids=identities(path); values=m['catalog'][ids['Script.pvf']]
    with ZipFile(path) as z:
        keys=key_pair(CLIENT/'DFO.exe'); d=z.read('dstr.dat'); raw=unpack(d,keys)
        if pack(raw,keys)!=d:raise ValueError('native DSTR round trip differs')
        labels=extract(raw,ROWS.keys()|SENTINELS.keys())
    if any(labels[k]!=v for k,v in SENTINELS.items()):raise ValueError('earlier localization changed')
    print(json.dumps({'assets':ids, 'item':values, 'labels':labels},ensure_ascii=False,sort_keys=True))


def prepare_pvf(out,run):
    inner=out/'Script.inner.pvf'; wrapped=out/'Script.pvf'; plan=out/'catalog-inline.json'
    core.atomic(plan,{ENTRY:[{'field':'[package display name]','source':OLD,'target':NEW,'count':1}]})
    empty=out/'empty'; empty.mkdir()
    if core.digest(BASE_INNER)!=BASE_INNER_HASH:raise ValueError('wrong inner PVF identity')
    run('APPLY CATALOG',AUDIT/'pvflocalize-checkout.exe','-source',BASE_INNER,'-dir',empty,'-inline-plan',plan,'-output',inner)
    run('WRAP',sys.executable,Path(__file__).with_name('rewrap_us100_pvf.py'),'--original',CLIENT/'Script.pvf-1',
        '--baseline',RUNTIME/'pvf_source/Script.inner.pvf','--candidate',inner,'--output',wrapped)
    return inner,wrapped


def diff_pvf(out, inner, wrapped, run):
    exports=[]
    for label, src in (('before-catalog',BASE_INNER),('after-catalog',inner)):
        target=out/label
        run(label,PVF_INSPECT,'-source',src,'-files',ENTRY+',etc/(f)cerashop.etc,etc/(r)cerashop_first.etc','-output',target)
        m=core.read(target/'exports.json')
        if m[0]['entry']['archive_path']!=ENTRY:raise ValueError('catalog export mismatch')
        exports.append((m,(target/m[0]['raw_file']).read_bytes(),(target/m[0]['text_file']).read_text(encoding='utf-8')))
    (b,raw,btxt),(m,raw2,mtxt)=exports
    a,bound=TITLE_CELL+1,TITLE_CELL+5
    if (b[1:]!=m[1:] or raw[:a]!=raw2[:a] or raw[bound:]!=raw2[bound:] or raw[a:bound]==raw2[a:bound]
        or btxt.count(OFFER_PREFIX+'`'+OLD+'`')!=1
        or btxt.replace(OFFER_PREFIX+'`'+OLD+'`',OFFER_PREFIX+'`'+NEW+'`',1)!=mtxt):
        raise ValueError('catalog changed outside a single display-name cell')
    return {k:{'product':3400149,'template':590005784,'price':3980,'name':n,'entry':ENTRY,'cell':52844}
             for k,n in ((EQUIP_PVF,OLD),(core.digest(wrapped),NEW))}


def build(out):
    out=out.resolve()
    if out.is_relative_to(CLIENT) or any(core.digest(p)!=EXPECTED[n] for n,p in LIVE.items()):
        raise ValueError('unknown live checkout baseline')
    out.mkdir(parents=True)
    before={n:out/('before-'+n) for n in LIVE}; new={n:out/n for n in LIVE}
    for n,p in LIVE.items():core.file_copy(p,before[n])
    z0,z1,d,r=(out/n for n in ('before-UI.zip','UI.zip','UI.diff.json','ROLLBACK.sh'))
    archive(z0,before); records=['Scope: checkout DSTR, IMG frames 13–24 and 29–36, product 3400149 display field only.']
    def run(label,*args):
        cmd=list(map(str,args));p=subprocess.run(cmd,capture_output=True,encoding='utf-8',env={**os.environ,'PYTHONIOENCODING':'utf-8'})
        records.extend([label,'argv='+json.dumps(cmd,ensure_ascii=False),f'exit={p.returncode}',
                        'stdout:',p.stdout,'stderr:',p.stderr]);p.check_returncode();return p.stdout
    try:
        spans=build_atlas(before[NAME],new[NAME]); p=new[NAME].read_bytes()
        keys=key_pair(CLIENT/'DFO.exe'); txt=unpack(before['dstr.dat'].read_bytes(),keys)
        patched=replace(txt,ROWS)
        if replace(patched,{k:(b,a) for k,(a,b) in ROWS.items()})!=txt:raise ValueError('DSTR delta differs')
        new['dstr.dat'].write_bytes(pack(patched,keys))
        inner,wrapped=prepare_pvf(out,run); catalog=diff_pvf(out,inner,wrapped,run)
        archive(z1,new)
        core.atomic(d,{'before':core.digest(z0),'after':core.digest(z1),'catalog':catalog,
            'rows':ROWS,'images':[{'begin':x,'end':y,'frame':i,'payload':base64.b64encode(p[x:y]).decode()}
                                 for x,y,i in spans]})
        prior=RUNTIME/'us100-town-shop-r3/ROLLBACK.sh'
        r.write_text(prior.read_text(encoding='utf-8'),encoding='utf-8',newline='\n');r.chmod(0o755)
        restore=out/'rollback-test.zip';core.file_copy(z1,restore); py=sys.executable; code=Path(__file__).resolve()
        b=run('BASELINE',py,code,'inspect','--file',z0,'--diff',d)
        a=run('MODIFIED',py,code,'inspect','--file',z1,'--diff',d)
        run('ROLLBACK.sh',py,r,restore)
        if b==a or b!=run('ROLLBACK',py,code,'inspect','--file',restore,'--diff',d) or core.digest(restore)!=core.digest(z0):
            raise ValueError('rollback behavior differs')
        again=out/'reapplied-UI.zip';run('REAPPLY',py,code,'apply','--file',restore,'--diff',d,'--out',again)
        records+=['Restored SHA-256: '+core.digest(restore),'Reapplied SHA-256: '+core.digest(again),
                  'Only four bytes of the existing offer title reference and the appended text pool changed in catalog scope. No transaction run.']
    finally:(out/'VERIFICATION.txt').write_text('\n'.join(records)+'\n',encoding='utf-8')
    for p in (z1,d,out/'VERIFICATION.txt',r):print(p,core.digest(p))


def replay(base,diff,out):
    m=core.read(diff)
    if core.digest(base)!=m['before'] or out.resolve().is_relative_to(CLIENT):raise ValueError('wrong patch baseline')
    with TemporaryDirectory(dir=out.parent) as x:
        root=Path(x); unpacked=extract_files(base,root); stage=root/'pvf';stage.mkdir()
        keys=key_pair(CLIENT/'DFO.exe'); source=unpacked['dstr.dat']; raw=unpack(source.read_bytes(),keys)
        rows={k:tuple(v) for k,v in m['rows'].items()}
        if rows!=ROWS:raise ValueError('wrong checkout text delta')
        source.write_bytes(pack(replace(raw,rows),keys))
        path=unpacked[NAME]; old=path.read_bytes(); b=bytearray(old); spans=[]
        for row in m['images']:
            a,z,i=row['begin'],row['end'],row['frame']; payload=base64.b64decode(row['payload'],validate=True)
            if len(payload)!=z-a:raise ValueError('bad atlas span')
            b[a:z]=payload;spans.append((a,z,i))
        verify_spans(old,b,spans);path.write_bytes(b)
        if core.digest(unpacked['Script.pvf'])!=EQUIP_PVF:raise ValueError('wrong original outer script')
        _,fresh=prepare_pvf(stage,lambda label,*cmd:subprocess.run(list(map(str,cmd)),check=True))
        unpacked['Script.pvf']=fresh;archive(out,unpacked)
    if core.digest(out)!=m['after']:raise ValueError('reapplied checkout package differs')


def deploy(out, rollback=False):
    out=out.resolve(); m=core.read(out/'UI.diff.json'); b,a=(out/n for n in ('before-UI.zip','UI.zip'))
    if out.is_relative_to(CLIENT) or (core.digest(b),core.digest(a))!=(m['before'],m['after']) or core.digest(out/'reapplied-UI.zip')!=m['after']:
        raise ValueError('unverified checkout package')
    left,right=({n:out/('before-'+n) for n in LIVE},{n:out/n for n in LIVE}); old,new=(right,left) if rollback else (left,right)
    x,y=(identities(a),identities(b)) if rollback else (identities(b),identities(a))
    fonts=GameFonts(CLIENT/'Fonts')
    with fonts.locked():
        if (core.client_up() or fonts.hashes()!=CLEAR_FONTS or core.digest(CLIENT/REL)!=ACT or core.digest(CLIENT/'DFO.exe')!=EXE
            or any((core.digest(old[n]),core.digest(new[n]),core.digest(p))!=(x[n],y[n],x[n]) for n,p in LIVE.items())):
            raise ValueError('client open, changed, or unknown resource identity')
        done=[]
        try:
            for n,p in LIVE.items():
                if core.client_up() or core.digest(p)!=x[n]:raise ValueError('client reopened or resource changed')
                core.file_copy(new[n],p);done.append(n)
                if core.digest(p)!=y[n]:raise ValueError('installed checkout file differs')
        except Exception:
            if not core.client_up():
                for n in reversed(done):core.file_copy(old[n],LIVE[n])
            raise
        data={'utc':datetime.now(timezone.utc).isoformat(),'live':{str(p):core.digest(p) for p in LIVE.values()},
              'offer':m['catalog'][y['Script.pvf']],'rollback':rollback,'game_checked':False}
        core.atomic(out/'deployment.json',data);print(json.dumps(data,ensure_ascii=False))


if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('action',choices=('build','inspect','apply','install','rollback'))
    for k in ('file','diff','out'):p.add_argument('--'+k,type=Path)
    a=p.parse_args()
    if a.action=='build':build(a.out)
    elif a.action=='inspect':inspect(a.file,a.diff)
    elif a.action=='apply':replay(a.file,a.diff,a.out)
    else:deploy(a.out,a.action=='rollback')
