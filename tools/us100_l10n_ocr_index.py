"""Map Windows OCR words back to NPK frames. OCR hits are not confirmation."""
import argparse
from collections import Counter, defaultdict
import csv
import json
from pathlib import Path
import re

from PIL import Image, ImageDraw


COMMON = frozenset("""
    accept achievement action active add advanced apply arena attack attribute avatar back battle board
    bonus buy cancel card castle change character choose claim close combat complete condition confirm
    contract create daily damage defense delete detail details dungeon effect element empty end enhance
    enter equipment event exchange exit experience fame fatigue favorite filter finish fire fixed
    friend game gift gold great group guild guide help heroic hidden high higher information inventory
    item jewel keyboard lamp leader leave left level life list loading local lock login legend legendary
    magic magical main map match material maximum member menu mileage mission monster move next normal
    notice open option options other party passive pets physical player point points power premium
    previous progress purchase quest raid ranking rate ready receive recommended record recovery
    reward rewards right save search select settings skill special speed start status step storage
    support system target time tip title total town trade training type unique unlock upgrade use
    value view warning weapon weekly welcome window world reset referral normal hard expert master
""".split())
NEUTRAL = frozenset("neo dfo dnf hp mp sp fp tp exp lv npc vip cera fps atk ok go on off esc auto".split())
SHORT = frozenset("accept arena avatar back buy cancel confirm create delete dungeon enter equipment event exit fatigue gift gold guild guide information inventory item level loading lock map menu mission notice option options party premium purchase quest raid ready receive reset reward save search select settings skill start status storage system town trade unlock upgrade weapon world".split())
WORD = re.compile(r"[A-Za-z]+")


def index(out):
    base=out/"atlas"
    sheets=json.loads((base/"atlas-sheets.json").read_bytes())
    ocr=json.loads((base/"ocr-summary.json").read_text(encoding="utf-8-sig"))
    candidates=[]
    tokens=Counter()
    for sheet,boxes in sheets.items():
        f=base/(sheet+".ocr.json")
        if not f.exists():
            continue
        data=json.loads(f.read_text(encoding="utf-8-sig"))
        if data["sheet"]!=sheet:
            raise ValueError("OCR sidecar belongs to a different sheet")
        lines_by_box=defaultdict(list)
        for line in data["lines"]:
            matches=defaultdict(list)
            for word in line:
                x=word["x"]+word["w"]*.5
                y=word["y"]+word["h"]*.5
                for i,b in enumerate(boxes):
                    x0,y0,x1,y1=b["box"]
                    if x0<=x<x1 and y0<=y<y1:
                        matches[i].append(word["text"])
                        break
            for i,v in matches.items():
                lines_by_box[i].append(" ".join(v))
        for i,v in lines_by_box.items():
            txt=" / ".join(v)
            words=[x.casefold() for x in WORD.findall(txt)]
            relevant=[x for x in words if x not in NEUTRAL and len(x)>=3]
            exact=sum(x in COMMON for x in relevant)
            if not exact or (exact==1 and not any(x in SHORT for x in relevant)):
                continue
            tokens.update(x for x in relevant if x in COMMON)
            score=2*exact+len(set(x for x in relevant if x in COMMON))-max(0,len(relevant)-exact)
            candidates.append(dict(boxes[i],sheet=sheet,ocr=txt,ocr_score=score))
    candidates.sort(key=lambda c:(-c["ocr_score"],c["npk"],c["img"],c["frame"]))
    with (out/"npk-ocr-candidates.tsv").open("w",encoding="utf-8-sig",newline="") as f:
        w=csv.writer(f,delimiter="\t")
        w.writerow(("npk","img","frame","ocr_score","ocr_text","sheet","box"))
        for c in candidates:
            w.writerow((c["npk"],c["img"],c["frame"],c["ocr_score"],c["ocr"],c["sheet"],",".join(map(str,c["box"]))))
    for start in range(0,min(len(candidates),160),40):
        picked=candidates[start:start+40]
        page=Image.new("RGB",(1280,1780),"#17202b")
        d=ImageDraw.Draw(page)
        open_sheets={}
        for n,c in enumerate(picked):
            sheet=c["sheet"]
            if sheet not in open_sheets:open_sheets[sheet]=Image.open(base/(sheet+".png"))
            tile=open_sheets[sheet].crop(c["box"])
            tile.thumbnail((300,150),Image.Resampling.LANCZOS)
            x=6+n%4*320;y=4+n//4*178
            d.text((x,y),f"#{start+n+1:03d} {c['npk'][7:45]}:{c['frame']}",fill="white")
            page.paste(tile,(x,y+18))
        for im in open_sheets.values(): im.close()
        page.save(out/f"ocr-review-{start//40+1:02d}.png")
    summary={"processed_sheets":ocr["completed_sheets"],"failed_sheets":len(ocr["errors"]),
             "ocr_candidate_frames":len(candidates),"ocr_candidate_packs":len({c["npk"] for c in candidates}),
             "frequent_words":tokens.most_common(20),"ocr_candidates_are_not_verified_screens":True}
    (out/"ocr-index-summary.json").write_text(json.dumps(summary,ensure_ascii=True,indent=2),encoding="utf-8")
    print(json.dumps(summary,ensure_ascii=True))


if __name__=="__main__":
    ap=argparse.ArgumentParser(description=__doc__);ap.add_argument("--out",type=Path,required=True)
    index(ap.parse_args().out)
