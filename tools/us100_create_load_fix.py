"""Build and verify the 100US creation strings and narrow loading-title repair."""

import argparse
import base64
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import struct
import subprocess
import sys
from zipfile import ZipFile, ZipInfo

from PIL import Image, ImageDraw, ImageFilter, ImageFont
import numpy as np

from us100_shop_load_atlas import LOAD, MEMBERS, FONT, encode, verify_spans
from us100_status_dstr import (ACT, CLEAR_FONTS, CLIENT, EXE, EQUIP_PVF, GameFonts, REL,
                              PROFILES, core, extract, key_pair, pack, register_create_profile,
                              replace, unpack)
from us100_ui_atlas import Frame, index, member, split_img


ROOT = Path(__file__).resolve().parents[1]
OLD_LOAD = '7ba4cff03eb16b4a109ff4b9bfd4305ecce7f8be912c0b8e0bbe8620d0f13b49'
PRISTINE = '897667d39a70fd046dc916e6a76c35dfd0268d58a73e074f36944bcce6caf576'
LIVE = {LOAD: CLIENT/'ImagePacks2'/LOAD, 'dstr.dat': CLIENT/'dstr.dat'}
SAMPLE = ('501031101','501031102','501031107','501031112','501031117','501031025',
          '501031031','501031701','501031702','100087494')
ART_RECT = (500,388,1100,788)
INK = {0:((712,515,887,551),(630,540,967,584),(618,568,980,630)),
       1:((329,339,464,367),(275,352,522,389),(273,377,523,420)),
       2:((331,115,444,136),(279,129,499,157),(270,148,505,190))}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def feather(size, rectangles, radius, floor, end):
    m = Image.new('L', size)
    d = ImageDraw.Draw(m)
    for box in rectangles:
        d.rounded_rectangle(box, radius=max(1,round(radius)), fill=255)
    m=m.filter(ImageFilter.GaussianBlur(radius))
    a=np.asarray(m).astype(np.float32)
    a=np.clip((a-2)/226,0,1)
    depth=np.clip((end-np.arange(size[1]))/(end-floor),0,1)
    return Image.fromarray(np.rint(a*depth[:,None]*255).astype(np.uint8))


def draw_title(im, x, positions, sizes):
    title = ('第7赛季 第4章','机械崛起','开战')
    for label, y, size in zip(title, positions, sizes):
        f = ImageFont.truetype(str(FONT), size, index=1)
        base = Image.new('RGBA', im.size)
        b = ImageDraw.Draw(base)
        stroke = 2 if size >= 22 else 1
        b.text((x,y),label,font=f,anchor='mm',fill=(58,26,12,255),stroke_width=stroke+1,
               stroke_fill=(32,15,13,220))
        im.alpha_composite(base)
        mask = Image.new('L',im.size)
        ImageDraw.Draw(mask).text((x,y),label,font=f,anchor='mm',fill=255)
        xs,ys,xe,ye = mask.getbbox()
        size_y = ye-ys
        ramp = Image.new('RGB',(1,size_y))
        ramp.putdata([tuple(round(a+(b-a)*j/max(1,size_y-1)) for a,b in zip((255,242,194),(225,161,57)))
                      for j in range(size_y)])
        im.paste(ramp.resize((xe-xs,size_y)), (xs,ys), mask.crop((xs,ys,xe,ye)))


