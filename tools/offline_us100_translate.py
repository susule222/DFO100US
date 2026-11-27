# -*- coding: utf-8 -*-
"""Translate deduplicated missing US100 display text locally and export an overlay."""
import argparse
from collections import Counter
from ctypes import WinDLL, create_unicode_buffer
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import time

from localize_us100_tables import HAN, KANA, KOREAN, MARKER, NUMBER, approved


BOUNDARY = re.compile(
    "(" + MARKER.pattern + r"|\\[nrt]|<[^<>\r\n]+::[^<>\r\n]+>|\[Event\]|[\[\]<>])",
    re.I,
)
WORDS = re.compile(r"[A-Za-z]{2}|[\uac00-\ud7af]|[\u3041-\u30ff]")
RESOURCE = re.compile(r"\S+[\\/]\S+\.(?:ogg|wav|img|npk|ani|nut|dds|tga|png|xml|lst|txt)|\bhttps?://\S+", re.I)
BROKEN = re.compile(r"\b(?:unk|undefined|nan)\b|([\u3400-\u9fff])\1{7}", re.I)
PRIORITIES = {"ui": 12, "dnf": 12, "character": 12, "town": 8, "quest": 8, "skill": 7,
              "newrandomoption": 7, "dungeon": 7, "equipment": 4, "stackable": 4}
DO_NOT_TRANSLATE = re.compile(r"^[A-Z0-9_./:\\ -]+$|^\S+_\S+_[\w.]+$")
SIGNED = re.compile(r"([+-])\s*(\d+(?:[.,]\d+)*)")
KNOWN_MARKUP = {"[event]": "[活动]"}


def waiting_rows(path, references):
    score = Counter()
    for table, _, _, source in (json.loads(line) for line in path.open(encoding="utf-8")):
        kind = Path(table).name.split(".", 1)[0]
        score[source] += PRIORITIES.get(kind, 3)
    if references:
        data = json.loads(references.read_text(encoding="utf-8"))
        for count, _, _, source in data["top_untranslated_references"]:
            if source in score:
                score[source] += count
    return sorted(score, key=lambda s: (-score[s], len(s), s))


def language(text):
    if KANA.search(text):
        return "jpn_Jpan"
    if KOREAN.search(text):
        return "kor_Hang"
    return "eng_Latn"


def fragmentable(text):
    return bool(WORDS.search(text) and not RESOURCE.search(text) and not (
        DO_NOT_TRANSLATE.fullmatch(text) and "_" in text
    ))


def fragments(text):
    for i, chunk in enumerate(BOUNDARY.split(text)):
        if i % 2 == 0 and fragmentable(chunk):
            yield chunk.strip()


def truth(src, dst):
    return (approved(src, dst) and NUMBER.findall(src) == NUMBER.findall(dst)
            and SIGNED.findall(src) == SIGNED.findall(dst)
            and src.count("%") == dst.count("%")
            and not BROKEN.search(dst) and len(dst) <= len(src) * 3 + 16)


def assemble(source, cache):
    result = []
    changed = False
    for i, part in enumerate(BOUNDARY.split(source)):
        if i % 2 or not fragmentable(part):
            replacement = KNOWN_MARKUP.get(part.casefold(), part)
            result.append(replacement)
            changed |= replacement != part
            continue
        key = part.strip()
        answer = cache.get(key)
        if not answer:
            return None
        changed = True
        result.append(part[:len(part) - len(part.lstrip())] + answer + part[len(part.rstrip()):])
    text = "".join(result)
    return text if changed and truth(source, text) else None


def simplify(text):
    if os.name != "nt":
        return text
    target = create_unicode_buffer(len(text) * 2 + 6)
    written = WinDLL("kernel32").LCMapStringW(0x0804, 0x02000000, text, -1, target, len(target))
    return target.value if written else text


