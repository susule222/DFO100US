"""Verify or restore a known latest 100US localization (no PVF/font writes)."""
import argparse
from datetime import datetime, timezone
import os
from pathlib import Path

from us100_avatar_install import staged_copy
from us100_dungeon_level_labels import BASE as LEVEL_BASE
from us100_status_dstr import CLIENT, EXE, GameFonts, REL, core

ROOT=core.ROOT/"runtime"/"us100-last-localization-rollback"
FILES={
    "dstr.dat": (
        "42bcb1ae24e8bddac911f42de7141da157be7dd9dd1f0f7ac61d66527cfcd16b",
        "3f1f43b42c0809592e09f3ac040703eb0900d0f24e245d80d4d998cdd52dfc5f"),
    REL.as_posix(): (
        "5bafe28958d51edf80c0492ca9f135232d977974b66706b3ad4189ead47019dc",
        "ef224c2064c8c156119f92c303a887a9db8922b3d766f7f857e9f55bdcc77c0b"),
}
LEVEL_FILES={
    "dstr.dat": (LEVEL_BASE,"c04de394e8e8e7f3bfbb83e969896d4dd3bcda11af007793997a03a4e6440a8e"),
}
FULL_FILES={
    "dstr.dat": (LEVEL_FILES["dstr.dat"][1],
                 "ecabd5f299fff101455a5dc791255742d95ce4f366815ce5715640d71472ebda"),
}


def file_table(bundle):
    m=core.read(bundle/"rollback.json")
    if m["client"]==str(CLIENT.resolve()):
        for table in (FILES,LEVEL_FILES,FULL_FILES):
            if m["files"]=={rel:{"restore":a,"installed":b} for rel,(a,b) in table.items()}:
                return table
    raise ValueError("rollback manifest mismatch")


def verify(bundle,require_installed=False):
    table=file_table(bundle)
    if core.digest(CLIENT/"DFO.exe")!=EXE:
        raise ValueError("client executable identity differs")
    state={}
    for rel,(base,installed) in table.items():
        src=bundle/"original"/rel
        actual=core.digest(CLIENT/rel)
        if core.digest(src)!=base or actual not in ({installed} if require_installed else {base,installed}):
            raise ValueError(f"backup mismatch or live file unrecognized: {rel}")
        state[rel]=actual
    return state


def restore(bundle):
    # Can complete a partially applied rollback after a process restart,
    # but never overwrite local files from a newer/unknown deployment.
    with GameFonts(CLIENT/"Fonts").locked():
        before=verify(bundle)
        wanted={n:a for n,(a,_) in file_table(bundle).items()}
        locked=[CLIENT/"DFO.exe",CLIENT/"Script.pvf",CLIENT/"Script.pvf-1",
                *(p for p in (CLIENT/REL,) if REL.as_posix() not in wanted),
                *(x for x in (CLIENT/"Fonts").iterdir() if x.is_file())]
        fixed={str(x):core.digest(x) for x in locked}
        temp={}
        try:
            for rel,h in wanted.items():
                if before[rel]!=h:
                    temp[rel]=staged_copy(bundle/"original"/rel,(CLIENT/rel).parent,h)
            if core.client_up() or verify(bundle)!=before:
                raise ValueError("client reopened or resources changed before rollback")
            core.atomic(bundle/"rollback-state.json",{"stage":"publishing","before":before})
            for rel,p in temp.items():
                if core.client_up() or core.digest(CLIENT/rel)!=before[rel]:
                    raise ValueError("client reopened or resource changed")
                os.replace(p,CLIENT/rel)
            if verify(bundle)!=wanted or any(core.digest(Path(p))!=h for p,h in fixed.items()):
                raise ValueError("post-rollback verification failed")
            core.atomic(bundle/"rollback-state.json",{"stage":"restored","before":before,"after":wanted,
                        "utc":datetime.now(timezone.utc).isoformat(),"verified_untouched":fixed})
        finally:
            for p in temp.values():p.unlink(missing_ok=True)
    print("Restored and verified the exact recorded files.")


if __name__=="__main__":
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("--bundle",default=ROOT,type=Path)
    g=p.add_mutually_exclusive_group(required=True)
    g.add_argument("--verify-only",action="store_true")
    g.add_argument("--restore",action="store_true")
    args=p.parse_args()
    if args.verify_only:
        print(verify(args.bundle.resolve(),require_installed=True))
    else:
        restore(args.bundle.resolve())
