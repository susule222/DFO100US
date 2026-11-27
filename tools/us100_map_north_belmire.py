"""Modify only proven text patches in the US100 North Bel Myre map atlas."""
import argparse
from datetime import datetime, timezone
import hashlib
from pathlib import Path
import struct

from PIL import Image, ImageChops, ImageDraw

from us100_quest_act import OUTLINE_FONTS, REL as QUEST_REL, compress_exact
from us100_quest_text_trial import ACT, EXPECTED_TRIAL
from us100_quest_ui_install import CLIENT, GameFonts, PVF, core
from us100_ui_atlas import Frame, index, member, split_img


PACK = "sprite_interface2_worldmap_step_1(town).NPK"
REL = Path("ImagePacks2") / PACK
MEMBER = "sprite/interface2/worldmap/step_1(town)/north_belmire.img"
BASELINE = "4c5cb8a845c22aad275aeb04e4f77c1d106f82aeeb2bbb99a57f6fe8b6b43860"
DONOR = "8396e96e1d25027c3fa9b8327788f688191084d694acf5a4030f7dfb3b64e3ef"
DSTR_R1 = "4ccf24f93922a31a2ace1b08d750a40fd5897ab91efd96aa22abe9486f21799e"
ALIGNED = {
    0: [((3,98,113,133),(0,39)), ((228,156,322,185),(37,-91))],
    1: [((315,37,384,72),(0,0))],
    2: [((191,55,264,76),(0,0))],
    4: [((64,240,220,269),(0,0))],
    5: [((214,131,271,158),(0,0))],
    8: [((40,77,94,99),(0,0))],
}
# Rectangles are inner faces of the US signs. Donor crops omit the border and road arrows.
SIGNS = {
    0: ((76,265,220,297),0,(301,185,407,203)),
    6: ((3,4,53,35),6,(5,7,52,19)),
    7: ((64,8,148,27),7,(66,8,123,27)),
    9: ((91,20,174,41),7,(66,8,123,27)),
}
TARGETS = ALIGNED.keys() | SIGNS.keys()
COPY_TEXT_ALPHA = {4,8}


def paste_aligned(out, cn, rect, shift, copy_alpha=False):
    x0,y0,x1,y1 = rect
    dx, dy = shift
    if not (0 <= x0 < x1 <= out.width and 0 <= y0 < y1 <= out.height
            and 0 <= x0+dx < x1+dx <= cn.width and 0 <= y0+dy < y1+dy <= cn.height):
        raise ValueError("out-of-range aligned map crop")
    patch = cn.crop((x0+dx,y0+dy,x1+dx,y1+dy))
    if not copy_alpha:
        patch.putalpha(out.getchannel("A").crop(rect))
    out.paste(patch,(x0,y0))


