"""Install/roll back the exact US100 avatar DSTR/PVF pair while DFO is closed."""
import argparse
from datetime import datetime, timezone
import os
from pathlib import Path
import shutil
import tempfile

from us100_avatar_pvf import BASE, INNER_BASE
from us100_avatar_atlas import BASE as ATLAS_BASE, NAME as ATLAS_NAME, apply as apply_atlas
from us100_status_dstr import (
    ACT, CLIENT, EXE, REL, GameFonts, PROFILES, core, extract, key_pair,
    register_avatar_ui_profile, replace, unpack,
)


def staged_copy(src, parent, want):
    temp = None
    try:
        with tempfile.NamedTemporaryFile(prefix=".avatar-l10n-", suffix=".part", dir=parent, delete=False) as stream:
            temp = Path(stream.name)
            with src.open("rb") as f:
                shutil.copyfileobj(f, stream, length=8 << 20)
            stream.flush()
            os.fsync(stream.fileno())
        if core.digest(temp) != want:
            raise ValueError("staged file digest differs")
        return temp
    except Exception:
        if temp is not None:
            temp.unlink(missing_ok=True)
        raise


def check_candidates(text_dir, pvf_dir, atlas_dir=None):
    register_avatar_ui_profile()
    prof = PROFILES["avatar-ui"]
    td = core.read(text_dir/"DSTR.diff.json")
    pd = core.read(pvf_dir/"PVF.diff.json")
    checked = core.read(pvf_dir/"Script.manifest.json")
    if (td["profile"] != "avatar-ui" or td["before"] != prof.baseline
        or pd["before"] != BASE or pd["inner_before"] != INNER_BASE
        or pd["after"] != checked["candidate_sha256"] or pd["inner_after"] != checked["candidate_inner_sha256"]
        or not checked["verified_rewrap_round_trip"]):
        raise ValueError("avatar bundle manifest identities differ")
    rows={r["id"]:(r["before"],r["after"]) for r in td["rows"]}
    if rows!=prof.rows or len(rows)!=len(td["rows"]):
        raise ValueError("unexpected avatar UI scope")
    k = key_pair(CLIENT/"DFO.exe")
    old, new = (unpack((text_dir/n).read_bytes(), k) for n in ("before-dstr.dat","dstr.dat"))
    if replace(old,rows)!=new or extract(new,prof.sentinels.keys())!=prof.sentinels:
        raise ValueError("localized UI or previous labels changed")
    if core.digest(text_dir/"reapplied-dstr.dat")!=td["after"] or core.digest(text_dir/"rollback-test.dat")!=td["before"]:
        raise ValueError("DSTR rollback/reapply proof differs")
    if core.digest(pvf_dir/"rollback-test.pvf")!=pd["before"] or core.digest(pvf_dir/"Script.inner.pvf")!=pd["inner_after"]:
        raise ValueError("PVF rollback/native proof differs")
    proof=(pvf_dir/"avatar-script-preservation.txt").read_text(encoding="utf-8-sig")
    if ("indexed 136880 avatar equipment scripts" not in proof
        or "path_order_and_raw_script_cells_sha256=07e2e2fd85ab817b83874936faabe4839a3529f5270c20bbe586f491773c798f" not in proof
        or core.digest(pvf_dir/"scripts-baseline.tsv")!=core.digest(pvf_dir/"scripts-candidate.tsv")):
        raise ValueError("avatar script preservation proof differs")
    if set(pd["tables"])!={"string/equipment.uv.str"}:
        raise ValueError("unexpected non-equipment PVF update")
    docs={"dstr.dat":(text_dir,td),"Script.pvf":(pvf_dir,pd)}
    if atlas_dir:
        m=core.read(atlas_dir/"NPK.diff.json")
        v=core.read(atlas_dir/"verification.json")
        if (m["before"]!=ATLAS_BASE or v["restored"]!=ATLAS_BASE or v["reapplied"]!=m["after"]
            or v["span_count"]!=3 or core.digest(atlas_dir/"rollback-test.NPK")!=ATLAS_BASE
            or apply_atlas(atlas_dir/("before-"+ATLAS_NAME),atlas_dir/"NPK.diff.json")!=(atlas_dir/ATLAS_NAME).read_bytes()):
            raise ValueError("aura-transfer atlas proof differs")
        docs[ATLAS_NAME]=atlas_dir,m
    return docs


