"""Build and verify Quest UI atlas substitutions and outline-only SimSun."""
import argparse
import json
from pathlib import Path
import sys

from us100_ui_atlas import digest, index, member, rebuild_img, rebuild_npk

sys.path.insert(0, str(Path(__file__).resolve().parent / "atelier"))
from game_fonts import BITMAP_TABLES, ORIGINAL, build_face, faces
from fontTools.ttLib import TTFont


QUEST = "sprite_interface_quest.NPK"
TITLE = "sprite_interface_quest_questtitle.NPK"
PLAN = {
    QUEST: {
        "sprite/interface/quest/quest_tag.img": {
            i:i for i in (*range(13), 14, 15, 16, 17, *range(22, 37), 39, 40, 41, 42)
        } | {44:36},
        "sprite/interface/quest/questguide.img": {
            i:i for i in (19,20,26,27,28,29,30,34,36,37,38,39,45,46,47,51,52)
        } | {53:47},
    },
    TITLE: {"sprite/interface/quest/questtitle/questtitle.img": {i:i for i in range(18) if i != 6}},
    "sprite_worldmap_act1.NPK": {
        key: {i:i for i in range(4)}
        for key in ("sprite/worldmap/act1/elvengard.img","sprite/worldmap/selectdungeonslot/elvengard.img")
    },
    "sprite_interface2_worldmap_step_2(area)_arad.NPK": {
        "sprite/interface2/worldmap/step_2(area)/arad/step_2(area)_aradtext.img": {i:i for i in range(8)}
    },
}


def build(args):
    live, out = args.client.resolve(), args.out.resolve()
    if out.is_relative_to(live):
        raise ValueError("candidate must not be written inside the live client")
    fonts, art = out/"Fonts", out/"ImagePacks2"
    out.mkdir(parents=True)
    fonts.mkdir()
    art.mkdir()
    report = {"source": str(live), "installed": False, "game_validated": False, "fonts": {}, "npk": {}}
    donor = args.simsun.resolve()
    if faces(donor)[0] != "SimSun":
        raise ValueError("require the regular SimSun face from the font collection")
    for name, checksum in ORIGINAL.items():
        original = args.original_fonts / name
        if digest(original) != checksum:
            raise ValueError(f"original font fingerprint differs: {name}")
        output = fonts / name
        build_face(donor, 0, original, output, outline_only=True)
        with TTFont(output) as font:
            samples = "传说中的白化变异哥布林完成条件任务奖励格兰之森赫顿玛尔"
            cmap = font.getBestCmap()
            if any(t in font for t in BITMAP_TABLES) or not all(ord(ch) in cmap for ch in samples):
                raise ValueError(f"outline font validation failed: {name}")
        report["fonts"][name] = {"before": digest(live/"Fonts"/name), "after": digest(output),
                                 "source_font": donor.name, "source_sha256": digest(donor), "outline_only": True}
    for file, items in PLAN.items():
        us = live/"ImagePacks2"/file
        cn = args.donor_packs/file
        changes = {name: rebuild_img(member(us,name),member(cn,name if name in index(cn) else
                    name.replace("/selectdungeonslot/","/act1/")),mapping) for name,mapping in items.items()}
        output = art/file
        with output.open("xb") as stream:
            stream.write(rebuild_npk(us, changes))
        old, new = index(us), index(output)
        if list(old) != list(new):
            raise ValueError(f"NPK member inventory changed: {file}")
        for name in old:
            expected = changes.get(name)
            if expected is None:
                expected = member(us,name)
            if member(output,name) != expected:
                raise ValueError(f"NPK content check failed: {file}: {name}")
        report["npk"][file] = {"before": digest(us), "after": digest(output),
                               "localized_frames": sum(map(len,items.values())), "frame_allowlist": items}
    (out/"manifest.json").write_text(json.dumps(report,indent=2),encoding="utf-8")
    print(json.dumps({"fonts": len(report["fonts"]), "npk_files": len(report["npk"]),
                      "frames": sum(row["localized_frames"] for row in report["npk"].values())}))


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    for name in ("client", "out", "original-fonts", "donor-packs", "simsun"):
        p.add_argument("--"+name,type=Path,required=True)
    build(p.parse_args())
