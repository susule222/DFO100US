"""Localize monster race badges in the 100US Category.img without touching DFO.exe."""
import argparse
from datetime import datetime, timezone
import hashlib
import os
from pathlib import Path
import struct
import zlib

import numpy as np
from PIL import Image, ImageDraw, ImageFont

from us100_avatar_install import staged_copy
from us100_img5 import View, views
from us100_premium_panel import unchanged
from us100_quest_act import compress_exact, inflate_exact
from us100_status_dstr import ACT, CLIENT, EXE, REL, GameFonts, core
from us100_ui_atlas import index, member, rebuild_npk

NAME = "sprite_common_etc.NPK"
MEMBER = "sprite/common/etc/category.img"
BASE = "f9b79723afbf313148bec9c83846736e609b35bb8a47653ea54c5a1f2d2107ac"
FONT = "C:/Windows/Fonts/msyhbd.ttc"
LIVE = CLIENT/"ImagePacks2"/NAME
ROWS = (
    ("Human", "人型"), ("Beast", "野兽"), ("Plant", "植物"),
    ("Insect", "昆虫"), ("Mechanic", "机械"), ("Hybrid", "混合"),
    ("Undead", "不死"), ("Demon", "恶魔"), ("Spirit", "精灵"),
    ("Dragon", "龙族"), ("Human", "人类"), ("Building", "建筑"),
    ("Holy", "生灵"), ("Divine", "圣灵"), ("Angel", "天使"), ("Beast", "野兽"),
)


