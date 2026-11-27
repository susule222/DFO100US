# -*- coding: utf-8 -*-
"""Inventory and selectively localize text-bearing STR attributes in US100 XUI."""
import argparse
from collections import Counter
from dataclasses import dataclass
import html
import json
from pathlib import Path
import re
import xml.etree.ElementTree as ET

from localize_us100_tables import HAN, KANA, KOREAN, NUMBER, approved, translatable


TOKEN = re.compile(r"<!--.*?-->|<(?P<tag>[A-Za-z_][\w.:-]*)(?P<attrs>(?:[^<>'\"]|'[^']*'|\"[^\"]*\")*)>", re.S)
ATTR = re.compile(r"(?<!\S)([A-Za-z_][\w:.-]*)\s*=\s*(?P<q>['\"])(?P<value>.*?)\2", re.S)
DISPLAY = frozenset({
    "string", "titlestring", "buttonname", "urllinkviewtext", "tooltipstring", "tooltiptext", "text",
    "defaulttext", "message", "area_tab_text", "titletext", "desc_text", "game_tab_text", "title",
    "tooltipmessage", "webbuttontext", "disablemessage", "popupexplain",
})
PLACEHOLDER = re.compile(
    r"\d+\s*::|^<\w+>$|^\[[\w .-]+\]$|^(?:\$|#|%)[\w.]+$|^STR:STR:|^[\w.-]+[\\/][\w.\\/-]+$",
    re.I,
)
NO_TOUCH = frozenset(s.casefold() for s in (
    "Static Text", "disabled", "itemName", "itemLimit", "TEXT", "name", "Button Text",
    "Check Box", "gmButton", "null", "nullstr", "default", "placeholder",
    "text string", "textstring", "sample", "sample text", "test text", "id", "??",
))
CAMEL = re.compile(r"^[A-Z]?[a-z\d]+(?:[A-Z][A-Za-z\d]+)+$")
TECHNICAL = re.compile(r"^(?:[a-zA-Z0-9]+_)+[a-zA-Z0-9_.]+$")
EDITOR_PATTERN = re.compile(r"^(?:(?:static\s*)?text\s*){2,}$|^(?:Lv\.?\s*0*|sp\d+|nnn(?:\.nn)?\s*(?:cm|%))$", re.I)
TECH_PLACEHOLDERS = re.compile(
    r"^(?:[NnAaWw]{3,}(?:\s*[/ :+]\s*[NnAaWw0-9]+)*|(?:text|favorcomment)\d+|#[a-z]\w*|//[a-z]+|"
    r"(?:[a-z]\w*Index|id|npc)\s*\[\d+\](?:\s*,\s*[a-z]\w*Index\s*\[\d+\])?|"
    r"y{4}\s+m{2}\s+d{2}\s*~\s*y{4}\s+m{2}\s+d{2})$", re.I,
)
NEUTRAL = frozenset((
    "pt", "sp", "mp", "wp", "ip", "tcp", "udp", "pvp", "pve", "esc", "vs", "gm", "utc",
    "steam dlc", "ms", ".atk", ".ani",
))
OBSERVED_PLACEHOLDERS = frozenset(s.casefold() for s in (
    "num", "NN PT", "Lv.nn", "LINK TEXT", "dummy", "rewardItem x 20", "ABCDEFABCDEF", "incount",
    "Static Textaaaaaaaaaaaaaaaaa", "Static Textaaaaaaaaaaaaaa", "+nnn íµ +nnn (+nnn)", "gg", "dif",
    "nnn% + nnn%", "/ nnn", "portal2", "CHARACTERNAME", "CHARACNAME", "REWEARD1 : NNN", "REWEARD2 : NNN",
    "MoveX", "MoveY", "growtype", "StrIndex :", "dstr", "Pos Info", "myside", "noshow", "5555ms",
    "name/level/job", "explain explain explain explain explain explain explain explain explain explain string",
    "contents explain contents explain contents explain contents explain contents explain",
    "asdnaosdhnasndklanskldnklaklasndlknaslkdnklasd",
    "adsfasdfasdfdssadfsadfsadfdsafsadfsadfsafsadfsafdsafadsfasdfasdfdsadsfasdfasdfdsadsfasdfasdfdsadsfasdfasdfdsads",
    "awefawefawefawefawefawefwef", "fdsfs",
    "UserName", "PlayerName", "JobName", "AreaName", "RecipeText", "ResetCount", "UnionLevel",
    "missionName", "MonsterName", "SkillName", "card_title", "card_explain", "hot_win_and_lose", "relay_wins",
    "grow_type", "1_1_value", "staticText", "static_text", "guide_test", "DLC_APP_ID_DEFAULT", "notReady",
    "3_3_value", "ButtonText", "map_1", "buildingName", "first_test", "db_kills", "all_kills", "double_kill",
    "all_kill", "anemyName", "pvpResult", "listTitle", "SelectItemString", "Pos_Change_In_Code", "JobNameText",
    "MoveType", "MoveTime", "ZoomType", "ZoomRate", "ZoomTime", "UI_Control_nptr_defence", "AaBbCc",
    "dimmedStaticText", "SkillAvatar", "right_01", "right_02", "pre_map", "guide_message", "market_message",
    "ENUM_NOTIPACKET_ABC_DEF", "win_lose", "SaveName", "FilePath", "message", "tooltip sample",
    "textxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
))
REFERENCE_SHAPE = re.compile(r"<\d+\s*:|\b\d+::[\w.\-]+(?:>|$)")
SAMPLE_SEQUENCE = re.compile(r"^abcde(?:fghijklmnopqrstuvwxyz?|fghi?|fg)$", re.I)
REPEATED_TEMPLATE = re.compile(r"^(.{3,35})(?:\s*\1){2,}$", re.I)
SAMPLE_TIMECODE = re.compile(r"^n{2,}(?:\s*[:/]\s*n{2,})+$", re.I)
NEUTRAL_TIME = re.compile(r"^\d{4}-\d{2}-\d{2} \d{2}:\d{2} \(UTC\)$")
MONTHS = dict(zip(
    "JAN FEB MAR APR MAY JUN JUL AUG SEP OCT NOV DEC".split(), "一 二 三 四 五 六 七 八 九 十 十一 十二".split()
))
MONTH = r"(JAN|FEB|MAR|APR|MAY|JUN|JUL|AUG|SEP|OCT|NOV|DEC)"
EN_DATE = re.compile(MONTH + r"\s+(\d{1,2})|(?<![A-Za-z0-9])(\d{1,2})\s+" + MONTH, re.I)
DATE_BODY = re.compile(
    r"(?:(?:On every weekend from )?)(?:" + MONTH + r"\s+\d{1,2}|\d{1,2}\s+" + MONTH
    + r")(?:\s*[~\-]\s*(?:" + MONTH + r"\s+\d{1,2}|\d{1,2}\s+" + MONTH
    + r"))?(?:, until maintenance\(UTC\))?", re.I,
)


