# -*- coding: utf-8 -*-
"""Deploy the rebuilt NGClient64.aes stub and ngs_stub.ini into the client directory.

Keeps a timestamped backup of whatever was deployed, stages cleanly if DFO.exe
is currently running (for automatic apply on next launch), and verifies the copy
by SHA-256 instead of trusting the copy call.

Usage: python tools/deploy_stub.py [--apply]
"""
import hashlib
import json
import pathlib
import shutil
import subprocess
import sys
import time

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import us100_preflight as preflight  # noqa: E402

ROOT = pathlib.Path(__file__).resolve().parent.parent
BUILT = ROOT / 'ngs_stub' / 'NGClient64.aes'
BUILT_INI = ROOT / 'ngs_stub' / 'ngs_stub.ini'
STAGE_DIR = ROOT / 'runtime' / 'ngs-stub-stage'
PENDING_JSON = STAGE_DIR / 'pending.json'


def digest(path: pathlib.Path) -> str:
    hasher = hashlib.sha256()
    with path.open('rb') as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b''):
            hasher.update(block)
    return hasher.hexdigest()


def client_is_running() -> bool:
    listing = subprocess.run(['tasklist', '/FI', 'IMAGENAME eq DFO.exe', '/NH'],
                             capture_output=True, text=True, errors='replace')
    return 'dfo.exe' in (listing.stdout or '').lower()


def _ensure_ini(client_dir: pathlib.Path) -> str:
    ini = client_dir / 'ngs_stub.ini'
    defaults = {
        '1': '0',
        '2': '0',
        '3': '0',
        '4': '0',
        '5': '0',
        '6': '0',
        'dismiss': 'RUN ERROR',
        'locale_hooks': '0',
        'fast_channel_select': '1',
        'skip_server_select': '0',
        'ime_fix': '1',
        'plugins_enabled': '1',
        'plugins_dir': 'plugins',
    }
    if not ini.is_file():
        if BUILT_INI.is_file():
            shutil.copy2(BUILT_INI, ini)
        else:
            ini.write_text('\n'.join(f'{k}={v}' for k, v in defaults.items()) + '\n', encoding='utf-8')
        return f'created {ini.name}'
    lines = [ln.rstrip('\r\n') for ln in ini.read_text(encoding='utf-8', errors='replace').splitlines()]
    settings = {}
    order = []
    for line in lines:
        if '=' in line and not line.strip().startswith('#'):
            key, val = line.split('=', 1)
            key = key.strip()
            settings[key] = val.strip()
            if key not in order:
                order.append(key)
    changed = False
    if settings.get('5') != '0':
        settings['5'] = '0'
        if '5' not in order:
            order.append('5')
        changed = True
    for key in ('dismiss', 'locale_hooks', 'fast_channel_select', 'skip_server_select', 'ime_fix', 'plugins_enabled', 'plugins_dir'):
        if key not in settings:
            settings[key] = defaults[key]
            order.append(key)
            changed = True
    if changed:
        ini.write_text('\n'.join(f'{k}={settings[k]}' for k in order) + '\n', encoding='utf-8')
        return f'updated {ini.name}'
    return f'{ini.name} up to date'


