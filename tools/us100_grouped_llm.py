# -*- coding: utf-8 -*-
"""Translate shared visible-text units once and reattach exact game formatting."""
import argparse
from collections import Counter
from concurrent.futures import FIRST_COMPLETED, ThreadPoolExecutor, wait
from dataclasses import dataclass
import http.client
import json
from pathlib import Path
import re
import sqlite3
import time
from urllib.error import HTTPError, URLError

from localize_us100_tables import add_lexicon, translatable
from us100_concordance import CURATED
from us100_formula_text import translate_formula
from us100_local_llm import (
    BOUNDARY, NO_LANGUAGE, PLACEHOLDER, can_process, load_game_terms, load_rows, make_prompt,
    mask, request, unmask, validate,
)


LINE = re.compile(r"(\\(?:br|[nrt]))")
VARIANT = re.compile(r"(\s*)\[Type ([A-Z])\]$")
ZH_VARIANT = re.compile(r"\s*\[([A-Z])型\]$")
DIGITS = re.compile(r"(?<![A-Za-z0-9_])(?:[+-]\s*)?\d+(?:[.,]\d+)*(?![A-Za-z0-9_])")
TOKEN = re.compile(f"(?:{BOUNDARY.pattern})|(?:{DIGITS.pattern})")
FORMAT = {
    "[Event]": "[活动]", "[EVENT]": "[活动]", "<Event>": "<活动>", "[Epic]": "[史诗]",
    "[Legendary]": "[传说]", "[Unique]": "[神器]", "[Rare]": "[稀有]",
}
SPECIAL = {
    "%d%% off": "%d%% 折扣",
    "%d/%d times": "%d/%d 次",
    "Weekly Entry Count: %d/%d": "每周入场次数：%d/%d",
    "GET": "领取",
    "Hoeny Time": "在线有礼",
    "One Day Avatar": "一日装扮",
    "<베히모스의 해적 패키지>": "<天帷巨兽海盗礼包>",
    "< 수인족 영웅 패키지 >": "< 兽人族英雄礼包 >",
    "[Applicable Titles]": "[适用称号]",
}
PRESERVE_QUOTED = re.compile(r'(?:\b(?:enter(?:ing)?|type|typing|input)\b|输入)[^"“]{0,100}["“]([^"”]+)["”]', re.I)
TERM_GATE = (
    (re.compile(r"\b(?:clone rare avatar|rare clone avatar)s?\b", re.I), "稀有克隆装扮"),
    (re.compile(r"\bavatar(s)?\b", re.I), "装扮"),
    (re.compile(r"\badventurer fame\b", re.I), "冒险家名望"),
    (re.compile(r"\bdamage value\b", re.I), "攻击强化"),
    (re.compile(r"\bskill atk\.", re.I), "技能攻击力"),
    (re.compile(r"\ball elemental damage\b", re.I), "所有属性强化"),
    (re.compile(r"\bclear cube fragment(s)?\b", re.I), "无色小晶块"),
    (re.compile(r"\bfragmented abyss\b", re.I), "深渊之鳞"),
    (re.compile(r"\bapplicable titles\b", re.I), "适用称号"),
    (re.compile(r"\bweekly entry count\b", re.I), "每周入场次数"),
)
QUALITY = re.compile(r"阿(?:瓦|凡)达|(?:时间|技能)降级|下载此设备|水人族|月球新年")
NEGATIVE_EN = re.compile(r"\b(?:cannot|can't|can’t|doesn't|doesn’t|must not|untradable|untradeable|not allowed)\b", re.I)


@dataclass(frozen=True)
class Unit:
    text: str
    key: str
    values: tuple[str, ...]
    left: str
    right: str
    kind: str


def prepare(text):
    vals = []

    def tag(match):
        vals.append(match[0])
        return f"<pv{len(vals)-1}>"

    return TOKEN.sub(tag, text), tuple(vals)


def units(source, category):
    for i, piece in enumerate(LINE.split(source)):
        if i % 2:
            yield piece
            continue
        core = piece.strip()
        left = piece[:len(piece) - len(piece.lstrip())]
        right = piece[len(piece.rstrip()):]
        if not core:
            yield piece
            continue
        if (alt := VARIANT.search(core)) and translatable(core[:alt.start()]):
            right = alt[1] + "[" + alt[2] + "型]" + right
            core = core[:alt.start()].rstrip()
        if core in FORMAT:
            yield left + FORMAT[core] + right
        elif core in SPECIAL:
            yield left + SPECIAL[core] + right
        elif rule := translate_formula(core):
            yield left + rule + right
        elif NO_LANGUAGE.fullmatch(core):
            yield piece
        elif not translatable(core):
            yield piece
        elif not can_process(core, 1250):
            yield Unit(core, "", (), left, right, category)
        else:
            key, values = prepare(core)
            if key != core and not translatable(PLACEHOLDER.sub("", key)):
                yield piece
            else:
                yield Unit(core, key, values, left, right, category)


