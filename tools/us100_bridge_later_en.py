# -*- coding: utf-8 -*-
"""Restore same-key Chinese text through a matching later US bilingual pair."""
import argparse
from collections import Counter
import json
from pathlib import Path

from localize_us100_tables import KOREAN, add_lexicon, canon, read_rows
from us100_filter_model_overlay import problem


def bridge(args):
    by_table = {}
    with args.missing.open(encoding="utf-8") as handle:
        for table, key, _, source in map(json.loads, handle):
            by_table.setdefault(table, []).append((key, source))
    result = {}
    totals = Counter()
    with args.ledger.open("x", encoding="utf-8") as proof:
        for table, rows in sorted(by_table.items()):
            suffix = table.replace(".uv.str", ".kor.str")
            en = read_rows(args.later_us / table)
            ko = read_rows(args.later_us / suffix)
            cn = [(read_rows(ref / suffix), read_rows(ref / table.replace(".uv.str", ".chn.str")))
                  for ref in args.cn]
            for key, source in rows:
                if source != en.get(key) or not (korean := ko.get(key)) or not KOREAN.search(korean):
                    continue
                options = {}
                for i, (ck, cz) in enumerate(cn):
                    target = cz.get(key)
                    if target and canon(ck.get(key)) == canon(korean) and not problem(source, target):
                        options.setdefault(target, []).append(str(args.cn[i].resolve()))
                if len(options) != 1:
                    continue
                target, evidence = next(iter(options.items()))
                add_lexicon(result, source, target)
                totals[table] += 1
                proof.write(json.dumps([table, key, evidence, source, target], ensure_ascii=True) + "\n")
    valid = {source: target for source, target in result.items() if target is not None}
    with args.out.open("x", encoding="utf-8") as output:
        json.dump(valid, output, ensure_ascii=True)
    print(json.dumps({"unique": len(valid), "matching_rows": totals}, indent=2))


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    for option in ("missing", "later-us", "out", "ledger"):
        p.add_argument("--" + option, type=Path, required=True)
    p.add_argument("--cn", action="append", type=Path, required=True)
    bridge(p.parse_args())
