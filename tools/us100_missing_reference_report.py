# -*- coding: utf-8 -*-
"""Separate US100 missing table keys from untranslated existing values."""
import argparse
from collections import Counter
import json
from pathlib import Path
import re

from localize_us100_tables import HAN, KOREAN, canon, read_rows


VALID_KEY = re.compile(r"^[A-Za-z0-9_][A-Za-z0-9_ .:/-]{1,180}$")
IGNORE = frozenset({"?", "dummy", "test", "null", "none", "name", "text"})


def report(args):
    paths = dict((i, s.lower()) for i, s in re.findall(
        r"(\d+) `([^`]+)`", args.list_file.read_text(encoding="utf-8")
    ))
    raw = json.loads(args.references.read_text(encoding="utf-8"))["references"]
    refs = {}
    counts = Counter()
    for key, n in raw.items():
        m = re.fullmatch(r"<(\d+)::(.+)>", key)
        if not m or m[1] not in paths:
            counts["invalid_reference"] += 1
            counts["invalid_reference_occurrences"] += n
            continue
        refs.setdefault(paths[m[1]], Counter())[m[2]] += n
    with args.rows.open("x", encoding="utf-8") as out:
        for path, h in sorted(refs.items()):
            us = read_rows(args.us / path)
            us_ko = read_rows(args.us / path.replace(".uv.str", ".kor.str"))
            cn = [(read_rows(ref / path.replace(".uv.str", ".kor.str")),
                   read_rows(ref / path.replace(".uv.str", ".chn.str"))) for ref in args.cn]
            later = read_rows(args.later / path) if args.later else {}
            folded = {}
            for key in us:
                folded.setdefault(key.casefold(), []).append(key)
            for key, count in h.most_common():
                if key in us:
                    continue
                anchor = us_ko.get(key)
                guarded = {}
                for i, (ko, zh) in enumerate(cn):
                    option = zh.get(key)
                    if (anchor and KOREAN.search(anchor) and canon(anchor) == canon(ko.get(key))
                            and option and HAN.search(option) and not KOREAN.search(option)):
                        guarded[i] = option
                if key.casefold() in folded:
                    reason = "case_variant_in_original"
                elif not VALID_KEY.fullmatch(key) or key.casefold() in IGNORE:
                    reason = "noncanonical_or_debug_reference"
                elif len(set(guarded.values())) == 1:
                    reason = "missing_uv_but_matching_us100_korean_and_chinese"
                elif anchor:
                    reason = "missing_uv_with_us100_korean_value"
                elif later.get(key):
                    reason = "only_later_us_value_without_us100_anchor"
                else:
                    reason = "no_us100_or_later_us_display_value"
                counts[reason] += 1
                counts[reason + "_occurrences"] += count
                out.write(json.dumps({
                    "table": path, "key": key, "static_references": count, "status": reason,
                    "local_korean": anchor, "later_us": later.get(key),
                    "same_key_other_cn": [zh.get(key) for _, zh in cn],
                    "corroborated_zh": guarded, "case_variants": folded.get(key.casefold(), []),
                }, ensure_ascii=True) + "\n")
    with args.summary.open("x", encoding="utf-8") as out:
        json.dump(counts, out, indent=2)
    print(json.dumps(counts, indent=2))


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    for flag in ("us", "later", "references", "list-file", "rows", "summary"):
        p.add_argument("--" + flag, type=Path, required=flag != "later")
    p.add_argument("--cn", action="append", type=Path, default=[])
    report(p.parse_args())
