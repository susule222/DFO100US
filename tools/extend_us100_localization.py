# -*- coding: utf-8 -*-
"""Extend a validated US100 localization without overwriting existing translations."""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
from us100_preserve_text import preservation_reason
from us100_contextual_terms import contextual_translation
from us100_symbolic_fallback import recover

from us100_avatar_names import AvatarNames
from us100_concordance import Concordance
from localize_us100_tables import (
    HAN, KANA, KOREAN, SPLIT, TEXT, add_lexicon, approved, canon, exact_mechanical,
    read_rows, table_parts, translatable,
)


OLD_DIAGNOSTIC = ("string/event.uv.str", "decide_levelup_event_06", "cn100_same_key_korean_verified",
                  "Test", "\u8ddd\u79bb\u9886\u53d6\u5956\u52b1\u8fd8\u5dee%dLv\u3002")
CORRECTED_DIAGNOSTIC = (OLD_DIAGNOSTIC[0], OLD_DIAGNOSTIC[1], "diagnostic_placeholder_translation",
                        "Test", "\u6d4b\u8bd5")


def load_pair(root, path):
    if root is None:
        return {}, {}
    return (
        read_rows(root / path.replace(".uv.str", ".kor.str")),
        read_rows(root / path.replace(".uv.str", ".chn.str")),
    )


def dictionary(ko, zh):
    pairs = {}
    for key, value in ko.items():
        if value and KOREAN.search(value) and (translation := zh.get(key)):
            add_lexicon(pairs, canon(value), translation)
    return pairs


def merge_translate_variants(data, supplement):
    for key, value in supplement.items():
        if not value or not KOREAN.search(value):
            continue
        if key.startswith("uv_"):
            # An explicitly regional key wins over a general Korean fallback.
            continue
        data.setdefault(key, value)
    for key, value in supplement.items():
        if key.startswith("uv_") and value and KOREAN.search(value):
            data[key[3:]] = value


def reference_lookup(relative, src, key, us_ko, cn100, cn115, us115):
    anchor = us_ko.get(key) or (src if KOREAN.search(src) else "")
    baseline, base_zh, base_index = cn100
    modern, mod_zh, mod_index = cn115
    later_us_ko, later_us_en = us115
    if not anchor or not KOREAN.search(anchor):
        anchor = ""
    comparable = relative != "string/ui.uv.str" or src == later_us_en.get(key)
    possible = []
    if comparable and anchor and canon(anchor) == canon(modern.get(key)):
        possible.append((mod_zh.get(key), "cn115_same_key_korean_verified"))
    if (later_us_en.get(key) == src and later_us_ko.get(key) and KOREAN.search(later_us_ko[key])
            and canon(later_us_ko[key]) == canon(modern.get(key))):
        possible.append((mod_zh.get(key), "cn115_us115_bilingual_bridge"))
    if comparable and anchor:
        possible.extend((
            (base_index.get(canon(anchor)), "cn100_category_korean_exact"),
            (mod_index.get(canon(anchor)), "cn115_category_korean_exact"),
        ))
    if not comparable or not anchor or canon(anchor) != canon(baseline.get(key)):
        return possible
    # The older same-key Chinese text remains the first choice if suitable.
    return [(base_zh.get(key), "cn100_additional_regional_anchor")] + possible


def load_glossary(path):
    if path is None:
        return {}
    items = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(items, dict) or not all(isinstance(v, str) for v in items.values()):
        raise ValueError(f"not a translation glossary: {path}")
    return items


