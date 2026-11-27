# -*- coding: utf-8 -*-
"""Preflight, build and storage lifecycle for the US100 development server.

Every check here exists because its absence cost a debugging round:

* the client directory was renamed once and 90 files kept the old literal, so
  the next launch died inside Popen and left the server orphaned;
* `bin\\wireprobe-us100.exe` was twice found older than the source it was
  supposed to contain, so a whole test round measured the previous build —
  `build_server()` now rebuilds it with the project's own Go toolchain
  whenever any source is newer;
* port 7001 was occupied by an orphaned server while a new client pointed at it;
* the database used to live in another project's tree — it is now db100, a
  single file inside this project that the server opens itself (no
  PostgreSQL, no Redis, no storage processes to start or stop).

Read-only by default: `report()` only looks. `build_server()`,
`start_storage()` and `stop_storage()` are the functions that change
anything, and they refuse to touch paths outside this project.
"""
from __future__ import annotations

import json
import pathlib
import socket
import subprocess
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
SERVER = ROOT / 'server-us100'
STORAGE = SERVER / 'runtime' / 'storage'
CONFIG = STORAGE / 'local.json'

SERVER_BIN = SERVER / 'bin' / 'wireprobe-us100.exe'
SOURCE_DIRS = [SERVER / 'cmd', SERVER / 'internal']

# Client candidates, in order. The directory was renamed from the first to the
# second; both are accepted so a rename in either direction keeps working.
CLIENT_CANDIDATES = [
    pathlib.Path(r'D:\DNF\client\100US\DFO'),
    pathlib.Path(r'D:\DNF\client\US100'),
    pathlib.Path(r'D:\DNF\客户端\US100'),
]

# Files this project's launchers pass that are NOT derived from the US100 PVF:
# they carry the 90-level reference model's numbers (see each file's own note,
# e.g. configs\us100\rules.bag.json "us100_note"). They are listed here so a
# run never implies these values were recovered from this client.
REFERENCE_MODEL_FILES = [
    'server-us100/configs/us100/rules.bag.json',
    'server-us100/configs/us100/rules.cards.json',
    'server-us100/configs/us100/rules.drop.json',
    'server-us100/configs/us100/rules.experience.json',
    'server-us100/configs/us100/rules.wear.json',
    'server-us100/configs/character-probe.json',
    'server-us100/configs/select-world-probe.json',
    'server-us100/configs/town-entry-probe.json',
    'server-us100/configs/world-probe.json',
    'server-us100/configs/fatigue-probe.json',
    'server-us100/configs/vault.generated.json',
    'server-us100/configs/channel.local34.json',
    'server-us100/configs/account-options.current35.json',
]

OK, WARN, FAIL = 'ok', 'warn', 'fail'


def client_dir() -> pathlib.Path | None:
    override = None
    try:
        import os
        override = os.environ.get('DFO_CLIENT_DIR')
    except Exception:
        override = None
    if override:
        candidate = pathlib.Path(override)
        if candidate.is_dir():
            return candidate
    for candidate in CLIENT_CANDIDATES:
        if candidate.is_dir():
            return candidate
    return None


def client_exe() -> pathlib.Path:
    directory = client_dir()
    return (directory / 'DFO.exe') if directory else CLIENT_CANDIDATES[0] / 'DFO.exe'


def storage_config() -> dict:
    return json.loads(CONFIG.read_text(encoding='utf-8-sig'))


def _listening(host: str, port: int, timeout: float = 0.4) -> bool:
    with socket.socket() as probe:
        probe.settimeout(timeout)
        return probe.connect_ex((host, port)) == 0


def _port_free(host: str, port: int) -> bool:
    with socket.socket() as probe:
        try:
            probe.bind((host, port))
        except OSError:
            return False
    return True


def newest_source_mtime() -> tuple[float, str]:
    newest, where = 0.0, ''
    for base in SOURCE_DIRS:
        for path in base.rglob('*.go'):
            mtime = path.stat().st_mtime
            if mtime > newest:
                newest, where = mtime, str(path.relative_to(ROOT))
    return newest, where


