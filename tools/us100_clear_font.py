"""Stage and verify the US100 Microsoft YaHei UI regular font pair."""
import argparse
from datetime import datetime, timezone
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
from tempfile import TemporaryDirectory
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo

from fontTools.ttLib import TTFont
from PIL import ImageFont

from us100_quest_act import OUTLINE_FONTS, REL
from us100_quest_ui_bundle import ORIGINAL
from us100_quest_ui_install import CLIENT, GameFonts, core
from game_fonts import BITMAP_TABLES, build_face, faces, windows_font_check


FACE = 1
FAMILY = "Microsoft YaHei UI"
SOURCE = Path("C:/Windows/Fonts/msyh.ttc")
DONOR = "d79c55e68b1131eea0cc1c47be4f572d964f28c682e143db2ad09c1e4cb07a3f"
SAMPLES = ("屏幕分辨率 UI清晰化", "垂直同步 角色遮挡功能", "攻击速度 施放速度 +0.0%", "格兰之森 火冰光暗")
PRESERVED = ("Script.pvf", "dstr.dat", REL.as_posix(), "ImagePacks2/sprite_interface2_worldmap_step_1(town).NPK")


def digest(data):
    return hashlib.sha256(data).hexdigest()


def archive(path, entries):
    with ZipFile(path,"x") as z:
        for name,data in sorted(entries.items()):
            z.writestr(ZipInfo(name),data,compress_type=ZIP_DEFLATED,compresslevel=5)


def unpack(path):
    with ZipFile(path) as z:
        if sorted(z.namelist())!=sorted((*ORIGINAL,"state.json")):
            raise ValueError("unexpected font archive entries")
        fonts={n:z.read(n) for n in ORIGINAL}
        state=json.loads(z.read("state.json"))
    if {n:digest(b) for n,b in fonts.items()}!=state["hashes"]:
        raise ValueError("font archive state differs from its contents")
    return fonts,state


def state_bytes(state):
    return (json.dumps(state,ensure_ascii=False,sort_keys=True)+"\n").encode("utf-8")


def inspect(path):
    fonts,state=unpack(path)
    rows={}
    with TemporaryDirectory() as d:
        for name,raw in fonts.items():
            f=Path(d)/name
            f.write_bytes(raw)
            windows_font_check(f)
            with TTFont(io.BytesIO(raw)) as font:
                family=font["name"].getBestFamilyName()
                cmap=font.getBestCmap()
                if family.casefold()!=Path(name).stem or any(ord(c) not in cmap for t in SAMPLES for c in t):
                    raise ValueError("invalid in-game font alias or missing sample glyph")
                if any(tag in font for tag in BITMAP_TABLES):
                    raise ValueError("unexpected embedded bitmap strikes")
                widths={str(size):[round(ImageFont.truetype(str(f),size).getlength(t),2) for t in SAMPLES]
                        for size in (11,13,15)}
                rows[name]={"alias":family,"glyphs":len(cmap),"sample_widths_px":widths,"gdi_loadable":True}
    print(json.dumps({"family":state["family"],"files":rows},ensure_ascii=False,sort_keys=True))


def render_bytes(source, originals):
    if core.digest(source)!=DONOR or faces(source)[FACE]!=FAMILY:
        raise ValueError("unexpected Windows font source")
    with TemporaryDirectory() as d:
        root=Path(d)
        for n,sha in ORIGINAL.items():
            if core.digest(originals/n)!=sha:
                raise ValueError(f"wrong factory game font {n}")
            build_face(source,FACE,originals/n,root/n,outline_only=True)
        pair={n:(root/n).read_bytes() for n in ORIGINAL}
    state={"family":FAMILY,"file":source.name,"face":FACE,"sha256":DONOR,
           "outline_only":True,"hashes":{n:digest(b) for n,b in pair.items()}}
    return pair|{"state.json":state_bytes(state)}


def apply(before, diff, source, originals, output):
    m=core.read(diff)
    _,state=unpack(before)
    if (core.digest(before)!=m["before"] or state["hashes"]!=OUTLINE_FONTS
            or m["source_sha256"]!=DONOR or m["source_face"]!=FACE or m["family"]!=FAMILY
            or output.resolve().is_relative_to(CLIENT.resolve())):
        raise ValueError("unsupported font patch or output")
    archive(output,render_bytes(source,originals))
    if core.digest(output)!=m["after"]:
        raise ValueError("rebuilt font archive differs")


