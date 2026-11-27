"""Localize verified US100 dungeon-clear atlases without changing frame layout."""
import argparse
import hashlib
from pathlib import Path
import struct

import numpy as np
from PIL import Image, ImageDraw, ImageFilter, ImageFont

from us100_dstr_ui import CLIENT, core
from us100_img5 import View, views
from us100_premium_panel import unchanged
from us100_quest_act import compress_exact, inflate_exact
from us100_ui_atlas import Frame, index, member, split_img

RESULT = "sprite_interface2_result_ver2.0.NPK"
CARDS = "sprite_interface2_reward_ver3.0.NPK"
UNITS = "sprite_interface_newresult.NPK"
SHOP = "sprite_interface2_dalilashop.NPK"
BASES = {
    RESULT: "027c4137a9530f629ba0ea72020268b3439b51dfbfa5ead5e12378cb18743a65",
    CARDS: "85648c680b435f313f52b88dbda9dd8a62f4f1db61c006e1b13079fb387006dc",
    UNITS: "6b520d9be74fb8931dd14ece0691e82da3bceb0fbca6dd788580b85955fb2911",
    SHOP: "2d6a9eb623e614af4e8a9f8f565037682b01cc6591a9c54a96da6853dba8f7aa",
}
FONT = "C:/Windows/Fonts/msyhbd.ttc"
GOLD = (229,191,109,255)
PINK = (255,227,226,255)
PALE = (208,237,249,255)

# Coordinates are native pixels. Insets deliberately stop at the existing
# tooltip icons, time colons, corner art, and card-divider lines.
# (rectangle, caption, point, pt, fill, alignment, background reference)
RESULT_TEXT = {
    5: [((0,0,91,25),"评级",(2,12),22,(236,240,244,255),"lm",None)],
    40: [((5,0,43,17),"评级",(6,8),13,GOLD,"lm",None),
         ((4,47,100,70),"评分",(7,58),13,PINK,"lm",(106,112))],
    41: [((0,0,97,18),"通关时间",(1,8),14,GOLD,"lm",None),
         ((0,24,97,40),"最佳时间",(1,31),14,GOLD,"lm",None)],
    42: [((5,4,80,26),"击杀经验",(8,15),12,GOLD,"lm",(147,153)),
         ((4,27,114,52),"击杀经验加成",(7,39),13,PINK,"lm",(147,153))],
    43: [((5,4,94,26),"通关经验",(8,15),12,GOLD,"lm",(145,151)),
         ((4,27,126,52),"通关经验加成",(7,39),13,PINK,"lm",(145,151))],
    44: [((0,4,99,34),"总经验",(4,19),18,GOLD,"lm",(103,109))],
}
CARD_TEXT = {
    1: [((0,0,206,17),"选择奖励（快捷键 1–8）",(103,7),14,GOLD,"mm",None)],
    2: [((0,0,76,13),"通关时间",(1,5),11,GOLD,"lm",None)],
    3: [((230,0,522,16),"消耗金币可翻开一张卡牌。",(376,8),13,GOLD,"mm",None)],
}
SHOP_TEXT = {
    0: ((113,8,213,34),"德利拉的商店",163),
    10: ((136,8,185,34),"商店",161),
    11: ((151,8,258,34),"德利拉的商店",204),
    14: ((180,8,229,34),"商店",204),
}

def unit_text(word):
    return [((0,0,27,17),word,(3,8),12,PALE,"lm",None)]

