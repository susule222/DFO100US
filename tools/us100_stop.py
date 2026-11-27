# -*- coding: utf-8 -*-
"""Stop the US100 development session: client, server, and storage.

This project had no stop path at all, so every session ended by hand: an
orphaned `wireprobe-us100.exe` kept 7001 bound, a leftover `DFO.exe` pointed at
a dead socket, and PostgreSQL was never shut down (its pid file was then stale
on the next start).

Order matters: the server first (so the client sees a clean disconnect), then
the client, then storage. Nothing here prints a secret; the Redis password goes
over the socket, never to the terminal.

Usage: python tools/us100_stop.py [--keep-storage] [--dry-run]
"""
from __future__ import annotations

import subprocess
import sys

sys.path.insert(0, str(__import__('pathlib').Path(__file__).resolve().parent))
import us100_preflight as preflight  # noqa: E402

IMAGES = ['wireprobe-us100.exe', 'DFO.exe']
dry = '--dry-run' in sys.argv
keep_storage = '--keep-storage' in sys.argv


def running(image: str) -> bool:
    listing = subprocess.run(['tasklist', '/FI', 'IMAGENAME eq %s' % image, '/NH'],
                             capture_output=True, text=True, errors='replace')
    return image.lower() in (listing.stdout or '').lower()


for image in IMAGES:
    if not running(image):
        print('%-24s not running' % image)
        continue
    if dry:
        print('%-24s would be killed' % image)
        continue
    result = subprocess.run(['taskkill', '/IM', image, '/T', '/F'],
                            capture_output=True, text=True, errors='replace')
    print('%-24s %s' % (image, 'killed' if result.returncode == 0 else 'kill failed: ' + (result.stderr or '').strip()[:120]))

if keep_storage:
    print('storage left running (--keep-storage)')
elif dry:
    print('storage would be stopped')
else:
    # Stop the watchdog first so it cannot race the shutdown and restart the
    # storage between the stop and the next manual start.
    pid_file = __import__('pathlib').Path(__file__).resolve().parent.parent / 'runtime' / 'storage' / 'watchdog.pid'
    try:
        pid = int(pid_file.read_text(encoding='utf-8').strip() or 0)
    except (OSError, ValueError):
        pid = 0
    if pid:
        subprocess.run(['taskkill', '/PID', str(pid), '/T', '/F'],
                       capture_output=True, text=True, errors='replace')
        print('storage watchdog pid %d stopped' % pid)
    preflight.stop_storage()

print('\ndone')
