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


root = Path(__file__).resolve().parent
if len(sys.argv) != 2:
    sys.exit("usage: ROLLBACK.sh <candidate copy>")
target = Path(sys.argv[1]).resolve()
before = root / "before-dstr.dat"
manifest = json.loads((root / "DSTR.diff.json").read_text(encoding="utf-8"))
if target == (root / "dstr.dat") or target == before or target.parent != root:
    sys.exit("restore only a separate copy in this bundle")
def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

if sha(before) != manifest["before"] or sha(target) != manifest["after"]:
    sys.exit("unexpected baseline or candidate copy identity")
with tempfile.NamedTemporaryFile(dir=root, delete=False) as out:
    out.write(before.read_bytes())
    tmp = Path(out.name)
try:
    os.replace(tmp, target)
finally:
    tmp.unlink(missing_ok=True)
result = sha(target)
if result != manifest["before"]:
    sys.exit("rollback verification failed")
print(f"restored_sha256={result}")
