"""Translate and re-open avatar-linked tables; never modify gameplay records."""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

from localize_us100_tables import FORMAT_MARKER, NUMBER, SPLIT, read_rows, table_parts
from us100_dstr_ui import CLIENT, core
from us100_l10n_census import PENDING, classify, clean


ROOT=Path(__file__).resolve().parents[1]
RT=ROOT/"runtime"
AUDIT=RT/"us100-avatar-l10n-audit"
CENSUS=RT/"us100-l10n-census-20261125"
LIVE=CLIENT/"Script.pvf"
INNER=RT/"us100-item-expiry"/"Script.inner.pvf"
BASE="a3cf04a87f295de24ec9753267865cbc8c70482a6e1da9519a1952be6ac4865a"
INNER_BASE="708869d667465e2fcae2eae8d2a39cf534e84080b1b5f426f002ed77e9eb8351"
TABLES={"3":"string/equipment.uv.str","13":"string/stackable.uv.str"}
REF=re.compile(r"<(\d+)::([^>]+)>")
TECH=re.compile(r"\b(?:HS-\d+|NK-\d+|(?:KN|TH|FT|DL|GB|GN|PR|SM)-[A-Z0-9]+|mg_\d+|SMG|Twitch|Facebook|DFO|DNF|DPL|GM|SWAG|VR|OX|R&B|P&B|B&B|huzzah|shiver|shvr|Whaa|Hmph|Aww|Hehe|Meow(?=”)|Baa)\b",re.I)
CODES=re.compile(r"\b[A-Z]{1,3}-[A-Z0-9]*\d+[A-Z0-9]*\b",re.I)
ALPHA=re.compile(r"[A-Za-z][A-Za-z'’-]{1,}")
DO_NOT_TRANSLATE=frozenset("""
apc dfo dnf dpl pc npc gm hp mp sp tp vp lv twitch facebook ox vr rpg smg
hs nk ft dl gb gn kn th sm pr mg swagg swag ex r b p s ii pc
huzzah shiver shvr whaa hmph aww hehe meow baa hello exstream pin ok
alpha aro ik ia kua nes in ele
""".split())
SKIP_REVIEW=re.compile(r"(?:\(\((?:后续|待|确认|未翻译)|\ufffd|\x00)")


def sha(blob):
    return hashlib.sha256(blob).hexdigest()


def read_rules():
    rules={}
    for row in Path(__file__).with_name("us100_avatar_lexicon.tsv").read_text(encoding="utf-8").splitlines():
        if not row or row.startswith("#"): continue
        a,b=row.split("\t")
        key=a.casefold()
        if key in rules and rules[key]!=b:raise ValueError(f"conflicting avatar glossary {a}")
        rules[key]=b
    pattern=re.compile(r"(?<![A-Za-z])(?:"+"|".join(re.escape(x) for x in sorted(rules,key=len,reverse=True))+
                       r")(?![A-Za-z])",re.I)
    return rules,pattern


def residual(val):
    t=CODES.sub(" ",clean(val))
    t=re.sub(r"\b(?:DFO\s+ExStream|EXStream|Hello语音|Twitch|Facebook)\b"," ",t,flags=re.I)
    return sorted({x for x in ALPHA.findall(t) if x.casefold().strip("-") not in DO_NOT_TRANSLATE})


