"""Check US100 table framing and count statically referenced untranslated keys."""
import argparse
from collections import Counter
import json
from pathlib import Path
import re

from extend_us100_localization import merge_translate_variants
from localize_us100_tables import approved, exact_mechanical, read_rows, SPLIT, table_parts, translatable
from us100_preserve_text import preservation_reason
from us100_symbolic_fallback import recover


def check(original_dir, candidate_dir, reference_file, list_file):
    reference_data = json.loads(reference_file.read_text(encoding="utf-8"))
    raw_refs = reference_data.pop("references")
    by_table = dict((i, s.lower()) for i, s in re.findall(
        r"(\d+) `([^`]+)`", list_file.read_text(encoding="utf-8")
    ))
    refs = {}
    totals = Counter()
    for raw_ref, count in raw_refs.items():
        match = re.fullmatch(r"<(\d+)::(.+)>", raw_ref)
        if not match or match.group(1) not in by_table:
            totals["unknown_or_invalid_ref"] += count
            continue
        refs.setdefault(by_table[match.group(1)], Counter())[match.group(2)] += count

    tables = {}
    priority = []
    changes = 0
    for original in sorted(original_dir.rglob("*.uv.str")):
        name = original.relative_to(original_dir).as_posix()
        candidate = candidate_dir / original.relative_to(original_dir)
        base = original.read_bytes()
        dest = candidate.read_bytes() if candidate.is_file() else base
        before = SPLIT.split(base.decode("utf-16-le", errors="surrogatepass"))
        after = SPLIT.split(dest.decode("utf-16-le", errors="surrogatepass"))
        old = {r[0]: r[1:] for r in table_parts(base)}
        new = {r[0]: r[1:] for r in table_parts(dest)}
        if old.keys() != new.keys() or len(before) != len(after):
            raise ValueError(f"row structure changed: {name}")
        korean = read_rows(original.with_name(original.name.replace(".uv.str", ".kor.str")))
        merge_translate_variants(korean, read_rows(original.with_name(original.name.replace(".uv.str", ".translate.str"))))
        state = {}
        for i, (a, b) in enumerate(zip(before, after)):
            if i not in old:
                if a != b:
                    raise ValueError(f"non-value text changed: {name}:{i}")
                continue
            key, prefix, source, suffix = old[i]
            after_key, after_prefix, target, after_suffix = new[i]
            if (key, prefix, suffix) != (after_key, after_prefix, after_suffix):
                raise ValueError(f"key or framing changed: {name}:{key}")
            if source != target:
                if not approved(source, target) or not exact_mechanical(name, key, source, target):
                    if recover(name, key, source.strip(), korean) != target.strip():
                        raise ValueError(f"text validation failed: {name}:{key}")
                changes += 1
                state[key] = ("translated", source.strip())
            elif preservation_reason(name, key, source.strip(), korean):
                state[key] = ("intentionally_preserved", source.strip())
            elif translatable(source.strip()):
                state[key] = ("untranslated", source.strip())
            else:
                state[key] = ("not_a_translation_target", source.strip())
        table_counts = Counter()
        for key, count in refs.pop(name, {}).items():
            mode, value = state.get(key, ("key_not_in_table", ""))
            table_counts[mode] += count
            table_counts[mode + "_unique"] += 1
            if mode == "untranslated":
                priority.append((count, name, key, value))
        tables[name] = dict(table_counts)
        totals.update(table_counts)
    for counter in refs.values():
        totals["table_not_in_archive_export"] += sum(counter.values())
    return {
        "verified_changed_rows": changes,
        **reference_data,
        "reference_counts": totals,
        "reference_counts_by_table": tables,
        "top_untranslated_references": sorted(priority, reverse=True)[:100],
    }


if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__)
    for flag in ("original-dir", "candidate-dir", "reference-file", "list-file", "output"):
        ap.add_argument("--" + flag, required=True, type=Path)
    args = ap.parse_args()
    data = check(args.original_dir, args.candidate_dir, args.reference_file, args.list_file)
    with args.output.open("x", encoding="utf-8") as f:
        json.dump(data, f, indent=2)
    print(json.dumps({k: v for k, v in data.items() if k not in (
        "reference_counts_by_table", "top_untranslated_references"
    )}, indent=2))