def _findings() -> list[tuple[str, str]]:
    out: list[tuple[str, str]] = []

    # --- client ---------------------------------------------------------
    directory = client_dir()
    if directory is None:
        out.append((FAIL, 'client directory not found; tried: %s' % ', '.join(str(c) for c in CLIENT_CANDIDATES)))
    else:
        out.append((OK, 'client directory: %s' % directory))
        for name, minimum in (('DFO.exe', 100 * 1024 * 1024), ('Script.pvf', 10 * 1024 * 1024), ('sk.dat', 1024)):
            path = directory / name
            if not path.is_file():
                out.append((FAIL, 'client file missing: %s' % path))
            elif path.stat().st_size < minimum:
                out.append((FAIL, 'client file too small (%d bytes): %s' % (path.stat().st_size, path)))
            else:
                out.append((OK, '%s %.1f MB' % (name, path.stat().st_size / 1024 / 1024)))
        stub = directory / 'NGClient64.aes'
        ours = ROOT / 'ngs_stub' / 'NGClient64.aes'
        if not stub.is_file():
            out.append((FAIL, 'patched NGClient64.aes missing from the client: %s' % stub))
        elif ours.is_file() and stub.stat().st_size != ours.stat().st_size:
            out.append((WARN, 'client NGClient64.aes (%d bytes) differs from this project\'s stub (%d bytes)'
                        % (stub.stat().st_size, ours.stat().st_size)))
        else:
            out.append((OK, 'NGClient64.aes present (%d bytes)' % stub.stat().st_size))
        ini = directory / 'ngs_stub.ini'
        if ini.is_file():
            text = ini.read_text(encoding='utf-8', errors='replace')
            setting = [ln for ln in text.splitlines() if ln.strip().startswith('5=')]
            if setting and setting[0].split('=')[1].strip() != '0':
                # ord5=1 makes the client allocate an image every frame: the
                # memory went to 22.7 GB before this was found.
                out.append((FAIL, 'ngs_stub.ini has %s — ord5 must be 0 or the client leaks memory' % setting[0].strip()))

    # --- server binary and freshness ------------------------------------
    if not SERVER_BIN.is_file():
        out.append((FAIL, 'server binary missing: %s' % SERVER_BIN))
    else:
        binary_mtime = SERVER_BIN.stat().st_mtime
        source_mtime, where = newest_source_mtime()
        binary_age = SERVER_BIN.stat().st_size
        if source_mtime > binary_mtime:
            out.append((FAIL, 'server binary is OLDER than the source: %s (newest %s). Rebuild before running.'
                        % (time.strftime('%H:%M:%S', time.localtime(binary_mtime)), where)))
        else:
            out.append((OK, 'server binary is newer than every source file (%d bytes)' % binary_age))

    # --- ports ----------------------------------------------------------
    ch_host, ch_port = '0.0.0.0', 7001
    ch_cfg = SERVER / 'configs' / 'us100' / 'channel.json'
    if ch_cfg.is_file():
        try:
            listen_val = json.loads(ch_cfg.read_text(encoding='utf-8')).get('Listen', '')
            if ':' in listen_val:
                h, p = listen_val.rsplit(':', 1)
                ch_host, ch_port = h, int(p)
        except Exception:
            pass
    if not _port_free(ch_host, ch_port):
        out.append((FAIL, '%s:%d is already in use (orphaned server?) — stop it first' % (ch_host, ch_port)))
    else:
        out.append((OK, '%s:%d is free' % (ch_host, ch_port)))

    # --- storage (db100 single file) ------------------------------------
    try:
        config = storage_config()
    except Exception as exc:
        out.append((FAIL, 'storage config unreadable: %r' % (exc,)))
        return out
    db_path = pathlib.Path(config.get('db_path', '') or (SERVER / 'runtime' / 'us100.db'))
    if not str(db_path).lower().startswith(str(ROOT).lower()):
        out.append((FAIL, 'database file lives outside this project: %s' % db_path))
    elif db_path.is_file():
        out.append((OK, 'db100 database: %s (%.1f KB)' % (db_path, db_path.stat().st_size / 1024)))
    else:
        out.append((OK, 'db100 database will be created on first open: %s' % db_path))
    seed_path = pathlib.Path(config.get('seed_path', '') or (SERVER / 'runtime' / 'us100_seed.json'))
    if seed_path.is_file():
        out.append((OK, 'db100 seed present: %s' % seed_path.name))
    else:
        out.append((WARN, 'db100 seed missing (fresh empty database): %s' % seed_path))

    # --- running instances ----------------------------------------------
    for image in ('wireprobe-us100.exe', 'DFO.exe'):
        listing = subprocess.run(['tasklist', '/FI', 'IMAGENAME eq %s' % image, '/NH'],
                                 capture_output=True, text=True, errors='replace')
        if image.lower() in (listing.stdout or '').lower():
            out.append((WARN, '%s is already running' % image))

    # --- versions -------------------------------------------------------
    if sys.version_info < (3, 10):
        out.append((FAIL, 'python %d.%d is too old (3.10+)' % sys.version_info[:2]))
    else:
        out.append((OK, 'python %d.%d.%d' % sys.version_info[:3]))

    # --- provenance -----------------------------------------------------
    reference = [rel for rel in REFERENCE_MODEL_FILES if (ROOT / rel).is_file()]
    if reference:
        out.append((WARN, '%d config files carry the 90-level reference model, not US100-recovered values: %s'
                    % (len(reference), ', '.join(reference[:4]) + ('...' if len(reference) > 4 else ''))))
    return out


