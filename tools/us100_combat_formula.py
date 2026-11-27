# -*- coding: utf-8 -*-
"""Number-preserving, explicit formulas for repeated US100 combat tooltips."""
import re


NUM = r"(?:[+-]?\d+(?:\.\d+)?)"
PCT = rf"{NUM}%%"
CORE_STAT = {
    "Physical/Magical/Independent Atk.": "物理/魔法/独立攻击力",
    "All Atk.": "所有攻击力",
    "Physical/Magical Critical Chances": "物理/魔法暴击率",
    "Physical/Magical Defense": "物理/魔法防御力",
    "Additional Critical Damage": "暴击伤害额外增加",
    "Additional Attack Damage": "攻击伤害额外增加",
    "Attack/Movement Speeds": "攻击/移动速度",
    "Casting/Movement Speeds": "施放/移动速度",
    "Attack/Movement/Casting Speed": "攻击/移动/施放速度",
    "Casting Speed": "施放速度",
    "All Elemental Damage": "所有属性强化",
    "Shadow Resistance": "暗属性抗性",
    "Light Resistance": "光属性抗性",
    "Fire Resistance": "火属性抗性",
    "Water Resistance": "冰属性抗性",
}
DOT = re.compile(
    rf"Attacks create a ({PCT}) chance of (Bleeding|Poisoning|Burning|Shocking) enemies, additionally damaging them "
    rf"at ({NUM}(?:%%?)?) Atk\. every ({NUM}) sec\. for ({NUM}) sec\."
)
BONUS = re.compile(rf"(?:Bonus Damage ({PCT}) on|Additional ({PCT}) damage on) (Bleeding|Poisoned|Burned|Shocked) enemies")
CORE_CHANGE = re.compile(
    rf"({'|'.join(re.escape(s) for s in CORE_STAT)}) ({NUM}(?:%%)?)"
)
FOUR_STATS = re.compile(
    rf"(Upon successfully attacking, has a|Has a) ({PCT}) chance to increase STR/INT/VIT/SPR by ({NUM}) for "
    rf"({NUM}) seconds(?: upon successful attacks)?\. \(Cooldown:? ({NUM}) sec\.?\)"
)
THREE_SPEEDS = re.compile(
    rf"(Upon Casting, has a|Has a) ({PCT}) chance to increase Attack/Movement/Casting Speed by ({PCT}) for "
    rf"({NUM}) seconds(?: upon casting)?\. \(Cooldown:? ({NUM}) sec\.?\)"
)
TWO_STATS = re.compile(
    rf"(Attacks create|Casting creates) a ({PCT}) chance of (STR|INT) ({NUM}), (VIT|SPR) ({NUM}), "
    rf"and (Attack|Casting)/Movement Speeds ({PCT}) for ({NUM}) sec\. at the cost of ({NUM}) Clear Cube Fragment\."
)
KIND = {"Bleeding": "出血", "Poisoning": "中毒", "Poisoned": "中毒", "Burning": "灼伤", "Burned": "灼伤",
        "Shocking": "感电", "Shocked": "感电"}
