"""Localize the US100 HUD menu labels in-place in nine original pixel slots."""
import argparse
import base64
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import struct
import subprocess
import sys

from PIL import Image, ImageChops, ImageDraw, ImageFilter, ImageFont

from us100_premium_panel import unchanged
from us100_quest_act import compress_exact, inflate_exact
from us100_status_dstr import CLIENT, EXE, GameFonts, core
from us100_ui_atlas import Frame, digest, index, member, split_img


NAME="sprite_interface2_hud.NPK"
MEMBER="sprite/interface2/hud/menu_iconver2.img"
LIVE=CLIENT/"ImagePacks2"/NAME
BASE="fbec688d508b005e681553d524cbcc468bd07821984dcb689a1f29253a02e385"
# Live 100US report, 2026-11-25: the full-screen red X disappeared after
# reverting this exact build. Retain it as evidence, not as an install target.
REJECTED=frozenset({"49815d72f14461d1fa53f649e4227d78d34fbed3b16418c1cef4f0dc93c7ac75"})
FONT=Path("C:/Windows/Fonts/msyhbd.ttc")
LABELS={32:("Select Menu","选择菜单"), **dict.fromkeys(range(34,38),("Close","关闭")),
        **dict.fromkeys(range(38,42),("Edit Menu","编辑"))}
# Native bitmap strikes for the 10 px button strip. The left and right
# components of each character have their own one-pixel counters.
MICRO = {
    "关":(
        "..#...#..","...#.#...","#########","....#....","....#....",
        "#########","....#....","...#.#...","..#...#..","##.....##"),
    "闭":(
        ".#..#####","..#.....#","........#","#...#...#","#.#####.#",
        "#...#...#","#..##...#","#.#.#...#","#...#...#","#......##"),
    "编":(
        ".#...#....","#.....#...","###.######","..#.#....#",
        ".#..######","###.#.....","....######","..#.#.#.##",
        ".#..######","#...#.#..#"),
    "辑":(
        ".#...#####","####.#...#",".#...#####","####......",
        "#.#..#####","####..#.#.",".#...#####","####..#.#.",
        ".#...#####",".#......#."),
}
PAINT={
    32:((251,227,153),(204,152,49)),
    34:((251,240,183),(172,139,66)),35:((253,244,188),(194,161,69)),
    36:((218,189,109),(130,99,45)),37:((215,215,215),(124,124,124)),
    38:((251,240,183),(172,139,66)),39:((253,244,188),(194,161,69)),
    40:((218,189,109),(130,99,45)),41:((215,215,215),(124,124,124)),
}


def paint(original,i,light):
    label=LABELS[i][1]
    w,h=original.size
    title=i==32
    glowing=i in (35,39)
    x0, y1 = (0,24) if title else ((12 if glowing else 11) if i<38 else (13 if glowing else 12),h)
    rw=w-x0
    s=4
    font=ImageFont.truetype(FONT,21*s,index=1) if title else None
    mask=Image.new("L",(rw*s,y1*s))
    d=ImageDraw.Draw(mask)
    b=font.getbbox(label,anchor="lt") if title else (0,0,21*s,10*s)
    x=(rw*s-b[2])//2 if title else 2*s
    y=(y1*s-b[3])//2
    if x+b[2]>rw*s or y<0:
        raise ValueError("localized HUD caption exceeds original button slot")
    if title:
        d.text((x,y),label,font=font,fill=255,anchor="lt")
    else:
        for j,ch in enumerate(label):
            for yy,row in enumerate(MICRO[ch]):
                for xx,ink in enumerate(row):
                    if ink=="#":
                        x1=x+(j*11+xx)*s;y2=y+yy*s
                        d.rectangle((x1,y2,x1+s-1,y2+s-1),fill=255)
    stroke=(mask.filter(ImageFilter.MaxFilter(13 if title else 5))
            .resize((rw,y1),Image.Resampling.LANCZOS))
    ink=mask.resize((rw,y1),Image.Resampling.LANCZOS)
    cell=Image.new("RGBA",(rw,y1))
    if glowing:
        g=stroke.filter(ImageFilter.MaxFilter(3)).filter(ImageFilter.GaussianBlur(.48))
        layer=Image.new("RGBA",cell.size,(29,103,127,0))
        layer.putalpha(g.point(lambda a: int(a*.65)))
        cell.alpha_composite(layer)
    shade=Image.new("RGBA",cell.size,(9,7,3,0));shade.putalpha(stroke)
    cell.alpha_composite(shade)
    top,bot=PAINT[i]
    colors=[]
    for row in range(y1):
        g=max(0,min(1,(row-y/s)/max(1,b[3]/s-1)))
        colors.append((*[int(a+(b-a)*g) for a,b in zip(top,bot)],255))
    layer=Image.new("RGBA",(1,y1));layer.putdata(colors)
    layer=layer.resize((rw,y1),Image.Resampling.NEAREST);layer.putalpha(ink)
    cell.alpha_composite(layer)
    # A small local palette fits the original zlib slots without altering
    # icons or geometry. The title divider is outside this region.
    if not title:
        ink=mask.resize((rw,y1),Image.Resampling.NEAREST)
        body=Image.new("RGBA",(rw,y1))
        rim=ink.filter(ImageFilter.MaxFilter(3))
        if glowing:
            halo=Image.new("RGBA",body.size,(29,103,127,0))
            halo.putalpha(rim.filter(ImageFilter.MaxFilter(3)).point(lambda a:84 if a else 0))
            body.alpha_composite(halo)
        if light>=4 or glowing:
            dark=Image.new("RGBA",body.size,(9,7,3,0));dark.putalpha(rim)
            body.alpha_composite(dark)
        fg=Image.new("RGBA",body.size,tuple(round((2*a+b)/3) for a,b in zip(top,bot))+(0,))
        fg.putalpha(ink);body.alpha_composite(fg)
        cell=body
    else:
        cell=cell.quantize(colors=light if light else 10,method=Image.Quantize.FASTOCTREE).convert("RGBA")
    result=original.copy();result.paste(cell,(x0,0))
    a=original.crop((0,y1,w,h) if title else (0,0,x0,h))
    b=result.crop((0,y1,w,h) if title else (0,0,x0,h))
    if a.tobytes()!=b.tobytes() or ImageChops.difference(original.convert("RGB"),result.convert("RGB")).getbbox() is None:
        raise ValueError("HUD divider/icon changed or caption unchanged")
    return result


