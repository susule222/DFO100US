"""Replace only text in approved quest tag/tracker frames; retain native slots."""
import argparse
from datetime import datetime, timezone
import hashlib
import os
from pathlib import Path
import struct

import numpy as np
from PIL import Image, ImageDraw, ImageFont
import zopfli.zlib

from us100_avatar_install import staged_copy
from us100_premium_panel import unchanged
from us100_quest_act import compress_exact, inflate_exact
from us100_quest_recommend_labels import PVF
from us100_status_dstr import ACT, CLIENT, EXE, REL, GameFonts, core
from us100_ui_atlas import Frame, index, member, split_img

NAME=REL.name
LIVE=CLIENT/REL
QUEST_TAG="sprite/interface/quest/quest_tag.img"
QUEST_GUIDE="sprite/interface/quest/questguide.img"
FONT="C:/Windows/Fonts/msyh.ttc"
BOLD="C:/Windows/Fonts/msyhbd.ttc"
GOLD=(251,196,80)
CREAM=(237,214,154)
MINT=(163,245,177)
BLUE=(131,229,255)
PINK=(249,170,201)
GREEN=(221,248,61)
ORANGE=(255,163,86)

# (label, glyph rectangle, color). None means the heading is wholly
# transparent artwork. Main/Act frames already accepted in game are excluded.
TAGS={
    0:("史诗",(16,2,39,13),GOLD), 1:("普通",(15,2,39,13),CREAM),
    2:("重复",(15,2,39,13),CREAM), 3:("修炼",(15,2,39,13),CREAM),
    4:("转职",(16,2,39,13),(188,123,255)), 5:("完成",(3,2,38,13),GOLD),
    6:("完成",(9,3,34,12),(255,255,223)), 7:("觉醒",(13,2,39,13),PINK),
    8:("进化",(14,2,40,13),GOLD), 9:("活动",(14,2,39,13),GREEN),
    10:("副职",(14,2,39,13),GOLD), 11:("限定",(15,2,39,13),PINK),
    12:("成就",(16,2,39,13),CREAM), 14:("紧急",(13,2,39,13),PINK),
    15:("子任务",(3,2,28,13),CREAM), 16:("系统",(13,2,39,13),MINT),
    17:("外传",(16,2,39,13),BLUE), 18:("完成",(2,2,39,13),GOLD),
    19:("完成",(3,3,38,12),(255,255,224)), 21:("后续",(15,2,39,13),GOLD),
    22:("共享",(15,2,39,13),MINT), 23:("任务",(12,2,39,13),PINK),
    24:("隐藏",(12,2,39,13),GOLD), 25:("修炼",(12,2,39,13),GREEN),
    26:("晋升",(13,3,40,13),ORANGE), 27:("决斗",(16,2,40,13),PINK),
    28:("胜利",(12,2,39,13),BLUE), 29:("觉醒",(13,2,39,13),PINK),
    30:("异界",(16,0,41,15),GOLD), 31:("远古",(14,2,40,13),GOLD),
    32:("镇魂",(13,2,40,13),GOLD), 33:("安徒恩",(15,3,39,13),GOLD),
    34:("冒险",(13,2,39,13),CREAM), 35:("每日",(16,2,40,13),MINT),
    36:("史诗",(17,2,39,13),MINT), 37:("经验",(16,2,39,13),MINT),
    38:("赛季",(13,2,39,13),GREEN), 40:("角色",(13,2,40,13),(194,161,255)),
    41:("事件",(3,3,39,13),ORANGE), 42:("隐藏",(14,2,40,13),ORANGE),
    43:("推荐",(17,2,39,13),GOLD), 45:("事件",(3,3,39,14),MINT),
    46:("引导",(12,2,40,13),GOLD), 47:("生活",(17,2,40,13),GREEN),
}
GUIDE={
    20:("完成",(7,5,44,16),GOLD), 25:("限定",(20,5,43,16),PINK),
    26:("异界",(20,3,44,17),GOLD), 27:("远古",(18,5,44,16),GOLD),
    28:("镇魂",(17,5,44,16),GOLD), 29:("安徒恩",(20,5,44,16),GOLD),
    30:("成就",(20,5,44,16),CREAM), 31:("隐藏",(17,5,44,16),GOLD),
    34:("任务提醒",None,GOLD), 36:("系统",(16,5,44,16),MINT),
    37:("外传",(20,5,44,16),BLUE), 38:("进行中的任务",(45,3,143,17),CREAM),
    39:("每日",(21,3,56,18),MINT), 45:("已完成",(7,5,44,16),GOLD),
    46:("已完成",(7,5,44,16),GOLD), 47:("史诗",(23,6,43,16),MINT),
    51:("事件",(8,5,43,16),ORANGE), 52:("已完成",(7,5,44,16),GOLD),
    54:("事件",(8,5,43,16),MINT),
}
SPECS={QUEST_TAG:TAGS,QUEST_GUIDE:GUIDE}


