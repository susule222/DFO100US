"""Reuse same-record Chinese display text while preserving the native DSTR controls."""
import argparse
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import sys

from localize_us100_tables import FORMAT_MARKER, HAN, NUMBER, read_rows
from us100_avatar_install import staged_copy
from us100_l10n_census import classify, PENDING
from us100_local_llm import validate, mechanical_signs
from us100_status_dstr import CLIENT, EXE, REL, GameFonts, ROW, core, key_pair, pack, unpack

ROOT=core.ROOT/"runtime"
BASE="c04de394e8e8e7f3bfbb83e969896d4dd3bcda11af007793997a03a4e6440a8e"
SOURCE=ROOT/"localize-us100-audit/us-tables/string/dnf.uv.str"
REFERENCE=ROOT/"us100-l10n-census-20261125/pvf-tables/string/dnf.uv.str"
OVERLAY=ROOT/"localize-us100-mt/overlay-gate-r23.json"
MANUAL=Path(__file__).with_name("us100_full_dstr_review.json")
GUARD={
    "DFO.exe":EXE,
    "Script.pvf":"6191877bca4012fe912afc92243ab98f606c82b49aeca7340e509b6123445b05",
    REL.as_posix():"ef224c2064c8c156119f92c303a887a9db8922b3d766f7f857e9f55bdcc77c0b",
    "ImagePacks2/sprite_interface2_hud.NPK":"fbec688d508b005e681553d524cbcc468bd07821984dcb689a1f29253a02e385",
    "ImagePacks2/sprite_common_etc.NPK":"f9b79723afbf313148bec9c83846736e609b35bb8a47653ea54c5a1f2d2107ac",
}

PRINTF=re.compile(
    r"%%|%[ +#0-]+(?:\d+|\*)?(?:\.(?:\d+|\*))?(?:I32|I64|hh|ll|[hlLzjt])?[diuoxXfFeEgGaAcCsSpPnA](?![A-Za-z])"
    r"|%(?:\d+\$|\([\w.]+\))?[-+#0]*(?:\d+|\*(?:\d+\$)?)?"
    r"(?:\.(?:\d+|\*(?:\d+\$)?))?(?:I32|I64|hh|ll|[hlLzjt])?[diuoxXfFeEgGaAcCsSpPnA]"
    r"|%[1-9]\d*|%"
)
ESCAPES=re.compile(r"\\(?:br|[nrt]|x[0-9a-fA-F]{2}|u[0-9a-fA-F]{4})")
URL=re.compile(r"(?i)(?:https?[:/]*/+|www\.)[^\s<>\[\]]+")
PATH=re.compile(r"(?i)(?<![A-Za-z0-9_.])(?:[A-Za-z]:)?(?:[A-Za-z0-9_.-]+[/\\])*[A-Za-z0-9_.-]*\.(?:npk|pvf|img|ani|ogg|wav|dds|tga|png|xml|nut|lst|txt|dll|exe|etc|obj|act|jpg|bmp|ttf|ttc)(?![A-Za-z0-9])")
COMMAND=re.compile(r"(?:(?<!\S)|(?<=\\n))/[a-z][a-z0-9_\-]*(?![\w/-])")
COMMAND_ALIAS=re.compile(r"(?:(?<!\S)|(?<=\\n))/(?:Arena Friends|Alone|Friends|Solo)(?![A-Za-z0-9_])")
KEY=re.compile(r"(?<![A-Za-z0-9])(?:F(?:1[0-2]|[1-9])|CTRL|ALT|(?<!Phase )(?<!Elemental )SHIFT|ESC|SPACE ?BAR|(?<=Press )SPACE|空格键|XInput|DirectInput)(?![A-Za-z0-9])",re.I)
IDENTIFIER=re.compile(
    r"(?i)^[a-z][a-z0-9]*_[a-z0-9_.]+$|^[a-z][a-z0-9]*(?:\.[a-z]+){2,}$|^[a-z0-9_.-]+(?:[/\\][a-z0-9_.-]+)+$"
)
DEBUG=re.compile(
    r"^\s*(?://|(?:\[(?:GM|DEBUG|DEV|TEST)\])|(?:debug|dummy|placeholder)\b|"
    r"\[(?:error|warn|info|log)\]\s*(?:0x|ASSERT|\w+::)|"
    r"(?:test|sample)(?:[ _-]?(?:text|string|data|quest|value|button))?[0-9_ -]*)\s*$",
    re.I)
