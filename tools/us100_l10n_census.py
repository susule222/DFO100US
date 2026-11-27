"""Read-only localization census for the installed 100US DFO text layers."""
import argparse
from collections import Counter, defaultdict
import csv
import hashlib
import json
from pathlib import Path
import re
import struct

from inspect_pe import PE
from localize_us100_tables import read_rows
from us100_aux_text_audit import inspect as inspect_aux
from us100_dstr_ui import CLIENT, ROW, key_pair, pack, unpack
from us100_xui_text import decoded, extract_slots, rewrite_reason


FORMAT = re.compile(r"%(?:%|(?:\d+\$)?[-+ #0]*(?:\d+|\*)?(?:\.(?:\d+|\*))?(?:hh|h|I64|I32|ll|l|z|t|L)?[aAcdefgiopsuxEFGXS])")
REFERENCE = re.compile(r"<(\d+)::([^>]+)>")
MARKUP = re.compile(r"<\d+:{1,2}[^>]+>|</?(?:font|color|br|img|a)(?:\s[^>]*)?>|&[a-z0-9#]+;", re.I)
LATIN = re.compile(r"[a-zA-Z]+")
HAN = re.compile(r"[\u3400-\u4dbf\u4e00-\u9fff]")
HANGUL = re.compile(r"[\u1100-\u11ff\u3130-\u318f\uac00-\ud7a3]")
KANA = re.compile(r"[\u3041-\u30fa\uff66-\uff9f]")
NEUTRAL = frozenset("hp mp sp tp fp ap bp qp exp xp lv utc am pm pst pdt est edt fps kg g hz mb gb ms alt ctrl shift esc tab pvp pve npc vip pc ui hpmp dfo dnf neo cera pve rgb k o x w a s d q e r f g t z c v b m f1 f2 f3 f4 f5 f6 f7 f8 f9 f10 f11 f12 xinput directx".split())
KEYS = re.compile(r"(?<![a-zA-Z])F\d{1,2}(?![a-zA-Z])", re.I)
OPAQUE = re.compile(r"""
    (?:https?://|www\.)\S+ |
    \S+\.(?:npk|img|ogg|wav|ani|dds|tga|png|xml|nut|lst|txt|xui|dll|exe|etc|obj|act|jpg|bmp) |
    [\w.-]+(?:[\\/][\w.-]+){2,} |
    (?:[a-zA-Z0-9]+_){1,}[a-zA-Z0-9_.]+ |
    0x[0-9a-fA-F]+ | (?:[A-Z]|[a-z])+\d+(?:_\d+)*
""", re.X)
DEBUG = re.compile(r"^(?://|debug\b|dummy\b|(?:sample|test)(?:\s*(?:test|text|quest|skill|item|button|str))?\s*[\d_-]*$)|(?:\.cpp\b|^\w+::\w+)", re.I)
CATEGORY = [(name,re.compile(regex,re.I)) for name,regex in (
    ("状态栏/疲劳/引导", r"fatigue|play point|play gauge|stamina|mini.m[ap]|quick slot|tutorial"),
    ("商城/礼包/会员", r"shop|purchase|checkout|receive|reward|coupon|cart|premium|contract|mileage|gift"),
    ("技能/操作", r"skill|casting|cooldown|hotkey|command|press|mouse|control|click|keybind"),
    ("物品/装备/词典", r"item|equipment|inventory|reinforc|amplif|disassembl|rare|rarity|avatar|socket|tooltip|enchant|synergy"),
    ("任务/地图/副本", r"quest|mission|map|dungeon|town|village|raid|channel|guide|region|stage"),
    ("系统/角色/设置", r"error|failed|setting|login|log.in|charact|party|guild|network|display|screen|window|server|menu|resolution|notice|sound"),
)]
LAYOUT_DEBT = frozenset(("STR:STR:","STR:","N/A","n/a"))
MATCH_IDS = {193,25144,100088588}
SUGGEST = {193:"疲劳值：%d/%d",25144:"疲劳值：%d/%d",100088588:"游戏点数：%d/%d"}


def sha(path):
    with path.open("rb") as f:
        return hashlib.file_digest(f,"sha256").hexdigest()


def clean(s):
    s=MARKUP.sub("",FORMAT.sub("",s))
    return s.replace(r"\n"," ").replace(r"\r"," ").replace(r"\t"," ").strip()


def classify(s):
    if not s or s in LAYOUT_DEBT:
        return "empty_or_token"
    t=clean(s)
    if DEBUG.search(t) or OPAQUE.fullmatch(t):
        return "debug_or_identifier"
    if HANGUL.search(t) or KANA.search(t):
        return "ko_ja_or_mixed"
    words=LATIN.findall(KEYS.sub(" ",t))
    relevant=[w for w in words if len(w)>=2 and w.casefold() not in NEUTRAL]
    if relevant:
        return "mixed_latin_han" if HAN.search(t) else "english_like"
    if HAN.search(t):
        return "han_no_substantial_latin"
    return "empty_or_token"


