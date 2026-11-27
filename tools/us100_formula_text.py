# -*- coding: utf-8 -*-
"""Literal, reviewed renderers for recurring game descriptions."""
import re

from us100_reviewed_residual import translate_reviewed
from us100_combat_formula import translate_combat
from us100_expiration_formulas import translate_expiration
from us100_korean_visible import korean_visible_line


D = r"(?:[+-]?\d+(?:\.\d+)?|[+-]?%d)"
P = rf"{D}%%?"
RANGE = r"(\d+\s+-\s+\d+)"
TITLE_NAMES = {
    "The Neo: Awakened Ones": "三次觉醒礼包",
    "Reincarnation of Apostles": "使徒转生",
    "Divine Revelation": "神圣启示",
    "Revelation of God": "神之启示",
    "[Special] Welcome to Hell": "[特别]欢迎来到深渊",
    "World's Blessing": "世界的祝福",
    "Pirate's Adventure": "海盗的冒险",
    "Seal Guard": "封印守护者",
    "Valhalla": "英灵殿",
    "Seria's Gift": "赛丽亚的礼物",
}
FORMULAS = [
    (r"Lv\.\s*(\d+|%d)", lambda a: f"等级 {a[1]}"),
    (r"([0-9,]+)\s*pt", lambda a: f"{a[1]} 点"),
    (rf"((?:- )?)STR/INT\s*({P})", lambda a: f"{a[1]}力量/智力 {a[2]}"),
    (rf"((?:- )?)STR/INT\s*({D})", lambda a: f"{a[1]}力量/智力 {a[2]}"),
    (rf"Skill Atk\.\s*({P})(?:\.)?", lambda a: f"技能攻击力 {a[1]}"),
    (rf"Skill Atk\. ({P}) if your combined option levels are (\d+) or higher \((#%d%%#)\)",
     lambda a: f"技能攻击力 {a[1]}（词条总等级达到{a[2]}级时，{a[3]}）"),
    (rf"Additional Skill Atk\. ({P}) for every (\d+) combined option levels",
     lambda a: f"技能攻击力额外 {a[1]}（词条总等级每增加{a[2]}级）"),
    (rf"Skill Atk\. ({P}) for every (\d+) combined option levels of this equipment \((#%d%%#)\)",
     lambda a: f"技能攻击力 {a[1]}（该装备词条总等级每增加{a[2]}级，{a[3]}）"),
    (r"Disabled if equipped with items that are Level (\d+) or below",
     lambda a: f"穿戴{a[1]}级及以下装备时不适用"),
    (rf"Lv\.\s*(\d+) Active Skill Atk\.\s*({P})(?:\.)?", lambda a: f"Lv. {a[1]}主动技能攻击力 {a[2]}"),
    (rf"All classes' Lv\.\s*(\d+) Skill Atk\.\s*({P})(?:\.)?",
     lambda a: f"所有职业Lv. {a[1]}技能攻击力 {a[2]}"),
    (rf"Skill Atk\.\s*({P}) (?:when|while) attacking (Beast|Dragon|Undead|Demon|Human) enemies(?:\.)?",
     lambda a: f"技能攻击力 {a[1]}（攻击{KIND[a[2].lower()]}敌人时）"),
    (rf"Bonus Damage ({P}) on enemies with ({P}) or more HP(?:\.)?",
     lambda a: f"附加伤害 {a[1]}（敌人HP不低于{a[2]}时）"),
    (rf"Bonus Damage ({P}) on enemies with ({P}) or less HP(?:\.)?",
     lambda a: f"附加伤害 {a[1]}（敌人HP不高于{a[2]}时）"),
    (rf"All Atk\. ({P}) for every ({D}) Shadow Resistance \(Up to ({P})\)(?:\.)?",
     lambda a: f"所有攻击力 {a[1]}（每{a[2]}点暗属性抗性叠加一次，最高{a[3]}）"),
    (rf"Additional Attack Damage ({P}) for every ({D}) Shadow Resistance \(Up to ({P})\)(?:\.)?",
     lambda a: f"攻击伤害额外增加 {a[1]}（每{a[2]}点暗属性抗性叠加一次，最高{a[3]}）"),
    (rf"Bonus Damage ({P})(?:\.)?", lambda a: f"附加伤害 {a[1]}"),
    (rf"Additional Critical Damage ({P})(?:\.)?", lambda a: f"暴击伤害额外增加 {a[1]}"),
    (rf"Additional attack damage ({P})(?:\.)?", lambda a: f"攻击伤害额外增加 {a[1]}"),
    (rf"Attacks create a ({P}) chance of Bleeding enemies, additionally damaging them at ({P}) Atk\. every ({D}) sec\. for ({D}) sec\.",
     lambda a: f"攻击时有{a[1]}的概率使敌人出血，以{a[2]}的攻击力每{a[3]}秒造成额外伤害，持续{a[4]}秒。"),
    (rf"(\d+)(?:st|nd|rd|th) slot Talisman skill cooldown ({P}) and Atk\. ({P})",
     lambda a: f"第{a[1]}护石栏技能冷却时间 {a[2]}，技能攻击力 {a[3]}"),
    (rf"Abnormal Status Tolerances ({D}) for every Reinforcement/Amplification level for all enemies in ({D}) px\.",
     lambda a: f"强化/增幅等级每提升一级，范围内敌人的异常状态抗性 {a[1]}，作用范围为{a[2]}像素。",
    ),
]
KIND = {"beast": "野兽类", "dragon": "龙族", "undead": "不死族", "demon": "恶魔族", "human": "人型"}
RULES = [(re.compile(pattern, re.I), render) for pattern, render in FORMULAS]
PREFIX = re.compile(r"^((?:(?:@[a-z0-9]{1,5}@)|(?:<npc::\d+>)|(?:-\s+))*)(.+)$", re.I)
EXACT = {
    "Petit Friends": "迷你伙伴",
    "<Purpose>": "<目标>",
    "<Bit Fighter>": "<像素勇士>",
    "<슈팅! 던파>": "<射击！地下城与勇士>",
    "< DANJI >": "< 丹吉 >",
    "<열파참 : 열번째 파티에 참여하세요! >": "<裂波斩：参加第十场派对！ >",
    "< 열파참 : 열번째 파티에 참여하세요! >": "< 裂波斩：参加第十场派对！ >",
    "<2015 어린이날 쇼콜라 패키지>": "<2015年儿童节巧克力礼包>",
    "< 쉽팜 이벤트 >": "< 绵羊农场活动 >",
    "MUST HAVE (July)": "必备礼包（七月）",
    "MUST HAVE (December)": "必备礼包（十二月）",
    "Creates a chance of reinforcing equipment.": "有一定概率强化装备。",
    "Equip a set of Surfing Arad Avatars, and your town walking animation will be updated.": (
        "穿戴一套阿拉德冲浪装扮后，将更换城镇内的行走动画。"
    ),
    "The updated animation does not reflect your weapon/shield images or Advanced/Awakening effects.": (
        "替换后的动画不会显示武器或盾牌外观，也不会显示转职或觉醒特效。"
    ),
    "<Surfing Arad Package>": "<阿拉德冲浪礼包>",
    "No Clear Cube Fragments or Gold required.": "无需消耗无色小晶块或金币。",
    "Options disabled if Mythic equipment is equipped": "穿戴神话装备时，词条效果不适用",
    "Only the highest value applied among Top, Bracelet, and Earrings": "上衣、手镯和耳环中仅最高数值生效",
    "Shares cooldown with Remy's Touch and Remy's Sparkling Touch.": "与雷米的援助、闪亮的雷米援助共用冷却时间。",
    "Can only be used on Untradable/Untradable (Sealed) titles.": "仅可用于无法交易或无法交易（封装）状态的称号。",
    "These Special Title Beads can only be used on Untradeable (Sealed) titles.": "这些特别称号宝珠仅可用于无法交易（封装）的称号。",
    "This Special Title Bead has a higher chance of adding a Level 2 skill than its normal counterpart.": (
        "此特别称号宝珠比普通宝珠更容易使技能等级提升2级。"
    ),
    "This Bead has a higher chance of adding Level 2 than the normal Bead.": "此宝珠比普通宝珠更容易使技能等级提升2级。",
    "Special Title Beads have a higher chance of being at Level 2 than their normal counterparts.": "特别称号宝珠比普通宝珠更容易使技能等级提升2级。",
    "Every time you use a Bead, your Conversion gauge increases. When it's full, you can use a Bead and add the option you want at Level 2. The Conversion gauge is reset if you move a Bead to a different account through trading, the Auction Hall, mail, etc.": (
        "每次使用宝珠都会增加转换进度。进度满后，可使用宝珠选择所需词条并使技能等级提升2级。"
        "通过交易、拍卖行或邮件等方式将宝珠转移到其他账号时，转换进度将重置。"
    ),
    "Applicable Titles": "适用称号",
    "Forerunner's Enlightenment - Danjin": "先驱者的觉悟 - 土罐",
    "Forerunner's Enlightenment - Siran": "先驱者的觉悟 - 西岚",
    "Forerunner's Enlightenment - Pungjin": "先驱者的觉悟 - 风振",
    "Forerunner's Enlightenment - Kiri": "先驱者的觉悟 - 凯丽",
    "Mighty Apostle - Sirocco": "强大的使徒 - 希洛克",
    "Mighty Apostle - Kasijas": "强大的使徒 - 卡西利亚斯",
    "Mighty Apostle - Hilder": "强大的使徒 - 赫尔德",
    "Mighty Apostle - Cain": "强大的使徒 - 卡恩",
    "Good One's Will [Fire]": "善者的意志 [火]",
    "Good One's Will [Water]": "善者的意志 [水]",
    "Good One's Will [Light]": "善者的意志 [光]",
    "Good One's Will [Shadow]": "善者的意志 [暗]",
    "The obtained Avatars cannot be traded/sundered/combined, but can be dyed.": (
        "获得的装扮无法交易、分解或合成，但可以染色。"
    ),
    "Generated Weapon Avatars can't be traded, disassembled, dyed, or crafted.": (
        "获得的武器装扮无法交易、分解、染色或制作。"
    ),
}
BEAD_LINE = re.compile(
    rf"Use on (.+?) titles to create a chance of adding a Level {RANGE} skill for the corresponding Advanced class at Level {RANGE}\. "
    rf"\(Certain skills may be excluded\. Level {RANGE} skills for Side Story characters\.\)"
)
BEAD_LIMIT = re.compile(
    rf"\(Certain skills may be excluded\. (?:Adds )?(?:Skills at )?Level {RANGE} "
    r"(?:skills for(?: the)? Side Story characters|are applied to the Side Story characters)\.\)"
)
BEAD_VARIANT = re.compile(
    rf"(?:Creates a chance of adding a Level {RANGE} skill for the corresponding (?:character|Advanced) class "
    rf"at Level {RANGE} (?:when used on|to) (.+?) titles|"
    rf"Can be used on (.+?) titles for a chance of adding a Level {RANGE} skill for the corresponding character "
    rf"class at Level {RANGE})\."
)
BEAD_INVERSE = re.compile(
    rf"(Creates a chance of adding|Randomly adds) a Level {RANGE} skill at Character Level {RANGE} for "
    r"the set (?:Advanced class|character class) when used on (.+?) titles\."
)
BEAD_GOD = re.compile(
    rf"Use on Revelation of God Titles to create a chance of adding (\+\d+(?:~\+\d+)?) to a Level {RANGE} skill "
    rf"for the corresponding Advancement\. \(Certain skills may be excluded\. Level {RANGE} skills can be selected for Side Story characters\.\)"
)
BEAD_SEALED = re.compile(r"Can only be used on Untradeable(?:/Untradeable)? \(Sealed\) (.+?) titles\.")
FATIGUE = re.compile(
    r"Restores (\d+) Fatigue\. \(Can be used once a day\. This item does not share cooldown with other "
    r"Fatigue (?:Healing Elixirs|Recovery Potions)\.\)"
)


