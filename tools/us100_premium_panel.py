"""Localize the 100US Neo membership panel in its two original NPK frame slots."""
import argparse
import base64
import hashlib
import json
from datetime import datetime, timezone
from pathlib import Path
import struct
import subprocess
import sys

import numpy as np
from PIL import Image, ImageDraw, ImageFilter, ImageFont

from us100_quest_act import compress_exact, inflate_exact
from us100_quest_ui_install import CLIENT, GameFonts, core
from us100_ui_atlas import Frame, digest, index, member, split_img


NAME = "sprite_interface2_ui_univ_neoscontract_22_new.NPK"
MEMBER = "sprite/interface2/ui/univ/neoscontract/22_new/neoscontract.img"
BASE = "348c87e3e1878ab34cb0175152abf0d8e77e02fee3890f4b3a29ed01ec0243e1"
LIVE = CLIENT / "ImagePacks2" / NAME
FONT = Path("C:/Windows/Fonts/msyh.ttc")
BOLD = Path("C:/Windows/Fonts/msyhbd.ttc")
EXPECTED = {
    0: (320, 480, 0, 0, 320, 480),
    5: (38, 40, 0, 0, 38, 40),
}
LOGO_REGIONS = ((121, 14, 271, 44), (121, 39, 155, 61), (190, 41, 283, 59), (211, 12, 224, 18))
GOLD_REGIONS = ((67, 77, 250, 91), (82, 351, 235, 367))
PARAGRAPHS = [
    ((20, 106, 231, 118), "权益1", "疲劳值上限提升至273"),
    ((20, 120, 235, 132), "权益2", "通关地下城经验 +10%"),
    ((20, 134, 233, 146), "权益3", "通关奖励卡牌 +1"),
    ((20, 148, 291, 160), "权益4", "退出地下城不扣除生命值"),
    ((20, 162, 304, 175), "权益5", "可租用会员专属武器："),
    ((75, 176, 120, 188), "", "成长型凤凰武器"),
    ((20, 190, 201, 202), "权益6", "每日领取专属奖励道具"),
    ((20, 380, 158, 392), "权益1", "免费进入团队副本"),
    ((76, 394, 197, 407), "", "适用团队副本："),
    ((86, 408, 288, 421), "", "超时空漩涡 / 普雷·伊希斯 / 希洛克 / 奥兹玛"),
    ((20, 422, 293, 435), "权益2", "普通、高级、军团地下城"),
    ((75, 436, 217, 449), "", "及团队副本可获得额外奖励"),
]
SUBHEADINGS = [
    ((72, 210, 247, 225), "成长型凤凰武器租用"),
    ((111, 273, 207, 287), "每日奖励道具"),
]
FOOTER = (42, 460, 282, 473)
BADGE = (6, 14, 30, 24)


def mask_for(shape, regions):
    out = np.zeros(shape, dtype=bool)
    for x0, y0, x1, y1 in regions:
        out[y0:y1, x0:x1] = True
    return out


def remove_dark_text(data, box):
    # Estimate the local grain from intact scanlines immediately above/below.
    # Replace the lettering/shadow footprint, not a rectangular strip.
    x0, y0, x1, y1 = box
    src = data[y0:y1, x0:x1, :3]
    top, bot = data[y0-1, x0:x1, :3], data[y1, x0:x1, :3]
    t = np.linspace(0, 1, y1-y0+2)[1:-1, None, None]
    bg = np.rint((1-t)*top + t*bot).astype(np.uint8)
    delta = np.abs(src.astype(np.int16) - bg.astype(np.int16))
    ink = ((delta[...,0] > 8) | (delta[...,1] > 5)).astype(np.uint8) * 255
    ink = np.asarray(Image.fromarray(ink).filter(ImageFilter.MaxFilter(3))) > 0
    np.copyto(src, bg, where=ink[...,None])