LOCKED = re.compile(
    r"(?:Cannot|Can't) be (?:reinforced|upgraded), amplified, refined, disassembled, gifted, or stored in the storage\.",
    re.I,
)
ELEMENT_INFUSION = re.compile(
    rf"Bestows your weapon with (Water|Fire|Light|Shadow) Attribute for ({NUM}) seconds at ({NUM})-second intervals "
    rf"at the cost of ({NUM}) Heavenly Pearl\."
)
ATK_CD = re.compile(
    rf"Attacks create a ({PCT}) chance of increasing Atk\. ({PCT}) for Level ({NUM}) skills for ({NUM}) seconds "
    rf"at the cost of ({PCT}) HP\. \(Cooldown: ({NUM}) sec\. Canceled when this item is unequipped\.\)"
)
ELEMENT_BUFF = re.compile(
    rf"Increases All Elemental Damage ({NUM}) for ({NUM}) seconds every ({NUM}) seconds at the cost of "
    rf"({NUM}) Clear Cube Fragment while equipped\."
)
BLEED_FIELD = re.compile(rf"Bleeds random targets within ({NUM}) px every ({NUM}) sec\. for ({NUM}) sec\.")
CAUSE = re.compile(rf"Attacks create a ({PCT}) chance of increasing STR ({NUM}), INT ({NUM}), and Light Damage ({NUM}) for ({NUM}) seconds\.")
MULTIPLIER_EXP = re.compile(
    rf"Increases Dungeon Clear EXP by ({NUM}) times for ({NUM}) minutes\. This effect persists even if you die in dungeons, "
    r"move to the town, or relog in, and stacks with those of EXP events\."
)
CUBES = re.compile(rf"Automatically consumes ({NUM}) Cube Fragments? every ({NUM}) (?:sec\.|seconds)\.?")
ARMOR_HIT = re.compile(rf"Restores this equipment's Super Armor Hit count ({NUM}) every ({NUM}) sec\.")
STATUS_HASTE = re.compile(
    rf"Attacking (Bleeding|Poisoned|Shocked|Burned) enemies increases Attack Speed ({PCT}) for ({NUM}) sec\."
    rf"(?: \(Max Stacks: ({NUM})\))?"
)
STATUS_AREA = re.compile(
    rf"(?:Randomly Bleeds targets|Bleeds (?:enemies|random targets)) in ({NUM}) px every ({NUM}) sec\."
    rf"(?: for ({NUM}) sec\.)?"
)
HP_DRAIN = re.compile(
    rf"When your HP drops to ({PCT}) or lower, Your HP decreases (-?\d+(?:\.\d+)?) every ({NUM}) seconds\."
)
STAR = re.compile(
    rf"Star Energy increases Additional Attack Damage ({PCT}) for ({NUM}) sec\. \(Max Stacks: ({NUM})\)"
)
ELEM_EQUIPPED = re.compile(
    rf"All Elemental Damage ({NUM}) every ({NUM}) seconds at the cost of ({NUM}) Clear Cube Fragments? while equipped"
)
FROST = re.compile(
    rf"Attacks create a ({PCT}) chance of casting up to ({NUM}) Frost Spikes on random enemies within ({NUM}) px\."
    rf"(?: \(Cooldown: ({NUM}) sec\.\))?"
)

EXACT = {
    "Increases the corresponding Piece count +1 in the Monster Collection.": "怪物图鉴中对应碎片的数量 +1。",
    "The generated weapon has Reinforcement Level 12 and Refining Level 8.": "获得的武器强化等级为12、锻造等级为8。",
    "Creates the following effects if you enter a dungeon without party members.": "单人进入地下城时，适用下列效果。",
    "Creates the following effects if you enter a dungeon with 2 or more party members.": (
        "以2人或以上队伍进入地下城时，适用下列效果。"
    ),
    "(All options canceled when unequipped.)": "（卸下时解除所有词条效果。）",
    "Can't be reinforced, amplified, disassembled, gifted, Transcended, Engraved, or Inherited.": (
        "无法强化、增幅、分解、赠送、跨界、铭刻或继承。"
    ),
    "*Multiple +%% damage bonus does not stack. Only the highest value of bonus applies. (Not applicable to Critical Damage)": (
        "*多项 +%% 的伤害加成不叠加，仅最高值生效。（不适用于暴击伤害。）"
    ),
    "Color Cube Fragments: Add their elemental attribute to your weapon.": "有色小晶块：为武器附加对应的属性。",
}


