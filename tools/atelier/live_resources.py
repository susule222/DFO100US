"""Real-time PVF and NPK source auto-detection, selection, unwrapping and catalog syncing."""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import shutil
import struct
import subprocess
import sys
import tempfile
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import atelier_runtime as rt
import core

CONFIG_PATH = core.DATA / 'resource_sources.json'
INDEX_PATH = rt.DATA / 'pvf-index.json'
PVF_CACHE = rt.DATA / 'pvf-cache'
EXPORTER_BIN = core.SERVER / 'bin' / 'atelierlibrary.exe'
EXPORTER_SRC = core.SERVER / 'scripts' / 'atelierlibrary'

PEM_PRIVATE_KEY_RVA = 0x7DCBE30
AES_KEY_RVA = 0x7DCC1D0
SEGMENT_SIZE = 0xA00000
SEGMENT_HEAD = 0x2800


def _stream_xor(key: str, raw: bytes) -> bytes:
    k = [ord(x) for x in key]
    seed = (0x339E9711 * k[0] + 0x393 * (k[3] + 0x393 * (k[2] + 0x393 * k[1]))) & 0xFFFFFFFF
    out = bytearray(raw)
    for off in range(0, len(out), 4):
        t = (0x343FD * seed + 0x269EC3) & 0xFFFFFFFF
        seed = (0x343FD * t + 0x269EC3) & 0xFFFFFFFF
        q = struct.pack('<I', (t & 0xFFFF0000) + (seed >> 16))
        for i in range(min(4, len(out) - off)):
            out[off + i] ^= q[i]
    return bytes(out)


def is_inner_pvf(path: Path) -> bool:
    """Return True if path is an already unwrapped inner PVF (nkpi or iNfO-XORed nkpi)."""
    if not path.is_file() or path.stat().st_size < 48:
        return False
    with path.open('rb') as f:
        head = f.read(48)
    if head[:4] == b'nkpi':
        return True
    if _stream_xor('iNfO', head)[:4] == b'nkpi':
        return True
    return False


def load_config(config_path: Path | None = None) -> dict:
    p = config_path or CONFIG_PATH
    defaults = {
        'pvf_mode': 'auto',
        'pvf_path': '',
        'npk_mode': 'auto',
        'npk_dir': '',
        'extra_npk_dirs': [],
        'wallpaper': 'wisdom',
    }
    if p.is_file():
        try:
            saved = core.read(p)
            if isinstance(saved, dict):
                defaults.update({k: saved[k] for k in defaults if k in saved})
        except (OSError, ValueError):
            pass
    return defaults


def save_config(cfg: dict, config_path: Path | None = None) -> dict:
    p = config_path or CONFIG_PATH
    clean = {
        'pvf_mode': 'manual' if cfg.get('pvf_mode') == 'manual' else 'auto',
        'pvf_path': str(cfg.get('pvf_path') or ''),
        'npk_mode': 'manual' if cfg.get('npk_mode') == 'manual' else 'auto',
        'npk_dir': str(cfg.get('npk_dir') or ''),
        'extra_npk_dirs': [str(x) for x in (cfg.get('extra_npk_dirs') or []) if x],
        'wallpaper': str(cfg.get('wallpaper') or 'wisdom'),
    }
    core.atomic(p, clean)
    return clean


def auto_pvf_path() -> Path | None:
    """Automatically choose the live client Script.pvf (never preferring stale cached copies)."""
    cdir = core.pre.client_dir()
    if cdir:
        live = cdir / 'Script.pvf'
        if live.is_file():
            return live
    fallback = core.ROOT / 'runtime' / 'pvf_source' / 'Script.inner.pvf'
    return fallback if fallback.is_file() else None


def auto_npk_dir() -> Path:
    cdir = core.pre.client_dir()
    if cdir and (cdir / 'ImagePacks2').is_dir():
        return cdir / 'ImagePacks2'
    return core.pre.client_exe().parent / 'ImagePacks2'


def resolve_pvf_path(cfg: dict | None = None) -> tuple[Path | None, str]:
    cfg = cfg if cfg is not None else load_config()
    if cfg.get('pvf_mode') == 'manual' and cfg.get('pvf_path'):
        p = Path(cfg['pvf_path'])
        if p.is_file():
            return p, 'manual'
    return auto_pvf_path(), 'auto'


