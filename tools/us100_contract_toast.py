"""Localize the five in-town contract toast captions without changing frame layout."""
import argparse
import base64
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import struct
import subprocess
import sys

from PIL import Image, ImageDraw, ImageFilter, ImageFont
import numpy as np

from us100_quest_act import compress_exact, inflate_exact
from us100_shop_load_atlas import verify_spans
from us100_status_dstr import CLIENT, EXE, GameFonts, core
from us100_ui_atlas import Frame, index, member, split_img


NAME='sprite_interface2_premiumservice.NPK'
LIVE=CLIENT/'ImagePacks2'/NAME
BASE='30ba724daf36a1e86f081160a8562f2cd852428f552a4efee4f70ca648299795'
FOLDER='sprite/interface2/premiumservice/'
TARGET={FOLDER+'premiumservice.img':{
    9:('Conqueror\'s Contract','霸王之契约',38,42),
    10:('Tactician\'s Contract','达人之契约',36,41),
    13:('Growth Contract','成长之契约',40,43),
    14:('Cube Contract','晶之契约',32,38),
},FOLDER+'premiumservice_univ.img':{0:('Neo Premium Plus','新尊享特权',38,42)}}
FONT='C:/Windows/Fonts/msyhbd.ttc'


def title(src, caption, cut, left, neo):
    w,h=src.size; data=np.array(src); ys=np.where(data[:,left:,3]>=225)[0]
    if not len(ys) or cut>=left:raise ValueError('no distinct original title shape')
    data[:,cut:]=0
    out=Image.fromarray(data); font=ImageFont.truetype(FONT,16,index=1)
    mask=Image.new('L',src.size); painter=ImageDraw.Draw(mask)
    b=painter.textbbox((0,0),caption,font=font,anchor='lt')
    y=max(2,round((int(ys.min())+int(ys.max())-b[3])/2));painter.text((left,y),caption,font=font,anchor='lt',fill=255)
    shade=Image.new('RGBA',src.size,(71,34,7,0)); shadow=Image.new('L',src.size)
    shadow.paste(mask.filter(ImageFilter.MaxFilter(3)).filter(ImageFilter.GaussianBlur(.64)),(0,1))
    shade.putalpha(shadow.point(lambda x:x*4//5));out.alpha_composite(shade)
    top=(255,242,158) if neo else (255,240,195)
    bottom=(236,200,19) if neo else (248,165,77)
    lay=np.zeros((h,w,4),dtype=np.uint8)
    for r in range(h):
        t=max(0,min(1,(r-y)/max(1,b[3]-1)))
        lay[r,:,:3]=[round(a+(z-a)*t) for a,z in zip(top,bottom)]
    lay[:,:,3]=np.asarray(mask); out.alpha_composite(Image.fromarray(lay))
    if not np.array_equal(np.asarray(src)[:,:cut],np.asarray(out)[:,:cut]):raise ValueError('icon changed')
    return out


def audit(path):
    out={}
    for name,spec in TARGET.items():
        raw=member(path,name); fs=split_img(raw)
        for i,(_,_,cut,_) in spec.items():
            f=fs[i]; image=f.image(); import hashlib
            out[name+':'+str(i)]={'size':f.geometry,'icon_pixels':hashlib.sha256(image.crop((0,0,cut,image.height)).tobytes()).hexdigest(),
                                  'text_pixels':hashlib.sha256(image.crop((cut,0,image.width,image.height)).tobytes()).hexdigest()}
    return {'sha256':core.digest(path),'frames':out}


def build(out):
    out=out.resolve()
    if out.is_relative_to(CLIENT) or core.digest(LIVE)!=BASE:raise ValueError('unexpected premiumservice baseline')
    out.mkdir(parents=True); before,after,diff,roll=(out/n for n in ('before-'+NAME,NAME,'NPK.diff.json','ROLLBACK.sh'))
    core.file_copy(LIVE,before); original=before.read_bytes(); result=bytearray(original); spans=[]; preview=[]
    ix=index(before)
    for name,spec in TARGET.items():
        raw=member(before,name); frames=split_img(raw); pos=ix[name][0]+32+struct.unpack_from('<I',raw,16)[0]
        for i,f in enumerate(frames):
            if isinstance(f,int):continue
            end=pos+len(f.raw)
            if i in spec:
                old,zh,cut,left=spec[i]; src=f.image()
                if f.meta[:8]!=struct.pack('<2I',16,6) or original[pos:end]!=f.raw:raise ValueError('unexpected frame encoding')
                changed=title(src,zh,cut,left,name.endswith('_univ.img'))
                pixels=changed.tobytes('raw','BGRA'); packed=compress_exact(pixels,len(f.raw),f.raw[:2])
                if inflate_exact(packed,len(pixels))!=pixels:raise ValueError('lossy toast frame')
                result[pos:end]=packed;spans.append((pos,end,i,name,old,zh));preview.append((src,Frame(f.meta,packed).image()))
            pos=end
    verify_spans(original,result,[(x,y,i) for x,y,i,*_ in spans]); after.write_bytes(result)
    if list(ix.items())!=list(index(after).items()):raise ValueError('NPK member offsets changed')
    a,b=(audit(z) for z in (before,after));
    for k,v in a['frames'].items():
        w=b['frames'][k]
        if v['icon_pixels']!=w['icon_pixels'] or v['size']!=w['size'] or v['text_pixels']==w['text_pixels']:
            raise ValueError('changed icon, geometry, or unmodified caption')
    panel=Image.new('RGB',(892,450),'#4b3726'); labels=ImageDraw.Draw(panel)
    labels.text((6,5),'BEFORE',fill='white');labels.text((452,5),'AFTER',fill='white')
    for row,images in enumerate(preview):
        for col,im in enumerate(images):
            t=im.resize((im.width*2,im.height*2),Image.Resampling.NEAREST);panel.paste(t,(8+col*446,27+row*83),t)
    panel.save(out/'contract-comparison.png')
    core.atomic(diff,{'before':a['sha256'],'after':b['sha256'],'spans':[
        {'start':x,'end':y,'frame':i,'member':n,'old':e,'new':z,'payload':base64.b64encode(result[x:y]).decode()}
        for x,y,i,n,e,z in spans]})
    script=Path(__file__).with_name('us100_status_rollback.sh').read_text(encoding='utf-8')
    roll.write_text(script.replace('dstr.dat',NAME).replace('DSTR.diff.json','NPK.diff.json'),encoding='utf-8',newline='\n');roll.chmod(0o755)
    copy=out/'rollback-test.NPK';core.file_copy(after,copy); log=['Only the five caption pixel payloads changed. Icon pixel hashes and frame layout preserved.']
    def run(label,*args):
        cmd=list(map(str,args));p=subprocess.run(cmd,capture_output=True,encoding='utf-8',env={**os.environ,'PYTHONIOENCODING':'utf-8'})
        log.extend([label,'argv='+json.dumps(cmd,ensure_ascii=False),f'exit={p.returncode}',
                    'stdout:',p.stdout,'stderr:',p.stderr]);p.check_returncode();return p.stdout
    py=sys.executable;code=Path(__file__).resolve()
    try:
        base=run('BASELINE',py,code,'inspect','--file',before)
        new=run('MODIFIED',py,code,'inspect','--file',after)
        run('ROLLBACK.sh',py,roll,copy)
        if base!=run('ROLLBACK',py,code,'inspect','--file',copy) or new==base:raise ValueError('rollback behaviors differ')
        replay=out/'reapplied.NPK';run('REAPPLY',py,code,'apply','--file',copy,'--diff',diff,'--out',replay)
        log+=['Restored SHA-256: '+core.digest(copy),'Reapplied SHA-256: '+core.digest(replay)]
    finally:(out/'VERIFICATION.txt').write_text('\n'.join(log)+'\n',encoding='utf-8')
    for p in (after,diff,out/'VERIFICATION.txt',roll):print(p,core.digest(p))


def apply(source,diff,out):
    m=core.read(diff)
    if core.digest(source)!=m['before'] or out.resolve().is_relative_to(CLIENT):raise ValueError('wrong patch source')
    raw=source.read_bytes(); b=bytearray(raw); spans=[]
    for v in m['spans']:
        x,y,i=v['start'],v['end'],v['frame']; data=base64.b64decode(v['payload'],validate=True)
        if len(data)!=y-x:raise ValueError('payload size changed')
        b[x:y]=data;spans.append((x,y,i))
    verify_spans(raw,b,spans)
    with out.open('xb') as f:f.write(b)
    if core.digest(out)!=m['after']:raise ValueError('reapplied atlas hash differs')


def deploy(out,rollback=False):
    out=out.resolve();m=core.read(out/'NPK.diff.json');a,b=out/('before-'+NAME),out/NAME
    if out.is_relative_to(CLIENT) or (core.digest(a),core.digest(b))!=(BASE,m['after']) or core.digest(out/'reapplied.NPK')!=m['after']:
        raise ValueError('contract candidate not verified')
    old,new=(b,a) if rollback else (a,b)
    with GameFonts(CLIENT/'Fonts').locked():
        if core.client_up() or core.digest(LIVE)!=core.digest(old) or core.digest(CLIENT/'DFO.exe')!=EXE:raise ValueError('live baseline changed')
        text={str(p):core.digest(p) for p in (CLIENT/'dstr.dat',CLIENT/'Script.pvf')}
        core.file_copy(new,LIVE)
        if core.digest(LIVE)!=core.digest(new) or any(core.digest(Path(n))!=v for n,v in text.items()):
            if not core.client_up():core.file_copy(old,LIVE)
            raise ValueError('asset install verify failure')
        info={'utc':datetime.now(timezone.utc).isoformat(),'file':str(LIVE),'sha256':core.digest(LIVE),'rollback':rollback,'preserved':text,'game_checked':False}
        core.atomic(out/'deployment.json',info);print(json.dumps(info,ensure_ascii=False))


if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('action',choices=('build','inspect','apply','install','rollback'))
    for k in ('file','diff','out'):p.add_argument('--'+k,type=Path)
    a=p.parse_args()
    if a.action=='build':build(a.out)
    elif a.action=='inspect':print(json.dumps(audit(a.file),ensure_ascii=False,sort_keys=True))
    elif a.action=='apply':apply(a.file,a.diff,a.out)
    else:deploy(a.out,a.action=='rollback')
