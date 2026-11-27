# -*- coding: utf-8 -*-
"""Resumable, loopback-only HY-MT translation and formatting validation."""
import argparse
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
import http.client
import json
import os
from pathlib import Path
import re
import sqlite3
import threading
import time
from urllib.error import HTTPError, URLError

from localize_us100_tables import DYNAMIC_TEXT, FORMAT_MARKER, HAN, KANA, KOREAN, MARKER, NUMBER, approved
from us100_numeric_roles import equivalent_numeric_roles


ESCAPE = re.compile(r"\\(?:br|[nrt])")
NARRATIVE_TAG = re.compile(r"</?(?:nar|image)(?=[\s/>])[^>]*>", re.I)
BOUNDARY = re.compile(r"(" + FORMAT_MARKER.pattern + "|" + DYNAMIC_TEXT.pattern + "|" + ESCAPE.pattern + "|" + NARRATIVE_TAG.pattern
                      + r"|(?<![\w])https?://[^\s\]<>]+)")
EN_MONTHS = {
    "january": "一", "february": "二", "march": "三", "april": "四", "may": "五", "june": "六",
    "july": "七", "august": "八", "september": "九", "october": "十", "november": "十一", "december": "十二",
}
MONTH_ALIASES = {**EN_MONTHS, **{k[:3]: v for k, v in EN_MONTHS.items()}}
MONTH_WORD = "|".join(sorted(MONTH_ALIASES, key=len, reverse=True))
EN_DATE = (
    rf"(?P<month1>{MONTH_WORD})\s+(?P<day1>[1-9]\d?)(?:st|nd|rd|th)?(?:,\s*(?P<year1>20\d{{2}}))?"
    rf"|(?P<day2>[1-9]\d?)(?:st|nd|rd|th)?\s+(?P<month2>{MONTH_WORD})(?: (?P<year2>20\d{{2}}))?"
)
CALENDAR_BOUNDARY = re.compile(
    BOUNDARY.pattern + rf"|\b(?:{EN_DATE}|(?P<iso>20\d{{2}}-[01]\d-[0-3]\d))\b",
    re.I,
)
PLACEHOLDER = re.compile(r"<pv(\d+)>")
SIGNED = re.compile(r"([+-])\s*(\d+(?:[.,]\d+)*)")
LIST_BULLET = re.compile(r"(^|\\(?:br|[nrt]))([ \t]*)- +(?=\d+(?:st|nd|rd|th)\b)", re.I)
CONTROL = re.compile(r"[\x00-\x1f\x7f\ufffd\u2047]")
LINK = re.compile(r"https?://[^\s\]<>]+")
OPAQUE_KEY = re.compile(r"^(?:[A-Za-z0-9]+_){2,}[A-Za-z0-9_.]+$|^(?:[A-Za-z0-9_]+[/\\])+[A-Za-z0-9_.]+$")
NO_LANGUAGE = re.compile(r"^(?:HP|MP|TP|SP|STR|INT|VIT|SPI|EXP|FPS|NPC|DFO|UI|OTP|PVP|AFK|OP|BGM|DPS)$")
MODELS = {
    "equipment": 4, "stackable": 4, "dnf": 12, "ui": 12, "quest": 9,
    "newrandomoption": 9, "skill": 9, "dungeon": 8, "town": 8, "npc": 7,
}
TERM_CANDIDATES = {
    "Avatar": "装扮", "Clone Rare Avatar": "稀有克隆装扮", "Rare Avatar": "稀有装扮",
    "Dungeon Fighter Online": "地下城与勇士", "Disassemble": "分解", "disassembled": "分解",
    "Reinforcement": "强化", "Amplification": "增幅", "Refining": "锻造", "Enchanting": "附魔",
    "untradable": "无法交易", "tradable": "可交易", "Fragmented Abyss": "深渊之鳞",
    "Skill Atk.": "技能攻击力", "Damage Value": "攻击强化", "Adventurer Fame": "冒险家名望",
    "Cooldown": "冷却时间", "All Elemental Damage": "所有属性强化", "Clear Cube Fragment": "无色小晶块",
    "Physical Atk.": "物理攻击力", "Magical Atk.": "魔法攻击力", "Independent Atk.": "独立攻击力",
    "Equipment Option Control": "装备属性指令", "Fair Arena": "公平决斗场", "Arena": "决斗场",
    "Fatigue Points": "疲劳值", "Awakening": "觉醒", "Apostle": "使徒", "Seria": "赛丽亚",
    "Behemoth": "天帷巨兽", "Epic Soul": "史诗灵魂", "Legendary Soul": "传说灵魂",
    "Avatar Closet": "装扮仓库", "Epic Road": "史诗之路", "Amplify": "增幅", "Disassembly": "分解",
    "Applicable Titles": "适用称号", "Weekly Entry Count": "每周入场次数",
    "Mind Stimulant Potion": "精神刺激灵药", "Soul Reaper": "弑魂",
    "X-1 Extruder": "X-1压缩量子炮", "X-2 Helios": "X-2太阳神光炮", "Charge Laser Rifle": "蓄电激光炮",
    "Gunner": "神枪手", "Fighter": "格斗家", "Slayer": "鬼剑士", "Enchantment": "附魔",
    "Wait": "等待", "Package": "礼包", "Guild": "公会", "Event": "活动",
    "아바타": "装扮", "스킬 공격력": "技能攻击力", "모험가 명성": "冒险家名望",
    "세리아": "赛丽亚", "크리쳐": "宠物", "던전": "地下城", "장비": "装备", "패키지": "礼包",
}
TERMS = [
    (a, b, re.compile((r"(?<![A-Za-z])" if a.isascii() else "") + re.escape(a) +
                      (r"(?![A-Za-z])" if a.isascii() else ""), re.I))
    for a, b in sorted(TERM_CANDIDATES.items(), key=lambda p: -len(p[0]))
]
WORD_TOKENS = re.compile(r"[A-Za-z0-9]+(?:['’-][A-Za-z0-9]+)*")
CONTEXT_TERMS = {}


