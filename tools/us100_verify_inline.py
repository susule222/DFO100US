"""Independently compare every typed cell of the allowlisted simulator scripts."""
import argparse
from collections import Counter
import json
from pathlib import Path

def exports(folder):
    return {row["entry"]["archive_path"]: folder / (row["raw_file"].removesuffix(".bin") + ".tokens.json")
            for row in json.loads((folder / "exports.json").read_text(encoding="utf-8"))}


def check(sources, candidate, plan):
    pending = json.loads(plan.read_text(encoding="utf-8"))
    originals = {}
    for folder in sources:
        originals.update(exports(folder))
    localized = exports(candidate)
    if set(pending) != set(localized) or not set(pending) <= set(originals):
        raise ValueError("script export does not match the inline plan")
    total = unchanged = 0
    per_script = {}
    for path, plan_rows in pending.items():
        script = Path(path).name
        old, new = (json.loads(file.read_text(encoding="utf-8")) for file in (originals[path], localized[path]))
        if len(old) != len(new):
            raise ValueError(f"cell count changed: {script}")
        expected = {(r["field"], r["source"]): r for r in plan_rows}
        if len(expected) != len(plan_rows):
            raise ValueError(f"duplicate inline source value: {script}")
        matched = Counter()
        for j, (src, dst) in enumerate(zip(old, new)):
            prev = old[j - 1] if j else {}
            key = (prev.get("text"), src.get("text"))
            row = expected.get(key) if prev.get("type") == 3 and src["type"] == 6 else None
            if row is None:
                if src != dst:
                    raise ValueError(f"non-localization cell changed: {script}:{j}")
                unchanged += 1
            else:
                if dst["type"] != 6 or dst.get("text") != row["target"] or dst["value"] % 2 != 1:
                    raise ValueError(f"unexpected localized cell: {script}:{j}")
                matched[key] += 1
        if any(matched[k] != row["count"] for k, row in expected.items()):
            raise ValueError(f"cell multiplicity changed: {script}")
        total += sum(matched.values())
        per_script[path] = sum(matched.values())
    return {"localized": total, "untouched_cells": unchanged, "scripts": per_script}


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--source", required=True, type=Path, action="append")
    for key in ("candidate", "plan", "report"):
        p.add_argument("--" + key, required=True, type=Path)
    a = p.parse_args()
    result = check(a.source, a.candidate, a.plan)
    with a.report.open("x", encoding="utf-8") as f:
        json.dump(result, f, indent=2)
    print(json.dumps(result, indent=2))