PENDING = frozenset(("english_like","mixed_latin_han","ko_ja_or_mixed"))


def cluster(s):
    return next((name for name,r in CATEGORY if r.search(s)),"其他文案")


def serial(path,obj):
    path.write_text(json.dumps(obj,ensure_ascii=True,indent=2)+"\n",encoding="utf-8")


def refs_and_literals(exe, ids, out):
    pe=PE(exe)
    try:
        _,va,_,start,size=next(row for row in pe.sections if row[0]==".text")
        _,rva,_,ra,rs=next(row for row in pe.sections if row[0]==".rdata")
        reg=re.compile(rb'\xb9(.{4})\xe8(.{4})',re.S)
        lea=re.compile(rb'[\x48-\x4f]\x8d[\x05\x0d\x15\x1d\x25\x2d\x35\x3d](.{4})',re.S)
        lookups=defaultdict(list)
        for m in reg.finditer(pe.data,start,start+size):
            k,d=struct.unpack("<Ii",m[1]+m[2])
            here=m.start()-start+va
            if k in ids and here+10+d==0x523ffa0:
                lookups[k].append(here)
        locations={}
        for m in lea.finditer(pe.data,start,start+size):
            loc=m.start()-start+va
            dst=loc+7+struct.unpack("<i",m[1])[0]
            if rva<=dst<rva+rs:
                row=locations.setdefault(dst,[loc,0])
                row[1]+=1
        literal_counts=Counter()
        accepted=0
        with (out/"exe-candidates.tsv").open("w",encoding="utf-8-sig",newline="") as f:
            w=csv.writer(f,delimiter="\t")
            w.writerow(("data_rva","first_possible_lea_rva","refs","class","category","value"))
            for dst,(loc,count) in sorted(locations.items()):
                val=pe.literal(dst)
                if not val or len(val)<2 or "\ufffd" in val or len(val)>400:
                    continue
                accepted+=1
                c=classify(val)
                literal_counts[c]+=1
                if c in PENDING:
                    w.writerow((hex(dst),hex(loc),count,c,cluster(val),ascii(val)[1:-1] if "\n" in val else val))
        return lookups, {"candidate_lea_targets_in_rdata":len(locations),"decoded_printable_literals":accepted,
                         "by_class":literal_counts,"scope":"RIP LEA + known local literal decoder; debug/common-library strings not proven visible"}
    finally:
        pe.close()


