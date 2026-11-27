"""Install or revert the R23-based quest text trial while DFO is closed."""
import argparse
from datetime import datetime, timezone
from pathlib import Path

from us100_quest_act import OUTLINE_FONTS, REL
from us100_quest_ui_install import CLIENT, DSTR, GameFonts, PVF, core


EXPECTED_TRIAL = "8ca10035002111c4f32adceca00b24a4a6113a89016eda2238dcda816d258cc5"
ACT = "5bafe28958d51edf80c0492ca9f135232d977974b66706b3ad4189ead47019dc"
ROUND_TRIP = "ba09578474ef10db772c8352e9e676ace6fa9c761ef0b29504151540aa1ad032"


def deploy(bundle, rollback):
    bundle = bundle.resolve()
    if bundle.is_relative_to(CLIENT.resolve()):
        raise ValueError("trial artifacts must be outside the client")
    live, candidate, backup = CLIENT/"Script.pvf", bundle/"Script.pvf", bundle/"before-Script.pvf"
    record = bundle/"deployment.json"
    fonts = GameFonts(CLIENT/"Fonts")
    with fonts.locked():
        if core.client_up():
            raise ValueError("close DFO before switching Script.pvf")
        current = core.digest(live)
        if current not in {PVF, EXPECTED_TRIAL}:
            raise ValueError("unknown live PVF; refusing to overwrite it")
        if not rollback:
            m = core.read(bundle/"Script.manifest.json")
            if (m["candidate_sha256"] != EXPECTED_TRIAL or not m["verified_rewrap_round_trip"]
                    or core.digest(candidate) != EXPECTED_TRIAL
                    or core.digest(bundle/"reopened"/"00-dnf.uv.str.bin") != ROUND_TRIP):
                raise ValueError("trial package or its round-trip evidence changed")
            if (fonts.hashes() != OUTLINE_FONTS or core.digest(CLIENT/REL) != ACT
                    or core.digest(CLIENT/"dstr.dat") != DSTR):
                raise ValueError("font, Act atlas or original dstr baseline changed")
            if not backup.exists() and current == PVF:
                core.file_copy(live, backup)
        if not backup.is_file() or core.digest(backup) != PVF:
            raise ValueError("no verified R23 backup")
        destination = PVF if rollback else EXPECTED_TRIAL
        if core.client_up() or core.digest(live) != current:
            raise ValueError("DFO reopened or the live PVF changed")
        if current != destination:
            core.file_copy(backup if rollback else candidate, live)
        if core.digest(live) != destination:
            raise ValueError("post-install PVF hash differs")
        core.atomic(record, {"utc":datetime.now(timezone.utc).isoformat(),
                    "status":"rolled_back" if rollback else "installed_pending_game_check",
                    "before":PVF,"live":destination,"client":str(CLIENT),
                    "changed_only":"Script.pvf","game_validated":False})
        print(f"Verified live Script.pvf: {destination}")


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("action", choices=("install", "rollback"))
    p.add_argument("--bundle", type=Path, required=True)
    a = p.parse_args()
    deploy(a.bundle, a.action == "rollback")
