"""Extract unambiguous DNF names for use as local-model prompt guidance."""
import argparse
import json
from pathlib import Path
import re

from localize_us100_tables import HAN, KANA, KOREAN, add_lexicon
from us100_concordance import CURATED


FILES = frozenset({"character", "creature", "dungeon", "monster", "npc", "region", "skill", "town", "worldmap"})
KEY = re.compile(r"^(?:name|growtype_name|map_name|dungeon_name|title|npc_name)[_a-zA-Z0-9.-]*$", re.I)
LATIN_NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9 .\-'’:/&]{5,57}$")


def extract(rows):
    terms = {}
    with rows.open(encoding="utf-8") as f:
        for line in f:
            path, key, status, source, target = json.loads(line)
            category = Path(path).name.split(".", 1)[0]
            if category not in FILES or status not in CURATED or not KEY.fullmatch(key):
                continue
            if not LATIN_NAME.fullmatch(source) or not 2 <= len(target) <= 28 or not HAN.search(target):
                continue
            if KANA.search(target) or KOREAN.search(target) or re.search(r"[%<>\\\r\n\[\]]", target):
                continue
            if len(source.split()) < 2 and not source[0].isupper():
                continue
            add_lexicon(terms, source.lower().replace("’", "'"), target)
    return {key: val for key, val in sorted(terms.items()) if val}


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--translations", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    args = p.parse_args()
    terms = extract(args.translations)
    with args.output.open("x", encoding="utf-8") as out:
        json.dump(terms, out, ensure_ascii=True, indent=2)
    print(f"Terms with trusted in-package references: {len(terms)}")
