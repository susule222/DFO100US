# -*- coding: utf-8 -*-
"""Check machine drafts for known DNF semantic failures before packing."""
import argparse
from collections import Counter
import json
from pathlib import Path
import re

from us100_grouped_llm import LINE, candidate as whole_candidate, quality, units
from us100_formula_text import translate_formula
from us100_local_llm import BOUNDARY, NUMBER


CANON = (
    ("Mind Stimulant Potion", "精神刺激灵药", ("精神刺激药剂", "心灵刺激药水", "精神刺激剂")),
    ("Soul Reaper", "弑魂", ("灵魂剪裁者", "灵魂收割者", "灵魂收割人")),
    ("X-1 Extruder", "X-1压缩量子炮", ("X-1 压缩量子炮", "X-1 挤出器", "X-1挤出机", "X-1 压出设备")),
    ("X-2 Helios", "X-2太阳神光炮", ("X-2 太阳神光炮", "X-2 赫利俄斯", "X-2赫利俄斯")),
    ("Charge Laser Rifle", "蓄电激光炮", ("电荷激光步枪", "充电激光枪", "充能激光步枪")),
    ("Equipment Option Control", "装备属性指令", ("装备选项控制", "装备选项指令", "设备选项控制", "装备属性操作")),
    ("Super Armor", "霸体", ("超级护甲", "超级装甲", "超级铠甲")),
)
SHARES_CD = re.compile(r"\bshares? (?:a |the )?cooldown with\b", re.I)
NEGATIVE_SHARES_CD = re.compile(r"\b(?:does not|doesn't|cannot|can't|not) share (?:a |the )?cooldown with\b", re.I)
NEGATIVE_ZH_COOLDOWN = re.compile(r"(?:不|未)(?:会|与|和)?[^。\n]{0,32}(?:共用|共享|共通).{0,12}冷却")
STACK_MAX = re.compile(r"\b(?:max stacks?|stacks? up to|maximum of \d+ stacks?)\b", re.I)
BAD_PERCENT_UNIT = re.compile(r"\d\s*%%?\s*(?:分(?:钟)?|秒|小时|天|级|等级|点额外)(?![之以])")
SOURCE_DURATION = re.compile(r"\bfor\s+(\d+(?:\.\d+)?)\s*(seconds?|secs?|minutes?|mins?|hours?|hrs?)\b", re.I)
TIME_UNIT = {"sec": r"(?:秒|s\b)", "min": r"(?:分钟|分(?:钟)?|m\b)", "hou": r"(?:小时|时|h\b)", "hrs": r"(?:小时|时|h\b)", "hr": r"(?:小时|时|h\b)"}
COLLECT = re.compile(r"^Collect (\d+) Seals of (Empress Skardi|Emperor Helm)\.$")
ALL_SKILL_CD = re.compile(r"^All skill cooldown ([+-]\d+(?:\.\d+)?)%% for (\d+) min\.$", re.I)
SHORT_PUNCT = re.compile(r"(技能攻击力|物理攻击力|魔法攻击力|独立攻击力|冷却时间)。(\s*)(?=[+-])")
UNLOCALIZED_MECHANICS = re.compile(
    r"\b(?:avatar(?:s)?|dungeon(?:s)?|movement speed|casting speed|cooldown|cannot be|untradeable|untradable|"
    r"disassembled|skill attack|adventurer fame|clear cube fragments?|set effect)\b", re.I
)
CHAT_COMMAND_CONTEXT = re.compile(r"\b(?:enter|type|input)\b.{0,90}\b(?:chat|command)\b", re.I)
QUOTED_ASCII = re.compile(r'[“"]([A-Za-z0-9_ -]{1,25})[”"]')
SIGNED_STAT = re.compile(
    r"((?:技能攻击力|物理攻击力|魔法攻击力|独立攻击力|所有攻击力|附加伤害|暴击伤害|攻击伤害|"
    r"攻击速度|移动速度|施放速度|命中率|回避率|暴击率|冷却时间|力量|智力|体力|精神|"
    r"属性强化|属性抗性|物理防御力|魔法防御力|HP上限|MP上限)(?:额外)?)"
    r"(?:\s*)(增加|提升|提高|降低|减少|下降|缩短)\s*(\d+(?:[.,]\d+)*)(?![\d.])"
)
ORDINARY_NUMBER = re.compile(r"(?:[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+)(?:\.[0-9]+)?")