CONNECTION = threading.local()


def load_game_terms(path):
    if path is None:
        default = Path(__file__).resolve().parents[1] / "runtime" / "localize-us100-mt" / "verified-game-terms.json"
        path = default if default.is_file() else None
    if path:
        loaded = json.loads(path.read_text(encoding="utf-8"))
        CONTEXT_TERMS.update(loaded)


def contextual_terms(source):
    if not CONTEXT_TERMS:
        return []
    spans = list(WORD_TOKENS.finditer(source))
    matches = {}
    for i, first in enumerate(spans):
        for j in range(i, min(len(spans), i + 9)):
            raw = source[first.start():spans[j].end()]
            if len(raw) > 60:
                break
            if localized := CONTEXT_TERMS.get(raw.lower().replace("’", "'")):
                matches[raw] = localized
    return sorted(matches.items(), key=lambda pair: -len(pair[0]))


def load_rows(input_file, references):
    counts, weight, kind_by_value = Counter(), Counter(), {}
    with input_file.open(encoding="utf-8") as handle:
        for row in map(json.loads, handle):
            table, _, _, source = row
            counts[source] += 1
            kind = Path(table).name.split(".", 1)[0]
            rank = MODELS.get(kind, 3)
            weight[source] += rank
            if source not in kind_by_value or rank > MODELS.get(kind_by_value[source], 3):
                kind_by_value[source] = kind
    if references:
        ref = json.loads(references.read_text(encoding="utf-8"))
        for count, _, _, source in ref["top_untranslated_references"]:
            if source in weight:
                weight[source] += count
    return sorted(counts, key=lambda s: (-weight[s], len(s), s)), counts, kind_by_value


def mask(source, calendar=False, retain_numeric_percents=False):
    originals = []

    def replace(match):
        raw = match[0]
        if retain_numeric_percents and raw in ("%", "%%") and match.start() and source[match.start() - 1].isdigit():
            return raw
        if calendar and (month := match["month1"] or match["month2"]):
            day = match["day1"] or match["day2"]
            year = match["year1"] or match["year2"]
            if 1 <= int(day) <= 31:
                raw = MONTH_ALIASES[month.casefold()] + "月" + day + "日" + (f"（{year}年）" if year else "")
        originals.append(raw)
        return f"<pv{len(originals)-1}>"

    return (CALENDAR_BOUNDARY if calendar else BOUNDARY).sub(replace, source), originals


def unmask(translated, originals):
    occurrences = [int(m) for m in PLACEHOLDER.findall(translated)]
    if occurrences != list(range(len(originals))):
        return None
    return PLACEHOLDER.sub(lambda m: originals[int(m[1])], translated)


