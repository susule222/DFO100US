"""Merge additional model results without overwriting accepted prior values."""
import argparse
import json
from pathlib import Path


def combine(first, second, output):
    base = json.loads(first.read_text(encoding="utf-8"))
    extra = json.loads(second.read_text(encoding="utf-8"))
    added = 0
    for k, v in extra.items():
        if k not in base:
            base[k] = v
            added += 1
    with output.open("x", encoding="utf-8") as f:
        json.dump(base, f, ensure_ascii=True)
    print(f"New values added: {added}")


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--first", type=Path, required=True)
    p.add_argument("--second", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    combine(**vars(p.parse_args()))