def slices(path):
    pos,size=index(path)[MEMBER]
    data=member(path,MEMBER)
    end=pos+size;pos+=32+struct.unpack_from("<I",data,16)[0]
    frames=split_img(data)
    for i,f in enumerate(frames):
        if isinstance(f,Frame):
            yield i,pos,f
            pos+=len(f.raw)
    if pos!=end:
        raise ValueError("HUD IMG tail mismatch")


def inspect(path):
    return {"file":digest(path),"frames":{str(i):{"offset":pos,"size":len(f.raw),
        "geometry":f.geometry,"meta":hashlib.sha256(f.meta).hexdigest(),
        "rgba":hashlib.sha256(f.image().tobytes()).hexdigest()} for i,pos,f in slices(path) if i in LABELS}}


def apply(original, diff):
    m=core.read(diff)
    if m["before"]!=BASE or digest(original)!=BASE or m["member"]!=MEMBER:
        raise ValueError("wrong HUD patch source")
    a=original.read_bytes();b=bytearray(a)
    frame_map={i:(pos,f) for i,pos,f in slices(original) if i in LABELS}
    if sorted(x["frame"] for x in m["spans"])!=sorted(frame_map):
        raise ValueError("HUD patch frame set changed")
    changes=[]
    for kv in m["spans"]:
        i=kv["frame"];x,y=kv["start"],kv["end"]
        pos,f=frame_map[i];c=base64.b64decode(kv["payload"],validate=True)
        if (x,y)!=(pos,pos+len(f.raw)) or len(c)!=y-x:
            raise ValueError("HUD patch offset or compressed length differed")
        w,h=f.geometry[:2]
        inflate_exact(c,w*h*4)
        b[x:y]=c;changes.append((x,y))
    unchanged(a,b,changes)
    if hashlib.sha256(b).hexdigest()!=m["after"]:
        raise ValueError("reapplied HUD patch mismatch")
    return bytes(b)