def extend(us, previous, out, cn100, cn115, us115, manual, machine, avatar_terms, concordance, calibrated):
    terms, mt = load_glossary(manual), load_glossary(machine)
    numeric_references = load_glossary(calibrated)
    avatars = AvatarNames(avatar_terms) if avatar_terms else None
    prior_words = Concordance(previous / "translations.jsonl") if concordance else None
    out.mkdir()
    stage = out / "replacement-tables"
    stage.mkdir()
    prior = json.loads((previous / "coverage.json").read_text(encoding="utf-8"))["tables"]
    report = {}
    repair_prior_placeholder = False
    with (out / "preserved.jsonl").open("x", encoding="utf-8") as preserved, (out / "untranslated.jsonl").open("x", encoding="utf-8") as missing, (
        out / "translations.jsonl"
    ).open("x", encoding="utf-8") as translations:
        with (previous / "translations.jsonl").open("r", encoding="utf-8") as done:
            for line in done:
                if OLD_DIAGNOSTIC[1] in line and tuple(json.loads(line)) == OLD_DIAGNOSTIC:
                    if repair_prior_placeholder:
                        raise ValueError("duplicate prior diagnostic correction")
                    line = json.dumps(CORRECTED_DIAGNOSTIC, ensure_ascii=True) + "\n"
                    repair_prior_placeholder = True
                translations.write(line)
        for path in sorted(us.rglob("*.uv.str")):
            rel = path.relative_to(us).as_posix()
            raw = path.read_bytes()
            previous_table = previous / "replacement-tables" / rel
            seed = previous_table.read_bytes() if previous_table.is_file() else raw
            parts = SPLIT.split(seed.decode("utf-16-le", errors="surrogatepass"))
            original = {r[0]: r[1:] for r in table_parts(raw)}
            if original.keys() != {r[0] for r in table_parts(seed)}:
                raise ValueError(f"previous table key positions differ: {rel}")
            us_ko = read_rows(us / rel.replace(".uv.str", ".kor.str"))
            merge_translate_variants(us_ko, read_rows(us / rel.replace(".uv.str", ".translate.str")))
            old_ko, old_zh = load_pair(cn100, rel)
            new_ko, new_zh = load_pair(cn115, rel)
            lat_ko = read_rows(us115 / rel.replace(".uv.str", ".kor.str")) if us115 else {}
            lat_en = read_rows(us115 / rel) if us115 else {}
            cn_old = old_ko, old_zh, dictionary(old_ko, old_zh)
            cn_new = new_ko, new_zh, dictionary(new_ko, new_zh)
            counts = Counter({k: v for k, v in prior.get(rel, {}).items() if isinstance(v, int)})
            for k in ("untranslated", "cn_key_not_corroborated", "cn_text_or_format_rejected",
                      "unmatched", "conflicting_concordance", "no_verified_translation", "new_translated",
                      "reviewed_preserved", "source_placeholder_without_text"):
                counts.pop(k, None)
            prior_changed = 0
            corrected = 0
            for i, key, prefix, value, suffix in table_parts(seed):
                b_key, b_prefix, b_value, b_suffix = original[i]
                if (key, prefix, suffix) != (b_key, b_prefix, b_suffix):
                    raise ValueError(f"previous table framing differs: {rel}:{key}")
                if value != b_value:
                    if repair_prior_placeholder and (rel, key, b_value.strip(), value.strip()) == (
                        OLD_DIAGNOSTIC[0], OLD_DIAGNOSTIC[1], OLD_DIAGNOSTIC[3], OLD_DIAGNOSTIC[4]
                    ):
                        left = value[:len(value) - len(value.lstrip())]
                        right = value[len(value.rstrip()):]
                        parts[i] = prefix + left + CORRECTED_DIAGNOSTIC[4] + right + suffix
                        counts[OLD_DIAGNOSTIC[2]] -= 1
                        counts[CORRECTED_DIAGNOSTIC[2]] += 1
                        corrected += 1
                    prior_changed += 1
                    continue
                source = value.strip()
                if (HAN.search(source) and not KOREAN.search(source) and not KANA.search(source)
                        and not TEXT.search(source)) or not translatable(source):
                    continue
                if reason := preservation_reason(rel, key, source, us_ko):
                    counts["reviewed_preserved"] += 1
                    counts["source_placeholder_without_text"] += int(reason.startswith("source_"))
                    preserved.write(json.dumps([rel, key, reason, source], ensure_ascii=True) + "\n")
                    continue
                method = ("diagnostic_placeholder_translation" if source.casefold() == "test"
                          else "self_translated_glossary")
                recovered = recover(rel, key, source, us_ko)
                options = [(recovered, "same_archive_korean_placeholder_recovered"), (terms.get(source), method)]
                options.append((contextual_translation(rel, key, source), "self_translated_contextual"))
                options.extend(reference_lookup(rel, source, key, us_ko, cn_old, cn_new, (lat_ko, lat_en)))
                options.append((numeric_references.get(source), "official_reference_with_us_numbers"))
                if prior_words:
                    options.append(prior_words.lookup(rel, key, source))
                if avatars and rel == "string/equipment.uv.str" and key.startswith(("name_", "uv_name_")):
                    options.append((avatars.translate(source), "self_translated_avatar_components"))
                options.append((mt.get(source), "offline_machine_translation"))
                for target, method in options:
                    from_korean_placeholder = method == "same_archive_korean_placeholder_recovered"
                    if not target or target == source:
                        continue
                    if not from_korean_placeholder and (
                        not approved(source, target) or not exact_mechanical(rel, key, source, target)
                    ):
                        continue
                    left = value[:len(value) - len(value.lstrip())]
                    right = value[len(value.rstrip()):]
                    parts[i] = prefix + left + target + right + suffix
                    counts[method] += 1
                    counts["new_translated"] += 1
                    translations.write(json.dumps([rel, key, method, source, target], ensure_ascii=True) + "\n")
                    break
                else:
                    counts["untranslated"] += 1
                    missing.write(json.dumps([rel, key, "no_verified_translation", source], ensure_ascii=True) + "\n")
            if prior_changed != counts["translated"]:
                raise ValueError(f"previous count mismatch: {rel}:{prior_changed}:{counts['translated']}")
            if repair_prior_placeholder and rel == OLD_DIAGNOSTIC[0] and corrected != 1:
                raise ValueError("known diagnostic correction did not match the prior table")
            counts["translated"] = prior_changed + counts["new_translated"]
            built = "".join(parts).encode("utf-16-le", errors="surrogatepass")
            report[rel] = dict(counts)
            if raw != built:
                dest = stage / rel
                dest.parent.mkdir(parents=True, exist_ok=True)
                with dest.open("xb") as f:
                    f.write(built)
                report[rel].update(source_sha256=hashlib.sha256(raw).hexdigest(),
                                   output_sha256=hashlib.sha256(built).hexdigest())
            print(f"{rel}: +{counts['new_translated']} translated, {counts['untranslated']} remaining", flush=True)
    totals = Counter()
    for counts in report.values():
        totals.update({k: v for k, v in counts.items() if isinstance(v, int)})
    summary = {"totals": totals, "tables": report, "scope": "all US .uv.str UTF-16 tables"}
    with (out / "coverage.json").open("x", encoding="utf-8") as f:
        json.dump(summary, f, indent=2)
    print(json.dumps(totals, indent=2))


if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__)
    for key in ("us", "previous", "out", "cn100", "cn115", "us115", "manual", "machine", "calibrated"):
        ap.add_argument("--" + key, type=Path, required=key in ("us", "previous", "out"))
    ap.add_argument("--avatar-terms", type=Path)
    ap.add_argument("--concordance", action="store_true")
    extend(**vars(ap.parse_args()))
