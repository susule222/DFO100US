"""Verify the installed load/shop localization as one reversible offline bundle."""
import argparse
import base64
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
from zipfile import ZipFile, ZipInfo

from us100_dstr_ui import CLIENT, core, key_pair, pack, unpack
from us100_shop_load_atlas import LOAD, MEMBERS, SHOP, verify_spans
from us100_status_dstr import PROFILES, extract, replace


NAMES={"dstr.dat",SHOP,LOAD}


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def zip_bytes(paths):
    buf=io.BytesIO()
    with ZipFile(buf,"w") as f:
        for n,data in sorted(paths.items()):
            f.writestr(ZipInfo(n),data)
    return buf.getvalue()


def read_zip(path):
    with ZipFile(path) as f:
        if set(f.namelist())!=NAMES or len(f.namelist())!=len(NAMES):
            raise ValueError("unexpected UI asset archive")
        return {n:f.read(n) for n in NAMES}


def digest_files(parts):
    return {n:sha(b) for n,b in parts.items()}


def inspect(path,exe):
    parts=read_zip(path)
    keys=key_pair(exe)
    plain=unpack(parts["dstr.dat"],keys)
    if pack(plain,keys)!=parts["dstr.dat"]:
        raise ValueError("DSTR format round trip differed")
    labels=extract(plain,{"46113","38397","23035","27115","100009013","100008129","42000","52021"})
    print(json.dumps({"assets":digest_files(parts),"labels":labels},ensure_ascii=False,sort_keys=True))


def apply(base,delta,exe,out):
    m=core.read(delta)
    if core.digest(base)!=m["before"]:
        raise ValueError("UI rollback baseline differs")
    old=read_zip(base)
    after=old.copy()
    keys=key_pair(exe)
    rows={r["id"]:(r["before"],r["after"]) for r in m["text"]}
    if rows!=PROFILES["shop-v2"].rows or m["images"].keys()!=MEMBERS.keys():
        raise ValueError("UI delta footprint differs")
    after["dstr.dat"]=pack(replace(unpack(old["dstr.dat"],keys),rows),keys)
    for n,ranges in m["images"].items():
        data=bytearray(old[n]); spans=[]
        for row in ranges:
            x,y=row["start"],row["end"]
            payload=base64.b64decode(row["payload"],validate=True)
            if len(payload)!=y-x:
                raise ValueError("image payload size differs")
            data[x:y]=payload
            spans.append((x,y,row["frame"]))
        verify_spans(old[n],data,spans)
        after[n]=bytes(data)
    result=zip_bytes(after)
    if sha(result)!=m["after"]:
        raise ValueError("reapplied UI delta differs")
    with out.open("xb") as f:
        f.write(result)


def make(out,shop,art,exe):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()):
        raise ValueError("require an offline output directory")
    out.mkdir(parents=True)
    old={"dstr.dat":(shop/"before-dstr.dat").read_bytes(),**{p:(art/"before"/p).read_bytes() for p in MEMBERS}}
    new={"dstr.dat":(shop/"dstr.dat").read_bytes(),**{p:(art/p).read_bytes() for p in MEMBERS}}
    live={n:(CLIENT/n if n=="dstr.dat" else CLIENT/"ImagePacks2"/n).read_bytes() for n in NAMES}
    if digest_files(new)!=digest_files(live):
        raise ValueError("file hashes in the client's installation differ")
    b,a,d,r=(out/n for n in ("before-UI.zip","UI.zip","UI.diff.json","ROLLBACK.sh"))
    b.write_bytes(zip_bytes(old)); a.write_bytes(zip_bytes(new))
    img=core.read(art/"ATLAS.diff.json")
    for n in MEMBERS:
        verify_spans(old[n],new[n],img[n]["spans"])
    core.atomic(d,{"before":core.digest(b),"after":core.digest(a),
                  "text":core.read(shop/"DSTR.diff.json")["rows"],
                  "images":{n:[{"start":x,"end":y,"frame":i,"payload":base64.b64encode(new[n][x:y]).decode()}
                               for x,y,i in rows["spans"]] for n,rows in img.items()}})
    rollback=Path(__file__).with_name("us100_font_rollback.sh").read_text(encoding="utf-8")
    r.write_text(rollback.replace("Font archive","UI archive").replace("Fonts.zip","UI.zip")
                         .replace("FONT.diff.json","UI.diff.json"),encoding="utf-8",newline="\n")
    r.chmod(0o755)
    copy=out/"rollback-test.zip"
    core.file_copy(a,copy)
    ledger=["Scope: loading and shop localization archives; installed file identities matched.",
            "Baseline archive SHA-256: "+core.digest(b),"Modified archive SHA-256: "+core.digest(a)]
    def run(label,*args):
        cmd=[str(v) for v in args]
        p=subprocess.run(cmd,capture_output=True,encoding="utf-8",env={**os.environ,"PYTHONIOENCODING":"utf-8"})
        ledger.extend([label,"argv="+json.dumps(cmd,ensure_ascii=False),f"exit={p.returncode}","stdout:",p.stdout,"stderr:",p.stderr])
        if p.returncode:raise RuntimeError(p.stderr)
        return p.stdout
    def probe(label,path):
        return run(label,sys.executable,Path(__file__).resolve(),"inspect","--archive",path,"--exe",exe.resolve())
    try:
        baseline=probe("BASELINE",b)
        modified=probe("MODIFIED",a)
        run("ROLLBACK.sh",sys.executable,r,copy)
        if baseline!=probe("ROLLBACK",copy) or baseline==modified or core.digest(copy)!=core.digest(b):
            raise ValueError("rollback or modification delta differs")
        p=out/"reapplied-UI.zip"
        run("REAPPLY",sys.executable,Path(__file__).resolve(),"apply","--base",copy,"--diff",d,"--out",p,"--exe",exe.resolve())
        if core.digest(p)!=core.digest(a):raise ValueError("reapplication differs")
        ledger.extend(["Rollback SHA-256: "+core.digest(copy),"Reapplied SHA-256: "+core.digest(p),
                       "Resource checks passed; no in-game display test performed by this tool."])
    finally:
        (out/"VERIFICATION.txt").write_text("\n".join(ledger)+"\n",encoding="utf-8")
    for f in (a,d,out/"VERIFICATION.txt",r):
        print(f"{f}: {core.digest(f)}")


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    sub=p.add_subparsers(dest="action",required=True)
    for name,fields in (("make",("out","shop","art","exe")),("inspect",("archive","exe")),
                        ("apply",("base","diff","exe","out"))):
        q=sub.add_parser(name)
        for field in fields:q.add_argument("--"+field,type=Path,required=True)
    a=p.parse_args()
    if a.action=="make":make(a.out,a.shop,a.art,a.exe)
    elif a.action=="inspect":inspect(a.archive,a.exe)
    else:apply(a.base,a.diff,a.exe,a.out)