def report() -> bool:
    findings = _findings()
    for level, message in findings:
        print('[%s] %s' % ({OK: ' ok ', WARN: 'warn', FAIL: 'FAIL'}[level], message), flush=True)
    failures = [m for level, m in findings if level == FAIL]
    print('\n%d check(s), %d failure(s)' % (len(findings), len(failures)), flush=True)
    return not failures


GO_BIN = ROOT / 'tools' / 'go' / 'bin' / 'go.exe'


def build_server() -> None:
    """Rebuild bin\\wireprobe-us100.exe with the project's own Go toolchain.

    Skipped when the binary is already newer than every source file, so the
    usual start pays no compile time; a stale binary is rebuilt instead of
    failing the run (it twice measured a previous build before this existed).
    """
    if SERVER_BIN.is_file():
        source_mtime, _ = newest_source_mtime()
        if source_mtime <= SERVER_BIN.stat().st_mtime:
            print('server binary is up to date', flush=True)
            return
    if not GO_BIN.is_file():
        raise RuntimeError('project Go toolchain missing: %s' % GO_BIN)
    env = dict(__import__('os').environ)
    env['PATH'] = str(GO_BIN.parent) + __import__('os').pathsep + env.get('PATH', '')
    env['GOPATH'] = str(ROOT / 'tools' / 'gopath')
    env['GOCACHE'] = str(ROOT / 'tools' / 'gocache')
    print('building server binary...', flush=True)
    SERVER_BIN.parent.mkdir(parents=True, exist_ok=True)
    result = subprocess.run([str(GO_BIN), 'build', '-o', str(SERVER_BIN), './cmd/wireprobe'],
                            cwd=str(SERVER), env=env, capture_output=True, text=True,
                            errors='replace', timeout=600)
    if result.returncode != 0:
        raise RuntimeError('server build failed: %s' % ((result.stderr or '').strip()[-800:]))
    print('server binary built: %s' % SERVER_BIN, flush=True)


def start_storage() -> None:
    """db100 storage: a single file the server opens itself.

    PostgreSQL and Redis are gone from this project; what is left to "start"
    is a sanity check that the database path is usable and inside the project.
    """
    config = storage_config()
    db_path = pathlib.Path(config.get('db_path', '') or (SERVER / 'runtime' / 'us100.db'))
    if not str(db_path).lower().startswith(str(ROOT).lower()):
        raise RuntimeError('database file is outside this project: %s' % db_path)
    db_path.parent.mkdir(parents=True, exist_ok=True)
    if db_path.is_file():
        print('db100 database ready: %s (%.1f KB)' % (db_path, db_path.stat().st_size / 1024), flush=True)
    else:
        print('db100 database will be created on first open: %s' % db_path, flush=True)


def stop_storage() -> None:
    """db100 storage has no processes; the database is a file."""
    print('db100 storage: nothing to stop (single-file database)', flush=True)


if __name__ == '__main__':
    if '--start-storage' in sys.argv:
        start_storage()
    elif '--stop-storage' in sys.argv:
        stop_storage()
    else:
        raise SystemExit(0 if report() else 1)