def build(out):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()) or digest(LIVE)!=BASE:
        raise ValueError("require the known live HUD baseline and separate output")
    out.mkdir(parents=True)
    old,new=out/("before-"+NAME),out/NAME
    core.file_copy(LIVE,old)
    if digest(old)!=BASE:raise ValueError("source changed during snapshot")
    data=old.read_bytes(); buf=bytearray(data)
    spans=[];pictures={}
    for i,pos,f in slices(old):
        if i not in LABELS:continue
        if f.meta[:8]!=struct.pack("<II",16,6):
            raise ValueError("unknown HUD caption mode")
        original=f.image()
        for style in (32,24,18,14,12,10,8,6,5,4,3,2,1,0) if i==32 else (6,5,4,3,2,1,0):
            result=paint(original,i,style)
            raw=result.tobytes("raw","BGRA")
            try:packed=compress_exact(raw,len(f.raw),f.raw[:2])
            except ValueError:continue
            break
        else:
            raise ValueError(f"HUD frame {i} cannot fit old slot")
        if Frame(f.meta,packed).image().tobytes()!=result.tobytes():
            raise ValueError("HUD frame reopen differs")
        dest=pos+len(f.raw)
        if data[pos:dest]!=f.raw:raise ValueError("HUD payload source offset invalid")
        buf[pos:dest]=packed; pictures[i]=(original,result)
        spans.append({"frame":i,"start":pos,"end":dest,"before":LABELS[i][0],"after":LABELS[i][1],
                      "payload":base64.b64encode(packed).decode("ascii")})
    if pictures.keys()!=LABELS.keys():raise ValueError("incomplete frame set")
    unchanged(data,buf,[(v["start"],v["end"]) for v in spans])
    new.write_bytes(buf)
    base,localized=inspect(old),inspect(new)
    if index(old)!=index(new):
        raise ValueError("HUD member offsets shifted")
    for i,v in base["frames"].items():
        vv=localized["frames"][i]
        if v["rgba"]==vv["rgba"] or any(v[k]!=vv[k] for k in v.keys()-{"rgba"}):
            raise ValueError(f"HUD caption frame {i} metadata differs")
    mf=out/"NPK.diff.json"; core.atomic(mf,{"before":BASE,"after":localized["file"],"member":MEMBER,"spans":spans})
    sheet=Image.new("RGB",(936,570),"#22232c")
    d=ImageDraw.Draw(sheet)
    d.text((8,6),"BEFORE",fill="white");d.text((476,6),"AFTER",fill="white")
    for row,(i,pair) in enumerate(pictures.items()):
        for c,im in enumerate(pair):
            x=c*468+8;y=(24 if row==0 else 95+(row-1)*56)
            d.text((x,y),f"Frame {i}",fill="white")
            s=1 if row==0 else 3
            im=im.resize((im.width*s,im.height*s),Image.Resampling.NEAREST)
            sheet.paste(im,(x,y+14),im)
    sheet.save(out/"hud-menu-comparison.png")
    roll=out/"ROLLBACK.sh"
    src=Path(__file__).with_name("us100_status_rollback.sh").read_text(encoding="utf-8")
    roll.write_text(src.replace("dstr.dat",NAME).replace("DSTR.diff.json","NPK.diff.json"),
                    encoding="utf-8",newline="\n")
    copy=out/"rollback-test.NPK"
    core.file_copy(new,copy)
    result=subprocess.run([sys.executable,str(roll),str(copy)],check=True,capture_output=True,text=True)
    if inspect(copy)!=base or apply(copy,mf)!=buf:
        raise ValueError("HUD rollback or reapply proof differs")
    core.atomic(out/"verification.json",{"baseline":base,"installed_candidate":localized,"restored":inspect(copy),
                 "reapplied_sha256":localized["file"],"rollback_stdout":result.stdout.strip(),
                 "verified_spans":len(spans),"game_checked":False})
    print(out,localized["file"])


def deploy(out,rollback):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()): raise ValueError("candidate must be offline")
    old,new=out/("before-"+NAME),out/NAME
    checks=core.read(out/"verification.json")
    if (checks["baseline"]!=checks["restored"] or digest(new)!=checks["reapplied_sha256"]
        or apply(old,out/"NPK.diff.json")!=new.read_bytes()):
        raise ValueError("HUD patch not verified")
    if not rollback and digest(new) in REJECTED:
        raise ValueError("known HUD regression: this atlas is blocked from installation")
    before,after=(new,old) if rollback else (old,new)
    with GameFonts(CLIENT/"Fonts").locked():
        if core.client_up() or digest(LIVE)!=digest(before) or digest(CLIENT/"DFO.exe")!=EXE:
            raise ValueError("DFO running or HUD baseline changed")
        guard={n:digest(CLIENT/n) for n in ("dstr.dat","Script.pvf")}
        if core.client_up(): raise ValueError("DFO reopened")
        core.file_copy(after,LIVE)
        try:
            if digest(LIVE)!=digest(after) or any(digest(CLIENT/n)!=h for n,h in guard.items()):
                raise ValueError("deployed HUD hash mismatch")
        except Exception:
            if not core.client_up():core.file_copy(before,LIVE)
            raise
        receipt={"utc":datetime.now(timezone.utc).isoformat(),"file":str(LIVE),"sha256":digest(LIVE),
                 "preserved":guard,"rolled_back":rollback,"game_checked":False}
        core.atomic(out/("rollback-deployment.json" if rollback else "deployment.json"),receipt)
        print(json.dumps(receipt,ensure_ascii=False))


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("action",choices=("build","install","rollback","inspect"))
    p.add_argument("--out",type=Path)
    p.add_argument("--file",type=Path)
    a=p.parse_args()
    if a.action=="build":build(a.out)
    elif a.action=="inspect":print(json.dumps(inspect(a.file),ensure_ascii=False))
    else:deploy(a.out,a.action=="rollback")