def resolve_npk_dirs(cfg: dict | None = None) -> tuple[Path, list[Path], str]:
    cfg = cfg if cfg is not None else load_config()
    extras = [Path(x) for x in (cfg.get('extra_npk_dirs') or []) if x and Path(x).is_dir()]
    if cfg.get('npk_mode') == 'manual' and cfg.get('npk_dir'):
        p = Path(cfg['npk_dir'])
        if p.is_dir():
            return p, extras, 'manual'
    return auto_npk_dir(), extras, 'auto'


def pvf_fingerprint(path: Path | None) -> dict | None:
    if path is None or not path.is_file():
        return None
    resolved = path.resolve()
    st = resolved.stat()
    raw = f'{str(resolved).lower()}|{st.st_size}|{st.st_mtime_ns}'.encode('utf-8')
    key = hashlib.sha256(raw).hexdigest()[:20]
    return {
        'path': str(resolved),
        'name': resolved.name,
        'size': st.st_size,
        'mtime_ns': st.st_mtime_ns,
        'key': key,
        'inner': is_inner_pvf(resolved),
    }


def npk_snapshot(npk_dir: Path, extra_dirs: list[Path] | None = None) -> dict:
    """Fast real-time snapshot of NPK directory state using Windows WIN32_FIND_DATA via scandir."""
    dirs = [d for d in [*(extra_dirs or []), npk_dir] if d and d.is_dir()]
    count = 0
    latest_mtime = 0
    total_size = 0
    dir_mtimes = []
    for d in dirs:
        try:
            dir_mtimes.append((str(d.resolve()).lower(), d.stat().st_mtime_ns))
            with os.scandir(d) as it:
                for entry in it:
                    if entry.name.lower().endswith('.npk') and entry.is_file():
                        st = entry.stat()
                        count += 1
                        total_size += st.st_size
                        if st.st_mtime_ns > latest_mtime:
                            latest_mtime = st.st_mtime_ns
        except OSError:
            pass
    token = (tuple(dir_mtimes), count, total_size, latest_mtime)
    return {
        'dir': str(npk_dir),
        'count': count,
        'total_size': total_size,
        'latest_mtime_ns': latest_mtime,
        'token': token,
    }


def current_meta() -> dict | None:
    pointer = rt.DATA / 'current.json'
    if not pointer.is_file():
        return None
    try:
        return core.read(pointer)
    except (OSError, ValueError):
        return None


def is_catalog_synced(fp: dict | None) -> bool:
    """Return True if the active runtime/atelier-data/current.json matches the given PVF fingerprint."""
    if not fp:
        return False
    meta = current_meta()
    if not meta:
        return False
    if meta.get('inputs') != rt.inputs():
        return False
    gen = meta.get('generation', '')
    if not gen or not (rt.DATA / gen / 'items.sqlite').is_file():
        return False
    pvf = meta.get('pvf') or {}
    if pvf.get('key') and pvf.get('key') == fp['key']:
        return True
    if (pvf.get('path', '').lower() == fp['path'].lower()
            and pvf.get('size') == fp['size']
            and pvf.get('mtime_ns') == fp['mtime_ns']):
        return True
    return False


def _load_index() -> dict:
    if INDEX_PATH.is_file():
        try:
            d = core.read(INDEX_PATH)
            if isinstance(d, dict):
                return d
        except (OSError, ValueError):
            pass
    return {}


def _save_index(idx: dict) -> None:
    core.atomic(INDEX_PATH, idx)


def try_fast_activate(fp: dict) -> dict | None:
    """If this exact PVF fingerprint was already indexed before, switch current.json instantly."""
    if not fp:
        return None
    idx = _load_index()
    entry = idx.get(fp['key'])
    if not isinstance(entry, dict):
        return None
    gen = entry.get('generation', '')
    folder = rt.DATA / gen
    if not gen or not (folder / 'items.sqlite').is_file():
        return None
    baseline = rt.inputs()
    if entry.get('inputs') != baseline:
        return None
    meta = dict(entry)
    meta['pvf'] = dict(fp)
    core.atomic(rt.DATA / 'current.json', meta)
    return meta


def _find_key_files(pvf_path: Path) -> tuple[Path, Path]:
    candidates = [pvf_path.parent]
    cdir = core.pre.client_dir()
    if cdir and cdir not in candidates:
        candidates.append(cdir)
    for folder in candidates:
        exe = folder / 'DFO.exe'
        sk = folder / 'sk.dat'
        if exe.is_file() and sk.is_file():
            return exe, sk
    raise FileNotFoundError(
        '需要同目录或客户端目录中的 DFO.exe 与 sk.dat 来自动解包外层加密的 Script.pvf'
    )


