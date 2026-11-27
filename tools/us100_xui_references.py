"""Audit XUI localization references against the unmodified US100 string tables."""
import argparse
from collections import Counter
import json
from pathlib import Path
import re

from localize_us100_tables import read_rows
from us100_xui_text import decoded, extract_slots


REFERENCE = re.compile(r"<(\d+)::([^>]+)>")


def run(xui, str_tables, list_file, output):
    table_paths = {i: table.lower() for i, table in re.findall(
        r"(\d+) `([^`]+)`", list_file.read_text(encoding="utf-8")
    )}
    loaded = {}
    stats = Counter()
    missing = Counter()
    for file in xui.rglob("*.xui"):
        stats["xui_files"] += 1
        text = decoded(file)
        if text is None:
            stats["odd_byte_length"] += 1
            continue
        for slot in extract_slots(text):
            for m in REFERENCE.finditer(slot.value):
                stats["xui_reference_occurrences"] += 1
                table = table_paths.get(m[1])
                if table is None:
                    stats["no_table_id"] += 1
                    missing[m[0]] += 1
                    continue
                if table not in loaded:
                    loaded[table] = read_rows(str_tables / table)
                if m[2] in loaded[table]:
                    stats["original_key_exists"] += 1
                else:
                    stats["original_key_absent"] += 1
                    missing[m[0]] += 1
    result = {"counts": stats, "distinct_original_missing": len(missing), "missing": missing}
    with output.open("x", encoding="utf-8") as f:
        json.dump(result, f, indent=2)
    print(json.dumps({k: v for k, v in result.items() if k != "missing"}, indent=2))


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    for name in ("xui", "str-tables", "list-file", "output"):
        p.add_argument("--" + name, type=Path, required=True)
    run(**vars(p.parse_args()))
