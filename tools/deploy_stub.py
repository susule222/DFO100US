# -*- coding: utf-8 -*-
"""Deploy the rebuilt NGClient64.aes stub into the client directory.

Keeps a timestamped backup of whatever was deployed, refuses to run while the
client is up (the file is mapped by the running process), and verifies the copy
by hash instead of trusting the copy call.

Usage: python tools/deploy_stub.py [--apply]
"""
import hashlib
import pathlib
import shutil
import subprocess
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
BUILT = ROOT / 'ngs_stub' / 'NGClient64.aes'
CLIENT = pathlib.Path(r'D:\DNF\client\US100')
TARGET = CLIENT / 'NGClient64.aes'

apply = '--apply' in sys.argv


def digest(path: pathlib.Path) -> str:
    hasher = hashlib.sha256()
    with path.open('rb') as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b''):
            hasher.update(block)
    return hasher.hexdigest()


if not BUILT.is_file():
    raise SystemExit('not built yet: %s' % BUILT)
if not TARGET.parent.is_dir():
    raise SystemExit('client directory not found: %s' % CLIENT)

listing = subprocess.run(['tasklist', '/FI', 'IMAGENAME eq DFO.exe', '/NH'],
                         capture_output=True, text=True, errors='replace')
if 'dfo.exe' in (listing.stdout or '').lower():
    raise SystemExit('DFO.exe is running; close the client before replacing its stub')

print('built   : %s (%d bytes, sha256 %s)' % (BUILT, BUILT.stat().st_size, digest(BUILT)[:16]))
if TARGET.is_file():
    print('deployed: %s (%d bytes, sha256 %s)' % (TARGET, TARGET.stat().st_size, digest(TARGET)[:16]))
    if digest(TARGET) == digest(BUILT):
        print('\nalready identical; nothing to do')
        raise SystemExit(0)
else:
    print('deployed: missing')

if not apply:
    print('\ndry run: nothing copied. Re-run with --apply')
    raise SystemExit(0)

if TARGET.is_file():
    stamp = time.strftime('%Y%m%d-%H%M%S')
    backup = TARGET.with_name('NGClient64.aes.%s.bak' % stamp)
    shutil.copy2(TARGET, backup)
    print('backup  : %s' % backup)

shutil.copy2(BUILT, TARGET)
if digest(TARGET) != digest(BUILT):
    raise SystemExit('copy verification failed: %s does not match the build' % TARGET)
print('deployed: %s (%d bytes, sha256 %s)' % (TARGET, TARGET.stat().st_size, digest(TARGET)[:16]))
print('match   : yes')