@dataclass(frozen=True)
class Slot:
    tag: str
    control: str
    attribute: str
    value: str
    start: int
    stop: int


def extract_slots(xml):
    for tag in TOKEN.finditer(xml):
        if tag["tag"] is None:
            continue
        values = list(ATTR.finditer(tag["attrs"]))
        control = next((m["value"] for m in values if m[1].casefold() == "id"), "")
        i = tag.start("attrs")
        for attribute in values:
            name = attribute[1].casefold()
            raw = attribute["value"]
            if name not in DISPLAY or not raw.startswith("STR:"):
                continue
            source = html.unescape(raw[4:])
            yield Slot(tag["tag"], control, name, source, i + attribute.start("value") + 4, i + attribute.end("value"))


def rewrite_reason(source, control="", path="", attribute=""):
    source = source.strip()
    if not source:
        return "empty"
    if REFERENCE_SHAPE.search(source):
        return "string_reference"
    if source.casefold() in NO_TOUCH or EDITOR_PATTERN.fullmatch(source):
        return "editor_placeholder"
    if (TECH_PLACEHOLDERS.fullmatch(source) or source.casefold() in OBSERVED_PLACEHOLDERS
            or SAMPLE_SEQUENCE.fullmatch(source) or REPEATED_TEMPLATE.fullmatch(source)
            or SAMPLE_TIMECODE.fullmatch(source)
            or re.fullmatch(r"a{10,}b{10,}(?:\s*[c-f]{10,})+", source)
            or (source.startswith("adsfasdfasdfdssadf") and control.casefold() == "str:dialogue_txt")):
        return "editor_placeholder"
    if (source.casefold() in NEUTRAL or NEUTRAL_TIME.fullmatch(source)
            or re.fullmatch(r"(?:\[GM\] mIdx : \d+|(?:SP|MP)\s*:)", source)):
        return "language_neutral_abbreviation"
    if path.endswith("/testui.xui") or "/gm/" in path or "/gmuidebugtool/" in path:
        return "debug_interface_literal"
    if not translatable(source) or (HAN.search(source) and not KOREAN.search(source) and not KANA.search(source)):
        return "already_chinese_or_not_language"
    compact = re.sub(r"[^a-z0-9]+", "", control.removeprefix("STR:").casefold())
    is_identifier = CAMEL.fullmatch(source) or TECHNICAL.fullmatch(source)
    if is_identifier and compact and re.sub(r"[^a-z0-9]+", "", source.casefold()) in compact:
        return "runtime_template_identifier"
    if (PLACEHOLDER.search(source) and not (
        source.startswith("[") and source.endswith("]") and " " in source
    )) or CAMEL.fullmatch(source) or TECHNICAL.fullmatch(source) or len(source) > 1600 or "\r" in source or "\n" in source:
        return "review_non_display_or_malformed"
    return ""


