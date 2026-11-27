# -*- coding: utf-8 -*-
"""Prove a limited set of numeric reorders without accepting untyped values."""
import re


LITERAL = re.compile(r"\d+(?:[.,]\d+)*")
VALUE = r"(?P<n>[+-]?\d+(?:\.\d+)?)"
SIGNED_VALUE = r"(?P<n>[+-]\d+(?:\.\d+)?)"
PERCENT = VALUE + r"\s*(?P<p>%%?)"
SIGNED_PERCENT = SIGNED_VALUE + r"\s*(?P<p>%%?)"
EN_TIME = r"(?P<u>seconds?|secs?|minutes?|mins?|hours?|hrs?)\b"
ZH_TIME = r"(?P<u>秒|分钟|小时)"
CODE = re.compile(r"%(?!%|\s|$)[A-Za-z]|@[A-Za-z0-9]+@|<[^>]+>|\{[^}]+\}|#target_name")
ESCAPES = re.compile(r"(\\(?:br|[nrt]))")
LITERAL_SIGN = re.compile(r"([+-])\s*(\d+(?:[.,]\d+)*)")
ROLE_PATTERNS = [
    ("duration", rf"\bfor\s+{VALUE}\s*{EN_TIME}", rf"(?:持续\s*{VALUE}\s*{ZH_TIME})"),
    ("interval", rf"\bevery\s+{VALUE}\s*{EN_TIME}", rf"(?:每隔|每)\s*{VALUE}\s*{ZH_TIME}"),
    ("cooldown", rf"\bCooldown:\s*{VALUE}\s*{EN_TIME}", rf"冷却(?:时间)?[：:]?\s*{VALUE}\s*{ZH_TIME}"),
    ("max_stacks", rf"\bMax Stacks:\s*{VALUE}\b", rf"(?:最多叠加|最大叠加层数[：:]?)\s*{VALUE}\s*层"),
    ("chance", rf"{PERCENT}\s+chance\b", rf"{PERCENT}\s*(?:的)?(?:概率|几率)"),
    ("radius", rf"(?:within|in)\s*(?:a )?{VALUE}\s*px\b", rf"{VALUE}\s*像素(?:的)?范围"),
    ("cube_clear", rf"\b(?:consumes?|at the cost of)\s+{VALUE}\s+Clear Cube Fragments?\b",
     rf"消耗\s*{VALUE}\s*(?:个|块)?无色小晶块"),
    ("cube_generic", rf"\b(?:consumes?|at the cost of)\s+{VALUE}\s+Cube Fragments?\b",
     rf"消耗\s*{VALUE}\s*(?:个|块)?小晶块"),
    ("attack_speed", rf"\bAttack Speed\s+(?:by )?{SIGNED_PERCENT}", rf"(?:攻击速度|攻速)\s*{SIGNED_PERCENT}"),
    ("cast_speed", rf"\bCasting Speed\s+(?:by )?{SIGNED_PERCENT}", rf"施放速度\s*{SIGNED_PERCENT}"),
    ("movement_speed", rf"\bMovement Speed\s+(?:by )?{SIGNED_PERCENT}", rf"移动速度\s*{SIGNED_PERCENT}"),
    ("skill_attack", rf"\bSkill Atk\.\s+(?:by )?{SIGNED_PERCENT}", rf"技能攻击力\s*{SIGNED_PERCENT}"),
    ("all_element", rf"\bAll Elemental Damage\s+{SIGNED_VALUE}\b", rf"所有属性强化\s*{SIGNED_VALUE}(?![\d.%])"),
]
PAIRS = [(role, re.compile(en, re.I), re.compile(zh)) for role, en, zh in ROLE_PATTERNS]


def unit(value):
    if not value:
        return ""
    if value == "秒" or value.casefold().startswith("sec"):
        return "second"
    if value == "分钟" or value.casefold().startswith("min"):
        return "minute"
    return "hour"


def bindings(text, chinese):
    covered = {}
    roles = set()
    for role, en, zh in PAIRS:
        for match in (zh if chinese else en).finditer(text):
            info = match.groupdict()
            number = info["n"]
            start, end = match.span("n")
            if number.startswith(("+", "-")):
                start += 1
            if (start, end) in covered or role in roles:
                return None
            roles.add(role)
            covered[start, end] = (role, number, info.get("p") or "", unit(info.get("u")))
    if set(covered) != {m.span() for m in LITERAL.finditer(text)}:
        return None
    return frozenset(covered.values())


def equivalent_line(source, target):
    if CODE.search(source) or CODE.search(target) or "\\" in source or "\\" in target:
        return False
    before = bindings(source, False)
    return bool(before and len(before) >= 2 and before == bindings(target, True))


def equivalent_numeric_roles(source, target):
    src, dst = ESCAPES.split(source), ESCAPES.split(target)
    if len(src) != len(dst) or src[1::2] != dst[1::2]:
        return False
    verified_reorder = False
    for left, right in zip(src[::2], dst[::2]):
        if LITERAL.findall(left) == LITERAL.findall(right) and LITERAL_SIGN.findall(left) == LITERAL_SIGN.findall(right):
            continue
        if not equivalent_line(left, right):
            return False
        verified_reorder = True
    return verified_reorder