def _derive_segment_keys(exe_path: Path, sk_path: Path) -> list[bytes]:
    from inspect_pe import PE
    from cryptography.hazmat.primitives.asymmetric.padding import PKCS1v15
    from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
    from cryptography.hazmat.primitives.serialization import load_pem_private_key

    pe = PE(str(exe_path))
    try:
        raw_pem = pe.read(PEM_PRIVATE_KEY_RVA, 0x600)
        end = raw_pem.find(b'-----END PRIVATE KEY-----')
        if end < 0:
            raise ValueError('DFO.exe 中未找到 PVF 私钥段')
        private = load_pem_private_key(raw_pem[:end + len(b'-----END PRIVATE KEY-----')], password=None)
        aes_hex = pe.read(AES_KEY_RVA, 64).decode('ascii')
        if len(aes_hex) != 64:
            raise ValueError('DFO.exe 中未找到 64 位十六进制 AES 密钥')
    finally:
        pe.close()

    sk_data = sk_path.read_bytes()
    block_size = private.key_size // 8
    if len(sk_data) % block_size:
        raise ValueError('sk.dat 长度不是 RSA 块大小的整数倍')
    unwrapped = b''.join(
        private.decrypt(sk_data[i:i + block_size], PKCS1v15())
        for i in range(0, len(sk_data), block_size)
    )
    prefix = len(unwrapped) // 256 * 256
    dec = Cipher(algorithms.AES(bytes.fromhex(aes_hex)), modes.CBC(bytes(16))).decryptor()
    unwrapped = dec.update(unwrapped[:prefix]) + dec.finalize() + unwrapped[prefix:]
    if len(unwrapped) % 32:
        raise ValueError('解密后的 sk.dat 密钥长度不是 32 字节对齐')
    return [unwrapped[i:i + 32] for i in range(0, len(unwrapped), 32)]


def ensure_inner_pvf(pvf_path: Path, fp: dict, progress=None) -> Path:
    """Return an unwrapped inner PVF path for reading by Go atelierlibrary."""
    if fp.get('inner') or is_inner_pvf(pvf_path):
        return pvf_path
    PVF_CACHE.mkdir(parents=True, exist_ok=True)
    cached = PVF_CACHE / f"{fp['key']}.inner.pvf"
    if cached.is_file() and cached.stat().st_size == fp['size'] and is_inner_pvf(cached):
        return cached
    if progress:
        progress(f'正在自动解包最新 PVF：{pvf_path.name}…')
    from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

    exe_path, sk_path = _find_key_files(pvf_path)
    keys = _derive_segment_keys(exe_path, sk_path)
    total = pvf_path.stat().st_size
    segments = (total + SEGMENT_SIZE - 1) // SEGMENT_SIZE
    if len(keys) < segments:
        raise ValueError(f'sk.dat 密钥数量 ({len(keys)}) 少于 PVF 分段数 ({segments})')

    with pvf_path.open('rb') as src:
        first = src.read(SEGMENT_HEAD)
    dec0 = Cipher(algorithms.AES(keys[0]), modes.CBC(bytes(16))).decryptor()
    head = _stream_xor('iNfO', (dec0.update(first) + dec0.finalize())[:48])
    if head[:4] != b'nkpi':
        raise ValueError('外层 Script.pvf 解包后内层头校验失败（非 nkpi）')

    tmp = PVF_CACHE / f".{fp['key']}.{os.getpid()}.part"
    try:
        with pvf_path.open('rb') as src, tmp.open('wb') as dst:
            seg = 0
            while True:
                chunk = src.read(SEGMENT_SIZE)
                if not chunk:
                    break
                if seg < len(keys):
                    if len(chunk) < SEGMENT_HEAD:
                        raise ValueError(f'PVF 第 {seg} 分段长度不足')
                    dec = Cipher(algorithms.AES(keys[seg]), modes.CBC(bytes(16))).decryptor()
                    chunk = dec.update(chunk[:SEGMENT_HEAD]) + dec.finalize() + chunk[SEGMENT_HEAD:]
                dst.write(chunk)
                seg += 1
        tmp.replace(cached)
    finally:
        tmp.unlink(missing_ok=True)
    _prune_pvf_cache(keep={cached.name})
    return cached


def _prune_pvf_cache(keep: set[str], max_files: int = 2) -> None:
    if not PVF_CACHE.is_dir():
        return
    files = sorted(PVF_CACHE.glob('*.inner.pvf'), key=lambda p: p.stat().st_mtime, reverse=True)
    kept = 0
    for f in files:
        if f.name in keep or kept < max_files:
            kept += 1
            continue
        f.unlink(missing_ok=True)


