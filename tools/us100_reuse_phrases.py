# -*- coding: utf-8 -*-
"""Reuse established translations for unambiguous punctuation/time-unit variants."""
import argparse
from collections import Counter, defaultdict
import json
from pathlib import Path
import re

from localize_us100_tables import MARKER, NUMBER, translatable
from us100_concordance import CURATED, spelling
from us100_filter_model_overlay import problem
from us100_grouped_llm import LINE
from us100_local_llm import mechanical_signs
from us100_rescue_llm import restore_numeric_percents


NUM = re.compile(r"(?<![A-Za-z0-9_])([+-]?)(\d+(?:[.,]\d+)*)(%%?)?(?![A-Za-z0-9_])")
TIME = re.compile(r"(?<=\d)\s*(seconds?|secs?\.?|minutes?|mins?\.?)(?=\s|[.);,]|$)", re.I)
SPACE_PUNCT = re.compile(r"\s*([,;:()/])\s*")
ENDS = re.compile(r"(?<!\.)\.$")
FORMAT_PCT = re.compile(r"(?<=\d)%%")


def phrase_shape(text, numbers=False):
    text = spelling(text.strip())
    text = TIME.sub(lambda m: " sec" if m[1].startswith("sec") else " min", text)
    text = SPACE_PUNCT.sub(r"\1", text)
    text = ENDS.sub("", text)
    if numbers:
        return NUM.sub(lambda m: m[1] + "#n#" + ("%" if m[3] else ""), text)
    return FORMAT_PCT.sub("%", text)


def adapt(src, donor, translated, numeric):
    if NUMBER.findall(donor) != NUMBER.findall(translated) or mechanical_signs(donor) != mechanical_signs(translated):
        return None
    if numeric:
        if len(NUMBER.findall(src)) != len(NUMBER.findall(donor)) or not NUMBER.search(src):
            return None
        if len(NUM.findall(src)) != len(NUMBER.findall(src)) or len(NUM.findall(donor)) != len(NUMBER.findall(donor)):
            return None
        if any(a != "%%" for a in MARKER.findall(src) + MARKER.findall(donor)):
            return None
        if not any(word in src.casefold() for word in ("sec", " min", " atk.", "chance", "damage", "speed", "cooldown", "strength", "fatigue")):
            return None
        values = iter(NUMBER.findall(src))
        translated = NUMBER.sub(lambda _: next(values), translated)
    elif NUMBER.findall(src) != NUMBER.findall(donor):
        return None
    result = restore_numeric_percents(src, translated)
    return result if not problem(src, result) else None


def build(args):
    needed = defaultdict(set)
    missing = []
    for table, _, _, source in map(json.loads, args.missing.open(encoding="utf-8")):
        missing.append((table, source))
        for line in LINE.split(source)[::2]:
            if translatable(line):
                needed[table].add(line.strip())
    wanted = defaultdict(set)
    for table, values in needed.items():
        for value in values:
            wanted[table, False, phrase_shape(value)].add(value)
            if NUMBER.search(value):
                wanted[table, True, phrase_shape(value, numbers=True)].add(value)
    candidates = defaultdict(lambda: defaultdict(dict))
    with args.translations.open(encoding="utf-8") as previous:
        for table, key, method, original, translated in map(json.loads, previous):
            if table not in needed or method.endswith("_placeholder_recovered") or method == "diagnostic_placeholder_translation":
                continue
            if len(original) > 4500:
                continue
            a, b = LINE.split(original), LINE.split(translated)
            if len(a) != len(b) or a[1::2] != b[1::2]:
                continue
            for source, target in zip(a[::2], b[::2]):
                source = source.strip()
                target = target.strip()
                if not source or not target:
                    continue
                methods = (False, True) if NUMBER.search(source) else (False,)
                for numeric in methods:
                    signature = phrase_shape(source, numeric)
                    for unresolved in wanted.get((table, numeric, signature), ()):
                        if dest := adapt(unresolved, source, target, numeric):
                            rank = (2 if method in CURATED else 0) + (0 if numeric else 1)
                            candidates[table, unresolved][rank].setdefault(dest, (key, method, source))
    exact = {}
    for (table, source), tiers in candidates.items():
        strongest = tiers[max(tiers)]
        if len(strongest) == 1:
            exact[table, source] = next(iter(strongest.items()))
    if args.unit_out:
        unit_rows = defaultdict(dict)
        for (table, source), (target, donor) in exact.items():
            unit_rows[Path(table).name.split(".", 1)[0]][source] = [target, donor]
        with args.unit_out.open("x", encoding="utf-8") as file:
            json.dump(unit_rows, file, ensure_ascii=True)
    output = {}
    counts = Counter()
    with args.ledger.open("x", encoding="utf-8") as ledger:
        for table, text in missing:
            resolved = []
            proofs = []
            for i, piece in enumerate(LINE.split(text)):
                src = piece.strip()
                if i % 2 or not translatable(src):
                    resolved.append(piece)
                    continue
                hit = exact.get((table, src))
                if not hit:
                    break
                dst, donor = hit
                resolved.append(piece[:len(piece) - len(piece.lstrip())] + dst + piece[len(piece.rstrip()):])
                proofs.append(donor)
            else:
                target = "".join(resolved)
                if not problem(text, target):
                    if text in output and output[text] != target:
                        output[text] = None
                    else:
                        output[text] = target
                    counts[table] += 1
                    ledger.write(json.dumps([table, text, target, proofs], ensure_ascii=True) + "\n")
    output = {src: dst for src, dst in output.items() if dst}
    with args.out.open("x", encoding="utf-8") as file:
        json.dump(output, file, ensure_ascii=True)
    print(json.dumps({"new_distinct": len(output), "by_table": counts, "reused_lines": len(exact)}, indent=2))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("missing", "translations", "out", "ledger"):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument("--unit-out", type=Path)
    build(parser.parse_args())
