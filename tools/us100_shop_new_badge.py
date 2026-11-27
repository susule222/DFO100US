"""Localize the exact New badge family in the native v5 cash-shop atlas."""
import argparse
import base64
import json
import os
from pathlib import Path
import subprocess
import sys

import numpy as np
from PIL import Image, ImageDraw, ImageFilter, ImageFont

from us100_img5 import views
from us100_quest_act import compress_exact, inflate_exact
from us100_shop_load_atlas import verify_spans
from us100_status_dstr import CLIENT, GameFonts, core
from us100_ui_atlas import index, member


NAME='sprite_interface_cashshop.NPK'
ENTRY='sprite/interface/cashshop/cs_eventtag.img'
LIVE=CLIENT/'ImagePacks2'/NAME
BASE='095fcaf022a5a66af3c74269fca655bb01cf71d489b3ab4ebe359fabc7fa8402'
TARGET=(0,11,12,13)
ROOT=Path(__file__).resolve().parents[1]


def identify(path):
    data=member(path,ENTRY); frames=views(data)
    return {'sha256':core.digest(path),'member':ENTRY,'count':len(frames),
            'frames':{str(i):[frames[i].image().size,list(frames[i].crop),frames[i].rotated,
                              __import__('hashlib').sha256(frames[i].image().tobytes()).hexdigest()] for i in TARGET}}


def draw_badge(n, size):
    w,h=size; f=ImageFont.truetype('C:/Windows/Fonts/msyhbd.ttc',h-2,index=1)
    label='新品'; p=(w/2,h/2-.5)
    mask=Image.new('L',size); d=ImageDraw.Draw(mask)
    glow=n==12
    d.text(p,label,font=f,fill=255,anchor='mm',stroke_width=1 if glow else 0)
    if glow:
        return Image.composite(Image.new('RGBA',size,(191,92,12,255)),Image.new('RGBA',size),mask.filter(ImageFilter.GaussianBlur(2)))
    im=Image.new('RGBA',size); d=ImageDraw.Draw(im)
    d.text(p,label,font=f,fill=(108,42,7,255),anchor='mm',stroke_width=1 if h<15 else 2,stroke_fill=(33,17,14,255))
    for y in range(h):
        color=(255,round(192-88*y/max(1,h-1)),round(42-28*y/max(1,h-1)),255)
        im.paste(color,(0,y,w,y+1),mask.crop((0,y,w,y+1)))
    return im