def build(out, source, originals):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()):
        raise ValueError("font trial must be outside the live client")
    service=GameFonts(CLIENT/"Fonts")
    previous=service.state()
    if service.hashes()!=OUTLINE_FONTS or previous.get("hashes")!=OUTLINE_FONTS or previous.get("family")!="SimSun":
        raise ValueError("unexpected live SimSun baseline")
    out.mkdir(parents=True)
    old,new,diff,rollback=(out/n for n in ("before-Fonts.zip","Fonts.zip","FONT.diff.json","ROLLBACK.sh"))
    archive(old,{n:(service.folder/n).read_bytes() for n in ORIGINAL}|{"state.json":state_bytes(previous)})
    unpack(old)
    archive(new,render_bytes(source,originals))
    _,fresh=unpack(new)
    core.atomic(diff,{"schema":"100US font-pair patch v1","before":core.digest(old),"after":core.digest(new),
                     "source":str(source.resolve()),"source_sha256":DONOR,"source_face":FACE,"family":FAMILY,
                     "fonts_before":previous["hashes"],"fonts_after":fresh["hashes"],
                     "preserved":{n:core.digest(CLIENT/n) for n in PRESERVED}})
    core.file_copy(Path(__file__).with_name("us100_font_rollback.sh"),rollback)
    rollback.chmod(0o755)
    copy=out/"rollback-test.zip"
    core.file_copy(new,copy)
    ledger=[f"UTC: {datetime.now(timezone.utc).isoformat()}",f"Original archive SHA-256: {core.digest(old)}",
            f"Candidate archive SHA-256: {core.digest(new)}","Changed: game Font aliases calibri.ttf and tahoma.ttf"]
    def run(label,*args):
        cmd=[str(x) for x in args]
        p=subprocess.run(cmd,capture_output=True,encoding="utf-8",env={**os.environ,"PYTHONIOENCODING":"utf-8"})
        ledger.extend([label,"argv="+json.dumps(cmd,ensure_ascii=False),f"exit={p.returncode}","stdout:",p.stdout,"stderr:",p.stderr])
        if p.returncode:
            raise RuntimeError(f"{label}: {p.stderr}")
        return p.stdout
    def probe(label,path):
        return run(label,sys.executable,Path(__file__).resolve(),"inspect","--archive",path)
    try:
        baseline=probe("BASELINE",old)
        modified=probe("MODIFIED",new)
        run("ROLLBACK.sh",sys.executable,rollback,copy)
        restored=probe("ROLLBACK",copy)
        if baseline!=restored or json.loads(baseline)["family"]!="SimSun" or json.loads(modified)["family"]!=FAMILY:
            raise ValueError("font transaction behavior differs")
        m=core.read(diff)
        reapplied=out/"reapplied-Fonts.zip"
        run("REAPPLY",sys.executable,Path(__file__).resolve(),"apply","--before",copy,"--diff",diff,
            "--source",source.resolve(),"--originals",originals.resolve(),"--out",reapplied)
        if core.digest(copy)!=m["before"] or core.digest(reapplied)!=m["after"]:
            raise ValueError("font rollback or reapplication fingerprint differs")
        ledger.extend(["Restored archive SHA-256: "+core.digest(copy),"Reapplied archive SHA-256: "+core.digest(reapplied),
                       "Offline sample metrics and private GDI registration, not in-game rendering."])
    finally:
        (out/"VERIFICATION.txt").write_text("\n".join(ledger)+"\n",encoding="utf-8")
    for role in (new,diff,out/"VERIFICATION.txt",rollback):
        print(f"{role}: {core.digest(role)}")


def deploy(out,rollback):
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()):
        raise ValueError("require a separate font bundle")
    m=core.read(out/"FONT.diff.json")
    p=out/("before-Fonts.zip" if rollback else "Fonts.zip")
    pair,state=unpack(p)
    prior=m["fonts_after"] if rollback else m["fonts_before"]
    wanted=m["before"] if rollback else m["after"]
    if core.digest(p)!=wanted or tuple(sorted(m["preserved"]))!=tuple(sorted(PRESERVED)):
        raise ValueError("archive identity or preserved asset list differs")
    service=GameFonts(CLIENT/"Fonts")
    with service.locked(), TemporaryDirectory(dir=service.root) as tmp:
        if service.hashes()!=prior or (not rollback and any(core.digest(CLIENT/n)!=v for n,v in m["preserved"].items())):
            raise ValueError("live font or preserved asset identity differs")
        stage=Path(tmp)
        for n,raw in pair.items():
            (stage/n).write_bytes(raw)
        service.commit(stage,state,prior)
        if service.hashes()!=state["hashes"]:
            raise ValueError("live game font fingerprint differs")
        core.atomic(out/"deployment.json",{"utc":datetime.now(timezone.utc).isoformat(),"family":state["family"],
                    "status":"rolled_back" if rollback else "installed_pending_game_check","hashes":service.hashes(),
                    "preserved":{n:core.digest(CLIENT/n) for n in PRESERVED}})
        print(json.dumps({"family":state["family"],"hashes":service.hashes()},ensure_ascii=False))


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    sp=p.add_subparsers(dest="action",required=True)
    for action,required in (("build",("out","source","originals")),("inspect",("archive",)),
                            ("apply",("before","diff","source","originals","out")),
                            ("install",("out",)),("rollback",("out",))):
        q=sp.add_parser(action)
        for key in required:
            q.add_argument("--"+key,type=Path,required=True)
    a=p.parse_args()
    if a.action=="build":build(a.out,a.source,a.originals)
    elif a.action=="inspect":inspect(a.archive)
    elif a.action=="apply":apply(a.before,a.diff,a.source,a.originals,a.out)
    else:deploy(a.out,a.action=="rollback")
