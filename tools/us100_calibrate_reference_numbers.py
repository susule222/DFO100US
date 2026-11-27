# -*- coding: utf-8 -*-
"""Reuse CN descriptions when only anchored US/CN numeric values differ."""
import argparse
from collections import Counter
import json
from pathlib import Path

from localize_us100_tables import KOREAN, MECHANICAL, NUMBER, add_lexicon, canon, read_rows
from us100_filter_model_overlay import problem


TYPES = frozenset({"equipment", "stackable", "skill", "randomoption", "newrandomoption"})


def adapt(source, korean, cn_korean, chinese):
    if not source or not korean or not cn_korean or not chinese or not KOREAN.search(korean) or not KOREAN.search(cn_korean):
        return None
    us_digits, cn_digits = NUMBER.findall(korean), NUMBER.findall(cn_korean)
    if not us_digits or us_digits == cn_digits or len(us_digits) != len(cn_digits):
        return None
    if NUMBER.findall(source) != us_digits or NUMBER.findall(chinese) != cn_digits:
        return None
    if NUMBER.sub("\t~N~\t", canon(korean)) != NUMBER.sub("\t~N~\t", canon(cn_korean)):
        return None
    values = iter(us_digits)
    result = NUMBER.sub(lambda _: next(values), chinese)
    return result if not problem(source, result) else None


def run(args):
    groups = {}
    with args.missing.open(encoding="utf-8") as f:
        for table, key, _, source in map(json.loads, f):
            if Path(table).name.split(".", 1)[0] in TYPES and MECHANICAL.search(key):
                groups.setdefault(table, []).append((key, source))
    values = {}
    ledger = Counter()
    with args.ledger.open("x", encoding="utf-8") as proof:
        for table, rows in sorted(groups.items()):
            ko = table.replace(".uv.str", ".kor.str")
            chn = table.replace(".uv.str", ".chn.str")
            anchor = read_rows(args.us / ko)
            cn = [(root.name, read_rows(root / ko), read_rows(root / chn)) for root in args.cn]
            for key, source in rows:
                original = anchor.get(key)
                if not original:
                    continue
                options = {}
                for title, ck, cz in cn:
                    if translated := adapt(source, original, ck.get(key), cz.get(key)):
                        options.setdefault(translated, []).append(title)
                if len(options) == 1:
                    target = next(iter(options))
                    add_lexicon(values, source, target)
                    ledger[table] += 1
                    proof.write(json.dumps([table, key, options[target], source, target], ensure_ascii=True) + "\n")
    result = {source: value for source, value in values.items() if value}
    with args.out.open("x", encoding="utf-8") as f:
        json.dump(result, f, ensure_ascii=True)
    print(json.dumps({"unique": len(result), "matching_rows_by_table": ledger}, indent=2))


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    for name in ("missing", "us", "out", "ledger"):
        p.add_argument("--" + name, type=Path, required=True)
    p.add_argument("--cn", action="append", type=Path, required=True)
    run(p.parse_args())
