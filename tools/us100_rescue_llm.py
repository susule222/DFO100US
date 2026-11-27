# -*- coding: utf-8 -*-
"""Translate residual sentences with numeric anchors and a resumable local cache."""
import argparse
from collections import Counter
from concurrent.futures import FIRST_COMPLETED, ThreadPoolExecutor, wait
import http.client
import json
from pathlib import Path
import re
import sqlite3
import time
from urllib.error import HTTPError

from localize_us100_tables import translatable
from us100_clause_tails import tails
from us100_filter_model_overlay import problem, repair
from us100_formula_text import translate_formula
from us100_grouped_llm import LINE, candidate, units
from us100_local_llm import (
    BOUNDARY, CALENDAR_BOUNDARY, MONTH_ALIASES, NO_LANGUAGE, NUMBER, can_process, load_game_terms,
    load_rows, make_prompt, mask, mechanical_signs, request, unmask,
)


ANCHORS = re.compile(
    CALENDAR_BOUNDARY.pattern
    + r"|(?<![A-Za-z0-9_])(?P<ordinal>\d+)(?:st|nd|rd|th)(?![A-Za-z0-9_])"
      r"|(?<![A-Za-z0-9_])[+-]?\d+(?=[smhd]\b)"
    + r"|(?<![A-Za-z0-9_])(?:[+-]\s*)?\d+(?:[.,]\d+)*(?:\s*-\s*\d+(?:\.\d+)?)?"
      r"(?:%%?|(?![A-Za-z0-9_]))",
    re.I,
)
SENTENCE_GAP = re.compile(r"(?<=[.!?])[ \t]+(?=[A-Z(\[]|@\w+@)")
ABBREV = re.compile(r"(?:\b(?:lv|atk|def|sec|min|hr|max|no|mr|ms|vs|st|etc|inc|approx|dr|e\.g|i\.e)\.|"
                    r"\b[a-z]\.[a-z]\.)$", re.I)
PERCENT = re.compile(r"(?<![\d.])([+-]?\d+(?:\.\d+)?)([ \t]*)([%％]{1,2})(?![%％])")
DISPLAY_TITLE = re.compile(r"(<\s*)([^<>\r\n]+?)(\s*>)")
UI_PAIR = re.compile(r"[A-Z][a-z]+(?:/[A-Z][a-z]+){1,3}")


def protect_numbers(text):
    originals = []

    def replace(match):
        value = match[0]
        if month := match["month1"] or match["month2"]:
            day = match["day1"] or match["day2"]
            year = match["year1"] or match["year2"]
            if 1 <= int(day) <= 31:
                value = MONTH_ALIASES[month.casefold()] + "月" + day + "日" + (f"（{year}年）" if year else "")
        elif ordinal := match["ordinal"]:
            value = "第" + ordinal
        originals.append(value)
        return f"<pv{len(originals)-1}>"

    return ANCHORS.sub(replace, text), originals


def sentences(line):
    start = 0
    for gap in SENTENCE_GAP.finditer(line):
        core = line[start:gap.start()]
        if len(core) < 16 or ABBREV.search(core):
            continue
        yield core
        yield gap[0]
        start = gap.end()
    yield line[start:]


def restore_numeric_percents(source, draft):
    original = {}
    for value, _, percent in PERCENT.findall(source):
        if original.setdefault(value, percent) != percent:
            original[value] = ""
    return PERCENT.sub(lambda m: m[1] + m[2] + (original.get(m[1]) or m[3]), draft)


def separate_controls(text):
    cursor = 0
    for mark in BOUNDARY.finditer(text):
        if mark[0] in ("%", "%%") and mark.start() and text[mark.start() - 1].isdigit():
            continue
        yield text[cursor:mark.start()], False
        yield mark[0], True
        cursor = mark.end()
    yield text[cursor:], False


def plan_visible(piece, isolate):
    core = piece.strip()
    if not core or not translatable(core) or NO_LANGUAGE.fullmatch(core):
        yield piece, False
        return
    if fixed := translate_formula(core):
        if not problem(core, fixed):
            yield piece[:len(piece) - len(piece.lstrip())] + fixed + piece[len(piece.rstrip()):], False
            return
    left = piece[:len(piece) - len(piece.lstrip())]
    right = piece[len(piece.rstrip()):]
    title = DISPLAY_TITLE.fullmatch(core) if isolate and not BOUNDARY.fullmatch(core) else None
    if title and translatable(title[2]) and not re.fullmatch(r"[A-Za-z]+\d", title[2]):
        yield left + title[1], False
        yield title[2], True
        yield title[3] + right, False
    else:
        yield left, False
        yield core, True
        yield right, False