INTERNAL_KEYS=frozenset(("Dotum","Gulim","Gasinamu","Arial","Calibri","Tahoma","Verdana",
                         "MS Gothic","Microsoft Sans Serif", "UTF-8","ESCAPE","null","NULL","NoneType",
                         "NA","N/A","n/a","GSP","gsp","OK_CANCEL"))
KEEP_ACRONYMS=frozenset(("ID","UI","PC","HP","MP","TP","SP","AP","FP","EP","BP","QP","EU","US","KR","JP","CN","EXP",
                        "DFO","DNF","NPC","VIP","CERA","UTC","EST","PST","BGM","FPS","RGB","SS","SSS","AOS",
                        "DPS","PVP","PVE","AFK","OTP","IRC","NA","MVP","SFX","HPMP","PNG","JPG","GIF",
                        "GB","MB","KB","HTTP","HTTPS","OS","GPU","CPU","D3D","DX","LCD","LED","USB","RAM"))
LITERAL_INPUT=re.compile(r"""(?i)\b(?:type|input|enter)\s+["“]([^"”]+)["”]""")
VISIBLE_MODE=re.compile(r"<(?:I Want To Be Alone|I Only Want To See Friends|免打扰|仅显示好友)>",re.I)
TERMS={
    "i want to be alone":"免打扰", "i only want to see friends":"仅显示好友",
    "only see friends":"仅显示好友",
    "pc cafe":"网吧", "pc café":"网吧", "burning time":"燃烧时间",
    "aganzo":"阿甘左", "arad":"阿拉德", "kiri":"凯丽", "correggio":"克雷泽",
    "goblin":"哥布林", "pandemonium":"魔界", "otherverse":"异界",
    "ispins":"伊斯大陆", "aeterna":"永恒大陆", "sirocco":"希洛克", "isys":"伊希斯",
    "anton":"安徒恩", "tayberrs":"泰波尔斯", "azalea":"艾泽拉",
    "seria":"赛丽亚", "roxy":"卢克西", "nex":"奈克斯", "vita":"维塔",
    "delilah":"德利拉", "hilder":"赫尔德", "ozma":"奥兹玛", "bakal":"巴卡尔",
    "siroco":"希洛克", "terranium":"时空石", "omikuji":"御神签",
    "player ranking":"玩家排名", "pop quiz":"突击问答", "chaser":"炫纹", "chasers":"炫纹",
    "character name":"角色名", "explorer club name":"冒险团名称",
    "message":"消息", "guild":"公会",
}
TERMS_RE=re.compile(r"(?<![A-Za-z0-9_/-])(?:"+
                    "|".join(re.escape(s) for s in sorted(TERMS,key=len,reverse=True))+
                    r")(?![A-Za-z0-9_./-])",re.I)


def digest(b):
    return hashlib.sha256(b).hexdigest()


def blank_envelope(s):
    v=s.strip()
    return s[:len(s)-len(s.lstrip())],v,s[len(s.rstrip()):]


def technical(s):
    s=s.strip()
    if not s or s in INTERNAL_KEYS or DEBUG.search(s) or re.match(
        r"(?i)^(?://|\[(?:GM|DEBUG|QA|DEV|TEST|Compare Var)\]|GM mode\s*[:：]|debug[ :]|dummy[ :])",s
    ):return "technical_token"
    t=PRINTF.sub("",s).strip(" .,:-+/\\" )
    if t in INTERNAL_KEYS or (t.upper() in KEEP_ACRONYMS and t.isupper()):return "standard_label"
    if (COMMAND.fullmatch(s) or COMMAND_ALIAS.fullmatch(s) or URL.fullmatch(s)
        or PATH.fullmatch(s) or IDENTIFIER.fullmatch(s)
        or (s.startswith("\\") and not s.startswith(("\\n","\\r","\\t","\\br")))):
        return "command_or_resource"
    if re.fullmatch(r"([A-Z])\1{1,4}|[A-Z]{1,2}(?:-\d+)?",s) and s not in ("NO","OK","ON","UP","GO"):
        return "control_or_grade"
    if not re.search(r"[A-Za-z\u3040-\u30ff\u3400-\u9fff\uac00-\ud7af]",FORMAT_MARKER.sub("",s)):
        return "format_or_empty"
    return ""


