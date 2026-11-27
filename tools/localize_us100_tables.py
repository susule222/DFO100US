# -*- coding: utf-8 -*-
"""Migrate reviewed Chinese strings into US100 tables without changing keys."""
import argparse
import collections
import hashlib
import json
from pathlib import Path
import re

from us100_numeric_roles import equivalent_numeric_roles


HAN = re.compile(r"[\u3400-\u9fff\uf900-\ufaff]")
KOREAN = re.compile(r"[\u1100-\u11ff\u3130-\u318f\uac00-\ud7af]")
KANA = re.compile(r"[\u3041-\u3096\u30a1-\u30fa]")
TEXT = re.compile(r"[a-zA-Z]{2}|[\u1100-\u11ff\u3130-\u318f\u3041-\u30fa\uac00-\ud7af]")
KEY = re.compile(r"[^>\x00\r\n]{1,240}")
MARKER = re.compile(
    r"%(?:\d+\$|\([\w.]+\))?[-+ #0]*(?:\*|\d+)?(?:\.(?:\*|\d+))?"
    r"(?:I64|hh|ll|[hlLzjt])?[diuoxXfFeEgGaAcspn%](?![A-Za-z])"
    r"|%[1-9]\d*\b|\{[\w.:-]+\}|<[\w]+::[^>]+>|&(?:#[\dxXabcdefABCDEF]+|[A-Za-z]+);"
    r"|</?(?:font|color|stc|tst|img|a|b|i|u|br)(?=[\s/>])[^>]*>"
    r"|</?(?:quorum|decimal\d*|integer|emphasis|shake|newline|linebreak|tab)>"
    r"|\[(?:stc|tst)\b[^\]]*\]|@[a-z0-9]{1,5}@",
    re.I,
)
FORMAT_MARKER = re.compile(
    MARKER.pattern + r"|%%|%\.\*f(?=Sec)|%d(?=(?:ea|secs?|min|times|month|day|hour|minute|line|lv|th|gm|[dhmsfxyptg])(?![A-Za-z]))",
    re.I,
)
SPLIT = re.compile(r"(\r\n|\n|\r)")
NUMBER = re.compile(r"\d+(?:[.,]\d+)*")
MECHANICAL = re.compile(r"(?:explain|option|property|damage|effect|condition)", re.I)
DYNAMIC_TEXT = re.compile(r"#target_name(?![A-Za-z0-9_])")


def table_parts(data):
    text = data.decode("utf-16-le", errors="surrogatepass")
    parts = SPLIT.split(text)
    for i in range(0, len(parts), 2):
        piece = parts[i]
        body = piece.lstrip("\ufeff \t")
        if body.startswith(("//", "#", ";")):
            continue
        before, delim, value = body.partition(">")
        key = before.strip()
        if not delim or not KEY.fullmatch(key):
            continue
        prefix = piece[:len(piece) - len(value)]
        clean_value = value.rstrip("\x00")
        if "\x00" not in clean_value:
            yield i, key, prefix, clean_value, value[len(clean_value):]


def read_rows(path):
    result = {}
    if path.is_file():
        for _, key, _, value, _ in table_parts(path.read_bytes()):
            v = value.strip()
            if result.setdefault(key, v) != v:
                result[key] = None
    return result


def canon(value):
    if not value:
        return ""
    return re.sub(r"\s+", " ", value.strip())


def approved(src, dst):
    if not dst or len(dst) > 16384 or not HAN.search(dst) or KOREAN.search(dst) or KANA.search(dst):
        return False
    if any(c in dst for c in ("\r", "\n", "\0", "\ufffd")):
        return False
    if re.search(r"\(\(\s*(?:后续|待|确认|未翻译)", dst):
        return False
    return (FORMAT_MARKER.findall(src) == FORMAT_MARKER.findall(dst)
            and DYNAMIC_TEXT.findall(src) == DYNAMIC_TEXT.findall(dst))


