# -*- coding: utf-8 -*-
"""Generate an allowlisted patch for two Wisdom Simulator scripts from typed cells."""
import argparse
from collections import Counter, defaultdict
import json
from pathlib import Path

from localize_us100_tables import approved, NUMBER
from us100_concordance import CURATED, NAME_KEY


SCRIPTS = ("guideofwisdomsimulator.evt", "guideofwisdomsimulatoritem.evt")
REVIEWED = {
    "Abysmal Sky Tower": "沉入深渊的天空之城",
    "Bloody Grudge": "泣血之恨",
    "Dark Bus of the Bottomless Pit": "无尽地狱黑暗之印",
    "Epic Drop Boost Potion": "史诗掉落率提升药剂",
    "Equipment Reinforcement Ticket": "装备强化券",
    "Harlem": "哈林",
    "Last-ditch Effort": "战无不胜上衣",
    "Mother Nature's Breath": "大自然的呼吸",
    "Normalizing Synchro": "能量主宰",
    "Power Station": "能源中心",
    "Random Mythic Box": "随机神话装备礼盒",
    "Super Undiluted STR/INT Potion": "超级力量/智力原液",
    "Character Damage +100 for 10 min.": "角色伤害 +100，持续10分钟。",
    "Character Damage +15%% for 1 min.": "角色伤害 +15%%，持续1分钟。",
    "Character Speed +10 for 1 min.": "角色速度 +10，持续1分钟。",
    "Item drop rate x2 for 10 min.": "道具掉落率 ×2，持续10分钟。",
    "Character Speed +5(Permanent)": "角色速度 +5（永久）。",
    "Adds 12 inventory slots": "增加12个物品栏格子。",
    "Character Damage +10(Permanent)": "角色伤害 +10（永久）。",
    "Randomly obtain a Mythic item": "随机获得一件神话装备。",
    "Seems like a normal Channel.": "看起来是个普通频道。",
    "Seems to drop Armor more often.": "似乎更容易掉落防具。",
    "Seems to drop Accessories more often.": "似乎更容易掉落首饰。",
    "Seems to drop Special Equipment more often.": "似乎更容易掉落特殊装备。",
}


def build(input_dir, translations, output, evidence, report):
    tokens = {}
    needed = set()
    fields = {"[name]", "[desc]"}
    for i, script in enumerate(SCRIPTS):
        raw = json.loads((input_dir / f"{i:02d}-{script}.tokens.json").read_text(encoding="utf-8"))
        typed = Counter((a["text"], b["text"]) for a, b in zip(raw, raw[1:])
                        if a["type"] == 3 and b["type"] == 6 and a.get("text") in fields)
        tokens[f"event/universal/{script}"] = typed
        needed.update(value.casefold() for _, value in typed)
    references = defaultdict(lambda: defaultdict(list))
    bases = {"equipment", "stackable", "event", "dungeon", "worldmap"}
    with translations.open(encoding="utf-8") as rows:
        for table, key, method, source, target in map(json.loads, rows):
            folded = source.casefold()
            if (folded in needed and Path(table).name.split(".")[0] in bases and
                    (NAME_KEY.search(key) or Path(table).name.startswith("event.")
                     or (table == "string/stackable.uv.str" and key.startswith("booster_category_100Lv_epic_")))):
                references[folded][target].append((method, table, key))
    untouched = []
    plans = {}
    stats = Counter()
    with evidence.open("x", encoding="utf-8") as proof:
        for path, values in tokens.items():
            plan = plans[path] = []
            for (field, source), count in sorted(values.items()):
                hits = references.get(source.casefold(), {})
                strong = {t: keys for t, keys in hits.items() if any(k[0] in CURATED for k in keys)}
                picks = strong or hits
                if source in REVIEWED:
                    dst, how = REVIEWED[source], "reviewed_inline_glossary"
                elif len(picks) == 1:
                    dst, how = next(iter(picks)), "exact_curated_name" if strong else "exact_existing_name"
                else:
                    untouched.append((path, field, source, count, tuple(picks)))
                    continue
                if not approved(source, dst) or NUMBER.findall(source) != NUMBER.findall(dst):
                    raise ValueError(f"inline value has changed format or number: {source!r}: {dst!r}")
                plan.append({"field": field, "source": source, "target": dst, "count": count})
                stats[how] += count
                proof.write(json.dumps([path, field, count, how, source, dst, hits.get(dst, [])[:8]], ensure_ascii=True) + "\n")
    summary = {"scripts": len(tokens), "cells": sum(sum(v.values()) for v in tokens.values()),
               "translated": sum(stats.values()), "untranslated": sum(row[3] for row in untouched),
               "methods": stats, "pending": untouched}
    with report.open("x", encoding="utf-8") as f:
        json.dump(summary, f, indent=2)
    if untouched:
        raise SystemExit(f"Simulator still has {len(untouched)} unmatched values; see {report}")
    with output.open("x", encoding="utf-8") as f:
        json.dump(plans, f, ensure_ascii=True, indent=2)
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    for arg in ("input-dir", "translations", "output", "evidence", "report"):
        p.add_argument("--" + arg, type=Path, required=True)
    build(**vars(p.parse_args()))