def repair_gold(data, box):
    x0, y0, x1, y1 = box
    src = data[y0:y1, x0:x1, :3]
    top, bottom = data[y0-1, x0:x1, :3], data[y1, x0:x1, :3]
    t = np.linspace(0, 1, y1-y0+2)[1:-1,None,None]
    bg = np.rint(top * (1-t) + bottom*t).astype(np.uint8)
    # Both headings sit on flat gradient rows. Use clean samples immediately
    # outside the ink bands; preserve the bevel and corner ornaments.
    ink = (np.abs(src.astype(np.int16) - bg.astype(np.int16)).max(axis=2) > 6)
    ink = np.asarray(Image.fromarray(ink.astype(np.uint8)*255).filter(ImageFilter.MaxFilter(3))) > 0
    np.copyto(src, bg, where=ink[...,None])


def repair_logo(data):
    rgb = data[:66, :, :3]
    cut = mask_for(rgb.shape[:2], LOGO_REGIONS)
    # Treat the actual text footprints as unknown; unchanged pixels constrain a
    # local smooth reconstruction. Retain the Neo mark and foreground glints.
    f = rgb.astype(np.float32)
    for x in np.flatnonzero(cut.any(axis=0)):
        ys = np.where(cut[:,x])[0]
        src = np.where(~cut[:,x])[0]
        for c in range(3):
            f[ys,x,c] = np.interp(ys, src, f[src,x,c])
    for _ in range(220):
        mid = f[1:-1,1:-1,:]
        neighbors = (f[:-2,1:-1,:] + f[2:,1:-1,:] + f[1:-1,:-2,:] + f[1:-1,2:,:])*0.25
        np.copyto(mid, neighbors, where=cut[1:-1,1:-1,None])
    rgb[cut] = np.rint(f[cut]).astype(np.uint8)


def gradient_title(im, xy, text, px, top, bottom, edge, stroke, anchor="lt"):
    # Use native frame coordinates. Antialias the glyphs, not the whole frame.
    s = 3
    v = ImageFont.truetype(BOLD, px*s)
    b = v.getbbox(text,anchor="lt",stroke_width=stroke*s)
    w,h = b[2]-b[0], b[3]-b[1]
    m, border = [Image.new("L",(w+6,h+6)) for _ in range(2)]
    offset = (3-b[0],3-b[1])
    ImageDraw.Draw(m).text(offset,text,font=v,anchor="lt",fill=255)
    ImageDraw.Draw(border).text(offset,text,font=v,anchor="lt",fill=255,stroke_width=stroke*s)
    pixels = np.zeros((h+6,w+6,4),dtype=np.uint8)
    blend=np.clip((np.arange(h+6)-(3+stroke*s))/max(1,h-2*stroke*s-1),0,1)[:,None]
    c=np.rint(np.asarray(top)*(1-blend)+np.asarray(bottom)*blend).astype(np.uint8)
    pixels[:,:,:3]=c[:,None,:]; pixels[:,:,3]=np.asarray(m)
    glyph=Image.new("RGBA",border.size,(*edge,0));glyph.putalpha(border)
    glyph.alpha_composite(Image.fromarray(pixels))
    glyph=glyph.resize((round(glyph.width/s),round(glyph.height/s)),Image.Resampling.LANCZOS)
    alpha=glyph.getchannel("A"); bb=alpha.getbbox()
    glyph=glyph.crop(bb)
    x,y=xy
    if anchor=="mt":
        x -= glyph.width//2
    im.alpha_composite(glyph,(x,y))
    return x,y,x+glyph.width,y+glyph.height