def fix_title(original, edited, i):
    frame = original.copy()
    if i < 2:
        if i == 0:
            transform=lambda x,y:(x,y)
            clean=edited.resize((600,400),Image.Resampling.LANCZOS)
            x,y=ART_RECT[:2]
        else:
            transform=lambda x,y:(round((x-800)*2/3+400),round(y*2/3))
            clean=edited.resize((400,267),Image.Resampling.LANCZOS)
            x,y=transform(*ART_RECT[:2])
        source=Image.new('RGBA',frame.size)
        source.paste(clean,(x,y))
        floor,end=((626,635) if i==0 else (418,425))
        m=feather(frame.size,INK[i],6 if i==0 else 4,floor,end)
        # Keep the gold diamond tip verbatim at the bottom edge of the title.
        dx=800 if i==0 else 398
        ImageDraw.Draw(m).rectangle((dx-11,629 if i==0 else 421,dx+11,end),fill=0)
        frame.paste(source,(0,0),m)
        if i == 0:
            draw_title(frame,800,(531,558,601),(23,29,52))
        else:
            draw_title(frame,398,(354,372,400),(15,19,34))
    else:
        # This frame is a transparent overlay, not a solid loading background.
        # Retain the original particles and gold diamond outside the lettering.
        mask=feather(frame.size,INK[2],4,186,191)
        frame.paste((0,0,0,0),(0,0,*frame.size),mask)
        draw_title(frame,386,(123,143,172),(13,16,33))
    return frame


def fix_atlas(live, original, clean, out):
    if (core.digest(live),core.digest(original)) != (OLD_LOAD,PRISTINE):
        raise ValueError('unexpected installed or original loading atlas')
    key=MEMBERS[LOAD]
    source=member(live,key); baseline=live.read_bytes(); changed=bytearray(baseline)
    frames=split_img(source); pristine=split_img(member(original,key)); changed_frames=[]
    pos=index(live)[key][0]+32+struct.unpack_from('<I',source,16)[0]
    ref=Image.open(clean).convert('RGB')
    if ref.width*2 != ref.height*3 or len(frames)!=len(pristine) or len(frames)!=8:
        raise ValueError('loading reconstruction or archive layout differed')
    for i, f in enumerate(frames):
        if not isinstance(f,Frame) or f.meta!=pristine[i].meta:
            raise ValueError('unexpected resource frame format')
        end=pos+len(f.raw)
        if i<3:
            im=fix_title(pristine[i].image(),ref,i)
            payload=encode(f,im)
            # The archive encoder must not quantize any color to force the slot to fit.
            decoded=Frame(f.meta,payload).image()
            if not np.array_equal(np.asarray(im),np.asarray(decoded)):
                raise ValueError(f'loading frame {i} is not lossless')
            changed[pos:end]=payload
            changed_frames.append((pos,end,i))
        pos=end
    verify_spans(baseline,changed,changed_frames)
    out.write_bytes(changed)
    if list(index(live).items())!=list(index(out).items()):
        raise ValueError('offsets or member layout changed')
    repaired=split_img(member(out,key))
    if any(a.meta!=b.meta or (i>=3 and a.raw!=b.raw) for i,(a,b) in enumerate(zip(frames,repaired))):
        raise ValueError('non-title frame or metadata changed')
    crops=((500,461,1100,681),(198,304,598,451),(240,75,540,222))
    panel=Image.new('RGB',(1200,585),'#22151a')
    d=ImageDraw.Draw(panel)
    for row,region in enumerate(crops):
        for col,ims in enumerate((frames,repaired)):
            im=ims[row].image().crop(region)
            x=col*600+(600-im.width)//2; y=sum((240,165,0)[:row])+(30 if row==2 else 18)
            panel.paste(im,(x,y),im)
            if row==0:d.text((col*600+5,3),('BEFORE','AFTER')[col],fill='white')
    panel.save(out.parent/'loading-native-comparison.png')
    return changed_frames


def zip_out(path, parts):
    with ZipFile(path,'x') as f:
        for n,data in sorted(parts.items()): f.writestr(ZipInfo(n), data)


def read(path):
    with ZipFile(path) as f:
        if sorted(f.namelist())!=sorted(LIVE) or f.testzip() is not None:
            raise ValueError('unexpected or corrupt verification ZIP')
        return {name:f.read(name) for name in LIVE}


