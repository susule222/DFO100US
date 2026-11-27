# -*- coding: utf-8 -*-
"""Exact event-expiration forms. Keep the source date, time and time zone."""
import re


MONTHS = {
    "january": "一", "february": "二", "march": "三", "april": "四", "may": "五", "june": "六",
    "july": "七", "august": "八", "september": "九", "october": "十", "november": "十一", "december": "十二",
}
DATE = rf"(?P<month>{'|'.join(MONTHS)}) (?P<day>\d{{1,2}})(?:st|nd|rd|th)?(?:, (?P<year>20\d{{2}}))?"
TIME = r"(?P<time>\d{1,2}:\d{2})\s*(?P<half>a\.m\.|p\.m\.)"
DATE_FIRST = re.compile(rf"on {DATE}(?:,)? at {TIME}", re.I)
TIME_FIRST = re.compile(rf"at {TIME} on {DATE}\.?", re.I)
UNTIL = re.compile(rf"{TIME} on {DATE}\.?", re.I)
DELETE_PREFIX = {
    "This item will be deleted ": "此道具将于",
    "Emblems from this Box will be deleted from your inventory ": "此礼盒开出的徽章将从物品栏删除，时间为",
    "Emblems from this Box will be deleted ": "此礼盒开出的徽章将于",
    "Emblems from the Boxes will be deleted ": "这些礼盒开出的徽章将于",
    "Generated Emblems will be deleted ": "获得的徽章将于",
    "The generated Emblems are untradable and deleted ": "获得的徽章无法交易，将于",
    "Items from the Chests, if not used, will be deleted ": "礼盒开出的道具若未使用，将于",
    "Emblems from this box can only be used on Untradeable Avatars and will be deleted ": (
        "此礼盒开出的徽章仅可用于无法交易的装扮，将于"
    ),
    "Emblems from this box can only be used on Untradable Avatars and will be deleted ": (
        "此礼盒开出的徽章仅可用于无法交易的装扮，将于"
    ),
    "(The generated items bind to your account, and will be deleted ": "（获得的道具为账号绑定，将于",
    "(The generated scroll binds to your account, and will be deleted ": "（获得的卷轴为账号绑定，将于",
    "These pouches and cards will be deleted ": "这些袋子及卡片将于",
    "Avatar Chests and Extension Tickets will be deleted ": "装扮礼盒及延期券将于",
}
EXPIRE_PREFIX = {
    "Can be used until ": "可使用至",
    "Generated items are issued in untradeable form, and can be used until ": "获得的道具无法交易，可使用至",
    "Transformation slightly increases Reinforcement success chance. Can be used until ": "变身后略微提高强化成功率。可使用至",
    "Items from this Chest can't be traded, and are usable until ": "此礼盒开出的道具无法交易，可使用至",
}


def when(match, date_first):
    date = f"{MONTHS[match['month'].casefold()]}月{match['day']}日"
    if match["year"]:
        date += f"（{match['year']}年）" if date_first else f"，{match['year']}年"
    time = ("上午" if match["half"].casefold() == "a.m." else "下午") + match["time"]
    return date + time if date_first else f"{time}（{date}）"


def translate_expiration(text):
    for start, target in DELETE_PREFIX.items():
        if not text.startswith(start):
            continue
        tail = text[len(start):].removesuffix(")").rstrip(".") + "."
        maintenance = tail.endswith(" after maintenance.")
        if maintenance:
            tail = tail.removesuffix(" after maintenance.") + "."
        for fmt in (DATE_FIRST, TIME_FIRST):
            if match := fmt.fullmatch(tail):
                action = "删除。" if "从物品栏删除" not in target else "。"
                if maintenance:
                    action = "维护后删除。"
                return target + when(match, fmt is DATE_FIRST) + action + ("）" if text.endswith(")") else "")
    for start, target in EXPIRE_PREFIX.items():
        if text.startswith(start):
            tail = text[len(start):].rstrip(".") + "."
            if match := UNTIL.fullmatch(tail):
                return target + when(match, False) + "。"
    return None