def build(out):
    out=out.resolve()
    if out.is_relative_to(CLIENT) or core.digest(LIVE)!=BASE:raise ValueError('wrong badge baseline')
    out.mkdir(parents=True)
    b,s,d,r=(out/x for x in ('before-'+NAME,NAME,'BADGE.diff.json','ROLLBACK.sh'))
    core.file_copy(LIVE,b); data=member(b,ENTRY); fs=views(data)
    a=fs[0].pixels
    if any(fs[i].pixels!=a for i in TARGET) or a.kind!=16 or a.compression!=6:
        raise ValueError('unexpected new badge atlas surface')
    surface=a.image(); allowed=np.zeros((surface.height,surface.width),dtype=bool)
    for i in TARGET:
        f=fs[i]; x,y,xx,yy=f.crop
        if any(j!=i and not isinstance(g,int) and g.pixels==a and max(x,g.crop[0])<min(xx,g.crop[2])
               and max(y,g.crop[1])<min(yy,g.crop[3]) for j,g in enumerate(fs)):
            raise ValueError('badge overlaps a shared UI frame')
        im=draw_badge(i,f.image().size)
        if f.rotated:im=im.transpose(Image.Transpose.ROTATE_270)
        surface.paste(im,(x,y));allowed[y:yy,x:xx]=True
    if not np.array_equal(np.asarray(a.image())[~allowed],np.asarray(surface)[~allowed]):
        raise ValueError('unexpected pixels outside badge crops')
    pixels=surface.tobytes('raw','BGRA'); payload=compress_exact(pixels,len(a.raw),a.raw[:2])
    if inflate_exact(payload,len(pixels))!=pixels:raise ValueError('lossy badge stream')
    x=index(b)[ENTRY][0]+a.start; y=x+len(a.raw); old=b.read_bytes(); new=old[:x]+payload+old[y:]
    if old[x:y]!=a.raw:raise ValueError('atlas payload offsets differ')
    verify_spans(old,new,[(x,y,0)]);s.write_bytes(new)
    rebuilt=views(member(s,ENTRY)); before=identify(b); after=identify(s)
    for i,(f,g) in enumerate(zip(fs,rebuilt)):
        if isinstance(f,int):assert f==g
        elif i not in TARGET and f.image().tobytes()!=g.image().tobytes():raise ValueError('adjacent frame changed')
    assert list(index(b).items())==list(index(s).items())
    im=Image.new('RGB',(400,160),'#163753');draw=ImageDraw.Draw(im)
    for j,fl in enumerate((fs,rebuilt)):
        draw.text((7,j*80+4),('BEFORE','AFTER')[j],fill='white')
        for k,i in enumerate(TARGET):
            t=fl[i].image();t=t.resize((2*t.width,2*t.height),Image.Resampling.NEAREST)
            im.paste(t,(7+k*100,j*80+25),t)
    im.save(out/'badge-comparison.png')
    core.atomic(d,{'before':before['sha256'],'after':after['sha256'],'span':[x,y],
                   'frames':TARGET,'payload':base64.b64encode(payload).decode()})
    rb=(ROOT/'tools/us100_status_rollback.sh').read_text(encoding='utf-8')
    r.write_text(rb.replace('dstr.dat',NAME).replace('DSTR.diff.json','BADGE.diff.json'),encoding='utf-8',newline='\n')
    r.chmod(0o755); copy=out/'rollback-test.NPK'; core.file_copy(s,copy)
    ledger=[ENTRY,f'Target frames: {TARGET}; unchanged member offsets and neighboring frame pixels']
    def run(label,*args):
        cmd=list(map(str,args));p=subprocess.run(cmd,capture_output=True,encoding='utf-8',
                                               env={**os.environ,'PYTHONIOENCODING':'utf-8'})
        ledger.extend([label,json.dumps(cmd,ensure_ascii=False),f'exit={p.returncode}','stdout:',p.stdout,'stderr:',p.stderr]);p.check_returncode()
        return p.stdout
    code=Path(__file__).resolve();py=sys.executable
    try:
        baseline=run('BASELINE',py,code,'inspect','--file',b); changed=run('MODIFIED',py,code,'inspect','--file',s)
        run('ROLLBACK.sh',py,r,copy)
        if baseline!=run('ROLLBACK',py,code,'inspect','--file',copy) or baseline==changed:
            raise ValueError('rollback behavior differs')
        run('REAPPLY',py,code,'apply','--file',copy,'--diff',d,'--out',out/'reapplied.NPK')
    finally:
        (out/'VERIFICATION.txt').write_text('\n'.join(ledger)+'\n',encoding='utf-8')
    for p in (s,d,out/'VERIFICATION.txt',r):print(p,core.digest(p))


def apply(src,diff,out):
    m=core.read(diff); x,y=m['span']; raw=src.read_bytes(); p=base64.b64decode(m['payload'],validate=True)
    if core.digest(src)!=m['before'] or len(p)!=y-x or out.resolve().is_relative_to(CLIENT):raise ValueError('wrong baseline')
    with out.open('xb') as f:f.write(raw[:x]+p+raw[y:])
    if core.digest(out)!=m['after']:raise ValueError('reapply SHA differs')


def deploy(out,rollback=False):
    out=out.resolve();m=core.read(out/'BADGE.diff.json');before=out/('before-'+NAME);after=out/NAME
    if (out.is_relative_to(CLIENT) or core.digest(before)!=BASE or m['before']!=BASE or
        core.digest(after)!=m['after']):raise ValueError('wrong patch identity')
    old,new=(after,before) if rollback else (before,after)
    with GameFonts(CLIENT/'Fonts').locked():
        if core.client_up() or core.digest(LIVE)!=core.digest(old):raise ValueError('client open or source differs')
        core.file_copy(new,LIVE)
        if core.digest(LIVE)!=core.digest(new):raise ValueError('live badge atlas mismatch')
        data={'live':str(LIVE),'sha256':core.digest(LIVE),'game_checked':False,'rollback':rollback}
        core.atomic(out/'deployment.json',data);print(json.dumps(data,ensure_ascii=False))


if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('action',choices=('build','inspect','install','apply','rollback'))
    for k in ('out','file','diff'):p.add_argument('--'+k,type=Path)
    a=p.parse_args()
    if a.action=='build':build(a.out)
    elif a.action=='inspect':print(json.dumps(identify(a.file),ensure_ascii=False,sort_keys=True))
    elif a.action=='apply':apply(a.file,a.diff,a.out)
    else:deploy(a.out,a.action=='rollback')