def canonical_number(value):
    if not ORDINARY_NUMBER.fullmatch(value):
        return value
    value = value.replace(",", "")
    integer, dot, fraction = value.partition(".")
    return (integer + dot + fraction.rstrip("0")).removesuffix(".")


def restore_number_style(source, target):
    original = NUMBER.findall(source)
    current = NUMBER.findall(target)
    if original == current or list(map(canonical_number, original)) != list(map(canonical_number, current)):
        return target
    supplied = iter(original)
    return NUMBER.sub(lambda _: next(supplied), target)


def restore_literal_stat_signs(source, candidate):
    source_numbers = list(NUMBER.finditer(source))
    target_numbers = list(NUMBER.finditer(candidate))
    if [m[0] for m in source_numbers] != [m[0] for m in target_numbers]:
        return candidate
    sign_at = {}
    for old, new in zip(source_numbers, target_numbers):
        if old.start() > 0 and source[old.start() - 1] in "+-":
            sign_at[new.start()] = source[old.start() - 1]

    def replace(m):
        sign = sign_at.get(m.start(3))
        direction = "+" if m[2] in ("增加", "提升", "提高") else "-"
        return f"{m[1]} {sign}{m[3]}" if sign == direction else m[0]

    return SIGNED_STAT.sub(replace, candidate)


def known_line(source):
    if (m := COLLECT.fullmatch(source)):
        person = {"Empress Skardi": "斯卡迪女王", "Emperor Helm": "赫仑皇帝"}[m[2]]
        return f"收集{m[1]}个{person}的印章。"
    if (m := ALL_SKILL_CD.fullmatch(source)):
        return f"所有技能冷却时间 {m[1]}%%，持续{m[2]}分钟。"
    if source == "Shares cooldown with Mind Stimulant Potions.":
        return "与精神刺激灵药共用冷却时间。"
    if source == "Reach 80% Conquest Rate":
        return "征服率达到80%。"
    return None


def repair(source, proposed):
    left, right = LINE.split(source), LINE.split(proposed)
    if len(left) == len(right) and left[1::2] == right[1::2]:
        for i in range(0, len(left), 2):
            if (replacement := known_line(left[i].strip()) or translate_formula(left[i].strip())):
                prefix = left[i][:len(left[i]) - len(left[i].lstrip())]
                suffix = left[i][len(left[i].rstrip()):]
                right[i] = prefix + replacement + suffix
        proposed = "".join(right)
    if re.search(r"\bavatars?\b", source, re.I):
        proposed = proposed.replace("时装", "装扮")
        proposed = re.sub(r"(?:克隆稀有装扮|稀有装扮克隆)", "稀有克隆装扮", proposed)
    if re.search(r"(?:\bAvatars and Creatures\b|\bCreature (?:Egg|Box|Chest|Skin)\b)", source, re.I):
        proposed = re.sub(r"装扮([、和及])(?:怪物|生物)", r"装扮\1宠物", proposed)
        proposed = re.sub(r"(?:怪物|生物)(蛋|礼盒|皮肤)", r"宠物\1", proposed)
    if re.search(r"\bcasting speeds?\b", source, re.I):
        proposed = proposed.replace("施法速度", "施放速度")
    if re.search(r"\bclear cube fragments?\b", source, re.I):
        proposed = re.sub(r"(?:清除立方体碎片|无色方块碎片|无色立方体碎片)", "无色小晶块", proposed)
    if re.search(r"\bstar energ(?:y|ies)\b", source, re.I):
        proposed = re.sub(r"(?:星能量|星能效果|星能)(?!量)", "星辰能量", proposed)
    if re.search(r"\badventurer fame\b", source, re.I):
        proposed = proposed.replace("冒险家声望", "冒险家名望")
    if re.search(r"\b(?:Fragmented Abyss)\b", source, re.I):
        proposed = proposed.replace("碎片深渊", "深渊之鳞")
    for src, localized, observed in CANON:
        if src.casefold() in source.casefold():
            for wrong in observed:
                proposed = proposed.replace(wrong, localized)
    if re.search(r"\bBottom\s+\[Type [A-Z]\]$", source):
        proposed = re.sub(r"下位(?=\s*\[[A-Z]型\]$)", "下装", proposed)
    proposed = restore_number_style(source, proposed)
    return SHORT_PUNCT.sub(r"\1\2", restore_literal_stat_signs(source, proposed))