def init_engine(args):
    paths = []
    for dll in (Path(os.sys.prefix) / "Lib" / "site-packages" / "nvidia").rglob("*.dll"):
        if dll.parent not in paths:
            paths.append(dll.parent)
    if paths:
        os.environ["PATH"] = os.pathsep.join([*(str(p) for p in paths), os.environ["PATH"]])
    handles = [os.add_dll_directory(str(path)) for path in paths]
    import ctranslate2
    import sentencepiece

    spms = ("source.spm", "target.spm") if args.engine == "opus" else ("sentencepiece.bpe.model",) * 2
    required = ("config.json", "model.bin", "shared_vocabulary.json", *spms)
    for f in required:
        if not (args.model / f).is_file():
            raise ValueError(f"missing model file: {f}")
    if args.engine == "opus":
        manifest = json.loads((args.model / "manifest.json").read_text())
        for row in manifest["files"]:
            f = args.model / row["name"]
            if f.name in required:
                with f.open("rb") as stream:
                    digest = hashlib.sha256()
                    for block in iter(lambda: stream.read(1 << 20), b""):
                        digest.update(block)
                if f.stat().st_size != row["size"] or digest.hexdigest() != row["sha256"]:
                    raise ValueError(f"model checksum mismatch: {f.name}")
    else:
        if not args.model_sha256:
            raise ValueError("NLLB requires --model-sha256")
        digest = hashlib.sha256()
        with (args.model / "model.bin").open("rb") as stream:
            for block in iter(lambda: stream.read(1 << 20), b""):
                digest.update(block)
        if digest.hexdigest() != args.model_sha256:
            raise ValueError("NLLB model checksum mismatch")
    try:
        translator = ctranslate2.Translator(
            str(args.model), device=args.device, compute_type="int8_float16" if args.device == "cuda" else "int8",
            inter_threads=1 if args.device == "cuda" else args.threads, intra_threads=2,
        )
    except (RuntimeError, ValueError) as exc:
        if args.device != "cuda" or not args.cpu_fallback:
            raise
        print(f"CUDA unavailable, using CPU: {exc}", flush=True)
        translator = ctranslate2.Translator(str(args.model), device="cpu", compute_type="int8",
                                            inter_threads=args.threads, intra_threads=2)
    source, target = (sentencepiece.SentencePieceProcessor(model_file=str(args.model / f)) for f in spms)
    return translator, source, target, handles


def batches(args, db, rows):
    translator, src_model, dst_model, handles = init_engine(args)
    known = set(r[0] for r in db.execute("SELECT src FROM fragments"))
    todo = {}
    for text in rows:
        for frag in fragments(text):
            lang = language(frag)
            if args.language and lang != args.language:
                continue
            if (args.engine == "nllb" or lang == "eng_Latn") and frag not in known and len(frag) <= 900:
                todo[frag] = None
    work = list(todo)
    if args.limit:
        work = work[:args.limit]
    print(f"Device: {translator.device}; fragments queued: {len(work)}", flush=True)
    start = time.monotonic()
    for offset in range(0, len(work), 384):
        pages = work[offset:offset + 384]
        encoded = src_model.encode(pages, out_type=str)
        valid = [
            (p, [">>cmn_Hans<<" if args.engine == "opus" else language(p), *tokens, "</s>"])
            for p, tokens in zip(pages, encoded) if 0 < len(tokens) <= 250
        ]
        results = translator.translate_batch(
            [tokens for _, tokens in valid], batch_type="tokens", max_batch_size=2048,
            beam_size=args.beam, max_input_length=256, max_decoding_length=300,
            target_prefix=[["zho_Hans"]] * len(valid) if args.engine == "nllb" else None,
        ) if valid else []
        accepted = []
        for (original, _), answer in zip(valid, results):
            pieces = answer.hypotheses[0]
            if pieces and pieces[0] == "zho_Hans":
                pieces = pieces[1:]
            value = simplify(dst_model.decode(pieces).strip())
            accepted.append((original, value if HAN.search(value) and truth(original, value) else None))
        db.executemany("INSERT OR IGNORE INTO fragments VALUES (?, ?)", accepted)
        db.commit()
        if offset == 0 or offset % (384 * 12) == 0:
            print(f"Fragments {offset + len(pages)}/{len(work)} in {time.monotonic() - start:.1f}s", flush=True)
    del handles


def main(args):
    with sqlite3.connect(args.cache, timeout=30) as db:
        db.execute("PRAGMA journal_mode=WAL")
        db.execute("CREATE TABLE IF NOT EXISTS fragments(src TEXT PRIMARY KEY, dst TEXT)")
        rows = waiting_rows(args.missing, args.references)
        if args.model:
            batches(args, db, rows)
        cache = dict(db.execute("SELECT src, dst FROM fragments WHERE dst IS NOT NULL"))
        translated = {src: dst for src in rows if (dst := assemble(src, cache))}
        with args.output.open("x", encoding="utf-8") as f:
            json.dump(translated, f, ensure_ascii=True)
        print(f"Accepted overlays: {len(translated)}/{len(rows)} unique display values", flush=True)


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    for flag in ("missing", "cache", "output", "references", "model"):
        p.add_argument("--" + flag, type=Path, required=flag in ("missing", "cache", "output"))
    p.add_argument("--device", default="cuda", choices=("cpu", "cuda"))
    p.add_argument("--engine", default="opus", choices=("opus", "nllb"))
    p.add_argument("--language", choices=("eng_Latn", "kor_Hang", "jpn_Jpan"))
    p.add_argument("--model-sha256")
    p.add_argument("--cpu-fallback", action="store_true")
    p.add_argument("--threads", type=int, default=5)
    p.add_argument("--beam", type=int, default=1)
    p.add_argument("--limit", type=int, default=0)
    main(p.parse_args())
