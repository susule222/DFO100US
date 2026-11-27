# -*- coding: utf-8 -*-
"""Resolve mechanical tail clauses without giving a model their numeric slots."""
import re


N = r"\d+(?:\.\d+)?"
UNIT = r"(?:seconds?|secs?\.?|minutes?|mins?\.?|hours?|hrs?\.?)"
END = r"(?=\s*(?:$|[.,;()]|at the cost\b|every\s+\d|when\b|while\b))"
TAILS = re.compile(
    rf"(?P<duration>\bfor (?P<d>{N})\s*(?P<du>{UNIT})){END}"
    rf"|(?P<interval>\bevery (?P<i>{N})\s*(?P<iu>{UNIT})){END}"
    rf"|(?P<cost>\bat the cost of (?P<c>{N})(?P<pct>%%?)?\s+(?P<thing>Clear Cube Fragments?|HP|MP)){END}",
    re.I,
)
TEXT = re.compile(r"[A-Za-z]{2}")


def unit(english):
    name = english.casefold()
    return "秒" if name.startswith("sec") else "分钟" if name.startswith("min") else "小时"


def tails(text):
    """Yield exact source substrings paired with a translation, if inferable."""
    cursor = 0
    for m in TAILS.finditer(text):
        if not TEXT.search(text[:m.start()]):
            continue
        yield text[cursor:m.start()], None
        if m["duration"]:
            value = f"，持续{m['d']}{unit(m['du'])}"
        elif m["interval"]:
            value = f"，每{m['i']}{unit(m['iu'])}一次"
        else:
            ratio, thing = m["pct"] or "", m["thing"]
            if thing.casefold().startswith("clear cube") and not ratio:
                value = f"，消耗{m['c']}个无色小晶块"
            elif thing in ("HP", "MP") and ratio:
                value = f"，消耗{m['c']}{ratio}的{thing}"
            else:
                value = None
        yield m[0], value
        cursor = m.end()
    yield text[cursor:], None