def _newest_exporter_source() -> float:
    newest = 0.0
    for base in (EXPORTER_SRC, core.SERVER / 'internal' / 'catalog'):
        if not base.is_dir():
            continue
        for p in base.rglob('*.go'):
            mt = p.stat().st_mtime
            if mt > newest:
                newest = mt
    return newest


def ensure_exporter_binary(progress=None) -> Path:
    if EXPORTER_BIN.is_file() and EXPORTER_BIN.stat().st_mtime >= _newest_exporter_source():
        return EXPORTER_BIN
    go_bin = core.pre.GO_BIN
    if not go_bin.is_file():
        raise FileNotFoundError(f'缺少工程内置 Go 工具链：{go_bin}')
    if progress:
        progress('正在编译实时 PVF 索引器…')
    env = dict(os.environ)
    env['PATH'] = str(go_bin.parent) + os.pathsep + env.get('PATH', '')
    env['GOPATH'] = str(core.ROOT / 'tools' / 'gopath')
    env['GOCACHE'] = str(core.ROOT / 'tools' / 'gocache')
    EXPORTER_BIN.parent.mkdir(parents=True, exist_ok=True)
    res = subprocess.run(
        [str(go_bin), 'build', '-o', str(EXPORTER_BIN), './scripts/atelierlibrary'],
        cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
        creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=300,
    )
    if res.returncode != 0:
        raise RuntimeError('编译 PVF 索引器失败：' + (res.stderr or res.stdout or '')[-600:])
    return EXPORTER_BIN


def _prune_old_generations(keep_gens: set[str], max_extra: int = 3) -> None:
    if not rt.DATA.is_dir():
        return
    folders = []
    for p in rt.DATA.iterdir():
        if p.is_dir() and len(p.name) == 32 and all(c in '0123456789abcdef' for c in p.name):
            folders.append(p)
    folders.sort(key=lambda p: p.stat().st_mtime, reverse=True)
    extra = 0
    for f in folders:
        if f.name in keep_gens:
            continue
        if extra < max_extra:
            extra += 1
            continue
        shutil.rmtree(f, ignore_errors=True)


def sync_pvf(pvf_path: Path, progress=None, force: bool = False) -> dict:
    """Synchronize the launcher's SQLite catalog with the chosen live PVF (auto-unwrapping if needed)."""
    pvf_path = Path(pvf_path)
    if not pvf_path.is_file():
        raise FileNotFoundError(f'未找到 PVF 文件：{pvf_path}')
    fp = pvf_fingerprint(pvf_path)
    if not force:
        if is_catalog_synced(fp):
            return current_meta()
        fast = try_fast_activate(fp)
        if fast is not None:
            return fast

    inner = ensure_inner_pvf(pvf_path, fp, progress=progress)
    exporter = ensure_exporter_binary(progress=progress)

    if progress:
        progress(f'正在扫描 PVF 数据与词条：{pvf_path.name}…')
    rt.DATA.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=rt.DATA, prefix='.sync-') as tmpdir:
        raw_out = Path(tmpdir) / 'atelier-library.jsonl.gz'
        env = dict(os.environ)
        env['GOGC'] = '10'
        res = subprocess.run(
            [str(exporter), '-source', str(inner), '-out', str(raw_out)],
            cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
            creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=600,
        )
        if res.returncode != 0:
            raise RuntimeError('PVF 扫描失败：' + (res.stderr or res.stdout or '')[-600:])
        if progress:
            progress('正在构建实时物品与时装索引数据库…')
        import build_library
        meta = build_library.build_from_jsonl(raw_out, pvf_meta=fp, activate=True)

    idx = _load_index()
    idx[fp['key']] = meta
    # Prune stale index entries whose folders no longer exist
    idx = {k: v for k, v in idx.items() if (rt.DATA / v.get('generation', '') / 'items.sqlite').is_file()}
    _save_index(idx)
    _prune_old_generations({meta['generation'], *(v['generation'] for v in idx.values())})
    return meta


def format_pvf_summary(fp: dict | None, mode: str, synced: bool) -> str:
    if not fp:
        return '未检测到可用 PVF'
    mb = fp['size'] / (1024 * 1024)
    ts = time.strftime('%Y-%m-%d %H:%M', time.localtime(fp['mtime_ns'] / 1e9))
    kind = '内层PVF' if fp.get('inner') else '客户端PVF'
    mode_zh = '自动实时' if mode == 'auto' else '手动指定'
    st = '已同步最新' if synced else '待同步最新'
    return f"{fp['name']} ({mb:.1f} MB · {ts} · {kind} · {mode_zh} · {st})"