def translate(value,rules,pattern):
    # Exact named command string literals and item-model tokens must survive.
    ref=re.compile(FORMAT_MARKER.pattern+r"|\\[nrt]",re.I)
    def segment(s):
        return pattern.sub(lambda m:rules[m[0].casefold()],s)
    chunks=[]
    pos=0
    for m in ref.finditer(value):
        chunks.append(segment(value[pos:m.start()]))
        chunks.append(m[0]);pos=m.end()
    chunks.append(segment(value[pos:]))
    out="".join(chunks)
    out=re.sub(r"\[(?:类型|Type)\s*([A-Z])\]",r"[\1型]",out,flags=re.I)
    if value!=out:
        out=re.sub(r"([\u4e00-\u9fff]) {1,2}(?=[\u4e00-\u9fff])",r"\1",out)
        out=out.replace("上衣上衣","上衣").replace("皮肤皮肤","皮肤").replace("圣诞圣诞帽","圣诞帽")
    if (not out or SKIP_REVIEW.search(out) or any(c in out for c in "\r\n\0")
        or FORMAT_MARKER.findall(value)!=FORMAT_MARKER.findall(out)
        or NUMBER.findall(value)!=NUMBER.findall(out)
        or any(value.count(x)!=out.count(x) for x in (r"\n","%"))
        or TECH.findall(value)!=TECH.findall(out) or CODES.findall(value)!=CODES.findall(out)):
        return value,"format_or_mechanical_guard"
    return out,"lexicon" if value!=out else "unchanged"


def evidence():
    keys={k:set() for k in TABLES}
    counts=Counter()
    with (AUDIT/"avatar-text-references.tsv").open(encoding="utf-8") as f:
        if next(f).strip()!="path\tfield\tencoding\tvalue_json":raise ValueError("wrong script audit")
        for line in f:
            _path,_field,typ,raw=line.rstrip("\n").split("\t",3)
            text=json.loads(raw)
            m=REF.fullmatch(text)
            if typ=="ref" and m and m[1] in keys:
                keys[m[1]].add(m[2]);counts["reference_occurrences"]+=1
            else:counts["unhandled_evidence"]+=1
    if counts["unhandled_evidence"]:
        raise ValueError(f"unsupported display evidence: {counts}")
    return keys,counts


def table_update(data,edits):
    parts=SPLIT.split(data.decode("utf-16-le",errors="surrogatepass"))
    touched=set()
    for i,k,pre,v,suf in table_parts(data):
        if k not in edits:continue
        old,new=edits[k]
        if k in touched or v!=old:raise ValueError(f"duplicate or changed source row: {k}")
        parts[i]=pre+new+suf;touched.add(k)
    if touched!=edits.keys():raise ValueError("replacement did not cover all expected rows")
    return "".join(parts).encode("utf-16-le",errors="surrogatepass")


def plan(out):
    out.mkdir(parents=True,exist_ok=False)
    if core.digest(LIVE)!=BASE or core.digest(INNER)!=INNER_BASE or out.resolve().is_relative_to(CLIENT.resolve()):
        raise ValueError("unexpected PVF baseline or output")
    rules,pat=read_rules();keys,counts=evidence()
    summary={};modified={};pending=[];paths={}
    for k,rel in TABLES.items():
        path=CENSUS/"pvf-tables"/rel
        raw=path.read_bytes(); edits={};unique=read_rows(path);st=Counter()
        st["referenced_keys"]=len(keys[k]); st["missing_exact"]=len(keys[k]-unique.keys())
        for _,name,_,val,_ in table_parts(raw):
            if name not in keys[k] or not val.strip() or unique[name] is None:continue
            if classify(val) not in PENDING:continue
            st["language_candidates"]+=1
            new,why=translate(val,rules,pat)
            rem=residual(new)
            if new!=val:
                if name in edits:raise ValueError(f"ambiguous duplicate translated key: {name}")
                edits[name]=(val,new)
                st["changed"]+=1
            if rem or why=="format_or_mechanical_guard":
                st["residual_review"]+=1
                pending.append((rel,name,val,new,why,rem))
        if edits:
            result=table_update(raw,edits)
            if table_update(result,{k:(v[1],v[0]) for k,v in edits.items()})!=raw:
                raise ValueError("table inverse not exact")
            dest=out/"replacement-tables"/rel
            dest.parent.mkdir(parents=True,exist_ok=True)
            dest.write_bytes(result)
            paths[rel]={"before":sha(raw),"after":sha(result)}
            modified[rel]=edits
        summary[rel]=dict(st)
    core.atomic(out/"PVF.rows.json",modified)
    core.atomic(out/"PVF.table-hashes.json",paths)
    core.atomic(out/"review.json",pending)
    core.atomic(out/"coverage.json",{"table_counts":summary,"reference_counts":dict(counts),
                                     "exact_table_inverse":True,"game_checked":False})
    print(json.dumps(summary,ensure_ascii=False))
    return modified,paths