def translate_formula(text):
    if text in EXACT:
        return EXACT[text]
    if reviewed := translate_reviewed(text) or translate_combat(text) or translate_expiration(text) or korean_visible_line(text):
        return reviewed
    if (m := BEAD_LINE.fullmatch(text)) and (name := TITLE_NAMES.get(m[1])):
        return (f"用于{name}称号时，有一定概率使对应转职的{m[2]}级技能提升{m[3]}级。"
                f"（部分技能不适用；外传职业适用{m[4]}级技能。）")
    if m := BEAD_LIMIT.fullmatch(text):
        return f"（部分技能不适用；外传职业适用{m[1]}级技能。）"
    if m := BEAD_VARIANT.fullmatch(text):
        first, second, title = (m[1], m[2], m[3]) if m[3] is not None else (m[5], m[6], m[4])
        if name := TITLE_NAMES.get(title):
            return f"用于{name}称号时，有一定概率使对应职业的{first}级技能提升{second}级。"
    if (m := BEAD_INVERSE.fullmatch(text)) and (name := TITLE_NAMES.get(m[4])):
        chance = "有一定概率" if m[1].startswith("Creates") else "随机"
        return f"用于{name}称号时，{chance}使技能等级提升{m[2]}级，适用于指定职业的{m[3]}级技能。"
    if m := BEAD_GOD.fullmatch(text):
        return f"用于神之启示称号时，有一定概率使技能等级{m[1]}，适用于对应转职的{m[2]}级技能。（部分技能不适用；外传职业可选择{m[3]}级技能。）"
    if (m := BEAD_SEALED.fullmatch(text)) and (name := TITLE_NAMES.get(m[1])):
        limit = "无法交易或无法交易（封装）" if "/" in text else "无法交易（封装）"
        return f"仅可用于{limit}状态的{name}称号。"
    if m := FATIGUE.fullmatch(text):
        return f"恢复{m[1]}点疲劳值。（每天可使用一次，不与其他疲劳恢复药剂共用冷却时间。）"
    for pattern, render in RULES:
        if match := pattern.fullmatch(text):
            return render(match)
    if (split := PREFIX.fullmatch(text)) and split[1]:
        if result := translate_formula(split[2]):
            return split[1] + result
    return None