def decoded(path):
    data = path.read_bytes()
    if len(data) % 2:
        return None
    return data.decode("utf-16-le", errors="surrogatepass")


def index(path):
    if not path.is_file() or (text := decoded(path)) is None:
        return {}
    pairs = {}
    for slot in extract_slots(text):
        if not slot.control:
            continue
        key = (slot.tag, slot.control, slot.attribute)
        value = slot.value.strip()
        if pairs.setdefault(key, value) != value:
            pairs[key] = None
    return pairs


def xml_skeleton(text):
    aux = text.lstrip("\ufeff").rstrip("\0")
    aux = re.sub(r"^(<\?xml\b[^?]*?)\s+encoding\s*=\s*([\"'])[^\"']+\2", r"\1", aux, count=1, flags=re.I)
    return ET.fromstring(aux)


def legit(src, dst):
    return (approved(src, dst) and NUMBER.findall(src) == NUMBER.findall(dst)
            and src.count("%") == dst.count("%") and len(dst) <= 2 * len(src) + 20
            and "STR:" not in dst and "<" not in dst)


def display_pattern(source):
    if match := re.fullmatch(r"([0-9,]+)\s*PT", source, re.I):
        return match[1] + " 点"
    if match := re.fullmatch(r"ADV LV\.?\s+(\d+)", source, re.I):
        return "冒险团等级 " + match[1]
    if match := re.fullmatch(r"LV\.?\s*(\d+)", source, re.I):
        return "等级 " + match[1]
    if match := re.fullmatch(r"EXP\s*:?\s*(\d+)\s*/\s*(\d+)", source, re.I):
        return "经验：" + match[1] + " / " + match[2]
    if DATE_BODY.fullmatch(source):
        def substitute(match):
            month, day = (match[1], match[2]) if match[1] else (match[4], match[3])
            return MONTHS[month.upper()] + "月" + day + "日"

        target = EN_DATE.sub(substitute, source)
        if target.startswith("On every weekend from "):
            target = target[22:] + "的每个周末"
        return target.replace(", until maintenance(UTC)", "，至维护前（UTC）")
    return None


def parse_json(path):
    return json.loads(path.read_text(encoding="utf-8")) if path else {}