def can_process(source, max_chars):
    return (0 < len(source) <= max_chars and not OPAQUE_KEY.fullmatch(source)
            and not NO_LANGUAGE.fullmatch(source) and not CONTROL.search(source)
            and not PLACEHOLDER.search(source) and not any("\ud800" <= ch <= "\udfff" for ch in source))


def make_prompt(source, domain, placeholders):
    lines = []
    used = []
    for text, cn, pattern in TERMS:
        if any(text.casefold() in existing.casefold() for existing in used):
            continue
        if pattern.search(source):
            lines.append(f"{text} 翻译成 {cn}")
            used.append(text)
        if len(lines) >= 12:
            break
    for text, cn in contextual_terms(source):
        if len(lines) >= 12:
            break
        if any(text.casefold() in existing.casefold() or existing.casefold() in text.casefold() for existing in used):
            continue
        lines.append(f"{text} 翻译成 {cn}")
        used.append(text)
    guide = (
        "参考下面的游戏术语：\n" + "\n".join(lines) + "\n\n" if lines else ""
    )
    format_note = "完整保留所有 <pv数字> 标记，顺序不能改变；不要改变数值。" if placeholders else "不要改变数值。"
    context = "这是《地下城与勇士》的游戏术语或可见文本。"
    if domain in ("equipment", "stackable", "newrandomoption"):
        context += "道具名称保持简短，装备效果不得漏译。"
    return (guide + context + format_note
            + "\n将以下文本翻译为简体中文，注意只需要输出翻译后的结果，不要额外解释：\n\n" + source)


def mechanical_signs(text):
    return SIGNED.findall(LIST_BULLET.sub(r"\1\2", text))


def validate(source, translated):
    if not translated or not approved(source, translated) or CONTROL.search(translated):
        return "not_clean_chinese"
    if ((NUMBER.findall(source) != NUMBER.findall(translated) or mechanical_signs(source) != mechanical_signs(translated))
            and not equivalent_numeric_roles(source, translated)):
        return "number_or_sign_changed"
    if ESCAPE.findall(source) != ESCAPE.findall(translated) or source.count("%") != translated.count("%"):
        return "formatting_changed"
    if NARRATIVE_TAG.findall(source) != NARRATIVE_TAG.findall(translated):
        return "narrative_tag_changed"
    if LINK.findall(source) != LINK.findall(translated):
        return "link_changed"
    if re.search(r"^\s*(?:翻译[：:]|译文[：:]|以下是|中文[：:]|注[：:])", translated):
        return "unexpected_commentary"
    visible_repeat = re.sub(r"([.!?。！？…_\-])\1{5,}", r"\1\1\1", translated)
    visible_repeat = re.sub(r"[ \u3000]{6,}", " ", visible_repeat)
    if re.search(r"([\u3400-\u9fff])\1{5}|(.{3,30})\2{3}", visible_repeat):
        return "broken_repetition"
    if len(translated) > 3 * len(source) + 20:
        return "unlikely_length"
    if PLACEHOLDER.search(translated) or KOREAN.search(translated) or KANA.search(translated) or not HAN.search(translated):
        return "unresolved_language_or_marker"
    return ""


def request(port, prompt, source_length):
    payload = {
        "messages": [{"role": "user", "content": prompt}],
        "temperature": 0.05, "top_k": 20, "top_p": 0.65,
        "max_tokens": max(64, min(600, source_length + 40)),
        "stream": False, "chat_template_kwargs": {"enable_thinking": False},
    }
    # The endpoint is fixed to local loopback; no proxy or redirect can receive the game text.
    headers = {"Content-Type": "application/json"}
    if token := os.environ.get("US100_LOCAL_MODEL_TOKEN"):
        headers["Authorization"] = "Bearer " + token
    conn = getattr(CONNECTION, "conn", None)
    if conn is None or conn.port != port:
        if conn is not None:
            conn.close()
        conn = CONNECTION.conn = http.client.HTTPConnection("127.0.0.1", port, timeout=150)
    try:
        conn.request("POST", "/v1/chat/completions", json.dumps(payload, ensure_ascii=True).encode("utf-8"), headers)
        with conn.getresponse() as response:
            raw = response.read()
            if response.status != 200:
                raise HTTPError(f"http://127.0.0.1:{port}/v1/chat/completions", response.status,
                                response.reason, response.headers, None)
            data = json.loads(raw)["choices"][0]
    except (OSError, http.client.HTTPException):
        conn.close()
        CONNECTION.conn = None
        raise
    if data.get("finish_reason") == "length":
        return "", "truncated"
    return data["message"].get("content", "").strip(), ""


