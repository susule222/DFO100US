"""Replace only matching purchase/coupon captions in the US100 v5 IMG."""
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw, ImageFont

from us100_img5 import views
from us100_quest_act import compress_exact, inflate_exact
from us100_shop_load_atlas import pixels as encode_pixels, verify_spans
from us100_status_dstr import core
from us100_ui_atlas import index, member


NAME='sprite_interface2_univ_coupon.NPK'
ENTRY='sprite/interface2/univ_coupon/univ_coupon_buywindow.img'
BASE='b2d7ced72fae3c1c2dcaeabf39e0d86613aa09f3103c98a30ac766531eea71fb'
DONOR=Path('D:/DNF/client/DNF100/ImagePacks2/sprite_interface_cashshop.NPK')
DONOR_HASH='53617ac6f24d34d1d8a1da4a9c7ca852d1f4a31816ba2a046176a47adb773a3a'
DONOR_ENTRY='sprite/interface/cashshop/cs_buywindow.img'
BUTTONS=(*range(13,25),*range(29,33))
COUPONS=tuple(range(33,37))
FRAMES=BUTTONS+COUPONS


def coupon(i, im):
    a=np.asarray(im).copy(); w,h=im.size
    if (w,h)!=(66,92):raise ValueError('unexpected coupon layout')
    x0,x1,y0,y1=7,59,51,81
    width=x1-x0
    # Reconstruct only the caption band from its existing two sides. The icon,
    # corners and entire original alpha channel remain intact.
    t=np.arange(1,width+1)/(width+1)
    rgb=(a[y0:y1,x0-1,:3].astype(float)[:,None,:]*(1-t[None,:,None])+
         a[y0:y1,x1,:3].astype(float)[:,None,:]*t[None,:,None])
    a[y0:y1,x0:x1,:3]=np.rint(rgb).astype(np.uint8)
    result=Image.fromarray(a)
    f=ImageFont.truetype('C:/Windows/Fonts/msyhbd.ttc',14,index=1)
    ImageDraw.Draw(result).text((33,66),'优惠券',font=f,anchor='mm',
        fill=(214,212,205,255) if i==36 else (255,229,172,255),stroke_width=1,
        stroke_fill=(55,55,53,255) if i==36 else (85,26,4,255))
    result.putalpha(im.getchannel('A'))
    return result


def build(source,output):
    if core.digest(source)!=BASE or core.digest(DONOR)!=DONOR_HASH:
        raise ValueError('wrong checkout atlas identities')
    raw=member(source,ENTRY); frames=views(raw); donor=views(member(DONOR,DONOR_ENTRY)); old=source.read_bytes(); buf=bytearray(old)
    start=index(source)[ENTRY][0]; spans=[]
    for i in FRAMES:
        f=frames[i]; p=f.pixels
        if f.rotated or f.crop!=(0,0,p.width,p.height) or p.compression!=6 or any(
            j!=i and not isinstance(z,int) and p.start==z.pixels.start for j,z in enumerate(frames)):
            raise ValueError('unexpected shared/rotated checkout frame')
        k=p.width*p.height*(2 if i in BUTTONS else 4)
        if i in BUTTONS:
            g=donor[i]; q=g.pixels
            if g.rotated or f.crop!=g.crop or p.kind!=14 or q.kind!=14 or q.compression!=6:
                raise ValueError('donor checkout frame differs')
            if i<29:
                pixels=inflate_exact(q.raw,k)
            else:
                button=g.image(); draw=ImageDraw.Draw(button)
                for y in range(7,27):draw.line((33,y,75,y),fill=button.getpixel((77,y)))
                font=ImageFont.truetype('C:/Windows/Fonts/msyhbd.ttc',10 if i==32 else 13,index=1)
                draw.text((51,16),'赠送',font=font,anchor='mm',stroke_width=1,
                          fill=(224,221,214,255) if i==32 else (255,225,150,255),stroke_fill=(35,27,18,255))
                pixels=encode_pixels(button,14)
        else:
            if p.kind!=16:raise ValueError('expected BGRA coupon')
            pixels=coupon(i,f.image()).tobytes('raw','BGRA')
        data=compress_exact(pixels,len(p.raw),p.raw[:2]); x=start+p.start; y=x+len(p.raw)
        if old[x:y]!=p.raw or inflate_exact(data,k)!=pixels:raise ValueError('non-native checkout payload')
        buf[x:y]=data; spans.append((x,y,i))
    verify_spans(old,buf,spans);output.write_bytes(buf)
    local=views(member(output,ENTRY))
    if list(index(source).items())!=list(index(output).items()) or any(a!=b for i,(a,b) in enumerate(zip(frames,local)) if i not in FRAMES):
        raise ValueError('NPK offsets or untouched frames changed')
    seq=(13,17,21,29,36,33)
    sheet=Image.new('RGB',(1100,240),'#1c2b34'); d=ImageDraw.Draw(sheet)
    for j,ims in enumerate((frames,local)):
        d.text((8,j*120+2),('BEFORE','AFTER')[j],fill='white')
        for i,k in enumerate(seq):
            a=ims[k].image(); a=a.resize((2*a.width,2*a.height),Image.Resampling.NEAREST);a.thumbnail((172,93))
            sheet.paste(a,(i*182+3,j*120+20),a)
    sheet.save(output.parent/'checkout-comparison.png')
    return spans