def signature(s):
    markup=FORMAT_MARKER.findall(VISIBLE_MODE.sub("mode",s))
    return (tuple(PRINTF.findall(s)),tuple(x for x in markup if not x.startswith("%")),tuple(ESCAPES.findall(s)),
            tuple(URL.findall(s)),tuple(PATH.findall(s)),tuple(COMMAND.findall(s))+tuple(COMMAND_ALIAS.findall(s)),
            tuple("SPACEBAR" if k=="空格键" or k.upper().startswith("SPACE") else k.upper()
                  for k in KEY.findall(ESCAPES.sub(" ",s))),
            tuple(NUMBER.findall(s)),tuple(mechanical_signs(s)),
            s.count("@"),s.count("|"),s.count("{"),s.count("}"),s.replace("\\'","'").count("\\"))


def valid(old,new):
    if not new or old==new:return "unchanged"
    if not HAN.search(new):return "no_chinese"
    if signature(old)!=signature(new):return "protected_control_or_number_differs"
    if any(token not in new for token in LITERAL_INPUT.findall(old)):
        return "typed_literal_changed"
    # Normalize recognized printf specifiers for the reused quality gate, not
    # for the file. This avoids rejecting native forms such as "%sto join".
    a,b=(PRINTF.sub(lambda m:"%" if m[0]=="%" else "%s ",VISIBLE_MODE.sub("模式",x.strip()))
         for x in (old,new))
    return validate(a,b)


def refine(text):
    # Match display terminology only from a fixed review list. Even this
    # change must pass the full placeholder, command, and literal gate.
    return TERMS_RE.sub(lambda m:TERMS[m[0].lower()],text)


def source_texts(raw):
    text=raw.decode("utf-8")
    rows={m[1]:m[2] for m in ROW.finditer(text)}
    if len(rows)!=sum(1 for _ in ROW.finditer(text)):
        raise ValueError("duplicate native DSTR ID")
    return text,rows


def make_plan(raw):
    _,current=source_texts(raw)
    en,zh=read_rows(SOURCE),read_rows(REFERENCE)
    if len(current)!=26296 or current.keys()!=en.keys() or en.keys()!=zh.keys():
        raise ValueError("reference does not share the exact US100 keyspace")
    overlay=core.read(OVERLAY)
    manual=core.read(MANUAL) if MANUAL.is_file() else {}
    bad_manual=set(manual)-set(current)
    if bad_manual:raise ValueError(f"unexpected review IDs {bad_manual}")
    unresolved=[]
    skipped=[]
    changed={}
    counts=Counter()
    for key,value in current.items():
        lead,trim,tail=blank_envelope(value)
        status=classify(value)
        if key in manual:
            reviewed=manual[key]
            if reviewed["old"]!=value:raise ValueError("review baseline differs: "+key)
            if reviewed.get("decision")=="keep":
                skipped.append([key,reviewed["reason"],value])
                counts["explicitly_preserved"]+=1
                continue
            new=reviewed["new"]
            if err:=valid(value,new):raise ValueError(f"manual row {key}: {err}")
            changed[key]=(value,new,"review")
            counts["review"]+=1
            continue
        if status not in PENDING or HAN.search(trim):
            counts["existing_chinese" if HAN.search(trim) else "neutral_or_internal"]+=1
            continue
        if held:=technical(trim):
            skipped.append([key,held,value])
            counts[held]+=1
            continue
        if en.get(key)!=trim:
            unresolved.append([key,"source_not_exact",value,None])
            continue
        via=[]
        for name,candidate in (("same_us100_id",zh[key]),("reuse_exact",overlay.get(trim))):
            if not candidate:continue
            new=lead+candidate.strip()+tail
            if not (err:=valid(value,new)):
                clean=refine(new)
                if clean!=new and not valid(value,clean):
                    new=clean
                    counts["terminology_refined"]+=1
                changed[key]=(value,new,name)
                counts[name]+=1
                break
            via.append((name,err,candidate))
        else:
            unresolved.append([key,via,value,zh[key]])
    return changed,unresolved,skipped,counts


