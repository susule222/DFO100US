"""Read/write only the verified English glyph regions of the aura-transfer atlas."""
import argparse
import base64
import hashlib
from pathlib import Path
import struct
import subprocess
import sys

import numpy as np
from PIL import Image, ImageDraw, ImageFilter, ImageFont

from us100_dstr_ui import CLIENT,core
from us100_premium_panel import unchanged
from us100_quest_act import compress_exact,inflate_exact
from us100_ui_atlas import digest,Frame,index,member,split_img


NAME="sprite_interface2_ui_auraemblemmove.NPK"
MEMBER="sprite/interface2/ui/auraemblemmove/aura_emblem.img"
BASE="997c1bbf377e637e20740f28e00b48c64a27189aa51e91caee2a0376a5aea44a"
FONT=Path("C:/Windows/Fonts/msyh.ttc")
BOLD=Path("C:/Windows/Fonts/msyhbd.ttc")
LIVE=CLIENT/"ImagePacks2"/NAME
PATCH={
    0: [((92,161,248,182),(169,170),"光环转移结果",16,(238,197,119),True)],
    1: [
        ((50,21,92,34),(70,27),"转移前",11,(218,192,152),False),
        ((192,21,230,34),(211,27),"转移后",11,(218,192,152),False),
        ((16,117,265,132),(140,123),"徽章将按上图所示转移。",10,(221,197,158),False),
        ((30,130,251,144),(140,136),"原光环装扮将被销毁。",10,(88,224,142),False),
        ((68,142,214,156),(140,148),"确定继续吗？",10,(221,197,158),False),
    ],
    2: [((35,4,166,37),(100,21),"徽章转移成功",15,(242,197,108),True)],
}
REPAIR={
    0:[(92,159,248,183)],
    1:[(50,21,92,34),(192,21,230,34),(16,118,265,156)],
    2:[(34,4,167,38)],
}


def draw(frame,which):
    a=np.array(frame)
    cover=np.zeros(a.shape[:2],dtype=bool)
    for x0,y0,x1,y1 in REPAIR[which]:
        im=a[y0:y1,x0:x1,:3]
        t=np.linspace(0,1,y1-y0+2)[1:-1,None,None]
        def quiet_rows(rows):
            # Suppress isolated sparks before extending background columns.
            v=np.median(rows,axis=0)
            return np.median(np.stack([np.roll(v,i,axis=0) for i in (-2,-1,0,1,2)]),axis=0)
        top=quiet_rows(a[y0-3:y0,x0:x1,:3])
        bottom=quiet_rows(a[y1:y1+3,x0:x1,:3])
        if which==1 and y0>=118:
            # The frame above contains the lit ring. Sampling upward would
            # drag that art into the notice area; the side margins here are
            # clean samples of the notice panel itself.
            lt=np.median(a[y0:y1,x0-4:x0,:3],axis=1)[:,None,:]
            rt=np.median(a[y0:y1,x1:x1+4,:3],axis=1)[:,None,:]
            x=np.linspace(0,1,x1-x0)[None,:,None]
            bg=np.rint(lt*(1-x)+rt*x).astype(np.uint8)
        else:
            bg=np.rint(top*(1-t)+bottom*t).astype(np.uint8)
        # Restrict reconstruction to bright old glyphs and their adjoining outline.
        diff=im.astype(np.int16)-bg.astype(np.int16)
        hit=(diff[:,:,0]>10)|(diff[:,:,1]>9)
        hit=np.asarray(Image.fromarray(hit.astype(np.uint8)*255).filter(ImageFilter.MaxFilter(5)))>0
        np.copyto(im,bg,where=hit[:,:,None])
        cover[y0:y1,x0:x1]=True
    dest=Image.fromarray(a)
    d=ImageDraw.Draw(dest)
    for (x0,y0,x1,y1),xy,word,size,color,bold in PATCH[which]:
        font=ImageFont.truetype(BOLD if bold else FONT,size,index=1)
        box=d.textbbox(xy,word,font=font,anchor="mm",stroke_width=1)
        if not (x0<=box[0]<box[2]<=x1 and y0<=box[1]<box[3]<=y1):
            raise ValueError(f"avatar label outside glyph region: {which} {word} {box}")
        d.text(xy,word,font=font,fill=color,anchor="mm",stroke_width=1,stroke_fill=(12,8,3))
        cover[y0:y1,x0:x1]=True
    after=np.array(dest);old=np.asarray(frame)
    # Keep the original alpha exactly; only color can change in these slots.
    after[:,:,3]=old[:,:,3]
    if np.any(after[~cover]!=old[~cover]):
        raise ValueError("non-glyph artwork changed")
    return Image.fromarray(after)