def panel(frame):
    original = np.asarray(frame)
    data = original.copy()
    repair_logo(data)
    for box in GOLD_REGIONS:
        repair_gold(data, box)
    for box,*_ in PARAGRAPHS+SUBHEADINGS+[(FOOTER,)]:
        remove_dark_text(data,box)
    # Continue the existing dotted rules across the shorter captions.
    for (x0,y0,x1,y1),caption in SUBHEADINGS:
        y = 219 if y0==210 else 280
        half = 6*len(caption)+7
        for x in range(x0,x1):
            if abs(x-160)>half and (x+2)%10<7:
                data[y,x,:3]=[72,48,24]
    im = Image.fromarray(data)
    logo = gradient_title(im,(121,16),"高级会员",35,(255,253,203),(255,167,3),(72,29,6),1)
    if logo[2]>275: raise ValueError("main logo out of bounds")
    gradient_title(im,(234,50),"契约",15,(255,227,126),(255,174,25),(67,21,10),1,"mt")
    d = ImageDraw.Draw(im)
    header = ImageFont.truetype(BOLD,14)
    d.text((159,79),"新尊享特权契约",font=header,anchor="mt",fill=(62,34,21),stroke_width=0)
    d.text((159,354),"VIP会员契约",font=header,anchor="mt",fill=(62,34,21),stroke_width=0)
    text_font = ImageFont.truetype(FONT,11)
    label_font = ImageFont.truetype(BOLD,11)
    for (x0,y0,x1,y1),label,caption in PARAGRAPHS:
        if label:
            d.text((23,y0),label,font=label_font,anchor="lt",fill=(255,174,60))
        if not caption:
            continue
        x = 76
        color = (240,201,137)
        if y0==394:
            color=(248,155,87)
        elif y0==408:
            color=(113,214,166)
            x=86
        b=d.textbbox((x,y0),caption,font=text_font,anchor="lt")
        if b[2]>303 or b[3]>y1: raise ValueError(f"caption outside slot: {caption}: {b}")
        d.text((x,y0),caption,font=text_font,anchor="lt",fill=color)
    for (x0,y0,x1,y1),caption in SUBHEADINGS:
        bb=gradient_title(im,(160,y0-1),caption,13,(255,230,128),(244,171,28),(48,22,10),1,"mt")
        if bb[3]>y1+2:raise ValueError("section heading outside slot")
    d.text((160,462),"详情请参阅官方网站。",font=text_font,anchor="mt",fill=(192,151,128))
    permitted = mask_for(original.shape[:2],
                         (*LOGO_REGIONS,*GOLD_REGIONS,(121,16,275,55),(212,50,257,69),
                          (109,79,209,94),(118,354,200,368),
                          (20,106,305,202),(20,380,305,449),FOOTER,(100,462,220,474),
                          *((a,b-1,c,d+2) for (a,b,c,d),_ in SUBHEADINGS)))
    arr = np.asarray(im)
    spill = np.argwhere(np.any(arr != original,axis=2) & ~permitted)
    if len(spill) or np.any(arr[:,:,3] != original[:,:,3]):
        raise ValueError(f"panel edit escaped allowed regions (y,x): {spill[:12].tolist()}")
    return im


def badge(frame):
    original = np.asarray(frame)
    data = original.copy()
    x0,y0,x1,y1=BADGE
    # Fill inside the medallion from its undisturbed side pixels. The gold ring,
    # crown and laurel above/below the caption remain in the original frame.
    t=np.linspace(0,1,x1-x0+2)[1:-1,None]
    data[y0:y1,x0:x1,:3]=np.rint(data[y0:y1,x0-1:x0,:3]*(1-t)
                                  +data[y0:y1,x1:x1+1,:3]*t).astype(np.uint8)
    im=Image.fromarray(data)
    d=ImageDraw.Draw(im)
    d.text((18,14),"生效",font=ImageFont.truetype(BOLD,10),anchor="mt",fill=(255,224,97))
    region=mask_for(original.shape[:2], (BADGE, ))
    if np.any(np.asarray(im)[~region] != original[~region]):
        raise ValueError("badge ring changed")
    return im