def translatable(s):
    if s in ("null", "NULL", "N/A", "n/a"):
        return False
    s = MARKER.sub("", s)
    return bool(TEXT.search(s) and not re.fullmatch(
        r"(?:https?://\S+|\S+\.(?:npk|img|ogg|wav|ani|dds|tga|png|xml|nut|lst|txt|xui))",
        s, re.I,
    ))


def add_lexicon(result, source, target):
    found = result.setdefault(source, target)
    if found != target:
        result[source] = None


def exact_mechanical(relative, key, src, dst):
    return not (relative in (
        "string/equipment.uv.str", "string/stackable.uv.str", "string/skill.uv.str",
        "string/newrandomoption.uv.str", "string/randomoption.uv.str",
    ) and MECHANICAL.search(key) and NUMBER.findall(src) != NUMBER.findall(dst)
                and not equivalent_numeric_roles(src, dst))


def collect_templates(lines, templates, src, dst):
    left, right = src.split(r"\n"), dst.split(r"\n")
    if len(left) != len(right):
        return
    for a, b in zip(left, right):
        a, b = a.strip(), b.strip()
        if not translatable(a) or not approved(a, b) or NUMBER.findall(a) != NUMBER.findall(b):
            continue
        add_lexicon(lines, a, b)
        if len(a) >= 14 and NUMBER.search(a) and not MARKER.search(a):
            add_lexicon(templates, NUMBER.sub("\x01", a), NUMBER.sub("\x01", b))


def compose(src, lines, templates):
    replacement = []
    for line in src.split(r"\n"):
        key = line.strip()
        if not translatable(key):
            replacement.append(line)
            continue
        candidate = lines.get(key)
        if not candidate and not MARKER.search(key):
            templated = templates.get(NUMBER.sub("\x01", key))
            digits = NUMBER.findall(key)
            if templated and templated.count("\x01") == len(digits):
                values = iter(digits)
                candidate = re.sub("\x01", lambda _: next(values), templated)
        if not approved(key, candidate):
            return None
        replacement.append(line[:len(line) - len(line.lstrip())] + candidate + line[len(line.rstrip()):])
    return r"\n".join(replacement)


def table_sources(us, cn, relative):
    variants = (
        relative.replace(".uv.str", ".kor.str"),
        relative.replace(".uv.str", ".chn.str"),
    )
    return read_rows(us / variants[0]), read_rows(cn / variants[0]), read_rows(cn / variants[1])


def corroborated(key, src, us_ko, cn_ko):
    anchor = us_ko.get(key) or (src if KOREAN.search(src) else "")
    reference = cn_ko.get(key)
    return bool(anchor and reference and canon(anchor) == canon(reference))