def apply_stub(client_dir: pathlib.Path | None = None, *, allow_stage: bool = False) -> dict:
    cdir = client_dir or preflight.client_dir()
    if cdir is None or not cdir.is_dir():
        raise RuntimeError('client directory not found')
    if not BUILT.is_file():
        raise RuntimeError(f'not built yet: {BUILT}')
    target = cdir / 'NGClient64.aes'
    built_sha = digest(BUILT)
    ini_status = _ensure_ini(cdir)
    if target.is_file() and digest(target) == built_sha:
        PENDING_JSON.unlink(missing_ok=True)
        return {'status': 'up_to_date', 'target': str(target), 'sha256': built_sha, 'ini': ini_status}
    if client_is_running():
        if not allow_stage:
            raise RuntimeError('DFO.exe is running; close the client before replacing its stub')
        STAGE_DIR.mkdir(parents=True, exist_ok=True)
        staged = STAGE_DIR / 'NGClient64.aes'
        shutil.copy2(BUILT, staged)
        PENDING_JSON.write_text(json.dumps({
            'staged': str(staged),
            'target': str(target),
            'sha256': built_sha,
            'staged_at': time.strftime('%Y-%m-%dT%H:%M:%S'),
        }, indent=2), encoding='utf-8')
        return {'status': 'staged', 'target': str(target), 'sha256': built_sha, 'ini': ini_status}
    backup_path = None
    if target.is_file():
        stamp = time.strftime('%Y%m%d-%H%M%S')
        backup_path = target.with_name(f'NGClient64.aes.{stamp}.bak')
        shutil.copy2(target, backup_path)
    shutil.copy2(BUILT, target)
    if digest(target) != built_sha:
        raise RuntimeError(f'copy verification failed: {target} does not match the build')
    PENDING_JSON.unlink(missing_ok=True)
    return {
        'status': 'deployed',
        'target': str(target),
        'backup': str(backup_path) if backup_path else '',
        'sha256': built_sha,
        'ini': ini_status,
    }


def apply_staged_if_pending() -> str:
    """Apply pending staged NGClient64.aes stub before DFO.exe starts."""
    if not PENDING_JSON.is_file() and BUILT.is_file():
        cdir = preflight.client_dir()
        if cdir and (cdir / 'NGClient64.aes').is_file():
            # Keep existing deployment in sync if a newer project stub was staged
            return ''
        return ''
    if not PENDING_JSON.is_file():
        return ''
    if client_is_running():
        return ''
    info = json.loads(PENDING_JSON.read_text(encoding='utf-8'))
    staged = pathlib.Path(info['staged'])
    target = pathlib.Path(info['target'])
    expected_sha = info['sha256']
    if not staged.is_file() or digest(staged) != expected_sha:
        PENDING_JSON.unlink(missing_ok=True)
        return ''
    if target.is_file() and digest(target) != expected_sha:
        stamp = time.strftime('%Y%m%d-%H%M%S')
        shutil.copy2(target, target.with_name(f'NGClient64.aes.{stamp}.bak'))
    shutil.copy2(staged, target)
    if digest(target) != expected_sha:
        raise RuntimeError(f'staged stub copy verification failed for {target}')
    _ensure_ini(target.parent)
    PENDING_JSON.unlink(missing_ok=True)
    return f'applied staged NGClient64.aes stub ({expected_sha[:12]}) to {target}'


def main() -> int:
    apply = '--apply' in sys.argv
    allow_stage = '--stage-if-running' in sys.argv
    cdir = preflight.client_dir()
    if cdir is None or not cdir.is_dir():
        raise SystemExit('client directory not found')
    target = cdir / 'NGClient64.aes'
    if not BUILT.is_file():
        raise SystemExit('not built yet: %s' % BUILT)
    built_sha = digest(BUILT)
    print('built   : %s (%d bytes, sha256 %s)' % (BUILT, BUILT.stat().st_size, built_sha[:16]))
    if target.is_file():
        target_sha = digest(target)
        print('deployed: %s (%d bytes, sha256 %s)' % (target, target.stat().st_size, target_sha[:16]))
        if target_sha == built_sha:
            if apply:
                print('ini     : %s' % _ensure_ini(cdir))
            print('\nalready identical; nothing to do')
            return 0
    else:
        print('deployed: missing')
    if not apply:
        print('\ndry run: nothing copied. Re-run with --apply')
        return 0
    res = apply_stub(cdir, allow_stage=allow_stage)
    if res.get('backup'):
        print('backup  : %s' % res['backup'])
    print('status  : %s (%s)' % (res['status'], res['ini']))
    print('match   : %s' % ('yes' if res['status'] in ('deployed', 'up_to_date') else 'staged'))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