def build(out):
    if core.digest(LIVE)!=BASE or core.digest(INNER)!=INNER_BASE:
        raise ValueError("unexpected PVF baseline")
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()):
        raise ValueError("PVF candidate must be offline")
    edits=core.read(out/"PVF.rows.json");checks=core.read(out/"PVF.table-hashes.json")
    if not edits or edits.keys()!=checks.keys() or not edits.keys()<=set(TABLES.values()):
        raise ValueError("no verified referenced avatar edits or unexpected table")
    for rel,h in checks.items():
        x=CENSUS/"pvf-tables"/rel
        assert core.digest(x)==h["before"]
        assert core.digest(out/"replacement-tables"/rel)==h["after"]
        assert table_update(x.read_bytes(),edits[rel])==(out/"replacement-tables"/rel).read_bytes()
    old=out/"before-Script.pvf";inner=out/"Script.inner.pvf";wrapped=out/"Script.pvf"
    core.file_copy(LIVE,old)
    if core.digest(old)!=BASE:raise ValueError("live archive changed during backup")
    checker=RT/"localize-us100-audit"/"pvfinspect-localization.exe"
    packer=RT/"localize-us100-audit"/"pvflocalize-inline-cinema.exe"
    ledger=[]
    def run(*arg):
        cmd=list(map(str,arg))
        p=subprocess.run(cmd,capture_output=True,encoding="utf-8",errors="replace")
        ledger.extend((json.dumps(cmd,ensure_ascii=False),f"exit={p.returncode}",p.stdout,p.stderr))
        p.check_returncode()
    try:
        run(packer,"-source",INNER,"-dir",out/"replacement-tables","-output",inner)
        run(sys.executable,Path(__file__).with_name("rewrap_us100_pvf.py"),"--original",CLIENT/"Script.pvf-1",
            "--baseline",RT/"pvf_source"/"Script.inner.pvf","--candidate",inner,"--output",wrapped)
        look=out/"reopened"
        run(checker,"-source",inner,"-files",",".join((*checks,"string/dnf.uv.str")),"-output",look)
        for row in core.read(look/"exports.json"):
            path=row["entry"]["archive_path"]
            want=checks[path]["after"] if path in checks else core.digest(CENSUS/"pvf-tables"/path)
            if row["raw_sha256"]!=want or core.digest(look/row["raw_file"])!=want:
                raise ValueError(f"reopened native table differs: {path}")
        if core.digest(LIVE)!=BASE:raise ValueError("live PVF changed during offline build")
        manifest={"before":BASE,"after":core.digest(wrapped),"inner_before":INNER_BASE,
                  "inner_after":core.digest(inner),"tables":checks,
                  "changed_rows":sum(map(len,edits.values())),"game_checked":False}
        core.atomic(out/"PVF.diff.json",manifest)
        rollback=Path(__file__).with_name("us100_status_rollback.sh").read_text(encoding="utf-8")
        rb=out/"ROLLBACK.sh"
        rb.write_text(rollback.replace("dstr.dat","Script.pvf").replace("DSTR.diff.json","PVF.diff.json"),encoding="utf-8",newline="\n")
        scratch=out/"rollback-test.pvf";core.file_copy(wrapped,scratch)
        run(sys.executable,rb,scratch)
        if core.digest(scratch)!=BASE:raise ValueError("wrapped rollback identity differs")
        print(json.dumps(manifest,ensure_ascii=False))
    finally:
        (out/"VERIFICATION.txt").write_text("\n".join(ledger)+"\n",encoding="utf-8")


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("action",choices=("plan","build"))
    p.add_argument("--out",type=Path,required=True)
    args=p.parse_args()
    (plan if args.action=="plan" else build)(args.out)
