"""Inventory display-like Lua calls and COS fields without editing resources."""
import argparse
from collections import Counter
import json
from pathlib import Path
import re


GAME_LABEL = re.compile(r"\[(/?[a-z][a-z 0-9/_]{0,70})\]", re.I)
DISPLAY_LABELS = frozenset(("name", "title", "explain", "description", "message", "tooltip", "caption", "text"))
REFERENCE = re.compile(r"<\d+:{1,2}[^>]+>")
DISPLAY_CALL = re.compile(
    r"\b((?:draw|show|add|set|send|print)(?:text|string|message|tooltip|caption|notice|chat|title)\w*)\s*\(",
    re.I,
)
COLOR_TABLE = frozenset(("RED", "BLUE", "YELLOW", "GREEN", "VIOLET"))
LANGUAGE = re.compile(r"[A-Za-z]{2}|[\u3041-\u30ff\uac00-\ud7af]")


def inspect(root):
    totals = Counter()
    notes = []
    for file in sorted(root.rglob("*")):
        if file.suffix not in (".lua", ".cos", ".json"):
            continue
        text = file.read_bytes().decode("utf-16-le", errors="replace")
        rel = file.relative_to(root).as_posix()
        totals[file.suffix[1:] + "_files"] += 1
        if file.suffix == ".lua":
            for match in DISPLAY_CALL.finditer(text):
                totals["lua_display_call_candidates"] += 1
                notes.append([rel, match[1], text[match.end():match.end() + 150].split("\n", 1)[0]])
        elif file.suffix == ".cos":
            sections = list(GAME_LABEL.finditer(text))
            for i, match in enumerate(sections):
                field = match[1].casefold().strip()
                if field not in DISPLAY_LABELS:
                    continue
                end = sections[i+1].start() if i+1 < len(sections) else len(text)
                raw = text[match.end():end]
                plain = REFERENCE.sub("", raw).strip().strip("`\0").strip()
                if not LANGUAGE.search(plain):
                    totals["cos_display_field_empty_numeric_or_referenced"] += 1
                elif rel.endswith("/colortable.cos") and field == "name" and plain in COLOR_TABLE:
                    totals["cos_internal_color_names"] += 1
                else:
                    totals["cos_review_candidates"] += 1
                    notes.append([rel, field, plain[:350]])
    return {"counts": totals, "review_candidates": notes,
            "scope": "syntax-based static inventory; not a runtime reachability proof"}


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--input", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    args = p.parse_args()
    data = inspect(args.input)
    with args.output.open("x", encoding="utf-8") as f:
        json.dump(data, f, indent=2)
    print(json.dumps(data["counts"], indent=2))
