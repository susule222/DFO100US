"""100US Elvenguard town sign and item-shop tab localization transaction."""

import argparse
import base64
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import struct
import subprocess
import sys
from zipfile import ZipFile

import numpy as np
from PIL import Image, ImageDraw
import zopfli.zlib

from us100_create_load_fix import sha, zip_out
from us100_img5 import views
from us100_quest_act import compress_exact, inflate_exact
from us100_shop_load_atlas import pixels, verify_spans
from us100_status_dstr import (ACT, CLEAR_FONTS, CLIENT, EQUIP_PVF, EXE, GameFonts, PROFILES, REL,
                              core, extract, key_pair, pack, replace, unpack)
from us100_ui_atlas import Frame, index, member, split_img


NAME='sprite_map_town_04_elvengard_object.NPK'
ENTRY='sprite/map/town/04_elvengard/object/sign.img'
PROFILE=PROFILES['shop-small-tabs']
LIVE={'dstr.dat':CLIENT/'dstr.dat',NAME:CLIENT/'ImagePacks2'/NAME}
SIGN_BASE='698da427123720ee069fe32a26f738c80183abdb834f9183ec44ae2613914f96'
DONOR='7b5948c71b97e2df83bc3964ebfd9e0c05f55628added322902d2db3b554057b'
RECTS=((54,23,127,42),(21,73,102,92),(102,87,174,115),(10,133,85,152),(68,155,150,177))
PLACENAMES=('艾尔文防线','公会大厅','亡者峡谷','格兰之森','赫顿玛尔')
ROOT=Path(__file__).resolve().parents[1]
LOAD=CLIENT/'ImagePacks2'/'sprite_interface2_nowloading.NPK'
SHOP=CLIENT/'ImagePacks2'/'sprite_interface2_cs_shop.NPK'


def read(archive):
    with ZipFile(archive) as f:
        if sorted(f.namelist())!=sorted(LIVE) or f.testzip() is not None:
            raise ValueError('wrong transaction entries')
        return {n:f.read(n) for n in LIVE}


