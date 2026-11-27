"""Index all installed NPKs and prepare a bounded UI texture OCR sample."""
import argparse
from collections import Counter
import csv
import hashlib
import json
from pathlib import Path
import re
import struct

from PIL import Image
from atelier.sprites import index, _pixels
from us100_dstr_ui import CLIENT


UI = re.compile(r"^sprite_(?:interface(?:2)?(?:[_.]|$)|worldmap(?:[_.]|$)|commonmap(?:[_.]|$)|internationalimage(?:[_.]|$))", re.I)
NAMED = re.compile(r"(?:text|label|title|caption|letter|font|signboard|sign[_./]|nametag|btn|button|menu|banner|tooltip|message|notice|loading)", re.I)
CORE = re.compile(r"quest|itemshop|cashshop|cera|premium|character(create|select)|selectcharacter|status|inventory|fatigue|hud|worldmap|townmap|nowloading|characterframe|commonbutton|dialog|setting|systemmenu",re.I)
NOISE = re.compile(r"particle|effect|shining|particle|glow|icon|cutin|skeleton|illustration|flash|avatar", re.I)
MAP = re.compile(r"signboard|signpost|nametag|mapname|townname",re.I)


def atlas_frames(f,off,span):
    f.seek(off)
    h=f.read(32)
    if len(h)!=32 or h[:16]!=b"Neople Img File\0":
        return "non_img",None
    total,_,v,n=struct.unpack_from("<4I",h,16)
    if not 0<n<=65536 or not 8*n<=total<=36*n or total>span-32:
        return f"invalid_{v}",None
    pal=None
    if v==5:
        if span<44 or struct.unpack("<3I",f.read(12))!=(0,span,0):
            return "v5_dds",None
    elif v in (4,6):
        np=1 if v==4 else struct.unpack("<I",f.read(4))[0]
        if not 0<np<=512:return f"unsupported_v{v}_palette",None
        for i in range(np):
            ln=struct.unpack("<I",f.read(4))[0]
            if not 0<=ln<=256:return f"invalid_v{v}_palette",None
            p=f.read(ln*4)
            if i==0:pal=p
    elif v!=2:
        return f"v{v}_unsupported",None
    body=f.tell()+total
    if body>off+span:return f"invalid_{v}",None
    table=f.read(total)
    at=0
    rows=[]
    for i in range(n):
        if at+8>total:return f"invalid_table_v{v}",None
        k,c=struct.unpack_from("<II",table,at)
        if k==17:
            at+=8;continue
        if k not in (14,15,16) or at+36>total:
            return f"unsupported_frame_v{v}",None
        w,h,size=struct.unpack_from("<III",table,at+8)
        if body+size>off+span:return f"out_of_bounds_v{v}",None
        if c in (5,6) and 24<=w<=2560 and 8<=h<=1920 and w*h<=3500000:
            rows.append((i,k,c,w,h,body,size,pal))
        body+=size;at+=36
    return f"v{v}",rows


def save_sheet(out,num,tiles):
    sheet=Image.new("RGB",(2160,2304),(22,22,28))
    x=y=16; bottom=0; slots=[]
    remains=[]
    for im,info in tiles:
        if x+im.width+16>sheet.width:
            x=16;y+=bottom+23;bottom=0
        if y+im.height+16>sheet.height:
            remains.append((im,info));continue
        sheet.paste(im,(x,y))
        slots.append(dict(info,box=[x,y,x+im.width,y+im.height]))
        x+=im.width+25;bottom=max(bottom,im.height)
    if not slots:
        raise ValueError("OCR sheet did not accept a tile")
    name=f"sheet-{num:03d}"
    sheet.crop((0,0,sheet.width,y+bottom+16)).save(out/(name+".png"))
    return slots,remains