def stitch_plan(text, category, history, isolate=False, phrase_memory=None, clauses=False):
    for part in LINE.split(text):
        if not part.strip() or LINE.fullmatch(part):
            yield part, False
            continue
        if phrase_memory and (hit := phrase_memory.get(part.strip())) and not problem(part.strip(), hit[0]):
            yield part[:len(part) - len(part.lstrip())] + hit[0] + part[len(part.rstrip()):], False
            continue
        old = candidate(part, units(part, category), history)
        if old and not problem(part, repair(part, old)):
            yield repair(part, old), False
            continue
        for piece in sentences(part):
            if not isolate:
                yield from plan_visible(piece, False)
                continue
            fixed = translate_formula(piece.strip())
            if fixed and not problem(piece.strip(), fixed):
                yield from plan_visible(piece, True)
                continue
            for fragment, control in separate_controls(piece):
                if control:
                    yield fragment, False
                elif fragment:
                    known = candidate(fragment, units(fragment, category), history)
                    if known and not problem(fragment, known):
                        yield known, False
                    elif clauses:
                        for segment, built in tails(fragment):
                            if built is not None:
                                yield built, False
                            elif segment:
                                yield from plan_visible(segment, True)
                    else:
                        yield from plan_visible(fragment, True)


def revision_prompt(source, masked, raw_draft, note):
    if note not in ("number_or_sign_changed", "duration_attached_to_wrong_value",
                    "max_stack_condition_missing", "game_term_missing", "known_dnf_term_not_preserved",
                    "negation_dropped", "not_clean_chinese"):
        return None
    if note == "number_or_sign_changed":
        digits = "、".join(NUMBER.findall(source))
        signs = "、".join(sign + number for sign, number in mechanical_signs(source))
        issue = f"原文的阿拉伯数字按出现顺序是：{digits}。必须原样保留的增减数值是：{signs or '无'}。"
    elif note == "duration_attached_to_wrong_value":
        issue = "初稿把持续时间的数值或单位放错了，请对照原文逐一配对。"
    elif note == "max_stack_condition_missing":
        issue = "保留原文的最大叠加层数和同句的其他限制条件。"
    elif note == "negation_dropped":
        issue = "保留原文的否定、禁止或不生效条件，不能把它写成允许。"
    elif note == "not_clean_chinese":
        issue = "把可见文案译成简体中文；代码标记和数值保留原样，不要直接复述英文。"
    else:
        issue = "补齐游戏术语：Avatar是装扮，Clear Cube Fragment是无色小晶块，Skill Atk.是技能攻击力。"
    return (
        "对照原文修正下列《地下城与勇士》简体中文初稿。只输出修正后的译文。"
        "必须保留数值、正负、百分号、触发条件以及所有<pv数字>标记的顺序。"
        + issue + "\n\n原文：" + masked + "\n\n参考初稿：" + raw_draft
    )


