"""Report independently verified localization scope and remaining work."""
import argparse
import hashlib
import json
from pathlib import Path


def read(path):
    return json.loads(path.read_text(encoding="utf-8"))


def sha256(path):
    with path.open("rb") as handle:
        d = hashlib.sha256()
        for block in iter(lambda: handle.read(4 << 20), b""):
            d.update(block)
    return d.hexdigest()


def inspect(root, audit):
    str_data = read(root / "coverage.json")["totals"]
    xui = read(root / "xui-coverage.json")
    inline_path = root / "inline-coverage.json"
    inline = read(inline_path) if inline_path.exists() else {}
    if inline and inline["translated"] != read(root / "inline-verification.json")["localized"]:
        raise ValueError("inconsistent inline localization evidence")
    verification = read(root / "verification.json")
    receipt = read(root / "Script.manifest.json")
    original_sha = sha256(Path(receipt["source"]))
    candidate_sha = sha256(root / "Script.pvf")
    if not (original_sha == receipt["source_sha256"] and candidate_sha == receipt["candidate_sha256"]
            and receipt["verified_rewrap_round_trip"]):
        raise ValueError("candidate or original could not be verified")
    translated = str_data["translated"]
    untranslated = str_data.get("untranslated", 0)
    if translated != verification["verified_changed_rows"] or str_data["rows"] != (
        translated + untranslated + str_data.get("not_language", 0) + str_data.get("already_chinese", 0)
        + str_data.get("reviewed_preserved", 0)
    ):
        raise ValueError("inconsistent STR evidence")
    missing = read(audit / "missing-value-references-summary.json")
    return {
        "candidate": str(root.resolve() / "Script.pvf"),
        "original_unchanged": True,
        "candidate_sha256": candidate_sha,
        "str_values_localized": translated,
        "str_values_pending": untranslated,
        "str_values_intentionally_preserved": str_data.get("reviewed_preserved", 0),
        "str_placeholders_without_original_text": str_data.get("source_placeholder_without_text", 0),
        "str_diagnostic_placeholders_localized": str_data.get("diagnostic_placeholder_translation", 0),
        "xui_values_localized": xui.get("translated", 0),
        "xui_values_pending": xui.get("untranslated", 0),
        "xui_values_needing_display_classification": xui.get("review_non_display_or_malformed", 0),
        "inline_scripts_localized": inline.get("scripts", 0),
        "inline_fields_localized": inline.get("translated", 0),
        "inline_fields_pending": inline.get("untranslated", 0),
        "inline_cinematic_fields_localized": inline.get("cinematic_values", 0),
        "original_scripts_references_without_exact_str_key": verification["reference_counts"].get("key_not_in_table", 0),
        "original_orphan_reference_anchors": missing,
        "tracked_translations_exhausted": not (
            untranslated or xui.get("untranslated", 0) or xui.get("review_non_display_or_malformed", 0)
            or inline.get("untranslated", 0)
        ),
        "game_validated": bool(receipt.get("game_validated", False)),
        "installed": bool(receipt.get("installed", False)),
    }


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--candidate", type=Path, required=True)
    p.add_argument("--audit", type=Path, required=True)
    p.add_argument("--out", type=Path)
    args = p.parse_args()
    data = inspect(args.candidate, args.audit)
    if args.out:
        with args.out.open("x", encoding="utf-8") as f:
            json.dump(data, f, indent=2)
    print(json.dumps(data, indent=2))
