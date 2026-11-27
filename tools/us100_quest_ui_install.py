"""Apply or roll back the validated US100 Quest UI files while DFO is closed."""
import argparse
from datetime import datetime, timezone
from pathlib import Path

from us100_quest_ui_bundle import PLAN, ORIGINAL, QUEST

import core
from game_fonts import GameFonts


CLIENT = Path("D:/DNF/client/100US/DFO")
PVF = "a4bbe38ace337f7684d31a2cc272384cbdbc11811611682b4f85161f9eab14bc"
DSTR = "39efa9db827a2901791f12d5369e0f33be5041214191b354160d5e1804230752"


def install(bundle, rollback, restore_quest_icons=False):
    bundle = bundle.resolve()
    receipt = core.read(bundle/"manifest.json")
    if Path(receipt["source"]).resolve() != CLIENT or bundle.is_relative_to(CLIENT):
        raise ValueError("unexpected client or bundle path")
    if set(receipt["fonts"]) != ORIGINAL.keys() or set(receipt["npk"]) != PLAN.keys():
        raise ValueError("unexpected install footprint")
    files = {Path(folder)/name: data for folder, rows in
             (("Fonts",receipt["fonts"]),("ImagePacks2",receipt["npk"])) for name,data in rows.items()}
    fonts = GameFonts(CLIENT/"Fonts")
    backups = bundle/"before"
    journal = bundle/"deployment.json"
    if core.digest(CLIENT/"Script.pvf") != PVF or core.digest(CLIENT/"dstr.dat") != DSTR:
        raise ValueError("current 100US R23 PVF or system table fingerprint differs")

    with fonts.locked():
        def current(rel):
            return core.digest(CLIENT/rel)

        if journal.exists():
            prior = core.read(journal)
            state = prior["previous_font_state"]
            if not (rollback or restore_quest_icons):
                raise ValueError("deployment already exists; inspect its receipt or roll back")
        else:
            if rollback or restore_quest_icons:
                raise ValueError("no deployment to restore")
            state = fonts.state()
            if state.get("hashes") != fonts.hashes():
                raise ValueError("font manager state does not match current fonts")

        for rel, data in files.items():
            present = current(rel)
            allowed = ({data["before"],data["after"]} if rollback or restore_quest_icons
                       else {data["before"]})
            if (present not in allowed
                    or core.digest(bundle/rel) != data["after"]):
                raise ValueError(f"unexpected live or candidate file: {rel}")
            backup = backups/rel
            if not (rollback or restore_quest_icons):
                if backup.exists():
                    if core.digest(backup) != data["before"]:
                        raise ValueError(f"unexpected existing backup: {rel}")
                else:
                    core.file_copy(CLIENT/rel,backup)
            if core.digest(backup) != data["before"]:
                raise ValueError(f"backup hash mismatch: {rel}")

        def record(status):
            core.atomic(journal, {"status":status,"previous_font_state":state,
                         "utc":datetime.now(timezone.utc).isoformat(),"pvf_unchanged":True,
                         "dstr_unchanged":True,"game_validated":False,
                         "files":{p.as_posix():d for p,d in files.items()}})

        def check_closed():
            if core.client_up():
                raise ValueError("DFO reopened during the file operation")

        def undo():
            for rel, data in files.items():
                check_closed()
                present = current(rel)
                if present == data["after"]:
                    core.file_copy(backups/rel,CLIENT/rel)
                if current(rel) != data["before"]:
                    raise ValueError(f"rollback verification failed: {rel}")
            core.atomic(fonts.root/"state.json",state)

        check_closed()
        if restore_quest_icons:
            rel = Path("ImagePacks2")/QUEST
            if prior["status"] not in {"installed", "quest_icons_restored"}:
                raise ValueError("full installation required for quest icon restore")
            for item, data in files.items():
                if item != rel and current(item) != data["after"]:
                    raise ValueError(f"unrelated installed file changed: {item}")
            if current(rel) == files[rel]["after"]:
                core.file_copy(backups/rel,CLIENT/rel)
            if current(rel) != files[rel]["before"]:
                raise ValueError("quest icon restore verification failed")
            core.atomic(journal, dict(prior, status="quest_icons_restored",
                        quest_icons_restore_utc=datetime.now(timezone.utc).isoformat(),
                        restored_file=rel.as_posix(), restored_sha256=current(rel)))
            print(f"Restored and verified {rel.as_posix()}")
            return
        if rollback:
            undo()
            record("rolled_back")
            print(f"Rolled back {len(files)} files")
            return
        record("pending")
        try:
            for rel, data in files.items():
                check_closed()
                if current(rel) != data["before"]:
                    raise ValueError(f"live file changed before replacement: {rel}")
                core.file_copy(bundle/rel,CLIENT/rel)
                if current(rel) != data["after"]:
                    raise ValueError(f"installed file hash mismatch: {rel}")
            info = next(iter(receipt["fonts"].values()))
            core.atomic(fonts.root/"state.json",dict(family="SimSun",file=info["source_font"],
                        face=0,sha256=info["source_sha256"],outline_only=True,hashes=fonts.hashes()))
        except Exception:
            undo()
            record("rolled_back")
            raise
        record("installed")
        print(f"Installed and verified {len(files)} files")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle", type=Path, required=True)
    action = parser.add_mutually_exclusive_group()
    action.add_argument("--rollback", action="store_true")
    action.add_argument("--restore-quest-icons", action="store_true")
    a = parser.parse_args()
    install(a.bundle, a.rollback, a.restore_quest_icons)