def census(out, inner, manifest):
    exe,dstr,pvf=[CLIENT/w for w in ("DFO.exe","dstr.dat","Script.pvf")]
    report={"source":str(CLIENT),"client_sha256":{p.name:sha(p) for p in (exe,dstr,pvf)}}
    m=json.loads(manifest.read_bytes())
    inner_hash=sha(inner)
    if (m["candidate_sha256"],m["candidate_inner_sha256"])!=(report["client_sha256"]["Script.pvf"],inner_hash):
        raise ValueError("PVF exports do not match the installed manifest")
    report["inner_sha256"]=inner_hash
    keys=key_pair(exe)
    blob=dstr.read_bytes()
    raw=unpack(blob,keys)
    if pack(raw,keys)!=blob:
        raise ValueError("DSTR native round trip mismatch")
    text=raw.decode("utf-8")
    records=[(int(m[1]),m[2]) for m in ROW.finditer(text)]
    ids={k for k,_ in records}
    lookups,report["exe"]=refs_and_literals(exe,ids,out)
    dstr_counts=Counter(); categories=Counter(); short=set(); texts=set(); shot=[]
    short_ids=set()
    with (out/"dstr-candidates.tsv").open("w",encoding="utf-8-sig",newline="") as f:
        w=csv.writer(f,delimiter="\t");w.writerow(("id","class","category","short_direct_lookup","refs","current_text","suggested"))
        for k,val in records:
            cl=classify(val);dstr_counts[cl]+=1
            if cl not in PENDING:continue
            texts.add(val.strip())
            direct=lookups.get(k,[])
            category=cluster(val)
            is_short=bool(direct and not re.search(r'[\r\n]|\\[nr]',val) and 0<len(val.strip())<=72)
            if is_short:
                categories[category]+=1;short.add(val.strip());short_ids.add(k)
            if k in MATCH_IDS:
                shot.append({"id":k,"source":val,"target":SUGGEST[k],"direct_lookup_rvas":[hex(a) for a in direct]})
            w.writerow((k,cl,category,int(is_short),len(direct),val,SUGGEST.get(k,"")))
    report["dstr"]={"rows":len(records),"unique_ids":len(ids),"by_class":dstr_counts,"distinct_candidate_texts":len(texts),
                    "short_rows_with_direct_lookup":len(short_ids),"distinct_short_texts_with_direct_lookup":len(short),
                    "short_row_categories":categories,"screenshot_keys":shot}

    p=out/"pvf-tables"
    ref_data=json.loads((out/"pvf-references.json").read_bytes())
    indexes={v.lower():int(i) for i,v in re.findall(r"(\d+) `([^`]+)`",(out/"pvf-string-index-r2"/"entry.txt").read_text(encoding="utf-8"))}
    static=ref_data["references"];tcounts={}; refs_ct=Counter(); totals=Counter()
    with (out/"pvf-candidates.tsv").open("w",encoding="utf-8-sig",newline="") as f:
        w=csv.writer(f,delimiter="\t");w.writerow(("table","key","class","static_refs","current_text"))
        for table in sorted(p.rglob("*.uv.str")):
            rel=table.relative_to(p).as_posix().lower()
            slot=indexes.get(rel)
            data=read_rows(table)
            stats=Counter()
            for k,val in data.items():
                c="conflicting_duplicate" if val is None else classify(val)
                stats[c]+=1
                if c not in PENDING:continue
                num=static.get(f"<{slot}::{k}>",0) if slot is not None else 0
                if num:
                    refs_ct["rows"]+=1;refs_ct["occurrences"]+=num
                w.writerow((rel,k,c,num,val))
            totals.update(stats);tcounts[rel]={"rows":len(data),"by_class":stats}
    report["pvf_str"]={"all_tables_exported":len(list(p.rglob("*.str"))),"univ_tables":len(tcounts),
                       "rows":sum(totals.values()),"by_class":totals,"candidates_with_exact_script_reference":refs_ct,
                       "reference_scripts_scanned":ref_data["scripts_scanned"],"tables":tcounts}
    del static,ref_data

    root=out/"pvf-xui"; files=list(root.rglob("*.xui"))
    stats=Counter(); reasons=Counter()
    with (out/"xui-candidates.tsv").open("w",encoding="utf-8-sig",newline="") as f:
        w=csv.writer(f,delimiter="\t");w.writerow(("path","id","attribute","class","rewrite_reason","current_text"))
        for x in sorted(files):
            rel=x.relative_to(root).as_posix()
            xml=decoded(x)
            if xml is None:
                stats["odd_length_skipped"]+=1;continue
            for slot in extract_slots(xml):
                stats["display_slots"]+=1
                reason=rewrite_reason(slot.value,slot.control,rel,slot.attribute)
                reasons[reason or "rewrite_eligible"]+=1
                cl=classify(slot.value)
                if cl in PENDING and reason not in ("string_reference","editor_placeholder","runtime_template_identifier",
                    "debug_interface_literal","language_neutral_abbreviation"):
                    stats["candidate_slots"]+=1
                    if not reason:stats["rewrite_eligible_slots"]+=1
                    safe=slot.value.encode("utf8",errors="replace").decode()
                    w.writerow((rel,slot.control,slot.attribute,cl,reason,safe))
    report["pvf_xui"]={"files":len(files),"counts":stats,"reason_counts":reasons}
    inline=json.loads((out/"pvf-inline.json").read_bytes()); ic=Counter()
    with (out/"inline-candidates.tsv").open("w",encoding="utf-8-sig",newline="") as f:
        w=csv.writer(f,delimiter="\t");w.writerow(("field","example_path","current_text","class","occurrences","evidence"))
        for x in inline["values"]:
            c=classify(x["source"])
            if c in PENDING:
                soft=(x["field"]=="[name]" or DEBUG.search(x["source"]) or re.search(r"passiveobject|/test|/archive/",x["example_path"],re.I))
                ic["candidate_groups"]+=1;ic["candidate_occurrences"]+=x["occurrences"]
                if not soft:ic["display_field_groups"]+=1
                w.writerow((x["field"],x["example_path"],x["source"],c,x["occurrences"],"manual_review" if soft else "structured_display_field"))
    report["pvf_inline"]={"scripts_scanned":inline["scripts_scanned"],"groups":len(inline["values"]),
                          "counts":ic}
    aux=inspect_aux(out/"pvf-aux")
    serial(out/"aux-candidates.json",aux)
    report["pvf_aux"]=aux["counts"]
    inv=json.loads((out/"pvf-inventory.json").read_bytes())
    report["pvf_archive_entries"]=sum(row["files"] for row in inv)
    assert all(sha(CLIENT/name)==v for name,v in report["client_sha256"].items())
    report["client_source_hashes_unchanged"]=True
    serial(out/"text-summary.json",report)
    print(json.dumps({k:v for k,v in report.items() if k not in ("pvf_str","pvf_xui")},ensure_ascii=True))


if __name__=="__main__":
    parser=argparse.ArgumentParser(description=__doc__)
    for n in ("out","inner","manifest"):
        parser.add_argument("--"+n,type=Path,required=True)
    args=parser.parse_args()
    census(args.out,args.inner,args.manifest)
