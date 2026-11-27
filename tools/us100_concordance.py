# -*- coding: utf-8 -*-
"""Reuse unambiguous same-category translations for spelling and item-name variants."""
import json
from pathlib import Path
import re

from localize_us100_tables import MARKER, NUMBER, add_lexicon, approved


CURATED = frozenset({
    "cn100_same_key_korean_verified", "cn115_same_key_korean_verified",
    "cn115_us115_bilingual_bridge", "reviewed_glossary", "self_translated_glossary",
})
GROUPS = {
    "string/equipment.uv.str": "item",
    "string/stackable.uv.str": "item",
    "string/creature.uv.str": "item",
}
QUOTE_AND_HYPHEN = str.maketrans({"’": "'", "‘": "'", "“": '"', "”": '"', "‑": "-", "−": "-"})
WHITESPACE = re.compile(r"[ \t]+")
BRACKET = re.compile(r"^(?:<([^<>]+)>|\(([^()]+)\)|\[([^\[\]]+)\])$")
NAME_SUFFIX = re.compile(
    r"^(.+?) (Selection Box|Equipment Box|Avatar Box|Set Box|Gift Box|Box|Recipe|Title|Package|Card|Bead|Potion|"
    r"Pouch|Coupon|Ticket|Fragment|Emblem|Aura Avatar|Avatar)$", re.I,
)
SUFFIX_ZH = {
    "selection box": "自选礼盒", "equipment box": "装备礼盒", "avatar box": "装扮礼盒",
    "set box": "套装礼盒", "gift box": "礼盒", "box": "礼盒", "recipe": "设计图",
    "title": "称号", "package": "礼包", "card": "卡片", "bead": "宝珠", "potion": "药剂",
    "pouch": "袋", "coupon": "兑换券", "ticket": "券", "fragment": "碎片", "emblem": "徽章",
    "aura avatar": "光环装扮", "avatar": "装扮",
}
SLOT = {
    "hat": "帽子", "hair": "头部", "face": "脸部", "torso": "胸部",
    "top": "上衣", "bottom": "下装", "waist": "腰部", "shoes": "鞋", "weapon": "武器",
}
CLONE = re.compile(r"^(?:(Rare|Advanced) )?Clone (Hat|Hair|Face|Torso|Top|Bottom|Waist|Shoes|Weapon) Avatar$", re.I)
CLONE_REVERSED = re.compile(r"^(Hat|Hair|Face|Torso|Top|Bottom|Waist|Shoes|Weapon) Clone Avatar$", re.I)
NAME_KEY = re.compile(r"^(?:uv_|chn_)?(?:name|title)_", re.I)
ITEM_NAMES = frozenset(GROUPS)


def spelling(value):
    return WHITESPACE.sub(" ", value.translate(QUOTE_AND_HYPHEN).strip()).casefold()


def item_name(path, key, text):
    return (path in ITEM_NAMES and NAME_KEY.search(key) and len(text) <= 125
            and not re.search(r"[<>\n\r]|\\[nrt]", text) and not MARKER.search(text))


def clone_name(text):
    if match := CLONE.fullmatch(text):
        rarity, slot = match.groups()
        return {None: "", "rare": "稀有", "advanced": "高级"}[rarity and rarity.lower()] + "克隆装扮（" + SLOT[slot.lower()] + "）"
    if match := CLONE_REVERSED.fullmatch(text):
        return "克隆装扮（" + SLOT[match[1].lower()] + "）"
    return None


class Concordance:
    def __init__(self, translations: Path):
        self.same = {}
        self.item = {}
        with translations.open(encoding="utf-8") as handle:
            for line in handle:
                path, key, source_kind, source, target = json.loads(line)
                if source_kind not in CURATED or not approved(source, target):
                    continue
                add_lexicon(self.same, (path, spelling(source)), target)
                if item_name(path, key, source) and NUMBER.findall(source) == NUMBER.findall(target):
                    add_lexicon(self.item, spelling(source), target)

    def lookup(self, path, key, source):
        if result := self.same.get((path, spelling(source))):
            return result, "same_category_spelling_variant"
        if name := clone_name(source):
            if path in ITEM_NAMES or path in ("string/dnf.uv.str", "string/ui.uv.str"):
                return name, "self_translated_clone_slot"
        if item_name(path, key, source):
            if result := self.item.get(spelling(source)):
                return result, "same_item_name_cross_table"
            if matched := NAME_SUFFIX.fullmatch(source):
                name, suffix = matched.groups()
                if result := self.item.get(spelling(name)):
                    tail = SUFFIX_ZH[suffix.lower()]
                    if not result.endswith(tail) and NUMBER.findall(source) == NUMBER.findall(result):
                        return result + tail, "self_translated_item_name_suffix"
        if path in ITEM_NAMES and (match := BRACKET.fullmatch(source)):
            part = next(s for s in match.groups() if s is not None)
            dst = self.same.get((path, spelling(part))) or self.item.get(spelling(part))
            if dst and dst[:1] not in "<([《【（" and NUMBER.findall(part) == NUMBER.findall(dst):
                return source[0] + dst + source[-1], "same_value_display_brackets"
        return None, ""