def apply(base, diff, output):
    m=core.read(diff)
    if core.digest(base)!=m['before'] or output.resolve().is_relative_to(CLIENT):
        raise ValueError('unknown source or live patch output')
    parts=read(base); before=parts[LOAD]; buff=bytearray(before); ranges=[]
    for row in m['image']:
        x,y,i=row['start'],row['end'],row['frame']
        span=base64.b64decode(row['bytes'],validate=True)
        if i not in (0,1,2) or len(span)!=y-x:raise ValueError('unexpected loading payload span')
        buff[x:y]=span; ranges.append((x,y,i))
    verify_spans(before,buff,ranges)
    register_create_profile()
    changes={a['id']:(a['before'],a['after']) for a in m['text']}
    if changes!=PROFILES['character-create'].rows:raise ValueError('creation text footprint differs')
    keys=key_pair(CLIENT/'DFO.exe')
    parts['dstr.dat']=pack(replace(unpack(parts['dstr.dat'],keys),changes),keys)
    parts[LOAD]=bytes(buff)
    zip_out(output,parts)
    if core.digest(output)!=m['after']:raise ValueError('candidate reapplication differs')


def inspect(archive):
    parts=read(archive); keys=key_pair(CLIENT/'DFO.exe'); blob=parts['dstr.dat']; raw=unpack(blob,keys)
    if pack(raw,keys)!=blob:raise ValueError('DSTR round trip differs')
    print(json.dumps({'assets':{k:sha(v) for k,v in parts.items()},'labels':extract(raw,set(SAMPLE))},
                     ensure_ascii=False,sort_keys=True))


def build(out,dstr,original,clean):
    out=out.resolve()
    if out.is_relative_to(CLIENT):raise ValueError('offline bundle required')
    out.mkdir(parents=True)
    register_create_profile()
    patch=core.read(dstr/'DSTR.diff.json')
    if any(core.digest(p)!=h for p,h in ((LIVE['dstr.dat'],patch['before']),
           (dstr/'dstr.dat',patch['after']),(dstr/'before-dstr.dat',patch['before']))):
        raise ValueError('creation text transaction changed')
    new_load=out/LOAD
    spans=fix_atlas(LIVE[LOAD],original,clean,new_load)
    old={LOAD:LIVE[LOAD].read_bytes(),'dstr.dat':(dstr/'before-dstr.dat').read_bytes()}
    new={LOAD:new_load.read_bytes(),'dstr.dat':(dstr/'dstr.dat').read_bytes()}
    before,after,diff,roll=(out/n for n in ('before-UI.zip','UI.zip','UI.diff.json','ROLLBACK.sh'))
    zip_out(before,old);zip_out(after,new)
    core.atomic(diff,{'before':core.digest(before),'after':core.digest(after),'text':patch['rows'],
                'image':[{'start':x,'end':y,'frame':i,'bytes':base64.b64encode(new[LOAD][x:y]).decode()}
                         for x,y,i in spans]})
    prior=ROOT/'runtime/us100-shop-load-r1/ROLLBACK.sh'
    roll.write_text(prior.read_text(encoding='utf-8').replace('font archive','UI archive'),
                    encoding='utf-8',newline='\n');roll.chmod(0o755)
    copy=out/'rollback-test.zip';core.file_copy(after,copy)
    ledger=['Scope: creation text and three loading title frames, exact original metadata and offsets',
            'Old loading NPK: '+sha(old[LOAD]), 'New loading NPK: '+sha(new[LOAD]),
            f'Reconstruction crop: {clean.resolve()} / {core.digest(clean)}',
            'Three lossless frame payloads at the original offsets: '+str(spans),
            'Spinner, gold rule anchor, font files, client executable, PVF and other atlases unchanged.']
    def run(label,*args):
        cmd=list(map(str,args))
        p=subprocess.run(cmd,capture_output=True,encoding='utf-8',env={**os.environ,'PYTHONIOENCODING':'utf-8'})
        ledger.extend([label,'argv='+json.dumps(cmd,ensure_ascii=False),f'exit={p.returncode}',
                       'stdout:',p.stdout,'stderr:',p.stderr]);p.check_returncode();return p.stdout
    try:
        script=Path(__file__).resolve();py=sys.executable
        b=run('BASELINE',py,script,'inspect','--file',before)
        a=run('MODIFIED',py,script,'inspect','--file',after)
        run('ROLLBACK.sh',py,roll,copy)
        if b!=run('ROLLBACK',py,script,'inspect','--file',copy) or b==a or core.digest(copy)!=core.digest(before):
            raise ValueError('verification probe mismatch')
        replay=out/'reapplied-UI.zip'
        run('REAPPLY',py,script,'apply','--file',copy,'--diff',diff,'--out',replay)
        if core.digest(replay)!=core.digest(after):raise ValueError('reapplied patch differs')
        ledger+=['Rollback SHA-256: '+core.digest(copy),'Reapplied SHA-256: '+core.digest(replay),
                 'PREVIEW was derived from reopened encoded images. No in-game test in this build.']
    finally:
        (out/'VERIFICATION.txt').write_text('\n'.join(ledger)+'\n',encoding='utf-8')
    for path in (after,diff,out/'VERIFICATION.txt',roll): print(f'{path}: {core.digest(path)}')