def atlas(src, donor, dst):
    if core.digest(src)!=SIGN_BASE or core.digest(donor)!=DONOR:
        raise ValueError('unexpected sign source or donor identity')
    frame,=split_img(member(src,ENTRY)); raw=member(donor,ENTRY); cn,=views(raw)
    if struct.unpack_from('<iiII',raw,64)!=frame.geometry[2:] or cn.image().size!=frame.geometry[:2]:
        raise ValueError('donor sign geometry differs')
    before=frame.image(); translated=cn.image(); result=before.copy()
    allowed=np.zeros((before.height,before.width),dtype=bool)
    for x0,y0,x1,y1 in RECTS:
        crop=translated.crop((x0,y0,x1,y1)); result.paste(crop,(x0,y0)); allowed[y0:y1,x0:x1]=True
    if frame.meta[:8]!=struct.pack('<2I',14,6):raise ValueError('unexpected RGB1555 source frame')
    native=np.frombuffer(inflate_exact(frame.raw,before.width*before.height*2),dtype='<u2').reshape(allowed.shape).copy()
    patch=np.frombuffer(pixels(result,14),dtype='<u2').reshape(allowed.shape)
    native[allowed]=(native[allowed]&0x8000)|(patch[allowed]&0x7fff)
    raw16=native.tobytes()
    try:payload=compress_exact(raw16,len(frame.raw),frame.raw[:2])
    except ValueError:
        for rounds in (12,15,20,25,30):
            stream=zopfli.zlib.compress(raw16,numiterations=rounds)
            padding=len(frame.raw)-len(stream)
            if padding>=0 and padding%5==0:
                # Byte-aligned non-final empty stored DEFLATE blocks. No trailing garbage.
                payload=frame.raw[:2]+b'\x00\x00\x00\xff\xff'*(padding//5)+stream[2:]
                break
        else:raise ValueError('localized sign does not fit the original NPK span')
    if inflate_exact(payload,len(raw16))!=raw16:raise ValueError('lossless sign encoding differed')
    reopened=Frame(frame.meta,payload).image()
    original=np.asarray(before); changed=np.asarray(reopened)
    if not np.array_equal(original[~allowed],changed[~allowed]) or not np.array_equal(original[:,:,3],changed[:,:,3]):
        raise ValueError('sign opacity or pixels outside of five label rectangles changed')
    pos=index(src)[ENTRY][0]+32+len(frame.meta); end=pos+len(frame.raw)
    source=src.read_bytes()
    if source[pos:end]!=frame.raw:raise ValueError('NPK frame offset differs')
    new=source[:pos]+payload+source[end:];verify_spans(source,new,[(pos,end,0)])
    dst.write_bytes(new)
    if list(index(src).items())!=list(index(dst).items()) or split_img(member(dst,ENTRY))[0].meta!=frame.meta:
        raise ValueError('NPK or frame metadata differs')
    panel=Image.new('RGB',(760,580),'#222e27'); d=ImageDraw.Draw(panel)
    for i,im in enumerate((before,reopened)):
        t=im.resize((352,536),Image.Resampling.NEAREST);d.text((i*380+12,8),('BEFORE','AFTER')[i],fill='white');
        panel.paste(t,(i*380+12,30),t)
    panel.save(dst.parent/'sign-comparison.png')
    return {'start':pos,'end':end,'frame':0,'bytes':base64.b64encode(payload).decode('ascii')}


def inspect(archive):
    parts=read(archive); keys=key_pair(CLIENT/'DFO.exe'); plain=unpack(parts['dstr.dat'],keys)
    if pack(plain,keys)!=parts['dstr.dat']:raise ValueError('DSTR round trip mismatch')
    print(json.dumps({'assets':{n:sha(b) for n,b in parts.items()},
                      'labels':extract(plain,PROFILE.rows.keys()|PROFILE.sentinels.keys())},ensure_ascii=False))


def apply(source,diff,out):
    m=core.read(diff)
    if core.digest(source)!=m['before'] or out.resolve().is_relative_to(CLIENT):raise ValueError('wrong base file')
    parts=read(source); z=m['span']; x,y=z['start'],z['end']; b=base64.b64decode(z['bytes'],validate=True)
    if len(b)!=y-x or z['frame']!=0:raise ValueError('scope or payload different')
    old=parts[NAME]; parts[NAME]=old[:x]+b+old[y:]; verify_spans(old,parts[NAME],[(x,y,0)])
    rows={r['id']:(r['before'],r['after']) for r in m['rows']}
    if rows!=PROFILE.rows:raise ValueError('shop display string set differs')
    keys=key_pair(CLIENT/'DFO.exe'); parts['dstr.dat']=pack(replace(unpack(parts['dstr.dat'],keys),rows),keys)
    zip_out(out,parts)
    if core.digest(out)!=m['after']:raise ValueError('reapply archive SHA differs')


def build(out, donor):
    out=out.resolve()
    if out.is_relative_to(CLIENT) or core.digest(LIVE['dstr.dat'])!=PROFILE.baseline:raise ValueError('baseline differs')
    out.mkdir(parents=True)
    span=atlas(LIVE[NAME],donor,out/NAME)
    source={k:p.read_bytes() for k,p in LIVE.items()}; keys=key_pair(CLIENT/'DFO.exe')
    plain=unpack(source['dstr.dat'],keys); local=replace(plain,PROFILE.rows)
    if replace(local,{k:(a,b) for k,(b,a) in PROFILE.rows.items()})!=plain:
        raise ValueError('DSTR changed outside keyed display fields')
    candidate={NAME:(out/NAME).read_bytes(),'dstr.dat':pack(local,keys)}
    b,m,delta,r=(out/n for n in ('before-UI.zip','UI.zip','UI.diff.json','ROLLBACK.sh'))
    zip_out(b,source);zip_out(m,candidate)
    core.atomic(delta,{'before':core.digest(b),'after':core.digest(m),'span':span,
                       'rows':[{'id':k,'before':a,'after':b} for k,(a,b) in PROFILE.rows.items()]})
    r.write_text((ROOT/'runtime/us100-create-load-fix-r2/ROLLBACK.sh').read_text(encoding='utf-8'),
                 encoding='utf-8',newline='\n');r.chmod(0o755)
    copy=out/'rollback-test.zip';core.file_copy(m,copy)
    ledger=[str(LIVE[NAME]),ENTRY,'Source sign/donor identities: '+SIGN_BASE+' / '+DONOR,
            'One frame, text rectangles: '+str(list(zip(PLACENAMES,RECTS))),
            'Changed DSTR rows: '+', '.join(PROFILE.rows),'Fonts, shop badge and all other files left unchanged.']
    def run(label,*args):
        cmd=list(map(str,args));p=subprocess.run(cmd,capture_output=True,encoding='utf-8',
                                                  env={**os.environ,'PYTHONIOENCODING':'utf-8'})
        ledger.extend([label,json.dumps(cmd,ensure_ascii=False),f'exit={p.returncode}','stdout:',p.stdout,'stderr:',p.stderr]);
        p.check_returncode();return p.stdout
    try:
        code=Path(__file__).resolve(); py=sys.executable
        baseline=run('BASELINE',py,code,'inspect','--file',b); mod=run('MODIFIED',py,code,'inspect','--file',m)
        run('ROLLBACK.sh',py,r,copy)
        if core.digest(copy)!=core.digest(b) or baseline!=run('ROLLBACK',py,code,'inspect','--file',copy) or baseline==mod:
            raise ValueError('rollback/promote probe results differ')
        reapplied=out/'reapplied-UI.zip';run('REAPPLY',py,code,'apply','--file',copy,'--diff',delta,'--out',reapplied)
        ledger+=['Restored SHA-256: '+core.digest(copy),'Reapplied SHA-256: '+core.digest(reapplied),
                 'Verified unchanged source alpha, unchanged wood and symbols outside label areas, unchanged NPK offsets. No live render test.']
    finally:
        (out/'VERIFICATION.txt').write_text('\n'.join(ledger)+'\n',encoding='utf-8')
    for p in (m,delta,out/'VERIFICATION.txt',r):print(p,core.digest(p))


def deploy(out,rollback=False):
    out=out.resolve(); d=core.read(out/'UI.diff.json')
    b,m=(out/n for n in ('before-UI.zip','UI.zip'))
    if out.is_relative_to(CLIENT) or (core.digest(b),core.digest(m))!=(d['before'],d['after']):
        raise ValueError('offline transaction identity differs')
    base,mod=read(b),read(m);old,new=(mod,base) if rollback else (base,mod)
    fonts=GameFonts(CLIENT/'Fonts')
    with fonts.locked():
        if (core.client_up() or fonts.hashes()!=CLEAR_FONTS or core.digest(CLIENT/'DFO.exe')!=EXE or
            core.digest(CLIENT/'Script.pvf')!=EQUIP_PVF or core.digest(CLIENT/REL)!=ACT or
            any(core.digest(path)!=sha(old[n]) for n,path in LIVE.items())):
            raise ValueError('DFO open or source/client identity changed')
        protected={str(p):core.digest(p) for p in (LOAD,SHOP,CLIENT/REL,CLIENT/'Script.pvf')}
        done=[]
        try:
            for name,target in LIVE.items():
                if core.client_up() or core.digest(target)!=sha(old[name]):raise ValueError('client or target changed')
                backup=out/('before-'+name); candidate=out/name
                backup.write_bytes(base[name]);candidate.write_bytes(mod[name]);
                core.file_copy(backup if rollback else candidate,target)
                done.append((target,candidate if rollback else backup))
                if core.digest(target)!=sha(new[name]):raise ValueError('deployed hash differs')
            if any(core.digest(Path(p))!=v for p,v in protected.items()):raise ValueError('protected asset changed')
        except Exception:
            if not core.client_up():
                for target,undo in reversed(done):core.file_copy(undo,target)
            raise
        data={'utc':datetime.now(timezone.utc).isoformat(),'state':'rolled_back' if rollback else 'installed_pending_game_check',
              'live':{str(p):core.digest(p) for p in LIVE.values()},'protected':protected,'fonts':fonts.hashes()}
        core.atomic(out/'deployment.json',data); print(json.dumps(data,ensure_ascii=False))


if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('action',choices=('build','inspect','apply','install','rollback'))
    for k in ('out','donor','file','diff'):p.add_argument('--'+k,type=Path)
    a=p.parse_args()
    if a.action=='build':build(a.out,a.donor)
    elif a.action=='inspect':inspect(a.file)
    elif a.action=='apply':apply(a.file,a.diff,a.out)
    else:deploy(a.out,a.action=='rollback')
