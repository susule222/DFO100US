#!/bin/sh
""":"
exec python3 "$0" "$@"
":"""
import hashlib
import json
import os
from pathlib import Path
import sys
import tempfile


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


root = Path(__file__).resolve().parent
if len(sys.argv) != 2:
    sys.exit("usage: ROLLBACK.sh <separate Font archive copy>")
target = Path(sys.argv[1]).resolve()
before = root / "before-Fonts.zip"
if target.parent != root or target in {root/"Fonts.zip", before}:
    sys.exit("restore only a separate copy in this bundle")
patch = json.loads((root/"FONT.diff.json").read_text(encoding="utf-8"))
if digest(before) != patch["before"] or digest(target) != patch["after"]:
    sys.exit("unexpected baseline or candidate copy fingerprint")
with tempfile.NamedTemporaryFile(dir=root, delete=False) as f:
    f.write(before.read_bytes())
    tmp = Path(f.name)
try:
    os.replace(tmp, target)
finally:
    tmp.unlink(missing_ok=True)
if digest(target) != patch["before"]:
    sys.exit("font archive rollback hash differs")
print(f"restored_sha256={digest(target)}")