def run(args):
    args.out.mkdir()
    rows = Counter()
    out_dir = args.out / "replacement-tables"
    out_dir.mkdir()
    glossaries = [parse_json(p) for p in args.glossary]
    additional = parse_json(args.overlay)
    with (
        (args.out / "untranslated.jsonl").open("x", encoding="utf-8") as missing,
        (args.out / "translations.jsonl").open("x", encoding="utf-8") as translated,
        (args.out / "preserved.jsonl").open("x", encoding="utf-8") as preserved,
    ):
        for file in sorted(args.us.rglob("*.xui")):
            rows["xui_files_scanned"] += 1
            rel = file.relative_to(args.us)
            source = decoded(file)
            if source is None:
                rows["odd_byte_length"] += 1
                continue
            by_reference = [index(path / rel) for path in args.reference]
            changes = []
            for slot in extract_slots(source):
                rows["display_attributes_scanned"] += 1
                original = slot.value.strip()
                fixed = next((g[original] for g in glossaries if original in g and legit(original, g[original])), None)
                reason = rewrite_reason(slot.value, slot.control, rel.as_posix(), slot.attribute)
                if fixed and reason in ("review_non_display_or_malformed", ""):
                    reason = ""
                if reason:
                    rows[reason] += 1
                    if reason in ("editor_placeholder", "review_non_display_or_malformed",
                                  "language_neutral_abbreviation", "runtime_template_identifier", "debug_interface_literal"):
                        preserved.write(json.dumps([rel.as_posix(), slot.control, slot.attribute, reason, slot.value],
                                                   ensure_ascii=True) + "\n")
                    continue
                key = (slot.tag, slot.control, slot.attribute)
                options = [(g.get(original), "reviewed_glossary") for g in glossaries]
                options.append((display_pattern(original), "verified_display_pattern"))
                # Shared XML structure alone does not prove regional text equivalence.
                if KOREAN.search(original):
                    options.extend((ref.get(key), "parallel_xui_control") for ref in by_reference if slot.control)
                options.append((additional.get(original), "offline_translation"))
                for new, method in options:
                    if legit(original, new):
                        prefix = slot.value[:len(slot.value) - len(slot.value.lstrip())]
                        suffix = slot.value[len(slot.value.rstrip()):]
                        changes.append((slot, prefix + new + suffix, method))
                        break
                else:
                    rows["untranslated"] += 1
                    missing.write(json.dumps([rel.as_posix(), "|".join(key), "unmatched_xui_display", original],
                                             ensure_ascii=True) + "\n")
            if not changes:
                continue
            rebuilt = source
            for slot, val, _ in reversed(changes):
                rebuilt = rebuilt[:slot.start] + html.escape(val, quote=True) + rebuilt[slot.stop:]
            try:
                old_xml, new_xml = xml_skeleton(source), xml_skeleton(rebuilt)
            except ET.ParseError:
                rows["source_or_candidate_xml_parse_error"] += 1
                for slot, _, _ in changes:
                    rows["untranslated"] += 1
                    missing.write(json.dumps([rel.as_posix(), slot.control, "xui_xml_parse_guard", slot.value.strip()],
                                             ensure_ascii=True) + "\n")
                continue
            before_nodes, after_nodes = list(old_xml.iter()), list(new_xml.iter())
            if len(before_nodes) != len(after_nodes):
                raise ValueError(f"XUI node count changed: {rel}")
            for old, new in zip(before_nodes, after_nodes):
                if old.tag != new.tag or old.text != new.text or old.tail != new.tail or old.attrib.keys() != new.attrib.keys():
                    raise ValueError(f"XUI structure changed: {rel}")
                for attr, old_value in old.attrib.items():
                    new_value = new.attrib[attr]
                    if new_value != old_value and (
                        attr.casefold() not in DISPLAY or not old_value.startswith("STR:") or not new_value.startswith("STR:")
                    ):
                        raise ValueError(f"XUI non-display attribute changed: {rel}:{attr}")
            dest = out_dir / rel
            dest.parent.mkdir(parents=True, exist_ok=True)
            with dest.open("xb") as out:
                out.write(rebuilt.encode("utf-16-le", errors="surrogatepass"))
            rows["xui_files_changed"] += 1
            for slot, val, method in changes:
                rows["translated"] += 1
                rows[method] += 1
                translated.write(json.dumps([rel.as_posix(), slot.control, slot.attribute, method, slot.value, val],
                                            ensure_ascii=True) + "\n")
    with (args.out / "coverage.json").open("x", encoding="utf-8") as out:
        json.dump(rows, out, indent=2)
    print(json.dumps(rows, indent=2))


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--us", type=Path, required=True)
    p.add_argument("--out", type=Path, required=True)
    p.add_argument("--reference", type=Path, action="append", default=[])
    p.add_argument("--glossary", type=Path, action="append", default=[])
    p.add_argument("--overlay", type=Path)
    run(p.parse_args())