def state():
    k=key_pair(CLIENT/"DFO.exe")
    blob=(CLIENT/"dstr.dat").read_bytes()
    raw=unpack(blob,k)
    if digest(blob)!=BASE or pack(raw,k)!=blob:
        raise ValueError("read-only source must be the current verified 100US DSTR")
    return blob,raw,k


def export(out):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()):raise ValueError("offline output only")
    out.mkdir(parents=True,exist_ok=True)
    _,raw,_=state()
    changed,unresolved,skipped,counts=make_plan(raw)
    core.atomic(out/"census.json",dict(counts)|{"changed":len(changed),"unresolved":len(unresolved),
        "total":26296,"player_facing_coverage_is_static":True})
    core.atomic(out/"unresolved.json",unresolved)
    core.atomic(out/"preserved-technical.json",skipped)
    print(dict(counts),f"changed={len(changed)}, unresolved={len(unresolved)}")


def substitute(data,edits):
    text,rows=source_texts(data)
    if any(rows.get(k)!=v[0] for k,v in edits.items()):
        raise ValueError("native DSTR rows changed")
    return ROW.sub(lambda m:m[1]+">"+edits[m[1]][1] if m[1] in edits else m[0],text).encode("utf-8")


def build(out):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()):raise ValueError("offline output only")
    out.mkdir(parents=True,exist_ok=True)
    orig,raw,keys=state()
    d,u,p,c=make_plan(raw)
    if u:raise ValueError(f"resolve or classify {len(u)} candidate rows before building")
    new=substitute(raw,d)
    if substitute(new,{k:(b,a) for k,(a,b,_) in d.items()})!=raw:
        raise ValueError("DSTR scope excludes bytes outside the selected value fields")
    encoded=pack(new,keys)
    if unpack(encoded,keys)!=new or pack(unpack(encoded,keys),keys)!=encoded:
        raise ValueError("native round-trip failed")
    (out/"before-dstr.dat").write_bytes(orig)
    (out/"dstr.dat").write_bytes(encoded)
    core.atomic(out/"DSTR.diff.json",{"before":BASE,"after":digest(encoded),"stats":dict(c),
        "rows":len(d),"references":{str(p):core.digest(p) for p in (SOURCE,REFERENCE,OVERLAY,MANUAL)},
        "game_checked":False})
    print(f"{len(d)} values / {digest(encoded)}")


def deploy(out):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()):raise ValueError("offline output only")
    m=core.read(out/"DSTR.diff.json")
    original,raw,k=state()
    result=(out/"dstr.dat").read_bytes()
    d,u,_,_=make_plan(raw)
    if (u or m["before"]!=BASE or len(d)!=m["rows"] or m["after"]!=digest(result)
        or core.digest(out/"before-dstr.dat")!=BASE or substitute(raw,d)!=unpack(result,k)):
        raise ValueError("reapplied candidate differs")
    for file,h in m["references"].items():
        if core.digest(Path(file))!=h:raise ValueError("reference changed since build")
    fonts=CLIENT/"Fonts"
    live=CLIENT/"dstr.dat"
    with GameFonts(fonts).locked():
        keep={str(CLIENT/p):h for p,h in GUARD.items()}
        keep.update({str(f):core.digest(f) for f in fonts.iterdir() if f.is_file()})
        if core.client_up() or core.digest(live)!=BASE or any(core.digest(Path(p))!=h for p,h in keep.items()):
            raise ValueError("unexpected active client state")
        temp=staged_copy(out/"dstr.dat",live.parent,m["after"])
        try:
            if core.client_up() or core.digest(live)!=BASE:raise ValueError("DFO reopened or baseline changed")
            os.replace(temp,live)
        finally:
            temp.unlink(missing_ok=True)
        if core.digest(live)!=m["after"] or any(core.digest(Path(p))!=h for p,h in keep.items()):
            raise ValueError("post-deployment fingerprint failed")
        core.atomic(out/"deployment.json",dict(m,utc=datetime.now(timezone.utc).isoformat(),
            file=str(live),changed_only="dstr.dat",preserved=keep))
        print(f"Installed {live} {m['after']}")


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("action",choices=("audit","build","install"))
    p.add_argument("--out",required=True,type=Path)
    args=p.parse_args()
    {"audit":export,"build":build,"install":deploy}[args.action](args.out)