def prepare(out, budget):
    packs=sorted((CLIENT/"ImagePacks2").glob("*.npk"))
    out.mkdir(parents=True,exist_ok=False)
    files=[]; census=Counter(); formats=Counter(); work=[]; failures=[]
    maps=set()
    for p in packs:
        try:r=index(p)
        except (OSError,ValueError,struct.error) as ex:
            failures.append([p.name,str(ex)]);continue
        census["packs_indexed"]+=1;census["members_indexed"]+=len(r)
        st=p.stat(); files.append([p.name,st.st_size,st.st_mtime_ns,len(r)])
        ui=bool(UI.search(p.name))
        if ui:census["ui_packs_indexed"]+=1
        picks=[(n,o,s) for n,(o,s) in r.items() if (ui or MAP.search(n))
               and n.endswith(".img") and (NAMED.search(n) or NAMED.search(p.name) or CORE.search(n+" "+p.name) or MAP.search(n))]
        if not picks:continue
        with p.open("rb") as f:
            for n,o,s in picks:
                try:
                    ver,frames=atlas_frames(f,o,s)
                except (OSError,ValueError,struct.error) as ex:
                    ver,frames="read_failure",None
                formats[ver]+=1
                if not frames:continue
                named=bool(NAMED.search(n)); core=bool(CORE.search(n+" "+p.name))
                if core: maps.add(p.name)
                for i,k,c,w,h,o2,s2,palette in frames:
                    # Avoid portraits and glow frames unless the member explicitly labels text.
                    score=5*core+4*named+2*bool(NAMED.search(p.name))+3*bool(MAP.search(n))
                    if w/h>=2.4:score+=3
                    if h<9 or (NOISE.search(n) and not named):score-=5
                    if s2<200:score-=3
                    work.append((score,i,p,n,(i,k,c,w,h,o2,s2,palette)))
    seen=set(); tiles=[]; sheets={}; seq=0
    # Spread the sample over more members: inspect early frames first; when a
    # label series has enough score, later distinct frames are also included.
    work.sort(key=lambda v:(-(v[0]-(0 if v[1]==0 else min(6,v[1]//4+1))),v[1],v[2].name,v[3]))
    picked_packs=set(); picked_members=set(); max_per_member=30
    members=Counter()
    for score,i,p,n,item in work:
        if len(seen)>=budget:break
        if score<4 or members[(p.name,n)]>=max_per_member:continue
        _,k,c,w,h,offset,size,palette=item
        try:
            with p.open("rb") as f:
                f.seek(offset)
                im=_pixels(k,c,w,h,f.read(size),palette)
        except (OSError,ValueError,struct.error):
            census["frame_decode_errors"]+=1;continue
        sig=hashlib.sha1(struct.pack("<II",w,h)+im.tobytes()).hexdigest()
        if sig in seen:
            census["duplicate_pixels_skipped"]+=1;continue
        seen.add(sig); members[(p.name,n)]+=1
        bg=Image.new("RGBA",im.size,(38,38,46,255));bg.alpha_composite(im)
        scale=min(3,780/w,600/h)
        if scale>1 and h>=50:scale=min(scale,2)
        tile=bg.convert("RGB").resize((max(1,round(w*scale)),max(1,round(h*scale))),Image.Resampling.LANCZOS)
        tiles.append((tile,{"npk":p.name,"img":n,"frame":i,"native":[w,h],"score":score}))
        picked_packs.add(p.name);picked_members.add((p.name,n))
        if len(tiles)==128:
            while len(tiles)>28:
                slots,tiles=save_sheet(out,seq,tiles)
                sheets[f"sheet-{seq:03d}"]=slots;seq+=1
    while tiles:
        slots,tiles=save_sheet(out,seq,tiles)
        sheets[f"sheet-{seq:03d}"]=slots;seq+=1
    stat={"packs_found":len(packs),"index":census,"unsupported_or_supported_img_members":formats,
          "eligible_frame_records":len(work),"sampled_unique_frames":len(seen),"sampled_packs":len(picked_packs),
          "sampled_members":len(picked_members),"ocr_sheets":len(sheets),"budget":budget,"index_failures":failures,
          "coverage":"metadata inventory for all installed packs; prioritized OCR sample, not a full raster scan"}
    for name,val in (("atlas-summary.json",stat),("atlas-sheets.json",sheets)):
        (out/name).write_text(json.dumps(val,ensure_ascii=True,indent=2),encoding="utf-8")
    with (out/"npk-inventory.tsv").open("w",encoding="utf-8-sig",newline="") as f:
        w=csv.writer(f,delimiter="\t");w.writerow(("npk","bytes","mtime_ns","members"));w.writerows(files)
    print(json.dumps(stat,ensure_ascii=True))


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("--out",type=Path,required=True)
    p.add_argument("--budget",type=int,default=2200)
    a=p.parse_args()
    prepare(a.out,a.budget)