def ask(text, category, port, prefer_plain=False, isolate=False, explicit_layout=False, revise=False):
    if not can_process(text, 1150) and not (isolate and UI_PAIR.fullmatch(text)):
        return text, None, "source_needs_review"
    masked, tokens = protect_numbers(text)
    prompt = make_prompt(masked, category, tokens)
    reason = ""
    alternate = (
        "将以下《地下城与勇士》原文翻译为简体中文。仅输出译文。"
        "按照原顺序完整保留每一个 <pv数字> 标记，不得遗漏、重写或添加标记。"
        "保留原意，不得省略限制条件。\n\n" + masked
    )
    if explicit_layout:
        visible, raw_tokens = mask(text, calendar=True, retain_numeric_percents=True)
        guide = (
            "翻译一段《地下城与勇士》的界面文本为简体中文。只输出译文，不要引号、Markdown或解释。"
            "数字保持阿拉伯数字，原有正负号和百分号不要省略。按原文顺序保留数字；"
            "如果中文习惯换序，就把次要条件写在句末或括号内。"
            "如果原文含有<pv数字>格式标记，须按原有顺序逐个原样保留。"
            "例如：Attack Speed +5%% for 10 seconds -> 攻击速度 +5%%，持续10秒。"
            "例如：Automatically consumes 1 Cube Fragment every 30 seconds. -> 自动消耗1个小晶块，每30秒触发一次。"
            "Avatar是装扮，Cooldown是冷却时间，Clear Cube Fragment是无色小晶块。\n\n"
        )
        attempts = ((guide + visible, raw_tokens), (make_prompt(visible, category, raw_tokens), raw_tokens))
    elif isolate:
        visible, raw_tokens = mask(text, calendar=True, retain_numeric_percents=True)
        first = ("将以下《地下城与勇士》的可见界面文本翻译成简体中文，只输出译文。"
                 "保留原有阿拉伯数字、单位和限制；装扮不要译成虚拟形象。\n\n" + visible)
        attempts = ((first, raw_tokens), (make_prompt(visible, category, raw_tokens), raw_tokens))
    elif prefer_plain:
        visible, raw_tokens = mask(text, calendar=True, retain_numeric_percents=True)
        first = ("数值用阿拉伯数字；数值、正负、单位和对应条件必须与原文一致。"
                 "请通过括号补充说明保持数字原有顺序。不要把数值换成汉字。\n\n"
                 + make_prompt(visible, category, raw_tokens))
        attempts = ((first, raw_tokens), (prompt, tokens))
    else:
        attempts = ((prompt, tokens), (alternate, tokens))
    correction = None
    for attempt, (question, selected_tokens) in enumerate(attempts):
        if attempt == 1 and correction:
            question = correction
        try:
            draft, reason = request(port, question, len(text))
        except HTTPError as exc:
            return text, None, f"http_{exc.code}"
        except (http.client.HTTPException, OSError, ValueError, KeyError):
            return text, None, "server_unavailable"
        if reason:
            continue
        if (target := unmask(draft, selected_tokens)) is None:
            reason = "sentinel_mismatch"
            continue
        if explicit_layout or isolate or (prefer_plain and attempt == 0):
            target = restore_numeric_percents(text, target)
        target = repair(text, target)
        reason = problem(text, target)
        if not reason:
            return text, target, "ok"
        if revise and explicit_layout and attempt == 0:
            correction = revision_prompt(text, visible, draft, reason)
        if not selected_tokens and not prefer_plain and not isolate and attempt == 0 and reason not in ("game_term_missing", "not_clean_chinese"):
            break
    return text, None, reason


def fill_cache(db, queued, categories, workers, port, prefer_plain, isolate, explicit_layout, revise):
    status = Counter()
    total = len(queued)
    waiting = iter(queued)
    batch = []
    processed = 0
    started = last = time.monotonic()
    print(f"Residual sentences: {total}; {workers} local workers", flush=True)
    with ThreadPoolExecutor(max_workers=workers) as pool:
        pending = set()

        def schedule():
            if (text := next(waiting, None)) is not None:
                pending.add(pool.submit(ask, text, categories[text], port, prefer_plain, isolate, explicit_layout, revise))

        for _ in range(workers):
            schedule()
        while pending:
            done, pending = wait(pending, return_when=FIRST_COMPLETED)
            for future in done:
                entry = future.result()
                batch.append(entry)
                status[entry[2]] += 1
            hard = sum(n for k, n in status.items() if k.startswith("http_") or k == "server_unavailable")
            stop = hard >= workers * 2 and hard > max(1, status["ok"]) * 2
            if not stop:
                for _ in done:
                    schedule()
            now = time.monotonic()
            if len(batch) >= 192 or now - last >= 10 or not pending:
                db.executemany("INSERT OR REPLACE INTO sentences_v1 VALUES (?,?,?)", batch)
                db.commit()
                processed += len(batch)
                batch.clear()
                last = now
                print(f"{processed}/{total}; {processed/max(1,now-started):.1f}/s {dict(status)}", flush=True)
            if stop and not pending:
                raise SystemExit("Local inference unavailable; existing successful results are saved.")