def problem(source, proposed):
    if source == proposed:
        return "unchanged_text"
    if error := quality(source, proposed):
        return error
    if BAD_PERCENT_UNIT.search(proposed):
        return "percentage_attached_to_wrong_unit"
    for value, unit in SOURCE_DURATION.findall(source):
        expected = TIME_UNIT[unit.casefold()[:3]]
        if not re.search(rf"(?<![\d.]){re.escape(value)}\s*{expected}", proposed, re.I):
            return "duration_attached_to_wrong_value"
    visible = BOUNDARY.sub("", proposed)
    if CHAT_COMMAND_CONTEXT.search(source):
        for command in QUOTED_ASCII.findall(source):
            visible = visible.replace(command, "")
    if UNLOCALIZED_MECHANICS.search(visible):
        return "visible_mechanical_term_left_in_english"
    if SHARES_CD.search(source):
        if not (
            re.search(r"(?:共用|共享|共通).{0,12}冷却", proposed)
            or re.search(r"冷却.{0,12}(?:共用|共享|共通)", proposed)
        ) or bool(NEGATIVE_SHARES_CD.search(source)) != bool(NEGATIVE_ZH_COOLDOWN.search(proposed)):
            return "shared_cooldown_changed"
    if STACK_MAX.search(source) and not (re.search(r"叠加|层|重叠", proposed) and re.search(r"最多|上限|最大", proposed)):
        return "max_stack_condition_missing"
    for src, localized, _ in CANON:
        if src.casefold() in source.casefold() and localized not in proposed:
            return "known_dnf_term_not_preserved"
    return ""


def filter_overlay(source_file, out, report, rejected, fallback=None, pending_values=None):
    totals = Counter()
    source_data = json.loads(source_file.read_text(encoding="utf-8"))
    retained = {}
    with rejected.open("x", encoding="utf-8") as ledger:
        for source, proposed in source_data.items():
            candidate = repair(source, proposed)
            if error := problem(source, candidate):
                totals[error] += 1
                ledger.write(json.dumps([error, source, candidate], ensure_ascii=True) + "\n")
            else:
                retained[source] = candidate
                totals["accepted"] += 1
                totals["deterministically_repaired"] += int(proposed != candidate)
        if fallback:
            for source, proposed in json.loads(fallback.read_text(encoding="utf-8")).items():
                if source in retained:
                    continue
                candidate = repair(source, proposed)
                if not problem(source, candidate):
                    retained[source] = candidate
                    totals["accepted"] += 1
                    totals["retained_from_previous_validated_overlay"] += 1
        if pending_values:
            with pending_values.open(encoding="utf-8") as pending:
                for row in map(json.loads, pending):
                    table, _, _, source = row
                    if source in retained:
                        continue
                    fixed = whole_candidate(source, units(source, Path(table).name.split(".", 1)[0]), {})
                    if fixed and not problem(source, fixed):
                        retained[source] = fixed
                        totals["accepted"] += 1
                        totals["added_by_exact_formula"] += 1
    with out.open("x", encoding="utf-8") as out_file:
        json.dump(retained, out_file, ensure_ascii=True)
    with report.open("x", encoding="utf-8") as detail:
        json.dump(dict(totals), detail, indent=2)
    print(json.dumps(totals, indent=2))


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    for key in ("source-file", "out", "report", "rejected"):
        p.add_argument("--" + key, type=Path, required=True)
    p.add_argument("--fallback", type=Path)
    p.add_argument("--pending-values", type=Path)
    filter_overlay(**vars(p.parse_args()))