def frame_spans(path):
    off,span=index(path)[MEMBER]
    raw=member(path,MEMBER)
    pos=off+32+struct.unpack_from("<I",raw,16)[0]
    frames=split_img(raw)
    if len(frames)!=3:raise ValueError("unexpected aura frame set")
    for i,f in enumerate(frames):
        if not isinstance(f,Frame) or f.meta[:8]!=struct.pack("<II",16,6):
            raise ValueError("unsupported aura frame")
        yield i,pos,f
        pos+=len(f.raw)
    if pos!=off+span:raise ValueError("aura image tail differs")


def apply(before,diff):
    m=core.read(diff)
    if digest(before)!=BASE or m["before"]!=BASE or m["member"]!=MEMBER:
        raise ValueError("aura menu baseline changed")
    data=before.read_bytes();result=bytearray(data)
    wanted={i:(start,start+len(f.raw),f.geometry[:2]) for i,start,f in frame_spans(before)}
    if sorted(s["frame"] for s in m["spans"])!=sorted(wanted):
        raise ValueError("wrong aura patch frame set")
    for s in m["spans"]:
        x,y,geom=wanted[s["frame"]]
        buf=base64.b64decode(s["payload"],validate=True)
        if (x,y)!=(s["start"],s["end"]) or y-x!=len(buf):raise ValueError("aura payload size differs")
        inflate_exact(buf,geom[0]*geom[1]*4)
        result[x:y]=buf
    unchanged(data,result,[(x,y) for x,y,_ in wanted.values()])
    if hashlib.sha256(result).hexdigest()!=m["after"]:raise ValueError("aura diff fingerprint differs")
    return bytes(result)


def build(out):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()) or digest(LIVE)!=BASE:
        raise ValueError("unexpected aura atlas identity")
    out.mkdir(parents=True)
    old=out/("before-"+NAME);new=out/NAME
    core.file_copy(LIVE,old)
    if digest(old)!=BASE:raise ValueError("aura source changed during copy")
    src=old.read_bytes();dst=bytearray(src);spans=[];preview=[]
    for i,pos,f in frame_spans(old):
        a=f.image()
        after=draw(a,i);origin=np.asarray(a);pixels=np.asarray(after)
        for scale in (1,2,3,4,6,8,10,14):
            result=pixels.copy()
            delta=np.any(origin[:,:,:3]!=result[:,:,:3],axis=2)
            result[delta,:3]=np.minimum(255,((result[delta,:3].astype(np.int16)+scale//2)//scale)*scale)
            outim=Image.fromarray(result);raw=outim.tobytes("raw","BGRA")
            try:p=compress_exact(raw,len(f.raw),f.raw[:2])
            except ValueError:continue
            break
        else:raise ValueError(f"cannot fit aura frame {i} in its native compressed slot")
        if Frame(f.meta,p).image().tobytes()!=outim.tobytes():raise ValueError("aura frame won't reopen")
        end=pos+len(p)
        if src[pos:end]!=f.raw:raise ValueError("wrong aura pixel span")
        dst[pos:end]=p
        spans.append({"frame":i,"start":pos,"end":end,"payload":base64.b64encode(p).decode("ascii")})
        preview.append((a,outim))
    new.write_bytes(dst)
    if index(old)!=index(new) or any(a.meta!=b.meta for (_,_,a),(_,_,b) in zip(frame_spans(old),frame_spans(new))):
        raise ValueError("aura atlas layout changed")
    d=out/"NPK.diff.json"
    core.atomic(d,{"before":BASE,"after":digest(new),"member":MEMBER,"spans":spans})
    panel=Image.new("RGB",(710,625),"#24262b")
    mark=ImageDraw.Draw(panel);mark.text((8,5),"BEFORE",fill="white");mark.text((361,5),"AFTER",fill="white")
    y=24
    for a,b in preview:
        panel.paste(a,(8,y),a);panel.paste(b,(361,y),b);y+=a.height+6
    panel.save(out/"aura-emblem-comparison.png")
    r=out/"ROLLBACK.sh"
    r.write_text(Path(__file__).with_name("us100_status_rollback.sh").read_text(encoding="utf-8")
         .replace("dstr.dat",NAME).replace("DSTR.diff.json","NPK.diff.json"),encoding="utf-8",newline="\n")
    copy=out/"rollback-test.NPK";core.file_copy(new,copy)
    p=subprocess.run([sys.executable,str(r),str(copy)],check=True,capture_output=True,text=True)
    if digest(copy)!=BASE or apply(copy,d)!=dst:raise ValueError("aura rollback or reapply differs")
    core.atomic(out/"verification.json",{"restored":BASE,"reapplied":digest(new),
         "span_count":len(spans),"rollback":p.stdout.strip(),"untouched_member_and_offsets":True})
    print(digest(new))


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("--out",type=Path,required=True);build(p.parse_args().out)