def translate(source, domain, port):
    masked, markers = mask(source, calendar=True)
    prompt = make_prompt(masked, domain, markers)
    for attempt in range(2):
        try:
            answer, err = request(port, prompt, len(source))
            break
        except (OSError, URLError, http.client.HTTPException, KeyError, ValueError) as exc:
            if isinstance(exc, HTTPError) and exc.code not in (429, 500, 502, 503, 504):
                return source, None, f"http_{exc.code}"
            if attempt:
                return source, None, "server_unavailable"
            time.sleep(1)
    if err:
        return source, None, err
    value = unmask(answer, markers)
    if value is None:
        return source, None, "sentinel_mismatch"
    error = validate(source, value)
    return source, value if not error else None, error or "ok"


def main(args):
    if not 1024 <= args.port <= 65535 or not 1 <= args.workers <= 256:
        raise ValueError("invalid loopback configuration")
    load_game_terms(args.terms)
    rows, counts, kind = load_rows(args.missing, args.references)
    for source_list in args.additional:
        more, extra_counts, extra_kind = load_rows(source_list, None)
        rows[:0] = [text for text in more if text not in counts]
        kind.update({text: extra_kind[text] for text in more if text not in counts})
        counts.update(extra_counts)
    with sqlite3.connect(args.cache, timeout=45) as db:
        db.execute("PRAGMA journal_mode=WAL")
        db.execute("CREATE TABLE IF NOT EXISTS values_v1 (src TEXT PRIMARY KEY, dst TEXT, status TEXT)")
        if args.translate:
            known = {s for s, status in db.execute("SELECT src,status FROM values_v1")
                     if status == "ok" or (not args.retry and status not in ("server_unavailable", "http_404"))}
            queued = [s for s in rows if s not in known and can_process(s, args.max_chars)]
            if args.limit:
                queued = queued[:args.limit]
            started = time.monotonic()
            print(f"Queue: {len(queued)} values, {len(rows)} unique total, {args.workers} workers", flush=True)
            seen = Counter()
            with ThreadPoolExecutor(max_workers=args.workers) as pool:
                for n in range(0, len(queued), max(128, args.workers * 8)):
                    batch = queued[n:n + max(128, args.workers * 8)]
                    evaluated = list(pool.map(lambda src: translate(src, kind[src], args.port), batch))
                    db.executemany("INSERT OR REPLACE INTO values_v1 VALUES (?,?,?)", evaluated)
                    db.commit()
                    seen.update(item[2] for item in evaluated)
                    if seen["server_unavailable"] >= 3 * args.workers and seen["ok"] == 0:
                        raise SystemExit("loopback backend unavailable; no valid output")
                    seconds = max(1, time.monotonic() - started)
                    print(f"{n+len(batch)}/{len(queued)} processed, {seen['ok']} accepted, {(n+len(batch))/seconds:.1f}/s; "
                          f"status={dict(seen)}", flush=True)
        accepted = dict(db.execute("SELECT src,dst FROM values_v1 WHERE status='ok'"))
        for source, value in accepted.items():
            if error := validate(source, value):
                raise ValueError(f"cached translation invalid: {error}")
        receipt = Counter(status for status, in db.execute("SELECT status FROM values_v1"))
        overlay = {s: accepted[s] for s in rows if s in accepted}
    with args.out.open("x", encoding="utf-8") as handle:
        json.dump(overlay, handle, ensure_ascii=True)
    counted = sum(counts[s] for s in overlay)
    print(f"Overlay has {len(overlay)} unique values covering {counted} rows; cache: {dict(receipt)}", flush=True)


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    for flag in ("missing", "cache", "out", "references"):
        p.add_argument("--" + flag, type=Path, required=(flag != "references"))
    p.add_argument("--port", type=int, default=18765)
    p.add_argument("--terms", type=Path)
    p.add_argument("--additional", action="append", type=Path, default=[])
    p.add_argument("--workers", type=int, default=32)
    p.add_argument("--max-chars", type=int, default=1100)
    p.add_argument("--limit", type=int, default=0)
    p.add_argument("--translate", action="store_true")
    p.add_argument("--retry", action="store_true")
    main(p.parse_args())