def run(args):
    if args.out.exists() or args.receipt.exists():
        raise FileExistsError("choose unused residual output paths")
    if not 1 <= args.workers <= 192 or not 1024 <= args.port <= 65535:
        raise ValueError("invalid local worker configuration")
    terms = args.terms or Path("runtime/localize-us100-mt/verified-game-terms.json")
    load_game_terms(terms if terms.is_file() else None)
    values, weights, categories = load_rows(args.missing, args.references)
    with sqlite3.connect(args.history.resolve().as_uri() + "?mode=ro", uri=True) as old:
        history = dict(old.execute("SELECT src,dst FROM units_v1 WHERE status='ok'"))
    phrase_memory = json.loads(args.phrase_memory.read_text(encoding="utf-8")) if args.phrase_memory else {}
    plans = {}
    frequency = Counter()
    unit_kinds = {}
    for text in values:
        kind = categories[text]
        parts = tuple(stitch_plan(text, kind, history, args.isolate_format, phrase_memory.get(kind), args.clause_tails))
        if args.whole_value and any(unknown for _, unknown in parts) and can_process(text, 1150):
            parts = ((text, True),)
        plans[text] = parts
        for chunk, unknown in parts:
            if unknown:
                unit_kinds.setdefault(chunk, categories[text])
                frequency[chunk] += weights[text]
    with sqlite3.connect(args.cache, timeout=45) as db:
        db.execute("PRAGMA journal_mode=WAL")
        db.execute("CREATE TABLE IF NOT EXISTS sentences_v1 (src TEXT PRIMARY KEY, dst TEXT, status TEXT)")
        if args.translate:
            retryable = {
                "sentinel_mismatch", "game_term_missing", "number_or_sign_changed", "duration_attached_to_wrong_value",
                "formatting_changed", "not_clean_chinese", "known_bad_term", "known_dnf_term_not_preserved",
                "max_stack_condition_missing", "negation_dropped",
            }
            seen = {s for s, status in db.execute("SELECT src,status FROM sentences_v1")
                    if not status.startswith("http_") and status != "server_unavailable"
                    and not (args.retry_failed and status in retryable)}
            queued = [text for text, _ in frequency.most_common() if text not in seen]
            if args.limit:
                queued = queued[:args.limit]
            fill_cache(db, queued, unit_kinds, args.workers, args.port, args.prefer_plain,
                       args.isolate_format, args.explicit_layout, args.revise_invalid)
        known = dict(db.execute("SELECT src,dst FROM sentences_v1 WHERE status='ok'"))
        stats = Counter(k for k, in db.execute("SELECT status FROM sentences_v1"))
    results = {}
    for text, parts in plans.items():
        if any(unknown and p not in known for p, unknown in parts):
            stats["unresolved_component"] += weights[text]
            continue
        target = repair(text, "".join(known[p] if unknown else p for p, unknown in parts))
        if target == text or (why := problem(text, target)):
            stats[f"full_value_{why}" if target != text else "unchanged"] += weights[text]
            continue
        results[text] = target
        stats["accepted_str_rows"] += weights[text]
    stats["accepted_distinct_values"] = len(results)
    with args.out.open("x", encoding="utf-8") as handle:
        json.dump(results, handle, ensure_ascii=True)
    with args.receipt.open("x", encoding="utf-8") as handle:
        json.dump(stats, handle, indent=2)
    print(json.dumps(stats, indent=2))


if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__)
    for arg in ("missing", "cache", "history", "out", "receipt"):
        ap.add_argument("--" + arg, required=True, type=Path)
    ap.add_argument("--references", type=Path)
    ap.add_argument("--terms", type=Path)
    ap.add_argument("--phrase-memory", type=Path)
    ap.add_argument("--port", type=int, default=18765)
    ap.add_argument("--workers", type=int, default=96)
    ap.add_argument("--translate", action="store_true")
    ap.add_argument("--prefer-plain", action="store_true")
    ap.add_argument("--retry-failed", action="store_true")
    ap.add_argument("--isolate-format", action="store_true")
    ap.add_argument("--clause-tails", action="store_true")
    ap.add_argument("--explicit-layout", action="store_true")
    ap.add_argument("--whole-value", action="store_true")
    ap.add_argument("--revise-invalid", action="store_true")
    ap.add_argument("--limit", type=int, default=0)
    run(ap.parse_args())