def localize(us, cn, out, glossary, overlay):
    out.mkdir()
    stage = out / "replacement-tables"
    stage.mkdir()
    tables = sorted(us.rglob("*.uv.str"))
    english, korean, lines, templates = {}, {}, {}, {}
    terms = json.loads(glossary.read_text(encoding="utf-8")) if glossary else {}
    supplied = json.loads(overlay.read_text(encoding="utf-8")) if overlay else {}
    if not all(isinstance(k, str) and isinstance(v, str) for d in (terms, supplied) for k, v in d.items()):
        raise ValueError("glossary and overlay must map source text to Chinese text")
    for path in tables:
        rel = path.relative_to(us).as_posix()
        us_ko, cn_ko, cn_zh = table_sources(us, cn, rel)
        pairs = 0
        for _, key, _, original, _ in table_parts(path.read_bytes()):
            source = original.strip()
            dst = cn_zh.get(key)
            if source in terms:
                continue
            if (translatable(source) and approved(source, dst) and exact_mechanical(rel, key, source, dst)
                    and corroborated(key, source, us_ko, cn_ko)):
                add_lexicon(english, source, dst)
                add_lexicon(korean, canon(us_ko.get(key) or source), dst)
                collect_templates(lines, templates, source, dst)
                pairs += 1
        print(f"aligned {rel}: {pairs}", flush=True)
    report = {}
    with (out / "untranslated.jsonl").open("x", encoding="utf-8") as missing, (
        out / "translations.jsonl"
    ).open("x", encoding="utf-8") as translations:
        for path in tables:
            rel = path.relative_to(us).as_posix()
            us_ko, cn_ko, cn_zh = table_sources(us, cn, rel)
            raw = path.read_bytes()
            parts = SPLIT.split(raw.decode("utf-16-le", errors="surrogatepass"))
            counts = collections.Counter()
            for i, key, prefix, original, suffix in table_parts(raw):
                counts["rows"] += 1
                src = original.strip()
                if HAN.search(src) and not KOREAN.search(src) and not KANA.search(src) and not TEXT.search(src):
                    counts["already_chinese"] += 1
                    continue
                if not translatable(src):
                    counts["not_language"] += 1
                    continue
                ref = cn_zh.get(key)
                proposal = ""
                if approved(src, terms.get(src)) and exact_mechanical(rel, key, src, terms[src]):
                    proposal, method = terms[src], "reviewed_glossary"
                elif (approved(src, ref) and exact_mechanical(rel, key, src, ref)
                      and corroborated(key, src, us_ko, cn_ko)):
                    proposal, method = ref, "cn100_same_key_korean_verified"
                elif approved(src, english.get(src)) and exact_mechanical(rel, key, src, english[src]):
                    proposal, method = english[src], "exact_source_concordance"
                elif (KOREAN.search(src) and approved(src, korean.get(canon(src)))
                      and exact_mechanical(rel, key, src, korean[canon(src)])):
                    proposal, method = korean[canon(src)], "exact_korean_concordance"
                elif approved(src, supplied.get(src)) and exact_mechanical(rel, key, src, supplied[src]):
                    proposal, method = supplied[src], "supplemental_translation"
                elif rel in ("string/equipment.uv.str", "string/stackable.uv.str",
                            "string/randomoption.uv.str", "string/newrandomoption.uv.str") and MECHANICAL.search(key):
                    combined = compose(src, lines, templates)
                    if approved(src, combined) and exact_mechanical(rel, key, src, combined):
                        proposal, method = combined, "verified_line_or_number_template"
                if not proposal:
                    reason = "unmatched"
                    if ref and not approved(src, ref):
                        reason = "cn_text_or_format_rejected"
                    elif ref:
                        reason = "cn_key_not_corroborated"
                    elif src in english and english[src] is None:
                        reason = "conflicting_concordance"
                    counts[reason] += 1
                    counts["untranslated"] += 1
                    missing.write(json.dumps([rel, key, reason, src], ensure_ascii=True) + "\n")
                    continue
                left = original[:len(original) - len(original.lstrip())]
                right = original[len(original.rstrip()):]
                parts[i] = prefix + left + proposal + right + suffix
                counts[method] += 1
                counts["translated"] += 1
                translations.write(json.dumps([rel, key, method, src, proposal], ensure_ascii=True) + "\n")
            result = "".join(parts).encode("utf-16-le", errors="surrogatepass")
            assert len(raw) % 2 == 0
            if result != raw:
                destination = stage / rel
                destination.parent.mkdir(parents=True, exist_ok=True)
                with destination.open("xb") as handle:
                    handle.write(result)
                report[rel] = {
                    "source_sha256": hashlib.sha256(raw).hexdigest(),
                    "output_sha256": hashlib.sha256(result).hexdigest(),
                }
            else:
                report[rel] = {}
            report[rel].update(counts)
            print(f"staged {rel}: {counts['translated']} translated, {counts['untranslated']} remaining", flush=True)
    totals = collections.Counter()
    for entry in report.values():
        totals.update({k: v for k, v in entry.items() if isinstance(v, int)})
    data = {"totals": totals, "tables": report, "scope": "all US .uv.str UTF-16 tables"}
    (out / "coverage.json").write_text(json.dumps(data, indent=2), encoding="utf-8")
    print(json.dumps(totals, indent=2))


if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--us", type=Path, required=True)
    ap.add_argument("--cn", type=Path, required=True)
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--glossary", type=Path)
    ap.add_argument("--overlay", type=Path)
    localize(**vars(ap.parse_args()))