def paint_sign(out, rect, donor, src, compact=False):
    x0,y0,x1,y1 = rect
    raw = donor.crop(src)
    glyph = Image.new("RGBA",raw.size)
    for y in range(raw.height):
        for x in range(raw.width):
            r,g,b,a = raw.getpixel((x,y))
            if a > 160 and r >= 80 and g >= 65 and r+g >= 170:
                shade=(213,184,122,255) if compact else (r,g,b,255)
                glyph.putpixel((x,y),shade)
    bounds = glyph.getbbox()
    if bounds is None:
        raise ValueError("empty map sign text crop")
    glyph = glyph.crop(bounds)
    mode=Image.Resampling.NEAREST if compact else Image.Resampling.LANCZOS
    glyph.thumbnail((x1-x0-2,y1-y0-2),mode)
    face = out.crop(rect)
    ink = (0,0,0,204)
    if face.get_flattened_data().count(ink) < face.width*face.height//3:
        raise ValueError("unexpected US map sign background")
    bg = Image.new("RGBA",face.size,ink)
    bg.alpha_composite(glyph,((face.width-glyph.width)//2,(face.height-glyph.height)//2))
    out.paste(bg,(x0,y0))


def build(source, donor, out):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()) or core.digest(source)!=BASELINE or core.digest(donor)!=DONOR:
        raise ValueError("unexpected map baseline or donor")
    src=member(source,MEMBER)
    native,new=split_img(src),split_img(member(donor,MEMBER))
    a=source.read_bytes()
    b=bytearray(a)
    base=index(source)[MEMBER][0]
    pos=base+32+struct.unpack_from("<I",src,16)[0]
    changes=[]
    previews=[]
    for i,frame in enumerate(native):
        if not isinstance(frame,Frame):
            continue
        end=pos+len(frame.raw)
        if i in TARGETS:
            if frame.meta[:8]!=struct.pack("<2I",16,6) or frame.raw[:2]!=b"\x78\x9c" or a[pos:end]!=frame.raw:
                raise ValueError(f"incompatible map frame {i}")
            im=frame.image()
            patched=im.copy()
            for rect,shift in ALIGNED.get(i,()):
                paste_aligned(patched,new[i].image(),rect,shift,copy_alpha=i in COPY_TEXT_ALPHA)
            if i in SIGNS:
                rect,j,src_rect=SIGNS[i]
                paint_sign(patched,rect,new[j].image(),src_rect,compact=i==6)
            alpha=ImageChops.difference(im.getchannel("A"),patched.getchannel("A"))
            if i in SIGNS:
                alpha.paste(0,SIGNS[i][0])
            if i in COPY_TEXT_ALPHA:
                for rect,_ in ALIGNED[i]:
                    alpha.paste(0,rect)
            if im.tobytes()==patched.tobytes() or alpha.getbbox():
                raise ValueError(f"no effective edit or changed frame alpha at {i}")
            b[pos:end]=compress_exact(patched.tobytes("raw","BGRA"),end-pos,frame.raw[:2])
            changes.append((pos,end,i))
            previews.append((i,im,patched))
        pos=end
    if {j for _,_,j in changes}!=TARGETS or len(a)!=len(b):
        raise ValueError("unexpected footprint")
    verify_spans(a,b,changes)
    out.mkdir(parents=True)
    dest=out/PACK
    dest.write_bytes(b)
    if list(index(source).items())!=list(index(dest).items()):
        raise ValueError("NPK index changed")
    reopened=split_img(member(dest,MEMBER))
    if len(reopened)!=len(native) or any(f.meta!=g.meta or (f.raw==g.raw)!=(i not in TARGETS)
                                            for i,(f,g) in enumerate(zip(native,reopened))):
        raise ValueError("IMG frame table or selection changed")
    w=max(im.width for _,im,_ in previews)
    h=sum(im.height+22 for _,im,_ in previews)
    panel=Image.new("RGB",(2*w+14,h),"#827047")
    d=ImageDraw.Draw(panel)
    y=0
    for i,im,patch in previews:
        d.text((4,y+3),f"US original ({i})",fill="white")
        d.text((w+11,y+3),f"US localized ({i})",fill="white")
        panel.paste(im,(4,y+21),im)
        panel.paste(patch,(w+11,y+21),patch)
        y+=im.height+22
    panel.save(out/"comparison.png")
    core.atomic(out/"manifest.json",{"before":BASELINE,"after":hashlib.sha256(b).hexdigest(),
                "donor":DONOR,"spans":changes,"frames":sorted(TARGETS),"game_validated":False})
    print(f"Verified {len(changes)} equal-size map frame replacements")


def verify_spans(a,b,spans):
    pos=0
    if len(a)!=len(b) or {i for _,_,i in spans}!=TARGETS or len(spans)!=len(TARGETS):
        raise ValueError("incorrect map patch footprint")
    for x,y,_ in sorted(spans):
        if not pos<=x<y<=len(a) or a[pos:x]!=b[pos:x]:
            raise ValueError("bytes outside map frame spans changed")
        pos=y
    if a[pos:]!=b[pos:]:
        raise ValueError("map trailer changed")


def deploy(bundle,rollback):
    bundle=bundle.resolve()
    if bundle.is_relative_to(CLIENT.resolve()):
        raise ValueError("map bundle must be outside the client")
    m=core.read(bundle/"manifest.json")
    live,patch,bak=CLIENT/REL,bundle/PACK,bundle/"before.npk"
    wanted=BASELINE if rollback else m["after"]
    fonts=GameFonts(CLIENT/"Fonts")
    with fonts.locked():
        current=core.digest(live)
        if m["before"]!=BASELINE or current not in {BASELINE,m["after"]} or core.client_up():
            raise ValueError("client up or original map identity changed")
        if not rollback:
            if (fonts.hashes()!=OUTLINE_FONTS or core.digest(CLIENT/QUEST_REL)!=ACT
                    or core.digest(CLIENT/"dstr.dat")!=DSTR_R1
                    or core.digest(CLIENT/"Script.pvf") not in {PVF,EXPECTED_TRIAL}
                    or core.digest(patch)!=wanted):
                raise ValueError("current UI baseline or map candidate changed")
            if current==BASELINE and not bak.exists():
                core.file_copy(live,bak)
        if core.digest(bak)!=BASELINE:
            raise ValueError("map backup verification failed")
        if not rollback:
            verify_spans(bak.read_bytes(),patch.read_bytes(),m["spans"])
        if core.client_up() or core.digest(live)!=current:
            raise ValueError("client reopened or map changed")
        if current!=wanted:
            core.file_copy(bak if rollback else patch,live)
        if core.digest(live)!=wanted:
            raise ValueError("map install verification failed")
        core.atomic(bundle/"deployment.json",{"utc":datetime.now(timezone.utc).isoformat(),
                    "status":"rolled_back" if rollback else "installed_pending_game_check",
                    "file":str(live),"sha256":wanted})
        print(f"Verified live map pack: {wanted}")


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    cmd=p.add_subparsers(dest="action",required=True)
    q=cmd.add_parser("build")
    for name in ("source","donor","out"):
        q.add_argument("--"+name,type=Path,required=True)
    for action in ("install","rollback"):
        cmd.add_parser(action).add_argument("--bundle",type=Path,required=True)
    a=p.parse_args()
    if a.action=="build":
        build(a.source,a.donor,a.out)
    else:
        deploy(a.bundle,a.action=="rollback")