def translate_combat(text):
    if text in EXACT:
        return EXACT[text]
    if m := DOT.fullmatch(text):
        return (f"攻击时有{m[1]}的概率使敌人{KIND[m[2]]}，以{m[3]}的攻击力每{m[4]}秒造成一次额外伤害，"
                f"持续{m[5]}秒。")
    if m := BONUS.fullmatch(text):
        return f"附加伤害 {m[1] or m[2]}（对{KIND[m[3]]}的敌人生效）。"
    if m := CORE_CHANGE.fullmatch(text):
        return f"{CORE_STAT[m[1]]} {m[2]}"
    if m := FOUR_STATS.fullmatch(text):
        return f"攻击命中时，有{m[2]}的概率使力量/智力/体力/精神增加{m[3]}点，持续{m[4]}秒。（冷却时间：{m[5]}秒。）"
    if m := THREE_SPEEDS.fullmatch(text):
        return f"施放时，有{m[2]}的概率使攻击/移动/施放速度增加{m[3]}，持续{m[4]}秒。（冷却时间：{m[5]}秒。）"
    if m := TWO_STATS.fullmatch(text):
        trigger = "攻击" if m[1].startswith("Attacks") else "施放"
        main = {"STR": "力量", "INT": "智力"}[m[3]]
        minor = {"VIT": "体力", "SPR": "精神"}[m[5]]
        speed = "攻击" if m[7] == "Attack" else "施放"
        return (f"{trigger}时有{m[2]}的概率使{main} {m[4]}、{minor} {m[6]}、{speed}/移动速度 {m[8]}，"
                f"持续{m[9]}秒，消耗{m[10]}个无色小晶块。")
    if LOCKED.fullmatch(text):
        return "无法强化、增幅、锻造、分解、赠送或存入仓库。"
    if m := ELEMENT_INFUSION.fullmatch(text):
        attr = {"Water": "冰", "Fire": "火", "Light": "光", "Shadow": "暗"}[m[1]]
        return f"为武器附加{attr}属性，持续{m[2]}秒，每隔{m[3]}秒触发一次，消耗{m[4]}个天界珍珠。"
    if m := ATK_CD.fullmatch(text):
        return (f"攻击时有{m[1]}的概率使技能攻击力 {m[2]}（适用于{m[3]}级技能），持续{m[4]}秒，"
                f"消耗{m[5]}的HP。（冷却时间：{m[6]}秒；卸下该装备时解除。）")
    if m := ELEMENT_BUFF.fullmatch(text):
        return f"穿戴时，所有属性强化 {m[1]}，持续{m[2]}秒，每{m[3]}秒触发一次，消耗{m[4]}个无色小晶块。"
    if m := BLEED_FIELD.fullmatch(text):
        return f"使{m[1]}像素范围内随机目标出血，每隔{m[2]}秒触发一次，持续{m[3]}秒。"
    if m := CAUSE.fullmatch(text):
        return f"攻击时有{m[1]}的概率使力量 {m[2]}、智力 {m[3]}、光属性强化 {m[4]}，持续{m[5]}秒。"
    if m := MULTIPLIER_EXP.fullmatch(text):
        return (f"地下城通关经验值提升至{m[1]}倍，持续{m[2]}分钟。"
                "在地下城阵亡、返回城镇或重新登录后仍有效，且可与经验值活动叠加。")
    if m := CUBES.fullmatch(text):
        return f"自动消耗{m[1]}个小晶块，每{m[2]}秒触发一次。"
    if m := ARMOR_HIT.fullmatch(text):
        return f"恢复该装备的霸体承受攻击次数 {m[1]}，每{m[2]}秒触发一次。"
    if m := STATUS_HASTE.fullmatch(text):
        stack = f"（最多叠加{m[4]}层。）" if m[4] else ""
        return f"攻击{KIND[m[1]]}状态的敌人时，攻击速度 {m[2]}，持续{m[3]}秒。{stack}"
    if m := STATUS_AREA.fullmatch(text):
        selected = "内随机目标" if "random" in text.casefold() else "内敌人"
        duration = f"，持续{m[3]}秒" if m[3] else ""
        return f"使{m[1]}像素范围{selected}出血，每{m[2]}秒触发一次{duration}。"
    if m := HP_DRAIN.fullmatch(text):
        modifier = f" {m[2]}" if m[2].startswith("-") else f"减少{m[2]}点"
        return f"HP降至{m[1]}或以下时，自身HP{modifier}，每{m[3]}秒触发一次。"
    if m := STAR.fullmatch(text):
        return f"星辰能量使攻击伤害额外增加 {m[1]}，持续{m[2]}秒。（最多叠加{m[3]}层。）"
    if m := ELEM_EQUIPPED.fullmatch(text):
        return f"穿戴时，所有属性强化 {m[1]}，每{m[2]}秒触发一次，消耗{m[3]}个无色小晶块。"
    if m := FROST.fullmatch(text):
        cd = f"（冷却时间：{m[4]}秒。）" if m[4] else ""
        return f"攻击时有{m[1]}的概率发动最多{m[2]}个冰刺，攻击{m[3]}像素范围内的随机敌人。{cd}"
    return None