def deploy(text_dir, pvf_dir, rollback=False, atlas_dir=None):
    text_dir,pvf_dir=text_dir.resolve(),pvf_dir.resolve()
    atlas_dir=atlas_dir.resolve() if atlas_dir is not None else None
    if any(x is not None and x.is_relative_to(CLIENT.resolve()) for x in (text_dir,pvf_dir,atlas_dir)):
        raise ValueError("candidate must be outside the live client")
    documents=check_candidates(text_dir,pvf_dir,atlas_dir)
    for name,(p,m) in documents.items():
        if (core.digest(p/("before-"+name)),core.digest(p/name))!=(m["before"],m["after"]):
            raise ValueError(f"avatar candidate or backup changed: {name}")
    fonts=CLIENT/"Fonts"
    retained=[CLIENT/"DFO.exe",CLIENT/REL,CLIENT/"Script.pvf-1"]+[
        CLIENT/"ImagePacks2"/n for n in ("sprite_interface2_hud.NPK","sprite_interface2_premiumservice.NPK",
             "sprite_interface2_ui_univ_neoscontract_22_new.NPK")
    ]+sorted(p for p in fonts.iterdir() if p.is_file())
    live={n:(CLIENT/"ImagePacks2"/n if n==ATLAS_NAME else CLIENT/n) for n in documents}
    receipt=text_dir/("rollback-deployment.json" if rollback else "deployment.json")
    journal=text_dir/"transaction.json"
    if not rollback and journal.exists():
        raise ValueError("prior avatar transaction detected; verify state before repeating")
    with GameFonts(fonts).locked():
        if core.client_up():
            raise ValueError("DFO is running; close it normally first")
        guard={str(x):core.digest(x) for x in retained}
        if guard[str(CLIENT/"DFO.exe")]!=EXE or guard[str(CLIENT/REL)]!=ACT:
            raise ValueError("DFO or quest asset identity changed")
        existing={n:core.digest(p) for n,p in live.items()}
        want={n:m["before" if rollback else "after"] for n,(_,m) in documents.items()}
        for n,(_,m) in documents.items():
            good={m["before"],m["after"]} if rollback else {m["before"]}
            if existing[n] not in good:
                raise ValueError(f"unexpected live baseline: {n}")
        temp={}
        changed=[]
        try:
            for n,(p,m) in documents.items():
                if existing[n]!=want[n]:
                    temp[n]=staged_copy(p/("before-"+n if rollback else n),live[n].parent,want[n])
            if core.client_up() or any(core.digest(live[n])!=v for n,v in existing.items()):
                raise ValueError("client reopened or installation identity changed during staging")
            core.atomic(journal,{"state":"publishing","before":existing,"target":want})
            for n,tmp in temp.items():
                if core.client_up():raise ValueError("DFO reopened during publishing")
                os.replace(tmp,live[n])
                changed.append(n)
            observed={n:core.digest(p) for n,p in live.items()}
            if observed!=want or any(core.digest(Path(p))!=h for p,h in guard.items()):
                raise ValueError("post-install fingerprint check failed")
            result={"utc":datetime.now(timezone.utc).isoformat(),"files":observed,
                    "before":existing,"unchanged_assets":guard,"rolled_back":rollback,"game_checked":False}
            core.atomic(receipt,result)
            core.atomic(journal,{"state":"rolled_back" if rollback else "installed","files":observed})
            print(result)
        except Exception:
            if changed and not core.client_up():
                for n in reversed(changed):
                    if core.client_up() or core.digest(live[n])!=want[n]:break
                    directory,meta=documents[n]
                    old=directory/("before-"+n if existing[n]==meta["before"] else n)
                    core.file_copy(old,live[n])
            if changed:
                core.atomic(journal,{"state":"failed_verify_live","files":{n:core.digest(p) for n,p in live.items()}})
            raise
        finally:
            for p in temp.values():p.unlink(missing_ok=True)


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("action",choices=("install","rollback"))
    p.add_argument("--dstr",type=Path,required=True)
    p.add_argument("--pvf",type=Path,required=True)
    p.add_argument("--atlas",type=Path)
    a=p.parse_args()
    deploy(a.dstr,a.pvf,a.action=="rollback",a.atlas)