def quality(source, target):
    if error := validate(source, target):
        return error
    if QUALITY.search(target):
        return "known_bad_term"
    for pattern, expected in TERM_GATE:
        if pattern.search(source) and expected not in target:
            return "game_term_missing"
    if NEGATIVE_EN.search(source) and not re.search(r"[不无未禁免]", target):
        return "negation_dropped"
    for command in PRESERVE_QUOTED.findall(source):
        if command.isascii() and command not in target:
            return "command_changed"
    return ""


def ask(unit, port, retry=False):
    for attempt in range(2):
        try:
            model_input, tokens = mask(unit.text, calendar=True) if retry else (unit.key, unit.values)
            prompt = make_prompt(model_input, unit.kind, tokens)
            answer, err = request(port, prompt, len(unit.text))
            if err:
                return unit.key, None, err
            restored = unmask(answer, tokens)
            if not restored:
                return unit.key, None, "sentinel_mismatch"
            status = quality(unit.text, restored)
            if not status and retry:
                answer, actual = prepare(restored)
                if actual != unit.values:
                    return unit.key, None, "noncanonical_token_spacing"
            return unit.key, answer if not status else None, status or "ok"
        except (URLError, ValueError, KeyError, OSError, http.client.HTTPException) as exc:
            if isinstance(exc, HTTPError) and exc.code in (400, 401, 403, 404):
                return unit.key, None, f"http_{exc.code}"
            if attempt:
                return unit.key, None, "server_unavailable"
            time.sleep(0.6)
    raise AssertionError("unreachable")


def candidate(text, segments, results):
    resolved = []
    for segment in segments:
        if isinstance(segment, str):
            resolved.append(segment)
            continue
        draft = results.get(segment.key)
        if draft is None or (dest := unmask(draft, segment.values)) is None or quality(segment.text, dest):
            return None
        resolved.append(segment.left + dest + segment.right)
    target = "".join(resolved)
    if target == text or quality(text, target):
        return None
    return target


def load_previews(paths):
    result = {}
    for p in paths:
        for src, dst in json.loads(p.read_text(encoding="utf-8")).items():
            if not quality(src, dst):
                result[src] = dst
    return result


def seed_units(paths, wanted):
    seeded = {}
    for path in paths:
        with path.open(encoding="utf-8") as handle:
            for row in map(json.loads, handle):
                _, _, method, source, translated = row
                if method not in CURATED and method != "self_translated_avatar_components":
                    continue
                left, right = LINE.split(source), LINE.split(translated)
                if len(left) != len(right) or left[1::2] != right[1::2]:
                    continue
                for src, dst in zip(left[::2], right[::2]):
                    src, dst = src.strip(), dst.strip()
                    if (s := VARIANT.search(src)) and (t := ZH_VARIANT.search(dst)) and s[2] == t[1]:
                        src, dst = src[:s.start()].rstrip(), dst[:t.start()].rstrip()
                    if not translatable(src):
                        continue
                    key, values = prepare(src)
                    if key not in wanted or quality(src, dst):
                        continue
                    answer, tr_values = prepare(dst)
                    if values == tr_values and unmask(answer, tr_values) == dst:
                        add_lexicon(seeded, key, answer)
    return {key: val for key, val in seeded.items() if val is not None}


def stream_to_cache(db, pending, args):
    start = time.monotonic()
    status_counts = Counter()
    total = len(pending)
    failed = False
    waiting = iter(pending)
    completed = []
    persisted = 0
    last_commit = start
    print(f"Ready: {total} units using {args.workers} local workers", flush=True)
    with ThreadPoolExecutor(max_workers=args.workers) as pool:
        futures = {pool.submit(ask, unit, args.port, args.retry) for unit in (next(waiting, None) for _ in range(args.workers))
                   if unit is not None}
        while futures:
            done, futures = wait(futures, return_when=FIRST_COMPLETED)
            for future in done:
                row = future.result()
                completed.append(row)
                status_counts[row[2]] += 1
            hard_errors = sum(n for status, n in status_counts.items() if status.startswith("http_")
                              or status == "server_unavailable")
            if hard_errors >= 2 * args.workers and hard_errors > status_counts["ok"] * 2:
                failed = True
            stopping = (args.stop_file and args.stop_file.is_file()) or failed
            if not stopping:
                for _ in done:
                    if (unit := next(waiting, None)) is not None:
                        futures.add(pool.submit(ask, unit, args.port, args.retry))
            now = time.monotonic()
            if completed and (len(completed) >= 256 or now - last_commit >= 9 or not futures):
                db.executemany("INSERT OR REPLACE INTO units_v1 VALUES (?,?,?)", completed)
                db.commit()
                persisted += len(completed)
                completed.clear()
                last_commit = now
                print(f"{persisted}/{total}, {persisted/max(1,now-start):.1f}/s {dict(status_counts)}", flush=True)
    if failed:
        raise SystemExit("Local backend rejected the requests; existing cache has been kept.")
    if args.stop_file and args.stop_file.is_file():
        raise SystemExit("Paused after committing the in-flight units; resume with the same cache.")