def draw(im, instructions):
    result = im.copy()
    changed = np.zeros((im.height, im.width), bool)
    original = np.asarray(im)
    for rect, word, (x,y), size, color, anchor, bg in instructions:
        l,t,r,b = rect
        if not (0<=l<r<=im.width and 0<=t<b<=im.height):
            raise ValueError(f"{word}: invalid crop {rect}")
        if bg is None:
            result.paste((0,0,0,0),rect)
        elif bg == "shop":
            # Preserve the textured header; clean only glyphs and their halos.
            target=original[t:b,l:r].copy()
            w=np.linspace(0,1,b-t+2)[1:-1,None,None]
            clean=np.rint((1-w)*np.median(original[t-2:t,l:r],axis=0)
                          +w*np.median(original[b:b+2,l:r],axis=0)).astype(np.uint8)
            excess=target.astype(np.int16)-clean.astype(np.int16)
            glyph=(excess[:,:,0]>17)&(excess[:,:,1]>10)
            halo=np.asarray(Image.fromarray(glyph.astype(np.uint8)*255).filter(ImageFilter.MaxFilter(5)))>0
            target[halo,:3]=clean[halo,:3]
            result.paste(Image.fromarray(target),(l,t))
        else:
            u,v=bg
            row=np.median(original[t:b,u:v],axis=1,keepdims=True).astype(np.uint8)
            result.paste(Image.fromarray(np.repeat(row,r-l,axis=1)),(l,t))
        font=ImageFont.truetype(FONT,size,index=1)
        overlay=Image.new("RGBA",im.size)
        d=ImageDraw.Draw(overlay)
        stroke=1
        bound=d.textbbox((x,y),word,font=font,anchor=anchor,stroke_width=stroke)
        if not (l<=bound[0] and t<=bound[1] and bound[2]<=r and bound[3]<=b):
            raise ValueError(f"{word}: {bound} does not fit {rect}")
        d.text((x,y),word,font=font,anchor=anchor,fill=color,stroke_width=stroke,stroke_fill=(17,12,9,238))
        result.alpha_composite(overlay)
        changed[t:b,l:r]=True
    if not np.array_equal(np.asarray(result)[~changed],original[~changed]):
        raise ValueError("unrelated image pixels changed")
    return result, changed

def specs():
    return {
        RESULT: {"sprite/interface2/result_ver2.0/result_ver2.0.img":RESULT_TEXT},
        CARDS: {"sprite/interface2/reward_ver3.0/reward.img":CARD_TEXT},
        UNITS: {
            "sprite/interface/newresult/result2_numset_small2.img":{12:unit_text("分"),13:unit_text("秒")},
            "sprite/interface/newresult/result2_numset_small_new.img":{13:unit_text("分"),14:unit_text("秒")},
        },
        SHOP: {"sprite/interface2/dalilashop/dalila_shop.img":{
            i:[(rect,word,(x,20),15,GOLD,"mm","shop")] for i,(rect,word,x) in SHOP_TEXT.items()
        }},
    }

