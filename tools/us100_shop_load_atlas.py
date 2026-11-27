"""Localize verified 100US loading and shop image slots without moving NPK offsets."""
import argparse
from datetime import datetime, timezone
from pathlib import Path
import struct

import numpy as np
from PIL import Image, ImageDraw, ImageFilter, ImageFont

from us100_quest_act import compress_exact
from us100_status_dstr import CLEAR_FONTS
from us100_quest_ui_install import CLIENT, GameFonts, core
from us100_ui_atlas import Frame, digest, index, member, split_img


SHOP="sprite_interface2_cs_shop.NPK"
LOAD="sprite_interface2_nowloading.NPK"
BASE={SHOP:"d801affc6f284d930d5a8abc57d1c83d0ac5ff3edc169ff181b373742eff5abd",
      LOAD:"897667d39a70fd046dc916e6a76c35dfd0268d58a73e074f36944bcce6caf576"}
MEMBERS={SHOP:"sprite/interface2/cs_shop/cs_shop.img",LOAD:"sprite/interface2/nowloading/nowloading.img"}
SHOP_FRAMES=tuple(sorted(set((*range(2,38),84,*range(90,102),*range(111,115),*range(125,128),
                         *range(156,160),*range(164,176),*range(187,199),*range(207,215),
                         *range(217,237),*range(242,247),*range(249,254),*range(265,273)))))
TITLE={
    0:((602,505,996,637),800,(528,558,604),(22,26,57)),
    1:((274,340,524,426),399,(350,372,402),(13,16,37)),
    2:((271,114,507,192),386,(123,142,171),(13,15,35)),
}
FONT=Path("C:/Windows/Fonts/msyhbd.ttc")


def text_layer(im,label,x,y,sz,color,stroke=1):
    font=ImageFont.truetype(str(FONT),sz,index=1)
    d=ImageDraw.Draw(im)
    d.text((x,y),label,font=font,anchor="mm",fill=color,stroke_width=stroke,stroke_fill=(29,16,10,255))


def compact_text(im,label,x,y,sz,color):
    mask=Image.new("1",im.size)
    font=ImageFont.truetype(str(FONT),sz,index=1)
    ImageDraw.Draw(mask).text((x,y),label,font=font,anchor="mm",fill=1)
    im.paste(color,(0,0,*im.size),mask)


def fill_strokes(crop,mask,channels):
    source=crop[:,:,:channels].astype(np.float32)
    missing=mask.copy(); a=source.copy()
    for _ in range(max(mask.shape)):
        if not missing.any():
            break
        ready=~missing
        count=np.zeros(mask.shape,dtype=np.int16)
        total=np.zeros_like(a)
        for axis,step in ((0,1),(0,-1),(1,1),(1,-1)):
            available=np.roll(ready,step,axis=axis)
            if axis==0:
                available[0 if step==1 else -1,:]=False
            else:
                available[:,0 if step==1 else -1]=False
            count+=available
            total+=np.roll(a,step,axis=axis)*available[:,:,None]
        front=missing&(count>0)
        a[front]=total[front]/count[front,None]
        missing[front]=False
    for _ in range(9):
        blurred=Image.fromarray(np.rint(a).astype(np.uint8)).filter(ImageFilter.GaussianBlur(1))
        a[mask]=np.asarray(blurred)[mask]
    crop[:,:,:channels]=np.rint(a).astype(np.uint8)


def loading_patch(i,src,cn):
    if i==7:
        if src.size!=cn.size:
            raise ValueError("mismatched loading badge")
        # Both builds share the spinner; use only the donor's localized caption band.
        box=(60,154,151,180)
        src.paste(cn.crop(box),box[:2])
        return src
    if i==3:
        bg=src.copy()
        for y in range(4,26):
            p=src.getpixel((95,y))
            for x in range(99,168):
                bg.putpixel((x,y),p)
        text_layer(bg,"加载中…",133,15,13,(233,200,134,255))
        return bg
    box,x,ys,sizes=TITLE[i]
    crop=np.asarray(src.crop(box)).copy()
    r,g,b,_=crop.transpose(2,0,1).astype(np.int16)
    # Only the gold title strokes and their immediate outline are erased.
    mask=((g>77)&(r>=g)&(g-b>19)).astype(np.uint8)*255
    mask=np.asarray(Image.fromarray(mask).filter(ImageFilter.MaxFilter(21 if i==0 else 15)))>0
    fill_strokes(crop,mask,4 if i==2 else 3)
    src.paste(Image.fromarray(crop),box[:2])
    for label,y,sz,tone in zip(("第7赛季 第4章","机械崛起","开战"),ys,sizes,
                               ((254,224,164,255),(238,202,112,255),(255,216,128,255))):
        text_layer(src,label,x,y,sz,tone,2 if i==0 or label=="开战" else 1)
    return src