def run(args):
    if not 1 <= args.workers <= 192 or not 1024 <= args.port <= 65535:
        raise ValueError("invalid loopback worker configuration")
    if args.out.exists():
        raise FileExistsError(args.out)
    load_game_terms(args.terms)
    rows, counts, categories = load_rows(args.missing, args.references)
    for path in args.additional:
        extra, weights, kinds = load_rows(path, None)
        for value in extra:
            if value not in counts:
                rows.insert(0, value)
                categories[value] = kinds[value]
        counts.update(weights)
    previous = load_previews(args.preview)
    grouped = {}
    freq = Counter()
    for priority, text in enumerate(rows):
        if text in previous or text in SPECIAL:
            continue
        for item in units(text, categories[text]):
            if isinstance(item, Unit) and item.key:
                grouped.setdefault(item.key, item)
                freq[item.key] += counts[text] * (5 if priority < 4500 else 1)
    print(f"Inventory: {len(rows)} unique text values, {len(grouped)} translation units", flush=True)
    seeded = seed_units(args.seed, grouped)
    if seeded:
        print(f"Reusing {len(seeded)} unambiguous translation units from audited prior values", flush=True)
    with sqlite3.connect(args.cache, timeout=45) as db:
        db.execute("PRAGMA journal_mode=WAL")
        db.execute("CREATE TABLE IF NOT EXISTS units_v1 (src TEXT PRIMARY KEY, dst TEXT, status TEXT)")
        if args.translate:
            known = set()
            for key, answer, code in db.execute("SELECT src,dst,status FROM units_v1"):
                if code == "ok":
                    if not args.retry or key not in grouped:
                        known.add(key)
                    else:
                        example = grouped[key]
                        restored = unmask(answer, example.values)
                        if restored is not None and not quality(example.text, restored):
                            known.add(key)
                elif not args.retry and code != "server_unavailable" and not code.startswith("http_"):
                    known.add(key)
            pending = [grouped[k] for k, _ in freq.most_common() if k not in known and k not in seeded]
            if args.limit:
                pending = pending[:args.limit]
            stream_to_cache(db, pending, args)
        receipts = Counter(code for code, in db.execute("SELECT status FROM units_v1"))
        resolved = dict(db.execute("SELECT src,dst FROM units_v1 WHERE status='ok'"))
        statuses = dict(db.execute("SELECT src,status FROM units_v1")) if args.ledger else None
    resolved.update(seeded)
    target = {}
    for source in rows:
        value = SPECIAL.get(source) or previous.get(source) or candidate(source, units(source, categories[source]), resolved)
        if value is not None and not quality(source, value):
            target[source] = value
    draft = args.out.with_name(args.out.name + ".pending")
    with draft.open("x", encoding="utf-8") as handle:
        json.dump(target, handle, ensure_ascii=True)
    draft.rename(args.out)
    if args.ledger:
        with args.ledger.open("x", encoding="utf-8") as ledger:
            for source in rows:
                if source in target:
                    continue
                reasons = set()
                for segment in units(source, categories[source]):
                    if isinstance(segment, Unit):
                        reason = (statuses or {}).get(segment.key, "not_evaluated") if segment.key else "unsafe_or_oversized"
                        if reason != "ok":
                            reasons.add(reason)
                ledger.write(json.dumps([counts[source], categories[source], sorted(reasons) or ["whole_value_guard"], source],
                                        ensure_ascii=True) + "\n")
    print(f"Output: {len(target)} unique values / {sum(counts[k] for k in target)} rows. Cache: {dict(receipts)}", flush=True)


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    for key in ("missing", "cache", "out", "references"):
        p.add_argument("--" + key, type=Path, required=key != "references")
    p.add_argument("--additional", type=Path, action="append", default=[])
    p.add_argument("--preview", type=Path, action="append", default=[])
    p.add_argument("--seed", type=Path, action="append", default=[])
    p.add_argument("--terms", type=Path)
    p.add_argument("--port", type=int, default=18765)
    p.add_argument("--workers", type=int, default=48)
    p.add_argument("--limit", type=int, default=0)
    p.add_argument("--translate", action="store_true")
    p.add_argument("--retry", action="store_true")
    p.add_argument("--stop-file", type=Path)
    p.add_argument("--ledger", type=Path)
    run(p.parse_args())
