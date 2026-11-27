"""Atomically replace the five localized US100 client resources, with rollback."""
import argparse
from datetime import datetime, timezone
import os
from pathlib import Path

from us100_avatar_install import staged_copy
from us100_dungeon_clear_atlas import BASES, specs
from us100_dungeon_clear_labels import PVF
from us100_premium_panel import unchanged
from us100_status_dstr import (
    ACT, CLIENT, EXE, GameFonts, PROFILES, REL, core, extract, key_pair, pack, replace, unpack,
)
from us100_ui_atlas import index

PROFILE="dungeon-clear-ui"

def checked(text,atlas):
    hashes=core.read(atlas/"atlas.json")
    if set(hashes)!=set(BASES):
        raise ValueError("unexpected atlas bundle")
    planned=specs()
    for n,m in hashes.items():
        before,new=atlas/("before-"+n),atlas/n
        locations=index(before)
        listed={}
        windows=[]
        for s in m["spans"]:
            member=s["member"]
            start,end=s["start"],s["end"]
            offset,size=locations[member]
            if not offset<start<end<=offset+size:
                raise ValueError("out-of-bounds atlas edit")
            frames=listed.setdefault(member,set())
            if frames.intersection(s["frames"]):
                raise ValueError("duplicate atlas frame")
            frames.update(s["frames"])
            windows.append((start,end))
        if (listed!={k:set(r) for k,r in planned[n].items()} or index(new)!=locations
            or m["before"]!=BASES[n] or (core.digest(before),core.digest(new))!=(m["before"],m["after"])):
            raise ValueError("atlas frame or hash mismatch")
        unchanged(before.read_bytes(),new.read_bytes(),sorted(windows))
    profile=PROFILES[PROFILE]
    meta=core.read(text/"DSTR.diff.json")
    rows={r["id"]:(r["before"],r["after"]) for r in meta["rows"]}
    if (meta["profile"]!=PROFILE or meta["before"]!=profile.baseline or rows!=profile.rows
        or len(rows)!=len(meta["rows"])
        or any(core.digest(text/n)!=meta["before"] for n in ("before-dstr.dat","rollback-test.dat"))
        or any(core.digest(text/n)!=meta["after"] for n in ("dstr.dat","reapplied-dstr.dat"))):
        raise ValueError("DSTR candidate or round-trip proof differs")
    keys=key_pair(CLIENT/"DFO.exe")
    old=unpack((text/"before-dstr.dat").read_bytes(),keys)
    new=(text/"dstr.dat").read_bytes()
    if pack(replace(old,rows),keys)!=new or extract(unpack(new,keys),profile.sentinels.keys())!=profile.sentinels:
        raise ValueError("candidate unrelated DSTR or preserved labels differ")
    return {"dstr.dat":(text,meta)} | {n:(atlas,m) for n,m in hashes.items()}

def deploy(text,atlas,rollback=False):
    text,atlas=text.resolve(),atlas.resolve()
    if any(p.is_relative_to(CLIENT.resolve()) for p in (text,atlas)):
        raise ValueError("expected staged resources outside client")
    docs=checked(text,atlas)
    packs=CLIENT/"ImagePacks2"
    paths={n:CLIENT/n if n=="dstr.dat" else packs/n for n in docs}
    fonts=CLIENT/"Fonts"
    protected=[CLIENT/"DFO.exe",CLIENT/"Script.pvf",CLIENT/REL,
               packs/"sprite_interface2_hud.NPK"]
    protected+=sorted(p for p in fonts.iterdir() if p.is_file())
    with GameFonts(fonts).locked():
        if core.client_up():
            raise ValueError("DFO is running; close it normally first")
        keep={str(p):core.digest(p) for p in protected}
        for p,h in ((CLIENT/"DFO.exe",EXE),(CLIENT/"Script.pvf",PVF),(CLIENT/REL,ACT)):
            if keep[str(p)]!=h:raise ValueError(f"unexpected preserved resource: {p}")
        old={n:core.digest(p) for n,p in paths.items()}
        before_key,after_key=("after","before") if rollback else ("before","after")
        expected={n:m[before_key] for n,(_,m) in docs.items()}
        target={n:m[after_key] for n,(_,m) in docs.items()}
        if old!=expected:
            raise ValueError("live resources no longer match the deployment baseline")
        temp={}
        written=[]
        journal=atlas/"transaction.json"
        try:
            for n,(directory,_) in docs.items():
                temp[n]=staged_copy(directory/("before-"+n if rollback else n),paths[n].parent,target[n])
            if core.client_up() or any(core.digest(p)!=old[n] for n,p in paths.items()):
                raise ValueError("DFO reopened or assets changed during staging")
            core.atomic(journal,{"status":"publishing","before":old,"target":target})
            for n,p in paths.items():
                if core.client_up() or core.digest(p)!=old[n]:
                    raise ValueError("DFO reopened or resource changed during publishing")
                os.replace(temp[n],p)
                written.append(n)
            current={n:core.digest(p) for n,p in paths.items()}
            if current!=target or any(core.digest(Path(p))!=h for p,h in keep.items()):
                raise ValueError("post-deployment fingerprint failed")
            result={"utc":datetime.now(timezone.utc).isoformat(),"files":current,
                    "before":old,"preserved":keep,"game_checked":False,
                    "status":"rolled_back" if rollback else "installed_pending_game_check"}
            core.atomic(atlas/("rollback-deployment.json" if rollback else "deployment.json"),result)
            core.atomic(journal,result)
            for n,h in current.items():print(f"{n} {h}")
        except Exception:
            recovered=[]
            for n in reversed(written):
                if core.client_up() or core.digest(paths[n])!=target[n]:break
                directory,_=docs[n]
                p=staged_copy(directory/(n if rollback else "before-"+n),paths[n].parent,old[n])
                try:
                    if core.client_up():break
                    os.replace(p,paths[n])
                    recovered.append(n)
                finally:
                    p.unlink(missing_ok=True)
            if written:
                core.atomic(journal,{"status":"reverted_after_error" if len(written)==len(recovered)
                    else "partial_write_inspect_before_retry","written":written,"recovered":recovered})
            raise
        finally:
            for p in temp.values():p.unlink(missing_ok=True)

if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("action",choices=("install","rollback"))
    p.add_argument("--dstr",type=Path,required=True)
    p.add_argument("--atlas",type=Path,required=True)
    a=p.parse_args()
    deploy(a.dstr,a.atlas,a.action=="rollback")