def shop_patch(i,src,cn):
    if i==84:
        for row,labels in enumerate((("武器","头发","帽子","脸部"),(None,"胸部","上衣","皮肤"),
                                     (None,"腰部","下装","鞋子"))):
            for col,label in enumerate(labels):
                if not label:
                    continue
                x,y=col*33,row*34
                d=ImageDraw.Draw(src)
                for dy in range(8,24):
                    d.line((x+3,y+dy,x+27,y+dy),fill=src.getpixel((x+3,y+5)))
                compact_text(src,label,x+15,y+16,9,(211,201,178,255))
        return src
    if i in (7,8,93,99):
        label={7:"道具",8:"道具",93:"赠送",99:"取消"}[i]
        x0,x1=(30,88) if i in (7,8) else (27,src.width-2)
        for y in range(3,src.height-3):
            blank=src.getpixel((x1-2,y))
            for x in range(x0,x1):
                src.putpixel((x,y),blank)
        compact_text(src,label,(x0+x1)//2,src.height//2,12,(239,211,149,255))
        return src
    if i in (211,214):
        src.paste((0,0,0,0),(35,2,src.width,src.height-1))
        tone=(246,214,151,255) if i==211 else (165,165,165,255)
        text_layer(src,"搜索",57,src.height//2,15,tone)
        return src
    if 26<=i<=37:
        word=("全部赠送","全部购买","点券充值")[(i-26)//4]
        out=src.copy()
        for y in range(6,src.height-5):
            blank=src.getpixel((src.width-8,y))
            for x in range(33,src.width-5):
                out.putpixel((x,y),blank)
        tone=(181,181,181,255) if i in (29,33,37) else (252,223,163,255)
        text_layer(out,word,src.width*2//3,src.height//2,14,tone)
        return out
    if cn.size==src.size:
        return cn
    # Preserve the US slot's logical dimensions; fit only the equivalent localized artwork.
    if 0.65<=cn.width/src.width<=1.30 and 0.8<=cn.height/src.height<=1.20:
        if src.getchannel("A").getextrema()[0]==255:
            return cn.resize(src.size,Image.Resampling.LANCZOS)
        cn.thumbnail(src.size,Image.Resampling.LANCZOS)
        out=Image.new("RGBA",src.size)
        out.paste(cn,((out.width-cn.width)//2,(out.height-cn.height)//2))
        return out
    return cn.resize(src.size,Image.Resampling.LANCZOS)


def pixels(im,kind):
    if kind==16:
        return im.tobytes("raw","BGRA")
    r,g,b,a=np.asarray(im,dtype=np.uint16).transpose(2,0,1)
    if kind==14:
        p=((a>=128).astype(np.uint16)<<15)|(((r*31+127)//255)<<10)|(((g*31+127)//255)<<5)|((b*31+127)//255)
    elif kind==15:
        p=(((a+8)//17)<<12)|(((r+8)//17)<<8)|(((g+8)//17)<<4)|((b+8)//17)
    else:
        raise ValueError("unsupported UI atlas pixel format")
    return p.astype("<u2").tobytes()


def encode(src,img):
    kind,comp=struct.unpack_from("<2I",src.meta)
    if comp==5:
        result=pixels(img,kind)
        if len(result)!=len(src.raw):
            raise ValueError("raw frame span changed")
        return result
    if comp!=6:
        raise ValueError("unsupported UI frame encoding")
    try:
        return compress_exact(pixels(img,kind),len(src.raw),src.raw[:2])
    except ValueError:
        if img.width*img.height>60000:
            raise
    for n in (5,4,3):
        a=np.asarray(img).copy()
        step=1<<(8-n)
        a[:,:,:3]=(np.minimum((a[:,:,:3].astype(np.uint16)+step//2)//step,(1<<n)-1)*255//((1<<n)-1)).astype(np.uint8)
        a[a[:,:,3]==0,:3]=0
        try:
            return compress_exact(pixels(Image.fromarray(a),kind),len(src.raw),src.raw[:2])
        except ValueError:
            continue
    raise ValueError("localized frame does not fit original compressed span")


def verify_spans(old,new,spans):
    at=0
    for x,y,_ in sorted(spans):
        if not at<=x<y<=len(old) or old[at:x]!=new[at:x]:
            raise ValueError("unexpected byte diff outside text frame payloads")
        at=y
    if len(old)!=len(new) or old[at:]!=new[at:]:
        raise ValueError("NPK length or tail changed")


def build_one(p,src,donor,out):
    n=MEMBERS[p]
    s=member(src,n)
    old=src.read_bytes()
    new=bytearray(old)
    fs=split_img(s)
    cn=split_img(member(donor,n))
    pos=index(src)[n][0]+32+struct.unpack_from("<I",s,16)[0]
    spans=[]; preview=[]; skipped=[]
    selected=SHOP_FRAMES if p==SHOP else (0,1,2,3,7)
    for i,f in enumerate(fs):
        if not isinstance(f,Frame):
            if i in selected:
                raise ValueError(f"unexpected frame link: {i}")
            continue
        end=pos+len(f.raw)
        if i in selected:
            a=f.image(); d=cn[i].image();
            img=(shop_patch if p==SHOP else loading_patch)(i,a.copy(),d)
            try:
                payload=encode(f,img)
            except ValueError as e:
                skipped.append((i,str(e)))
            else:
                if len(payload)!=len(f.raw) or old[pos:end]!=f.raw:
                    raise ValueError("invalid target frame span")
                exact=Frame(f.meta,payload).image()
                new[pos:end]=payload
                spans.append((pos,end,i))
                preview.append((i,a,exact))
        pos=end
    if not spans:
        raise ValueError("no localized slots")
    verify_spans(old,new,spans)
    dest=out/p
    dest.write_bytes(new)
    rebuilt=split_img(member(dest,n))
    if list(index(src).items())!=list(index(dest).items()) or len(rebuilt)!=len(fs) or any(
            (a!=b if isinstance(a,int) else a.meta!=b.meta) for a,b in zip(fs,rebuilt)):
        raise ValueError("archive or IMG frame metadata changed")
    pp=[v for v in preview if p==LOAD or v[0] in (2,3,7,14,18,22,29,33,34,84,90,93,99,112,156,168,187,191,195,207,211,214)]
    w,h=(410,250) if p==LOAD else (160,93)
    cols=1 if p==LOAD else 3
    gallery=Image.new("RGB",(cols*2*w,h*((len(pp)+cols-1)//cols)),"#34312c"); d=ImageDraw.Draw(gallery)
    for i,(j,a,b) in enumerate(pp):
        x,y=(i%cols)*w*2,(i//cols)*h
        for k,im in enumerate((a,b)):
            im.thumbnail((w-8,h-25))
            d.text((x+k*w+4,y+3),f"{j} {'US' if k==0 else 'CN'}",fill="white")
            gallery.paste(im,(x+k*w+4,y+18),im)
    gallery.save(out/("shop-comparison.png" if p==SHOP else "loading-comparison.png"))
    return {"before":digest(src),"after":digest(dest),"spans":spans,"skipped":skipped}


def build(out,donor):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()):
        raise ValueError("use a separate build location")
    out.mkdir(parents=True)
    m={}
    for p,sha in BASE.items():
        src=CLIENT/"ImagePacks2"/p
        if digest(src)!=sha:
            raise ValueError("US atlas baseline changed")
        m[p]=build_one(p,src,donor/p,out)
        print(f"{p}: translated={len(m[p]['spans'])}, skipped={m[p]['skipped']}")
    core.atomic(out/"ATLAS.diff.json",m)


def deploy(out,rollback):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()):
        raise ValueError("candidate overlaps live client")
    m=core.read(out/"ATLAS.diff.json")
    if m.keys()!=BASE.keys():
        raise ValueError("unexpected patch archive list")
    fonts=GameFonts(CLIENT/"Fonts")
    journal=out/"deployment.json"
    with fonts.locked():
        plan=[]
        if not rollback and fonts.hashes()!=CLEAR_FONTS:
            raise ValueError("live font state differs")
        for p,rows in m.items():
            src=CLIENT/"ImagePacks2"/p; b=out/"before"/p; c=out/p
            current=rows["after"] if rollback else rows["before"]
            if rows["before"]!=BASE[p] or digest(src)!=current or digest(c)!=rows["after"]:
                raise ValueError(f"unknown resource baseline or patch: {p}")
            if not rollback and not b.exists():
                core.file_copy(src,b)
            if digest(b)!=BASE[p]:
                raise ValueError("map resource backup differs")
            verify_spans(b.read_bytes(),c.read_bytes(),rows["spans"])
            plan.append((src,b if rollback else c,c if rollback else b,current))
        completed=[]
        try:
            for dst,source,undo,current in plan:
                if core.client_up() or digest(dst)!=current:
                    raise ValueError("DFO reopened or asset changed")
                core.file_copy(source,dst)
                completed.append((dst,undo))
                if digest(dst)!=digest(source):
                    raise ValueError("deployed atlas fingerprint differs")
        except Exception:
            if not core.client_up():
                for dst,undo in reversed(completed):
                    core.file_copy(undo,dst)
            raise
        core.atomic(journal,{"utc":datetime.now(timezone.utc).isoformat(),
                    "state":"rolled_back" if rollback else "installed_pending_game_check",
                    "assets":{dst.name:digest(dst) for dst,*_ in plan}})
        print(f"Verified {len(completed)} live NPK files")


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    sub=p.add_subparsers(dest="action",required=True)
    for action in ("build","install","rollback"):
        q=sub.add_parser(action)
        q.add_argument("--out",type=Path,required=True)
        if action=="build":q.add_argument("--donor",type=Path,required=True)
    a=p.parse_args()
    if a.action=="build":build(a.out,a.donor)
    else:deploy(a.out,a.action=="rollback")