def labeled(image, i, coverage):
    """Replace only lettering, retaining the frame border and optional crown."""
    a=np.array(image)
    w,h=image.size
    fancy=i in (10,11,12,13)
    if h!=15 or w!=(33 if fancy else 32):
        raise ValueError(f"bad badge geometry {i}")
    l,t,r,b=(11,3,w-2,13) if fancy else (2,3,w-2,13)
    bg=np.array(image.getpixel((w-4,3)),dtype=np.uint8)
    if tuple(bg)!=(0,0,0,179):
        raise ValueError(f"unexpected plain interior of badge {i}")
    a[t:b,l:r]=bg
    fg=np.array((198,204,220,255) if fancy else (255,150,72,255),dtype=np.int32)
    text=ROWS[i][1]
    mask=Image.new("L",(r-l,b-t))
    pen=ImageDraw.Draw(mask)
    for px in range(10 if fancy else 11,7,-1):
        font=ImageFont.truetype(FONT,px,index=1)
        box=pen.textbbox((0,0),text,font=font)
        gw,gh=box[2]-box[0],box[3]-box[1]
        if gw<=mask.width and gh<=mask.height:break
    else:
        raise ValueError(f"badge text clipped {i}: {box}")
    pen.text(((mask.width-gw)//2-box[0],(mask.height-gh)//2-box[1]),text,font=font,fill=255)
    alpha=np.asarray(mask)
    if coverage==2:
        val=np.where(alpha>=90,255,0)
    elif coverage==3:
        val=np.where(alpha>=145,255,np.where(alpha>=57,160,0))
    elif coverage==5:
        val=np.digitize(alpha,[35,86,142,202])*255//4
    else:
        val=np.rint(255*np.power(alpha.astype(np.float32)/255,0.72)).astype(np.int32)
    a[t:b,l:r]=bg+((fg-bg)*val[:,:,None]+127)//255
    allowed=np.zeros((h,w),dtype=bool)
    allowed[t:b,l:r]=True
    if not np.array_equal(a[~allowed],np.asarray(image)[~allowed]):
        raise ValueError(f"decorative art changed {i}")
    return Image.fromarray(a), (l,t,r,b)


def build(out):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()) or core.digest(LIVE)!=BASE:
        raise ValueError("unexpected live base or output directory")
    out.mkdir(parents=True,exist_ok=True)
    before,candidate=out/("before-"+NAME),out/NAME
    core.file_copy(LIVE,before)
    if core.digest(before)!=BASE:
        raise ValueError("source changed during snapshot")
    buf=before.read_bytes()
    pack_map=index(before)
    original_member=member(before,MEMBER)
    views_in=views(original_member)
    if len(views_in)!=len(ROWS) or not all(isinstance(v,View) for v in views_in):
        raise ValueError("unexpected badge frames")
    pix=views_in[0].pixels
    if pix.kind!=16 or pix.compression!=6 or any(v.pixels!=pix for v in views_in):
        raise ValueError("unexpected badge surface type")
    orig=pix.image()
    if inflate_exact(pix.raw,orig.width*orig.height*4)!=orig.tobytes("raw","BGRA"):
        raise ValueError("native surface did not round-trip")
    crops={}
    for i,v in enumerate(views_in):
        if v.crop in crops:
            k=crops[v.crop]
            if v.rotated!=views_in[k].rotated or ROWS[i]!=ROWS[k]:
                raise ValueError("unexpected shared crop")
        else:
            crops[v.crop]=i
    method="original_slot"
    # Prefer readable antialiased strokes over one-bit text just to save bytes.
    for mode in (256,5,3):
        rendered=orig.copy()
        mask=Image.new("1",orig.size)
        for crop,i in crops.items():
            v=views_in[i]
            image=v.image()
            dest,region=labeled(image,i,mode)
            zone=Image.new("1",image.size)
            zone.paste(1,region)
            if v.rotated:
                dest=dest.transpose(Image.Transpose.ROTATE_270)
                zone=zone.transpose(Image.Transpose.ROTATE_270)
            if np.any(np.asarray(mask.crop(crop)) & np.asarray(zone)):
                raise ValueError("overlapping edits")
            rendered.paste(dest,crop[:2],zone)
            mask.paste(zone,crop[:2],zone)
        used=np.asarray(mask)
        if not np.array_equal(np.asarray(rendered)[~used],np.asarray(orig)[~used]):
            raise ValueError("pixels outside glyph regions changed")
        native=rendered.tobytes("raw","BGRA")
        if mode==256:full=(native,rendered.copy())
        try:
            payload=compress_exact(native,len(pix.raw),pix.raw[:2])
        except ValueError:
            continue
        break
    else:
        method="member_repack"
        mode=256
        native,rendered=full
        payload=zlib.compress(native,9)
    if inflate_exact(payload,len(native))!=native:
        raise ValueError("encoded badge surface is invalid")
    start=pack_map[MEMBER][0]+pix.start
    end=start+len(pix.raw)
    if buf[start:end]!=pix.raw:
        raise ValueError("payload range mismatch")
    if method=="original_slot":
        patched=buf[:start]+payload+buf[end:]
        unchanged(buf,patched,[(start,end)])
        restored=patched[:start]+pix.raw+patched[end:]
    else:
        changed_member=bytearray(original_member[:pix.start]+payload)
        struct.pack_into("<I",changed_member,36,len(changed_member))
        struct.pack_into("<I",changed_member,56,len(payload))
        patched=rebuild_npk(before,{MEMBER:bytes(changed_member)})
    candidate.write_bytes(patched)
    verify_pack(before,candidate)
    if method=="member_repack":
        restored=rebuild_npk(candidate,{MEMBER:original_member})
    if hashlib.sha256(restored).hexdigest()!=BASE:
        raise ValueError("rollback mismatch")
    opened=views(member(candidate,MEMBER))
    if len(opened)!=len(ROWS) or opened[0].pixels.image().tobytes()!=rendered.tobytes():
        raise ValueError("saved surface re-open differs")
    panel=Image.new("RGB",(352*4,108*4),"#263345")
    d=ImageDraw.Draw(panel)
    for i,(en,zh) in enumerate(ROWS):
        v=opened[i]
        if v.crop!=views_in[i].crop or v.rotated!=views_in[i].rotated:
            raise ValueError("sprite shape differs")
        x,y=i%4*352,i//4*108
        d.text((x+5,y+4),f"{i:02d}  {en}",fill="white")
        for j,im in enumerate((views_in[i].image(),v.image())):
            scaled=im.resize((im.width*4,im.height*4),Image.Resampling.NEAREST)
            panel.paste(scaled,(x+5+j*160,y+23),scaled)
    panel.save(out/"preview.png")
    report={"before":BASE,"after":core.digest(candidate),"glyphs":dict(enumerate(ROWS)),
            "member":MEMBER,"start":start,"end":end,"recovered":BASE,
            "frame_count":len(ROWS),"unique_crops":len(crops),"coverage_levels":mode,"method":method}
    core.atomic(out/"verification.json",report)
    print(report)


def verify_pack(before,after):
    a,b=index(before),index(after)
    if list(a)!=list(b):
        raise ValueError("NPK member list or order differs")
    left,right=before.read_bytes(),after.read_bytes()
    for n,(u,length) in a.items():
        if n==MEMBER:continue
        v,count=b[n]
        if length!=count or left[u:u+length]!=right[v:v+count]:
            raise ValueError(f"unrelated member changed: {n}")
    x,y=member(before,MEMBER),member(after,MEMBER)
    old,new=views(x),views(y)
    o,p=old[0].pixels,new[0].pixels
    if o.start!=p.start or x[60:o.start]!=y[60:p.start]:
        raise ValueError("native frame table changed")
    if any((u.crop,u.rotated)!=(v.crop,v.rotated) for u,v in zip(old,new,strict=True)):
        raise ValueError("atlas coordinates changed")


def deploy(out,rollback):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()):
        raise ValueError("bundle must be outside the client")
    report=core.read(out/"verification.json")
    if not rollback and report.get("method") != "original_slot":
        raise ValueError("Do not deploy repacked shared sprite_common_etc.NPK: the member index must remain byte-for-byte unchanged")
    source,after=out/("before-"+NAME),out/NAME
    if (report["before"]!=BASE or report["recovered"]!=BASE or report["member"]!=MEMBER
        or core.digest(source)!=BASE or core.digest(after)!=report["after"]):
        raise ValueError("badge bundle changed")
    offset,size=index(source)[MEMBER]
    expected=views(member(source,MEMBER))[0].pixels
    a,b=report["start"],report["end"]
    if (a,b)!=(offset+expected.start,offset+expected.start+len(expected.raw)) or b>offset+size:
        raise ValueError("unexpected change range")
    verify_pack(source,after)
    if report["method"]=="original_slot":
        unchanged(source.read_bytes(),after.read_bytes(),[(a,b)])
    elif report["method"]=="member_repack":
        if hashlib.sha256(rebuild_npk(after,{MEMBER:member(source,MEMBER)})).hexdigest()!=BASE:
            raise ValueError("offline reverse rebuild differs")
    else:
        raise ValueError("unknown bundle format")
    before_hash,after_hash=(report["after"],BASE) if rollback else (BASE,report["after"])
    fonts=CLIENT/"Fonts"
    guard=[CLIENT/"DFO.exe",CLIENT/"Script.pvf",CLIENT/"dstr.dat",CLIENT/REL,
           *sorted(f for f in fonts.iterdir() if f.is_file())]
    with GameFonts(fonts).locked():
        if core.client_up():
            raise ValueError("close DFO normally before replacing the pack")
        retained={p:core.digest(p) for p in guard}
        if retained[CLIENT/"DFO.exe"]!=EXE or retained[CLIENT/REL]!=ACT or core.digest(LIVE)!=before_hash:
            raise ValueError("live client does not match expected build or asset")
        tmp=staged_copy(source if rollback else after,LIVE.parent,after_hash)
        try:
            if core.client_up() or core.digest(LIVE)!=before_hash:
                raise ValueError("DFO reopened or baseline changed before write")
            os.replace(tmp,LIVE)
        finally:
            tmp.unlink(missing_ok=True)
        if core.digest(LIVE)!=after_hash or any(core.digest(p)!=h for p,h in retained.items()):
            raise ValueError("post-deployment verification failed; inspect live state before retrying")
        core.atomic(out/("rollback-deployment.json" if rollback else "deployment.json"),{
            "utc":datetime.now(timezone.utc).isoformat(),"before":before_hash,"after":after_hash,
            "path":str(LIVE),"preserved":{str(p):h for p,h in retained.items()},"game_checked":False})
        print(f"Verified live {LIVE}: {after_hash}")


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("action",choices=("build","install","rollback"))
    p.add_argument("--out",type=Path,required=True)
    args=p.parse_args()
    if args.action=="build":build(args.out)
    else:deploy(args.out,args.action=="rollback")