def render(orig, spec, mode, template=None):
    word,rect,color=spec
    dest=orig.copy()
    if rect is None:
        dest=Image.new("RGBA",orig.size)
        rect=(0,0,orig.width,orig.height)
        start_size=15
    else:
        l,t,r,b=rect
        raw=np.array(orig)
        if template is not None:
            # Legacy two-line Otherverse glyphs were rendered across the rim.
            # Use the matching gold badge border under the glyph at the edge.
            peer=np.asarray(template)
            raw[0:2,l:r]=peer[0:2,l:r]
            raw[13:15,l:r]=peer[13:15,l:r]
            raw[2:13,39:]=peer[2:13,39:]
            l,t,r,b=(l,2,39,13)
        part=raw[t:b,l:r]
        use=part.copy()
        if word=="进行中的任务":
            # The centered heading sits directly on a wide brown gradient;
            # sampling its black outline would leave a rectangular shadow.
            w=np.linspace(0,1,r-l+2)[1:-1,None]
            for j in range(b-t):
                left=raw[t+j,l-1].astype(np.float32)
                right=raw[t+j,r].astype(np.float32)
                use[j]=np.rint((1-w)*left+w*right).astype(np.uint8)
        else:
            for j,row in enumerate(part):
                brightness=row[:,:3].astype(np.uint16).sum(1)
                # Repaint the native label strip without sampling the colored
                # letters as background. Retain the source strip's opacity.
                dark=row[brightness<=np.percentile(brightness,30)]
                base=np.median(dark,axis=0).astype(np.uint8)
                if template is not None:
                    base=raw[t+j,37] if max(raw[t+j,37,:3])<75 else base
                if mode<=5:
                    base[:3]=np.minimum(255,((base[:3].astype(np.uint16)+4)//8)*8)
                use[j,:,:]=base
        raw[t:b,l:r]=use
        dest=Image.fromarray(raw)
        start_size=12 if orig.width>74 and (r-l)>70 else 11
    x,y,u,v=rect
    # Keep a one-pixel right-side inset from the label's frame.
    dim=(u-x,v-y)
    mask=Image.new("L",dim)
    d=ImageDraw.Draw(mask)
    for point in range(start_size,6,-1):
        font=ImageFont.truetype(BOLD if spec[1] is None else FONT,point,index=1)
        box=d.textbbox((0,0),word,font=font)
        w,h=box[2]-box[0],box[3]-box[1]
        if w<=dim[0]-1 and h<=dim[1]:break
    else:raise ValueError(f"label does not fit: {word} in {dim}")
    d.text(((dim[0]-w)//2-box[0],(dim[1]-h)//2-box[1]),word,font=font,fill=255)
    coverage=np.asarray(mask,dtype=np.float32)/255
    if mode==2:
        alpha=(coverage>=.33).astype(np.uint8)*255
    else:
        alpha=np.rint(np.rint(np.power(coverage,.72)*(mode-1))*(255/(mode-1))).astype(np.uint8)
    fg=Image.new("RGBA",dim,(*color,0));fg.putalpha(Image.fromarray(alpha))
    dest.alpha_composite(fg,(x,y))
    return dest


def into_slot(raw,original):
    try:
        return compress_exact(raw,len(original),original[:2])
    except ValueError:
        for rounds in (6,9,14,20,30):
            stream=zopfli.zlib.compress(raw,numiterations=rounds)
            pad=len(original)-len(stream)
            if pad<0:continue
            # End a run of non-final, empty fixed blocks with an empty
            # stored block to reach the original stream on a byte boundary.
            pads={0:b""}
            blocks=[sum(2<<(10*i) for i in range(k)).to_bytes((10*k+10)//8,"little")
                    +b"\x00\x00\xff\xff" for k in range(4)]
            for count in range(pad+1):
                if count in pads:
                    for block in blocks:
                        if count+len(block)<=pad:
                            pads.setdefault(count+len(block),pads[count]+block)
            if pad in pads:
                packed=original[:2]+pads[pad]+stream[2:]
                if len(packed)==len(original) and inflate_exact(packed,len(raw))==raw:
                    return packed
        raise


def targets(p):
    idx=index(p)
    for n,groups in SPECS.items():
        off,length=idx[n]
        data=member(p,n)
        offset=off+32+struct.unpack_from("<I",data,16)[0]
        frames=split_img(data)
        for i,f in enumerate(frames):
            if isinstance(f,Frame):
                if i in groups:
                    if f.meta[:8]!=struct.pack("<II",16,6):raise ValueError("wrong quest frame encoding")
                    yield n,i,offset,f,frames
                offset+=len(f.raw)
            elif i in groups:raise ValueError("unexpected quest frame redirect")
        if offset!=off+length:raise ValueError("quest IMG tail differs")


def build(out):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()) or core.digest(LIVE)!=ACT:
        raise ValueError("require verified client with prior working Act atlas")
    out.mkdir(parents=True,exist_ok=True)
    old,new=out/("before-"+NAME),out/NAME
    core.file_copy(LIVE,old)
    if core.digest(old)!=ACT:raise ValueError("base changed while backing up")
    a=old.read_bytes()
    b=bytearray(a)
    entries=[]
    prev=[]
    for n,i,offset,f,frames in targets(old):
        src=f.image()
        spec=SPECS[n][i]
        template=frames[31].image() if n==QUEST_TAG and i==30 else None
        for mode in (16,8,5,3,2):
            dest=render(src,spec,mode,template)
            raw=dest.tobytes("raw","BGRA")
            try:packed=into_slot(raw,f.raw)
            except ValueError:continue
            break
        else:raise ValueError(f"no equal-length packing: {n}:{i}")
        sec=offset+len(f.raw)
        if a[offset:sec]!=f.raw or inflate_exact(packed,src.width*src.height*4)!=raw:
            raise ValueError(f"frame stream differs: {n}:{i}")
        rect=spec[1] or (0,0,src.width,src.height)
        l,t,r,s=rect
        drawmask=np.zeros((src.height,src.width),dtype=bool)
        drawmask[t:s,l:r]=True
        if not np.array_equal(np.asarray(src)[~drawmask],np.asarray(dest)[~drawmask]):
            raise ValueError(f"badge art outside the caption changed: {n}:{i}")
        b[offset:sec]=packed
        entries.append({"member":n,"frame":i,"start":offset,"end":sec,"label":spec[0]})
        prev.append((n,i,src,dest))
    new.write_bytes(b)
    verify(old,new,entries)
    frames={n:split_img(member(new,n)) for n in SPECS}
    for n,i,_,p in prev:
        if frames[n][i].image().tobytes()!=p.tobytes():raise ValueError("saved frame did not reopen")
    for content in (QUEST_TAG,QUEST_GUIDE):
        xs=[r for r in prev if r[0]==content]
        cols=4 if content==QUEST_GUIDE else 6
        cellw,cellh=(394,66) if content==QUEST_GUIDE else (190,66)
        panel=Image.new("RGB",(cols*cellw,((len(xs)+cols-1)//cols)*cellh),"#293141")
        pen=ImageDraw.Draw(panel)
        for j,(_,i,p,q) in enumerate(xs):
            x,y=j%cols*cellw,j//cols*cellh
            pen.text((x+4,y+2),f"#{i}",fill="white")
            for k,im in enumerate((p,q)):
                if content==QUEST_TAG:im=im.resize((im.width*2,im.height*2),Image.Resampling.NEAREST)
                panel.paste(im,(x+4+k*(cellw//2),y+19),im)
        panel.save(out/(content.rsplit("/",1)[-1]+".png"))
    core.atomic(out/"verification.json",{"before":ACT,"after":core.digest(new),
        "spans":entries,"atlas_index_identical":True,"rollback_hash":ACT,"game_checked":False})
    print(f"Verified {len(prev)} equal-length quest frames")


def verify(old,new,entries):
    a,b=old.read_bytes(),new.read_bytes()
    if core.digest(old)!=ACT or list(index(old).items())!=list(index(new).items()):
        raise ValueError("quest NPK base or index changed")
    offsets={(n,i):(at,at+len(f.raw)) for n,i,at,f,_ in targets(old)}
    if len(offsets)!=len(entries) or len(entries)!=sum(len(r) for r in SPECS.values()):
        raise ValueError("target frame count changed")
    for e in entries:
        n,i=e["member"],e["frame"]
        if offsets.pop((n,i))!=(e["start"],e["end"]) or e["label"]!=SPECS[n][i][0]:
            raise ValueError("patch span differs")
    spans=sorted((x["start"],x["end"]) for x in entries)
    unchanged(a,b,spans)
    restored=bytearray(b)
    for l,r in spans:restored[l:r]=a[l:r]
    if hashlib.sha256(restored).hexdigest()!=ACT:raise ValueError("rollback proof differs")


def deploy(out,rollback):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()):raise ValueError("require offline bundle")
    old,new=out/("before-"+NAME),out/NAME
    proof=core.read(out/"verification.json")
    if proof["before"]!=ACT or core.digest(new)!=proof["after"]:
        raise ValueError("bundle identity changed")
    verify(old,new,proof["spans"])
    before,after=(new,old) if rollback else (old,new)
    fonts=CLIENT/"Fonts"
    hud=CLIENT/"ImagePacks2"/"sprite_interface2_hud.NPK"
    shared=CLIENT/"ImagePacks2"/"sprite_common_etc.NPK"
    with GameFonts(fonts).locked():
        tracked=[CLIENT/"Script.pvf",CLIENT/"dstr.dat",CLIENT/"DFO.exe",hud,shared,
                 *(x for x in fonts.iterdir() if x.is_file())]
        keep={x:core.digest(x) for x in tracked}
        if (keep[hud]!="fbec688d508b005e681553d524cbcc468bd07821984dcb689a1f29253a02e385"
            or keep[shared]!="f9b79723afbf313148bec9c83846736e609b35bb8a47653ea54c5a1f2d2107ac"
            or keep[CLIENT/"Script.pvf"]!=PVF or keep[CLIENT/"DFO.exe"]!=EXE
            or core.client_up() or core.digest(LIVE)!=core.digest(before)):
            raise ValueError("client must be closed with original HUD and known baseline")
        expected=core.digest(after)
        tmp=staged_copy(after,LIVE.parent,expected)
        try:
            if core.client_up() or core.digest(LIVE)!=core.digest(before):
                raise ValueError("client reopened or pack changed")
            os.replace(tmp,LIVE)
        finally:
            tmp.unlink(missing_ok=True)
        if core.digest(LIVE)!=expected or any(core.digest(x)!=h for x,h in keep.items()):
            raise ValueError("deployed file verification failed")
        core.atomic(out/("rollback-deployment.json" if rollback else "deployment.json"),{
            "utc":datetime.now(timezone.utc).isoformat(),"path":str(LIVE),"before":core.digest(before),
            "after":expected,"retained":{str(x):h for x,h in keep.items()},"game_checked":False})
        print(f"Verified live {NAME}: {expected}")


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("action",choices=("build","install","rollback"))
    p.add_argument("--out",required=True,type=Path)
    args=p.parse_args()
    if args.action=="build":build(args.out)
    else:deploy(args.out,args.action=="rollback")