def palette_precision(original, edited, step):
    # Round only modified RGB samples: 0-3 levels of error on the main
    # texture, with a coarser palette reserved for the pixel-art seal.
    before, out = np.asarray(original), np.array(edited)
    sel = np.any(before[:,:,:3] != out[:,:,:3],axis=2)
    rgb = out[sel,:3].astype(np.int16)
    out[sel,:3] = np.minimum(255, ((rgb+step//2)//step)*step).astype(np.uint8)
    return Image.fromarray(out)


def slices(path):
    off,size = index(path)[MEMBER]
    raw=member(path,MEMBER)
    pos=off+32+struct.unpack_from("<I",raw,16)[0]
    for i,f in enumerate(split_img(raw)):
        if isinstance(f,Frame):
            yield i,pos,f
            pos+=len(f.raw)
    if pos!=off+size:
        raise ValueError("unrecognized atlas tail")


def unchanged(original, candidate, spans):
    if len(original)!=len(candidate):
        raise ValueError("atlas size changed")
    tick=0
    for x,y in sorted(spans):
        if x<tick or original[tick:x]!=candidate[tick:x] or original[x:y]==candidate[x:y]:
            raise ValueError("unexpected change outside two pixel streams")
        tick=y
    if original[tick:]!=candidate[tick:]:
        raise ValueError("unexpected change after edited streams")


def inspect(path):
    fs = list(slices(path))
    for i, pos, f in fs:
        if i in EXPECTED and (f.geometry!=EXPECTED[i] or f.meta[:8]!=struct.pack("<2I",16,6)):
            raise ValueError("unexpected premium atlas frame layout")
        f.image()
    return {
        "sha256":digest(path),"index":index(path),
        "frames":{i:{"geometry":f.geometry,"pos":pos,"bytes":len(f.raw),
                     "meta_sha256":hashlib.sha256(f.meta).hexdigest(),
                     "rgba_sha256":hashlib.sha256(f.image().tobytes()).hexdigest()}
                  for i,pos,f in fs},
    }


def build(out):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()) or digest(LIVE)!=BASE:
        raise ValueError("expected installed original premium atlas and isolated output")
    out.mkdir(parents=True,exist_ok=False)
    before,after=out/("before-"+NAME),out/NAME
    core.file_copy(LIVE,before)
    if digest(before)!=BASE:
        raise ValueError("premium atlas changed during isolated snapshot")
    src=before.read_bytes(); candidate=bytearray(src)
    baseline=inspect(before); spans=[]; preview={}
    for i, pos, f in slices(before):
        if i not in EXPECTED: continue
        orig=f.image(); rendered=(panel if i==0 else badge)(orig)
        for step in ((6,7,8) if i==0 else (20,22,24,26,28,32,36)):
            changed=palette_precision(orig,rendered,step)
            data=changed.tobytes("raw","BGRA")
            try:
                packed=compress_exact(data,len(f.raw),f.raw[:2])
                break
            except ValueError:
                continue
        else:
            raise ValueError(f"localized frame does not fit the original compressed slot: {i}")
        if inflate_exact(packed,len(data))!=data or Frame(f.meta,packed).image().tobytes()!=changed.tobytes():
            raise ValueError("encoded pixels do not round trip")
        end=pos+len(f.raw)
        if src[pos:end]!=f.raw:raise ValueError("unexpected source payload")
        candidate[pos:end]=packed
        spans.append({"frame":i,"start":pos,"end":end,"rgb_quantization_step":step,
                      "payload":base64.b64encode(packed).decode("ascii")})
        preview[i]=(orig,changed)
    if {s["frame"] for s in spans}!=EXPECTED.keys():raise ValueError("missing localized frame")
    unchanged(src,candidate,[(s["start"],s["end"]) for s in spans])
    after.write_bytes(candidate)
    localized=inspect(after)
    if baseline["index"]!=localized["index"]:raise ValueError("NPK index changed")
    for i,v in baseline["frames"].items():
        dst=localized["frames"][i]
        if (v["rgba_sha256"]!=dst["rgba_sha256"])!=(i in EXPECTED) or any(dst[k]!=v[k] for k in v.keys()-{"rgba_sha256"}):
            raise ValueError(f"frame integrity check failed: {i}")
    core.atomic(out/"NPK.diff.json",{"before":baseline["sha256"],"after":localized["sha256"],
                                      "member":MEMBER,"spans":spans})
    album=Image.new("RGB",(2*(320+8)*2,2*(480+24)),"#262121")
    captions=ImageDraw.Draw(album)
    for c in (0,1):
        x=8+656*c
        captions.text((x,8),("BEFORE","AFTER")[c],fill="white")
        mock=preview[0][c].copy()
        mock.alpha_composite(preview[5][c],(257,68))
        album.paste(mock.resize((640,960),Image.Resampling.NEAREST),(x,32))
    album.save(out/"premium-comparison.png")
    script=Path(__file__).with_name("us100_status_rollback.sh").read_text(encoding="utf-8")
    roll=out/"ROLLBACK.sh"
    roll.write_text(script.replace("dstr.dat",NAME).replace("DSTR.diff.json","NPK.diff.json"),
                    encoding="utf-8",newline="\n")
    scratch=out/"rollback-test.NPK"
    core.file_copy(after,scratch)
    proc = subprocess.run([sys.executable,str(roll),str(scratch)],capture_output=True,text=True)
    proc.check_returncode()
    if inspect(scratch)!=baseline:raise ValueError("round-trip rollback differs from baseline")
    applied=apply(scratch,out/"NPK.diff.json")
    if applied!=candidate:raise ValueError("reapplying diff differs from built candidate")
    core.atomic(out/"verification.json",{"baseline":baseline,"candidate":localized,
                 "rollback":inspect(scratch),"rollback_stdout":proc.stdout.strip(),
                 "reapply_sha256":hashlib.sha256(applied).hexdigest(),
                 "only_two_original_compressed_slots_changed":True,"game_checked":False})
    print(out,localized["sha256"])


def apply(path, diff):
    m=core.read(diff)
    if digest(path)!=BASE or m["before"]!=BASE or m["member"]!=MEMBER:
        raise ValueError("unexpected diff baseline")
    original=path.read_bytes()
    candidate=bytearray(original)
    expected={i:(pos,pos+len(f.raw)) for i,pos,f in slices(path) if i in EXPECTED}
    if sorted(s["frame"] for s in m["spans"])!=sorted(expected):raise ValueError("wrong span count")
    for s in m["spans"]:
        i,x,y=(s[k] for k in ("frame","start","end"))
        data=base64.b64decode(s["payload"],validate=True)
        if expected[i]!=(x,y) or len(data)!=y-x:raise ValueError("wrong compressed slot")
        inflate_exact(data,EXPECTED[i][0]*EXPECTED[i][1]*4)
        candidate[x:y]=data
    unchanged(original,candidate,list(expected.values()))
    if hashlib.sha256(candidate).hexdigest()!=m["after"]:raise ValueError("diff candidate hash mismatch")
    return bytes(candidate)


def deploy(out, rollback=False):
    out=out.resolve()
    a,b=out/("before-"+NAME),out/NAME
    m=core.read(out/"verification.json")
    if out.is_relative_to(CLIENT.resolve()) or apply(a,out/"NPK.diff.json")!=b.read_bytes() or m["baseline"]!=m["rollback"]:
        raise ValueError("candidate has not passed isolated verification")
    if digest(b)!=m["reapply_sha256"]:raise ValueError("candidate verification fingerprint differs")
    old,new=(b,a) if rollback else (a,b)
    fonts=CLIENT/"Fonts"
    preserved_paths=[CLIENT/n for n in ("Script.pvf","dstr.dat","DFO.exe")] + [
        CLIENT/"ImagePacks2"/n for n in ("sprite_interface2_premiumservice.NPK","sprite_interface2_ui_univ_neoscontract.NPK")
    ] + sorted(p for p in fonts.iterdir() if p.is_file())
    with GameFonts(fonts).locked():
        if core.client_up() or digest(LIVE)!=digest(old):
            raise ValueError("DFO is still running or live atlas fingerprint has changed")
        protected={str(p):digest(p) for p in preserved_paths}
        if core.client_up():raise ValueError("DFO started during preflight")
        core.file_copy(new,LIVE)
        try:
            if digest(LIVE)!=digest(new) or any(digest(Path(p))!=v for p,v in protected.items()):
                raise ValueError("installation verification failed")
        except Exception:
            if not core.client_up(): core.file_copy(old,LIVE)
            raise
        info={"utc":datetime.now(timezone.utc).isoformat(),"file":str(LIVE),"sha256":digest(LIVE),
              "rollback":rollback,"protected":protected,"game_checked":False}
        core.atomic(out/("rollback-deployment.json" if rollback else "deployment.json"),info)
        print(json.dumps(info,ensure_ascii=False))


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("action",choices=("build","inspect","install","rollback"))
    p.add_argument("--out",type=Path)
    p.add_argument("--file",type=Path)
    args=p.parse_args()
    if args.action=="inspect": print(json.dumps(inspect(args.file),ensure_ascii=False))
    elif args.action=="build":build(args.out)
    else:deploy(args.out,args.action=="rollback")