def deploy(out,rollback=False):
    out=out.resolve(); delta=core.read(out/'UI.diff.json')
    if out.is_relative_to(CLIENT) or any(core.digest(out/n)!=delta[h] for n,h in
       (('before-UI.zip','before'),('UI.zip','after'))) or 'REAPPLY' not in (out/'VERIFICATION.txt').read_text(encoding='utf-8'):
        raise ValueError('unexpected UI patch bundle')
    base,mod=read(out/'before-UI.zip'),read(out/'UI.zip')
    old,new=(mod,base) if rollback else (base,mod)
    fonts=GameFonts(CLIENT/'Fonts')
    with fonts.locked():
        if (core.client_up() or fonts.hashes()!=CLEAR_FONTS or core.digest(CLIENT/'DFO.exe')!=EXE or
            core.digest(CLIENT/'Script.pvf')!=EQUIP_PVF or core.digest(CLIENT/REL)!=ACT or
            any(core.digest(p)!=sha(old[k]) for k,p in LIVE.items())):
            raise ValueError('DFO is open or protected client identities changed')
        done=[]
        try:
            for k,p in LIVE.items():
                if core.client_up() or core.digest(p)!=sha(old[k]):raise ValueError('client reopened or resource changed')
                before=out/('before-'+k); after=out/k
                before.write_bytes(base[k]); after.write_bytes(mod[k]); source=before if rollback else after
                core.file_copy(source,p);done.append((p,after if rollback else before))
                if core.digest(p)!=sha(new[k]):raise ValueError('post-install asset hash mismatch')
        except Exception:
            if not core.client_up():
                for p,undo in reversed(done):core.file_copy(undo,p)
            raise
        receipt={'utc':datetime.now(timezone.utc).isoformat(),'state':'rolled_back' if rollback else 'installed_pending_game_check',
                 'assets':{str(p):core.digest(p) for p in LIVE.values()},'fonts':fonts.hashes()}
        core.atomic(out/'deployment.json',receipt)
        print(json.dumps(receipt,ensure_ascii=False))


if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('action',choices=('build','apply','inspect','install','rollback'))
    for arg in ('out','dstr','original','clean','file','diff'):p.add_argument('--'+arg,type=Path)
    a=p.parse_args()
    if a.action=='build':build(a.out,a.dstr,a.original,a.clean)
    elif a.action=='apply':apply(a.file,a.diff,a.out)
    elif a.action=='inspect':inspect(a.file)
    else:deploy(a.out,a.action=='rollback')
