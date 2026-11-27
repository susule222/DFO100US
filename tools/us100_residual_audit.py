"""Group unresolved values by source and distinguish reference evidence from coverage."""
import argparse
from collections import Counter, defaultdict
import json
from pathlib import Path
import re

from localize_us100_tables import FORMAT_MARKER, KANA, KOREAN, NUMBER
from us100_local_llm import OPAQUE_KEY


def inspect(candidate, reference_file, list_file):
    ids = {n: path.casefold() for n, path in re.findall(r"(\d+) `([^`]+)`", list_file.read_text(encoding="utf-8"))}
    original_refs = json.loads(reference_file.read_text(encoding="utf-8"))["references"]
    hits = {}
    for token, total in original_refs.items():
        if (m := re.fullmatch(r"<(\d+)::(.+)>", token)) and m[1] in ids:
            hits[ids[m[1]], m[2]] = total
    counts = Counter()
    shapes = Counter()
    grouped = defaultdict(list)
    with (candidate / "untranslated.jsonl").open(encoding="utf-8") as rows:
        for table, key, reason, source in map(json.loads, rows):
            occurrence = hits.get((table, key), 0)
            counts[table] += 1
            if KOREAN.search(source) or KANA.search(source):
                shape = "legacy_east_asian_or_mixed"
            elif OPAQUE_KEY.fullmatch(source):
                shape = "code_like_or_compact_label"
            elif any(token != "%%" for token in FORMAT_MARKER.findall(source)):
                shape = "formatted_text"
            elif NUMBER.search(source):
                shape = "literal_numbers"
            else:
                shape = "plain_text_or_proper_name"
            shapes[shape] += 1
            grouped[source].append((table, key, occurrence, shape, reason))
    groups = sorted(grouped.items(), key=lambda row: (-sum(s[2] for s in row[1]), -len(row[1]), row[0]))
    return {
        "str_rows": sum(counts.values()),
        "distinct_original_values": len(groups),
        "rows_with_static_script_references": sum(s[2] > 0 for _, slots in groups for s in slots),
        "static_script_references_to_pending_rows": sum(s[2] for _, slots in groups for s in slots),
        "rows_by_table": counts,
        "rows_by_source_shape": shapes,
        "note": "Source shapes are triage, not proof that a row is visible. Static references are not use frequency.",
    }, groups


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    for key in ("candidate", "reference-file", "list-file", "out", "groups"):
        p.add_argument("--" + key, required=True, type=Path)
    args = p.parse_args()
    report, groups = inspect(args.candidate, args.reference_file, args.list_file)
    with args.out.open("x", encoding="utf-8") as f:
        json.dump(report, f, indent=2)
    with args.groups.open("x", encoding="utf-8") as f:
        for source, rows in groups:
            f.write(json.dumps({"source": source, "rows": rows}, ensure_ascii=True) + "\n")
    print(json.dumps(report, indent=2))