def encode(original, wanted, allowed, raw):
    old=np.asarray(original)
    new=np.asarray(wanted)
    if inflate_exact(raw, original.width*original.height*4)!=original.tobytes("raw","BGRA"):
        raise ValueError("BGRA decoder does not match native pixels")
    delta=np.any(old!=new,axis=2)
    if not delta.any() or np.any(delta&~allowed):
        raise ValueError("empty or out-of-bounds change")
    for q in (1,2,3,4,6,8,12,16,24,32):
        pixels=new.copy()
        pixels[delta]=np.minimum(255,((pixels[delta].astype(np.int16)+q//2)//q)*q)
        im=Image.fromarray(pixels)
        native=im.tobytes("raw","BGRA")
        try:
            payload=compress_exact(native,len(raw),raw[:2])
        except ValueError:
            continue
        if inflate_exact(payload,len(native))!=native:
            raise ValueError("pixel stream failed round trip")
        return payload,im
    raise ValueError(f"patch does not fit {len(raw)}-byte slot")

def atlas(src, dst, members, base):
    if core.digest(src)!=base:
        raise ValueError(f"unexpected base {src.name}")
    old=src.read_bytes()
    new=bytearray(old)
    edits=[]
    preview=[]
    idx=index(src)
    def add(name, ids, pos, raw, payload):
        end=pos+len(raw)
        if old[pos:end]!=raw or len(payload)!=len(raw):
            raise ValueError("wrong atlas span")
        new[pos:end]=payload
        edits.append({"member":name,"frames":ids,"start":pos,"end":end})
    for name, rows in members.items():
        off,count=idx[name]
        blob=member(src,name)
        if blob[24:28]==b"\x05\0\0\0":
            decoded=views(blob)
            chosen={i:decoded[i] for i in rows}
            surfaces={v.pixels.start:v.pixels for v in chosen.values() if isinstance(v,View)}
            if len(surfaces)!=1 or not all(isinstance(v,View) for v in chosen.values()):
                raise ValueError("unexpected unit atlas views")
            p,=surfaces.values()
            if p.kind!=16 or p.compression!=6:
                raise ValueError("unexpected surface encoding")
            im=p.image()
            dest=im.copy()
            allowed=np.zeros((im.height,im.width),bool)
            for i,v in chosen.items():
                replacement,_=draw(v.image(),rows[i])
                a,b,c,d=v.crop
                if allowed[b:d,a:c].any():
                    raise ValueError("overlapping frame crops")
                for j,o in enumerate(decoded):
                    if j not in chosen and isinstance(o,View) and o.pixels.start==p.start:
                        l,t,r,q=o.crop
                        if a<r and l<c and b<q and t<d:
                            raise ValueError("unit crop overlaps a neighboring frame")
                allowed[b:d,a:c]=True
                dest.paste(replacement.transpose(Image.Transpose.ROTATE_270) if v.rotated else replacement,(a,b))
            payload,opened=encode(im,dest,allowed,p.raw)
            add(name,sorted(rows),off+p.start,p.raw,payload)
            for i,v in chosen.items():
                extracted=opened.crop(v.crop)
                if v.rotated:extracted=extracted.transpose(Image.Transpose.ROTATE_90)
                preview.append((name,i,v.image(),extracted))
        else:
            pos=off+32+struct.unpack_from("<I",blob,16)[0]
            frames=split_img(blob)
            for i,f in enumerate(frames):
                if not isinstance(f,Frame):
                    if i in rows:raise ValueError("localized frame is a redirect")
                    continue
                if i in rows:
                    if f.meta[:8]!=struct.pack("<II",16,6):
                        raise ValueError("unexpected v2 frame format")
                    im=f.image()
                    wanted,mask=draw(im,rows[i])
                    payload,opened=encode(im,wanted,mask,f.raw)
                    if Frame(f.meta,payload).image().tobytes()!=opened.tobytes():
                        raise ValueError("v2 parser cannot reopen patch")
                    add(name,[i],pos,f.raw,payload)
                    preview.append((name,i,im,opened))
                pos+=len(f.raw)
            if pos!=off+count:raise ValueError("v2 atlas member size mismatch")
    spans=[(s["start"],s["end"]) for s in edits]
    unchanged(old,new,spans)
    restored=bytearray(new)
    for a,b in spans:restored[a:b]=old[a:b]
    if hashlib.sha256(restored).hexdigest()!=base:
        raise ValueError("rollback failed")
    for a,b in spans:restored[a:b]=new[a:b]
    if restored!=new:raise ValueError("reapply failed")
    dst.write_bytes(new)
    if list(index(dst).items())!=list(idx.items()):
        raise ValueError("NPK directory changed")
    for name,i,_,expected in preview:
        raw=member(dst,name)
        frames=views(raw) if raw[24:28]==b"\x05\0\0\0" else split_img(raw)
        if expected.tobytes()!=frames[i].image().tobytes():
            raise ValueError("saved atlas frame differs from validated render")
    return {"before":base,"after":core.digest(dst),"spans":edits},preview

def sheet(preview,out):
    w=max(a.width for _,_,a,_ in preview)+18
    height=sum(min(a.height,70)+22 for _,_,a,_ in preview)
    panel=Image.new("RGB",(w*2,height),"#273240")
    y=0
    marker=ImageDraw.Draw(panel)
    for name,i,a,b in preview:
        marker.text((6,y+2),f"{name.rsplit('/',1)[-1]}:{i}   ORIGINAL",fill="white")
        marker.text((w+6,y+2),"LOCALIZED",fill="white")
        if a.height>70:
            a,b=(im.crop((0,0,im.width,70)) for im in (a,b))
        panel.paste(a,(6,y+15),a)
        panel.paste(b,(w+6,y+15),b)
        y+=a.height+22
    panel.save(out/"preview.png")

def build(out):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()):
        raise ValueError("use an offline staging directory")
    out.mkdir(parents=True,exist_ok=True)
    manifest={}
    all_previews=[]
    for n,members in specs().items():
        src=CLIENT/"ImagePacks2"/n
        before=out/("before-"+n)
        core.file_copy(src,before)
        entry,preview=atlas(before,out/n,members,BASES[n])
        manifest[n]=entry
        all_previews+=preview
    sheet(all_previews,out)
    core.atomic(out/"atlas.json",manifest)
    print(f"Verified {len(all_previews)} frames in {len(manifest)} NPK files")

if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("--out",type=Path,required=True)
    build(p.parse_args().out)
