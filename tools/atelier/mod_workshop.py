"""90JP-style unified PVF + NPK + External DLL Mod Workshop: collection ZIP scanning,
priority ordering, PVF merging (with .lst/.str merge, read-back verification, AES rewrap),
NPK sync, PE64/ABI-v1 DLL validation & priority deployment, and rollback."""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import sys
import tempfile
import time
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import core
import live_resources as lr
from sprites import index as npk_index

CACHE_ROOT = core.ROOT / 'runtime' / 'mod-pvf-cache'
MERGER_BIN = core.SERVER / 'bin' / 'pvfmerge.exe'
MERGER_SRC = core.SERVER / 'cmd' / 'pvfmerge'

CLIENT_PLUGIN_INIT_SYMBOL = 'us100_client_plugin_init'
SERVER_PLUGIN_INIT_SYMBOL = 'us100_server_plugin_init'


def inspect_plugin_dll_bytes(raw: bytes) -> dict:
    """Validate a Windows x64 DLL binary and inspect its 100US ABI v1 plugin init export.

    Returns {'target': 'client' | 'server' | 'both', 'exports': [...], 'machine': 'AMD64'}.
    Raises ValueError with a clear Chinese diagnostic message on invalid/unsupported binaries.
    """
    if len(raw) < 0x40 or raw[:2] != b'MZ':
        raise ValueError('文件头不是有效的 PE/DLL（缺少 MZ 标识）')
    pe_off = struct.unpack_from('<I', raw, 0x3C)[0]
    if pe_off + 24 > len(raw) or raw[pe_off:pe_off + 4] != b'PE\x00\x00':
        raise ValueError('无效的 PE 结构（缺少 PE 签名）')

    machine, num_sections, _ts, _sym_ptr, _sym_cnt, opt_size, characteristics = struct.unpack_from(
        '<HHIIIHH', raw, pe_off + 4
    )
    if machine != 0x8664:
        raise ValueError(f'非 64 位 x64 DLL（Machine=0x{machine:04X}），100US 仅支持 Windows x64 插件')
    if not (characteristics & 0x2000):
        raise ValueError('目标 PE 不是 DLL 模块（缺少 IMAGE_FILE_DLL 标志）')

    opt_off = pe_off + 24
    if opt_size < 120 or opt_off + opt_size > len(raw):
        raise ValueError('PE 可选头不完整或已损坏')
    magic = struct.unpack_from('<H', raw, opt_off)[0]
    if magic != 0x20B:
        raise ValueError(f'非 PE32+ (64 位) 格式（Magic=0x{magic:04X}）')

    num_rvas = struct.unpack_from('<I', raw, opt_off + 108)[0]
    export_rva, export_size = (0, 0)
    if num_rvas >= 1 and opt_size >= 120:
        export_rva, export_size = struct.unpack_from('<II', raw, opt_off + 112)

    sections: list[tuple[int, int, int, int]] = []
    sec_base = opt_off + opt_size
    for idx in range(num_sections):
        off = sec_base + idx * 40
        if off + 40 > len(raw):
            break
        vsize, vaddr, raw_size, raw_ptr = struct.unpack_from('<IIII', raw, off + 8)
        sections.append((vaddr, max(vsize, raw_size), raw_ptr, raw_size))

    def rva_to_off(rva: int) -> int | None:
        for vaddr, span, raw_ptr, raw_size in sections:
            if vaddr <= rva < vaddr + span:
                delta = rva - vaddr
                if delta < raw_size and raw_ptr + delta < len(raw):
                    return raw_ptr + delta
        return None

    exports: set[str] = set()
    if export_rva and export_size:
        exp_off = rva_to_off(export_rva)
        if exp_off is not None and exp_off + 40 <= len(raw):
            num_names = struct.unpack_from('<I', raw, exp_off + 24)[0]
            names_rva = struct.unpack_from('<I', raw, exp_off + 32)[0]
            names_off = rva_to_off(names_rva)
            if names_off is not None:
                for i in range(min(num_names, 4096)):
                    entry_off = names_off + i * 4
                    if entry_off + 4 > len(raw):
                        break
                    name_rva = struct.unpack_from('<I', raw, entry_off)[0]
                    str_off = rva_to_off(name_rva)
                    if str_off is None:
                        continue
                    end = raw.find(b'\x00', str_off, min(len(raw), str_off + 256))
                    if end > str_off:
                        try:
                            exports.add(raw[str_off:end].decode('ascii'))
                        except UnicodeDecodeError:
                            pass

    has_client = CLIENT_PLUGIN_INIT_SYMBOL in exports
    has_server = SERVER_PLUGIN_INIT_SYMBOL in exports
    if has_client and has_server:
        target = 'both'
    elif has_client:
        target = 'client'
    elif has_server:
        target = 'server'
    else:
        raise ValueError(
            f'缺少 100US ABI v1 插件入口导出（需导出 {CLIENT_PLUGIN_INIT_SYMBOL} 或 {SERVER_PLUGIN_INIT_SYMBOL}）'
        )
    return {'target': target, 'exports': sorted(exports), 'machine': 'AMD64'}


def _safe_slug(text: str, max_len: int = 28) -> str:
    slug = re.sub(r'[^a-zA-Z0-9_-]+', '_', str(text or '')).strip('_')
    return (slug[:max_len] or 'mod').lower()


def _safe_rel(raw: str) -> str | None:
    s = raw.replace('\\', '/').strip().lstrip('/')
    if not s or s.startswith('/') or ':' in s:
        return None
    parts = [p for p in s.split('/') if p]
    if not parts or any(p in ('.', '..') for p in parts):
        return None
    return '/'.join(parts)


def _read_json_from_zip(zf: zipfile.ZipFile, member: str) -> dict | None:
    try:
        return json.loads(zf.read(member).decode('utf-8-sig'))
    except Exception:
        return None


def _ensure_merger_binary(progress=None) -> Path:
    newest = 0.0
    for base in (MERGER_SRC, core.SERVER / 'internal' / 'catalog' / 'pvf'):
        if base.is_dir():
            for p in base.rglob('*.go'):
                mt = p.stat().st_mtime
                if mt > newest:
                    newest = mt
    if MERGER_BIN.is_file() and MERGER_BIN.stat().st_mtime >= newest:
        return MERGER_BIN
    go_bin = core.pre.GO_BIN
    if not go_bin.is_file():
        raise FileNotFoundError(f'缺少工程内置 Go 工具链：{go_bin}')
    if progress:
        progress('正在编译 PVF Mod 合并引擎…')
    env = dict(os.environ)
    env['PATH'] = str(go_bin.parent) + os.pathsep + env.get('PATH', '')
    env['GOPATH'] = str(core.ROOT / 'tools' / 'gopath')
    env['GOCACHE'] = str(core.ROOT / 'tools' / 'gocache')
    MERGER_BIN.parent.mkdir(parents=True, exist_ok=True)
    res = subprocess.run(
        [str(go_bin), 'build', '-o', str(MERGER_BIN), './cmd/pvfmerge'],
        cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
        creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=300,
    )
    if res.returncode != 0:
        raise RuntimeError('编译 pvfmerge 失败：' + (res.stderr or res.stdout or '')[-600:])
    return MERGER_BIN


def rewrap_inner_to_client_pvf(inner_path: Path, output_path: Path, client_dir: Path, progress=None) -> str:
    """Re-wrap an inner FormatDFO20260901 PVF into client AES-segmented Script.pvf and verify full round-trip."""
    from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

    exe_path = client_dir / 'DFO.exe'
    sk_path = client_dir / 'sk.dat'
    if not exe_path.is_file() or not sk_path.is_file():
        raise FileNotFoundError('客户端目录缺少 DFO.exe 或 sk.dat，无法重封装外层 Script.pvf')
    keys = lr._derive_segment_keys(exe_path, sk_path)
    size = inner_path.stat().st_size
    segments = (size + lr.SEGMENT_SIZE - 1) // lr.SEGMENT_SIZE
    if not 0 < segments <= len(keys):
        raise ValueError(
            f'合并后的 PVF 体积 ({size / (1024*1024):.1f} MB, {segments} 段) 超出当前 sk.dat 密钥容量 ({len(keys)} 段)'
        )
    if size - (segments - 1) * lr.SEGMENT_SIZE < lr.SEGMENT_HEAD:
        raise ValueError('合并后的 PVF 尾段长度小于加密头大小')

    if progress:
        progress('正在重封装客户端 Script.pvf 并执行整包回读校验…')
    output_path.parent.mkdir(parents=True, exist_ok=True)
    tmp = output_path.with_name('.' + output_path.name + '.rewrap.part')
    inner_h = hashlib.sha256()
    outer_h = hashlib.sha256()
    try:
        with inner_path.open('rb') as src, tmp.open('wb') as dst:
            seg = 0
            while True:
                chunk = src.read(lr.SEGMENT_SIZE)
                if not chunk:
                    break
                if seg == 0 and not lr._stream_xor('iNfO', chunk[:48]).startswith(b'nkpi'):
                    raise ValueError('待封装内层 PVF 头部校验失败（非 nkpi）')
                inner_h.update(chunk)
                enc = Cipher(algorithms.AES(keys[seg]), modes.CBC(bytes(16))).encryptor()
                wrapped = enc.update(chunk[:lr.SEGMENT_HEAD]) + enc.finalize() + chunk[lr.SEGMENT_HEAD:]
                dst.write(wrapped)
                outer_h.update(wrapped)
                seg += 1

        # Full round-trip decrypt check before publishing
        check_inner = hashlib.sha256()
        with tmp.open('rb') as src:
            seg = 0
            while True:
                chunk = src.read(lr.SEGMENT_SIZE)
                if not chunk:
                    break
                dec = Cipher(algorithms.AES(keys[seg]), modes.CBC(bytes(16))).decryptor()
                plain = dec.update(chunk[:lr.SEGMENT_HEAD]) + dec.finalize() + chunk[lr.SEGMENT_HEAD:]
                check_inner.update(plain)
                seg += 1
        if check_inner.digest() != inner_h.digest():
            raise ValueError('重封装后的 Script.pvf 往返解密校验不匹配，已中止写入')
        tmp.replace(output_path)
    finally:
        tmp.unlink(missing_ok=True)
    return outer_h.hexdigest()


class ModWorkshop:
    """Manages 90JP-style MOD/*.zip (PVF/ + NPK/ + DLL/), standalone .npk, and ABI-v1 .dll plugins."""

    def __init__(
        self,
        client_dir: Path | None = None,
        mod_dir: Path | None = None,
        cache_root: Path | None = None,
        server_plugins_dir: Path | None = None,
    ):
        self.client_dir = Path(client_dir) if client_dir else (core.pre.client_dir() or core.pre.client_exe().parent)
        self.mod_dir = Path(mod_dir) if mod_dir else (self.client_dir / 'MOD')
        self.client_plugins_dir = self.client_dir / 'plugins'
        self.server_plugins_dir = Path(server_plugins_dir) if server_plugins_dir else (core.ROOT / 'plugins' / 'server')
        self.cache_root = Path(cache_root) if cache_root else CACHE_ROOT
        self.baseline_dir = self.cache_root / 'baseline'
        self.npk_backup_dir = self.cache_root / 'npk-backups'
        self.dll_backup_dir = self.cache_root / 'dll-backups'
        self.selection_path = self.cache_root / 'selection.json'
        self.state_path = self.cache_root / 'state.json'
        self.cache_root.mkdir(parents=True, exist_ok=True)

    def ensure_mod_dir(self) -> Path:
        self.mod_dir.mkdir(parents=True, exist_ok=True)
        return self.mod_dir

    def load_selection(self) -> dict:
        if self.selection_path.is_file():
            try:
                d = core.read(self.selection_path)
                if isinstance(d, dict):
                    return {
                        'order': list(d.get('order') or []),
                        'enabled': list(d.get('enabled') or []),
                    }
            except (OSError, ValueError):
                pass
        return {'order': [], 'enabled': []}

    def save_selection(self, order: list[str], enabled: list[str]) -> dict:
        clean = {
            'order': list(dict.fromkeys(order)),
            'enabled': list(dict.fromkeys(enabled)),
        }
        core.atomic(self.selection_path, clean)
        return clean

    def load_state(self) -> dict:
        if self.state_path.is_file():
            try:
                d = core.read(self.state_path)
                if isinstance(d, dict):
                    return d
            except (OSError, ValueError):
                pass
        return {}

    def _save_state(self, state: dict) -> None:
        core.atomic(self.state_path, state)

    def import_mod_file(self, src_path: Path) -> Path:
        """Import a 90JP collection .zip, flat PVF/NPK/DLL .zip, standalone .npk, or ABI-v1 .dll into MOD/."""
        src = Path(src_path)
        ext = src.suffix.lower()
        if not src.is_file() or ext not in ('.zip', '.npk', '.dll'):
            raise ValueError('仅支持导入 Mod 压缩包 (.zip)、资源包 (.npk) 或 100US 外接插件 (.dll)')
        if ext == '.npk':
            npk_index(src)
        elif ext == '.dll':
            info = inspect_plugin_dll_bytes(src.read_bytes())
            if info['target'] == 'both':
                raise ValueError('单文件 DLL 同时导出了客户端与服务端入口，请打包为 ZIP 并分别放入 DLL/client 与 DLL/server')
        else:
            with zipfile.ZipFile(src) as zf:
                if not zf.namelist():
                    raise ValueError('ZIP 压缩包为空')
        self.ensure_mod_dir()
        dst = self.mod_dir / src.name
        if dst.resolve() != src.resolve():
            core.file_copy(src, dst)
        return dst

    def scan(self) -> list[dict]:
        """Scan MOD/ for 90JP collection ZIPs, flat PVF/NPK/DLL ZIPs, standalone NPKs, and standalone ABI-v1 DLLs."""
        if not self.mod_dir.is_dir():
            return []
        modules: list[dict] = []
        seen_ids: set[str] = set()

        for entry in sorted(self.mod_dir.iterdir(), key=lambda p: p.name.lower()):
            if not entry.is_file():
                continue
            ext = entry.suffix.lower()
            if ext == '.npk':
                try:
                    npk_index(entry)
                    mod_id = f'npk.{entry.stem.lower()}'
                    if mod_id in seen_ids:
                        continue
                    seen_ids.add(mod_id)
                    modules.append({
                        'id': mod_id,
                        'name': entry.stem,
                        'collection_id': mod_id,
                        'collection_name': '独立 NPK 资源',
                        'version': '1.0',
                        'description': f'直接部署至 ImagePacks2/{entry.name}',
                        'dependencies': [],
                        'source_path': str(entry),
                        'source_type': 'npk',
                        'pvf_entries': [],
                        'npk_entries': [{'member': '', 'target_rel': f'ImagePacks2/{entry.name}'}],
                        'client_dll_entries': [],
                        'server_dll_entries': [],
                        'error': '',
                    })
                except Exception as exc:
                    modules.append({
                        'id': f'broken.{entry.name}',
                        'name': entry.name,
                        'collection_id': 'broken',
                        'collection_name': '损坏资源',
                        'version': '',
                        'description': '',
                        'dependencies': [],
                        'source_path': str(entry),
                        'source_type': 'npk',
                        'pvf_entries': [],
                        'npk_entries': [],
                        'client_dll_entries': [],
                        'server_dll_entries': [],
                        'error': str(exc),
                    })
            elif ext == '.dll':
                try:
                    info = inspect_plugin_dll_bytes(entry.read_bytes())
                    target = info['target']
                    if target == 'both':
                        raise ValueError('同时导出客户端与服务端入口，需通过 ZIP 指定 DLL/client 或 DLL/server')
                    mod_id = f'dll.{target}.{entry.stem.lower()}'
                    if mod_id in seen_ids:
                        continue
                    seen_ids.add(mod_id)
                    dll_rec = {'member': '', 'filename': entry.name, 'target': target, 'kind': 'dll'}
                    modules.append({
                        'id': mod_id,
                        'name': entry.stem,
                        'collection_id': mod_id,
                        'collection_name': '客户端外接 DLL' if target == 'client' else '服务端外接 DLL',
                        'version': 'ABI v1',
                        'description': (
                            f'部署至客户端 plugins/（由 NGClient64.aes 加载）'
                            if target == 'client'
                            else f'部署至服务端 plugins/server/（由 wireprobe-us100.exe 加载）'
                        ),
                        'dependencies': [],
                        'source_path': str(entry),
                        'source_type': 'dll',
                        'pvf_entries': [],
                        'npk_entries': [],
                        'client_dll_entries': [dll_rec] if target == 'client' else [],
                        'server_dll_entries': [dll_rec] if target == 'server' else [],
                        'error': '',
                    })
                except Exception as exc:
                    modules.append({
                        'id': f'broken.{entry.name}',
                        'name': entry.name,
                        'collection_id': 'broken',
                        'collection_name': '无效 DLL 插件',
                        'version': '',
                        'description': '',
                        'dependencies': [],
                        'source_path': str(entry),
                        'source_type': 'dll',
                        'pvf_entries': [],
                        'npk_entries': [],
                        'client_dll_entries': [],
                        'server_dll_entries': [],
                        'error': str(exc),
                    })
            elif ext == '.zip':
                modules.extend(self._scan_zip(entry, seen_ids))

        # Sort by saved order first, then preserve natural scan order
        sel = self.load_selection()
        order_idx = {mid: i for i, mid in enumerate(sel['order'])}
        modules.sort(key=lambda m: (order_idx.get(m['id'], 10**9), m['collection_name'], m['name']))
        enabled_set = set(sel['enabled'])
        for m in modules:
            m['enabled'] = m['id'] in enabled_set
        self._annotate_dependencies(modules)
        return modules

    def _scan_zip(self, zip_path: Path, seen_ids: set[str]) -> list[dict]:
        out: list[dict] = []
        try:
            with zipfile.ZipFile(zip_path) as zf:
                names = [n for n in zf.namelist() if not n.endswith('/')]
                # Check case-insensitive duplicates
                lower_seen = set()
                for n in names:
                    nl = n.replace('\\', '/').lower()
                    if nl in lower_seen:
                        raise ValueError(f'压缩包内存在大小写重复路径：{n}')
                    lower_seen.add(nl)

                root_meta = _read_json_from_zip(zf, 'version.json')
                if isinstance(root_meta, dict) and root_meta.get('kind') == 'collection':
                    col_id = str(root_meta.get('id') or zip_path.stem)
                    col_name = str(root_meta.get('name') or zip_path.stem)
                    col_ver = str(root_meta.get('version') or '1.0')
                    mod_order = [str(x) for x in (root_meta.get('moduleOrder') or [])]
                    # Discover direct sub-directories containing version.json
                    sub_dirs: dict[str, list[str]] = {}
                    for n in names:
                        norm = _safe_rel(n)
                        if not norm or '/' not in norm:
                            continue
                        top = norm.split('/', 1)[0]
                        sub_dirs.setdefault(top, []).append(n)
                    sub_mods = []
                    for top, members in sorted(sub_dirs.items()):
                        meta_member = next((m for m in members if _safe_rel(m).lower() == f'{top.lower()}/version.json'), None)
                        if not meta_member:
                            continue
                        meta = _read_json_from_zip(zf, meta_member)
                        if not isinstance(meta, dict) or meta.get('kind') != 'mod':
                            continue
                        mod_id = str(meta.get('id') or f'{col_id}.{top}')
                        if mod_id in seen_ids:
                            continue
                        seen_ids.add(mod_id)
                        pvf_entries, npk_entries, c_dlls, s_dlls, err = self._extract_component_lists(zf, members, prefix=top + '/')
                        sub_mods.append({
                            'id': mod_id,
                            'name': str(meta.get('name') or top),
                            'collection_id': col_id,
                            'collection_name': col_name,
                            'version': str(meta.get('version') or col_ver),
                            'description': str(meta.get('description') or ''),
                            'dependencies': [str(d) for d in (meta.get('dependencies') or []) if d],
                            'source_path': str(zip_path),
                            'source_type': 'zip',
                            'pvf_entries': pvf_entries,
                            'npk_entries': npk_entries,
                            'client_dll_entries': c_dlls,
                            'server_dll_entries': s_dlls,
                            'error': err,
                        })
                    order_map = {mid: i for i, mid in enumerate(mod_order)}
                    sub_mods.sort(key=lambda m: (order_map.get(m['id'], 10**6), m['name']))
                    out.extend(sub_mods)
                else:
                    # Flat / simple PVF, NPK, or DLL ZIP without 90JP collection version.json
                    mod_id = f'zip.{zip_path.stem.lower()}'
                    if mod_id not in seen_ids:
                        seen_ids.add(mod_id)
                        pvf_entries, npk_entries, c_dlls, s_dlls, err = self._extract_component_lists(zf, names, prefix='')
                        if not pvf_entries and not npk_entries and not c_dlls and not s_dlls and not err:
                            err = '压缩包内未发现 PVF/ 脚本、NPK 资源或有效 DLL 插件'
                        out.append({
                            'id': mod_id,
                            'name': zip_path.stem,
                            'collection_id': mod_id,
                            'collection_name': '标准 ZIP 模组',
                            'version': '1.0',
                            'description': f'来源：{zip_path.name}',
                            'dependencies': [],
                            'source_path': str(zip_path),
                            'source_type': 'zip',
                            'pvf_entries': pvf_entries,
                            'npk_entries': npk_entries,
                            'client_dll_entries': c_dlls,
                            'server_dll_entries': s_dlls,
                            'error': err,
                        })
        except Exception as exc:
            out.append({
                'id': f'broken.{zip_path.name}',
                'name': zip_path.name,
                'collection_id': 'broken',
                'collection_name': '无效压缩包',
                'version': '',
                'description': '',
                'dependencies': [],
                'source_path': str(zip_path),
                'source_type': 'zip',
                'pvf_entries': [],
                'npk_entries': [],
                'client_dll_entries': [],
                'server_dll_entries': [],
                'error': str(exc),
            })
        return out

    def _extract_component_lists(
        self, zf: zipfile.ZipFile, members: list[str], prefix: str
    ) -> tuple[list[dict], list[dict], list[dict], list[dict], str]:
        pvf_entries = []
        npk_entries = []
        client_dll_entries = []
        server_dll_entries = []
        plen = len(prefix)
        for m in members:
            norm = _safe_rel(m)
            if not norm:
                return [], [], [], [], f'检测到非法路径：{m}'
            if prefix and not norm.lower().startswith(prefix.lower()):
                continue
            rel = norm[plen:]
            rl = rel.lower()
            if rl.startswith('pvf/'):
                inner = rel[4:]
                if inner:
                    pvf_entries.append({'member': m, 'pvf_path': inner.lower()})
            elif rl.startswith('npk/'):
                sub = rel[4:]
                sl = sub.lower()
                if sl.endswith('npkindex.etc'):
                    continue
                if sl.endswith('.npk'):
                    if sl.startswith('imagepacks2/'):
                        npk_entries.append({'member': m, 'target_rel': 'ImagePacks2/' + Path(sub).name})
                    elif sl.startswith('soundpacks/'):
                        npk_entries.append({'member': m, 'target_rel': 'SoundPacks/' + Path(sub).name})
                    else:
                        npk_entries.append({'member': m, 'target_rel': 'ImagePacks2/' + Path(sub).name})
            elif rl.startswith(('dll/', 'plugins/')):
                sub = rel.split('/', 1)[1]
                sl = sub.lower()
                fname = Path(sub).name
                if sl.endswith('.dll'):
                    hint = ''
                    if sl.startswith('client/'):
                        hint = 'client'
                    elif sl.startswith('server/'):
                        hint = 'server'
                    try:
                        info = inspect_plugin_dll_bytes(zf.read(m))
                    except Exception as exc:
                        return [], [], [], [], f'DLL 校验失败 ({fname})：{exc}'
                    detected = info['target']
                    if hint:
                        if detected not in (hint, 'both'):
                            return [], [], [], [], f'DLL 目录与导出符号不匹配 ({fname}：目录为 {hint}，导出为 {detected})'
                        target = hint
                    else:
                        if detected == 'both':
                            return [], [], [], [], f'DLL ({fname}) 同时导出双端入口，请放入 DLL/client/ 或 DLL/server/'
                        target = detected
                    rec = {'member': m, 'filename': fname, 'target': target, 'kind': 'dll'}
                    if target == 'client':
                        client_dll_entries.append(rec)
                    else:
                        server_dll_entries.append(rec)
                elif sl.endswith('.ini'):
                    if sl.startswith('server/'):
                        server_dll_entries.append({'member': m, 'filename': fname, 'target': 'server', 'kind': 'ini'})
                    elif sl.startswith(('client/', 'config/')) or '/' not in sl:
                        client_dll_entries.append({'member': m, 'filename': fname, 'target': 'client', 'kind': 'ini'})
            elif not prefix and rl.startswith('imagepacks2/') and rl.endswith('.npk'):
                npk_entries.append({'member': m, 'target_rel': 'ImagePacks2/' + Path(rel).name})
            elif not prefix and rl.startswith('soundpacks/') and rl.endswith('.npk'):
                npk_entries.append({'member': m, 'target_rel': 'SoundPacks/' + Path(rel).name})
            elif not prefix and rl.endswith('.npk') and '/' not in rl:
                npk_entries.append({'member': m, 'target_rel': 'ImagePacks2/' + Path(rel).name})
            elif not prefix and rl.endswith('.dll') and '/' not in rl:
                fname = Path(rel).name
                try:
                    info = inspect_plugin_dll_bytes(zf.read(m))
                except Exception as exc:
                    return [], [], [], [], f'DLL 校验失败 ({fname})：{exc}'
                if info['target'] == 'both':
                    return [], [], [], [], f'DLL ({fname}) 同时导出双端入口，请放入 DLL/client/ 或 DLL/server/'
                rec = {'member': m, 'filename': fname, 'target': info['target'], 'kind': 'dll'}
                if info['target'] == 'client':
                    client_dll_entries.append(rec)
                else:
                    server_dll_entries.append(rec)
        return pvf_entries, npk_entries, client_dll_entries, server_dll_entries, ''

    def _annotate_dependencies(self, modules: list[dict]) -> None:
        valid_enabled = {m['id'] for m in modules if m['enabled'] and not m['error']}
        # Iteratively exclude modules whose dependencies are missing or disabled (90JP rule)
        changed = True
        while changed:
            changed = False
            for m in modules:
                if m['id'] not in valid_enabled:
                    continue
                missing = [d for d in m['dependencies'] if d not in valid_enabled]
                if missing:
                    valid_enabled.remove(m['id'])
                    changed = True
        for m in modules:
            missing = [d for d in m['dependencies'] if d not in valid_enabled] if m['enabled'] else []
            m['missing_deps'] = missing
            m['effective'] = m['id'] in valid_enabled

    def _client_pvf_path(self) -> Path:
        return self.client_dir / 'Script.pvf'

    def _ensure_baseline_pvf(self, state: dict, progress=None) -> tuple[Path, str]:
        """Ensure an unmodified baseline Script.pvf copy exists before any PVF merge."""
        live_pvf = self._client_pvf_path()
        if not live_pvf.is_file():
            raise FileNotFoundError(f'未找到客户端 Script.pvf：{live_pvf}')
        baseline_file = self.baseline_dir / 'Script.pvf'
        live_sha = core.digest(live_pvf)
        saved_base_sha = state.get('baseline_pvf_sha256', '')
        saved_applied_sha = state.get('applied_pvf_sha256', '')

        if baseline_file.is_file() and saved_base_sha:
            # Verify live Script.pvf hasn't been externally altered into an unknown state
            if saved_applied_sha:
                if live_sha not in (saved_applied_sha, saved_base_sha):
                    # User replaced Script.pvf externally (e.g. new localization): adopt new clean baseline
                    if progress:
                        progress('检测到客户端 Script.pvf 已更新，正在刷新原版基线备份…')
                    core.file_copy(live_pvf, baseline_file)
                    state['baseline_pvf_sha256'] = live_sha
                    state['applied_pvf_sha256'] = ''
                    return baseline_file, live_sha
            else:
                if live_sha != saved_base_sha:
                    core.file_copy(live_pvf, baseline_file)
                    state['baseline_pvf_sha256'] = live_sha
            return baseline_file, state['baseline_pvf_sha256']

        if progress:
            progress('首次合并 PVF Mod：正在备份原版 Script.pvf 基线…')
        self.baseline_dir.mkdir(parents=True, exist_ok=True)
        core.file_copy(live_pvf, baseline_file)
        if core.digest(baseline_file) != live_sha:
            raise RuntimeError('备份基线 Script.pvf 校验失败')
        state['baseline_pvf_sha256'] = live_sha
        return baseline_file, live_sha

    def _sync_npks(self, effective_mods: list[dict], state: dict, progress=None) -> dict:
        """Install enabled Mod NPKs (with backup of original files) and restore disabled ones."""
        prev_installed: dict[str, dict] = dict(state.get('installed_npks') or {})
        desired: dict[str, tuple[dict, dict]] = {}
        # Priority order: index 0 wins per target_rel
        for m in effective_mods:
            for entry in m['npk_entries']:
                trel = entry['target_rel']
                if trel.lower() not in desired:
                    desired[trel.lower()] = (m, entry)

        self.npk_backup_dir.mkdir(parents=True, exist_ok=True)
        # 1. Restore previously installed NPKs that are no longer desired
        for key in list(prev_installed.keys()):
            if key in desired:
                continue
            rec = prev_installed[key]
            dst = self.client_dir / rec['target_rel']
            orig_sha = rec.get('original_sha256')
            if orig_sha:
                bak = self.npk_backup_dir / rec['backup_name']
                if bak.is_file() and core.digest(bak) == orig_sha:
                    core.file_copy(bak, dst)
            else:
                if dst.is_file() and core.digest(dst) == rec.get('installed_sha256'):
                    dst.unlink(missing_ok=True)
            del prev_installed[key]

        # 2. Install desired NPKs
        installed_count = 0
        for key, (m, entry) in desired.items():
            trel = entry['target_rel']
            dst = self.client_dir / trel
            dst.parent.mkdir(parents=True, exist_ok=True)
            if progress:
                progress(f'正在部署 Mod NPK：{trel}…')
            with tempfile.NamedTemporaryFile(dir=self.cache_root, delete=False, suffix='.npk') as tf:
                tmp_npk = Path(tf.name)
            try:
                if m['source_type'] == 'npk':
                    shutil.copyfile(m['source_path'], tmp_npk)
                else:
                    with zipfile.ZipFile(m['source_path']) as zf:
                        with zf.open(entry['member']) as src_f, tmp_npk.open('wb') as dst_f:
                            shutil.copyfileobj(src_f, dst_f, 1 << 20)
                npk_index(tmp_npk)
                new_sha = core.digest(tmp_npk)
                rec = prev_installed.get(key)
                if rec and dst.is_file() and core.digest(dst) == new_sha:
                    installed_count += 1
                    continue
                if not rec:
                    orig_sha = None
                    bak_name = ''
                    if dst.is_file():
                        orig_sha = core.digest(dst)
                        bak_name = hashlib.sha256(trel.lower().encode()).hexdigest()[:16] + '.bak'
                        core.file_copy(dst, self.npk_backup_dir / bak_name)
                    rec = {'target_rel': trel, 'original_sha256': orig_sha, 'backup_name': bak_name}
                core.file_copy(tmp_npk, dst)
                rec['installed_sha256'] = new_sha
                rec['mod_id'] = m['id']
                prev_installed[key] = rec
                installed_count += 1
            finally:
                tmp_npk.unlink(missing_ok=True)

        state['installed_npks'] = prev_installed
        return {'npk_count': installed_count}

    def _sync_dll_target(
        self,
        target: str,
        effective_mods: list[dict],
        state: dict,
        progress=None,
    ) -> int:
        """Synchronize client or server external DLL plugins (plus companion .ini) in priority order."""
        is_client = (target == 'client')
        state_key = 'installed_client_dlls' if is_client else 'installed_server_dlls'
        entries_key = 'client_dll_entries' if is_client else 'server_dll_entries'
        dst_dir = self.client_plugins_dir if is_client else self.server_plugins_dir

        prev_installed: dict[str, dict] = dict(state.get(state_key) or {})
        desired: dict[str, tuple[dict, dict, str]] = {}
        dll_seq = 0
        for m in effective_mods:
            slug = _safe_slug(m['id'])
            for entry in m.get(entries_key) or []:
                orig_name = Path(entry['filename']).name
                if entry.get('kind', 'dll') == 'dll':
                    dll_seq += 1
                    clean_name = orig_name.lstrip('_') or orig_name
                    deploy_name = f'{dll_seq:03d}_{slug}__{clean_name}'
                else:
                    # Companion .ini keeps its exact filename so plugin_name:key lookup matches
                    deploy_name = orig_name
                dkey = deploy_name.lower()
                if dkey not in desired:
                    desired[dkey] = (m, entry, deploy_name)

        self.dll_backup_dir.mkdir(parents=True, exist_ok=True)
        # 1. Clean up or restore previously managed files no longer desired
        for key in list(prev_installed.keys()):
            if key in desired:
                continue
            rec = prev_installed[key]
            dst = dst_dir / rec['filename']
            orig_sha = rec.get('original_sha256')
            if orig_sha:
                bak = self.dll_backup_dir / rec['backup_name']
                if bak.is_file() and core.digest(bak) == orig_sha:
                    dst.parent.mkdir(parents=True, exist_ok=True)
                    core.file_copy(bak, dst)
            else:
                if dst.is_file() and core.digest(dst) == rec.get('installed_sha256'):
                    dst.unlink(missing_ok=True)
            del prev_installed[key]

        # 2. Deploy desired DLLs & companion INIs
        if desired:
            dst_dir.mkdir(parents=True, exist_ok=True)
            if is_client:
                try:
                    import deploy_stub as _dstub
                    _dstub._ensure_ini(self.client_dir)
                except Exception:
                    pass

        installed_dll_count = 0
        for key, (m, entry, deploy_name) in desired.items():
            dst = dst_dir / deploy_name
            dst.parent.mkdir(parents=True, exist_ok=True)
            is_dll = entry.get('kind', 'dll') == 'dll'
            if progress:
                label = '客户端插件' if is_client else '服务端插件'
                progress(f'正在部署{label}：{deploy_name}…')
            suffix = '.dll' if is_dll else '.ini'
            with tempfile.NamedTemporaryFile(dir=self.cache_root, delete=False, suffix=suffix) as tf:
                tmp_file = Path(tf.name)
            try:
                if m['source_type'] == 'dll':
                    shutil.copyfile(m['source_path'], tmp_file)
                else:
                    with zipfile.ZipFile(m['source_path']) as zf:
                        with zf.open(entry['member']) as src_f, tmp_file.open('wb') as dst_f:
                            shutil.copyfileobj(src_f, dst_f, 1 << 20)
                if is_dll:
                    info = inspect_plugin_dll_bytes(tmp_file.read_bytes())
                    if info['target'] not in (target, 'both'):
                        raise ValueError(f'DLL 导出目标不匹配：{deploy_name} ({info["target"]} != {target})')
                new_sha = core.digest(tmp_file)
                rec = prev_installed.get(key)
                if rec and dst.is_file() and core.digest(dst) == new_sha:
                    if is_dll:
                        installed_dll_count += 1
                    continue
                if not rec:
                    orig_sha = None
                    bak_name = ''
                    if dst.is_file():
                        orig_sha = core.digest(dst)
                        bak_name = hashlib.sha256(f'{target}/{deploy_name.lower()}'.encode()).hexdigest()[:16] + '.bak'
                        core.file_copy(dst, self.dll_backup_dir / bak_name)
                    rec = {
                        'filename': deploy_name,
                        'kind': entry.get('kind', 'dll'),
                        'original_sha256': orig_sha,
                        'backup_name': bak_name,
                    }
                core.file_copy(tmp_file, dst)
                if core.digest(dst) != new_sha:
                    raise RuntimeError(f'部署插件校验不一致：{dst}')
                rec['installed_sha256'] = new_sha
                rec['mod_id'] = m['id']
                prev_installed[key] = rec
                if is_dll:
                    installed_dll_count += 1
            finally:
                tmp_file.unlink(missing_ok=True)

        state[state_key] = prev_installed
        return installed_dll_count

    def needs_server_stop_for_apply(self, modules: list[dict] | None = None) -> bool:
        """Return True if applying current selection touches server DLLs while gateway is running."""
        mods = self.scan() if modules is None else modules
        state = self.load_state()
        has_server_mods = any(m.get('server_dll_entries') for m in mods if m.get('effective'))
        has_prev_server = bool(state.get('installed_server_dlls'))
        return (has_server_mods or has_prev_server) and (core.gateway() is not None)

    def needs_server_stop_for_restore(self) -> bool:
        """Return True if restoring touches server DLLs while gateway is running."""
        state = self.load_state()
        return bool(state.get('installed_server_dlls')) and (core.gateway() is not None)

    def apply_merge(self, progress=None, auto_stop_server: bool = False) -> dict:
        """Save & merge all effective enabled PVF + NPK + DLL mods into client/server directories."""
        modules = self.scan()
        effective = [m for m in modules if m['effective']]
        state = self.load_state()

        touches_client = (
            any(m['pvf_entries'] or m['npk_entries'] or m.get('client_dll_entries') for m in effective)
            or bool(state.get('applied_pvf_sha256'))
            or bool(state.get('installed_npks'))
            or bool(state.get('installed_client_dlls'))
        )
        if touches_client and core.client_up():
            raise ValueError('请先完全退出游戏客户端 (DFO.exe)，再执行 Mod 合并。')

        touches_server = (
            any(m.get('server_dll_entries') for m in effective)
            or bool(state.get('installed_server_dlls'))
        )
        server_stopped = False
        if touches_server and core.gateway() is not None:
            if not auto_stop_server:
                raise ValueError('服务端网关 (wireprobe-us100.exe) 正在运行并占用服务端插件 DLL，请先停止服务端后再执行 Mod 同步。')
            if progress:
                progress('正在停止服务端网关以同步服务端外接 DLL…')
            core.stop_server()
            server_stopped = True

        live_pvf = self._client_pvf_path()

        # Collect PVF layers in priority order
        pvf_items = []
        for prio, m in enumerate(effective):
            for entry in m['pvf_entries']:
                pvf_items.append((prio, m, entry))

        pvf_report = None
        if pvf_items:
            baseline_pvf, base_sha = self._ensure_baseline_pvf(state, progress=progress)
            base_fp = lr.pvf_fingerprint(baseline_pvf)
            is_inner = bool(base_fp and base_fp.get('inner'))
            inner_source = baseline_pvf if is_inner else lr.ensure_inner_pvf(baseline_pvf, base_fp, progress=progress)
            merger = _ensure_merger_binary(progress=progress)

            with tempfile.TemporaryDirectory(dir=self.cache_root, prefix='.merge-') as tmpdir:
                tmp = Path(tmpdir)
                stage_dir = tmp / 'layers'
                stage_dir.mkdir()
                plan_layers = []
                # Extract PVF files per ZIP in bulk
                by_zip: dict[str, list[tuple[int, int, dict, dict]]] = {}
                for seq, (prio, m, entry) in enumerate(pvf_items):
                    by_zip.setdefault(m['source_path'], []).append((seq, prio, m, entry))
                if progress:
                    progress(f'正在提取 {len(pvf_items)} 个 Mod PVF 文件…')
                for zpath, rows in by_zip.items():
                    with zipfile.ZipFile(zpath) as zf:
                        for seq, prio, m, entry in rows:
                            out_f = stage_dir / f'{seq:06d}_{Path(entry["pvf_path"]).name}'
                            with zf.open(entry['member']) as sf, out_f.open('wb') as df:
                                shutil.copyfileobj(sf, df, 1 << 16)
                            plan_layers.append({
                                'path': entry['pvf_path'],
                                'source': str(out_f),
                                'mod_id': m['id'],
                                'priority': prio,
                            })
                plan_file = tmp / 'plan.json'
                plan_file.write_text(json.dumps({'layers': plan_layers}, ensure_ascii=False), encoding='utf-8')
                merged_inner = tmp / 'merged.inner.pvf'

                if progress:
                    progress(f'正在将 {len(plan_layers)} 个 Mod 文件合并入 PVF 并回读校验…')
                env = dict(os.environ)
                env['GOGC'] = '10'
                res = subprocess.run(
                    [str(merger), '-source', str(inner_source), '-plan', str(plan_file), '-output', str(merged_inner)],
                    cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
                    creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=600,
                )
                if res.returncode != 0:
                    raise RuntimeError('PVF 合并失败：' + (res.stderr or res.stdout or '')[-800:])
                pvf_report = json.loads((res.stdout or '{}').strip().splitlines()[-1])

                if is_inner:
                    core.file_copy(merged_inner, live_pvf)
                    applied_sha = core.digest(live_pvf)
                else:
                    wrapped_tmp = tmp / 'Script.pvf'
                    applied_sha = rewrap_inner_to_client_pvf(merged_inner, wrapped_tmp, self.client_dir, progress=progress)
                    core.file_copy(wrapped_tmp, live_pvf)
                    if core.digest(live_pvf) != applied_sha:
                        raise RuntimeError('安装合并后的 Script.pvf 时校验不一致')

            state['applied_pvf_sha256'] = applied_sha
            state['pvf_report'] = pvf_report
            state['merged_at'] = time.strftime('%Y-%m-%d %H:%M:%S')
        else:
            # No active PVF mods: restore baseline Script.pvf if previously merged
            baseline_file = self.baseline_dir / 'Script.pvf'
            if state.get('applied_pvf_sha256') and baseline_file.is_file():
                if progress:
                    progress('当前无启用的 PVF Mod，正在还原原版 Script.pvf…')
                core.file_copy(baseline_file, live_pvf)
            state['applied_pvf_sha256'] = ''
            state['pvf_report'] = None

        npk_res = self._sync_npks(effective, state, progress=progress)
        client_dll_count = self._sync_dll_target('client', effective, state, progress=progress)
        server_dll_count = self._sync_dll_target('server', effective, state, progress=progress)
        state['enabled_ids'] = [m['id'] for m in effective]
        self._save_state(state)
        return {
            'pvf_report': pvf_report,
            'npk_count': npk_res['npk_count'],
            'client_dll_count': client_dll_count,
            'server_dll_count': server_dll_count,
            'server_stopped': server_stopped,
            'effective_mods': len(effective),
            'pvf_layers': len(pvf_items),
        }

    def restore_original(self, progress=None, auto_stop_server: bool = False) -> str:
        """Restore exact original Script.pvf and remove/restore all Mod NPKs and DLL plugins."""
        state = self.load_state()
        touches_client = bool(
            state.get('applied_pvf_sha256')
            or state.get('installed_npks')
            or state.get('installed_client_dlls')
        )
        if touches_client and core.client_up():
            raise ValueError('请先完全退出游戏客户端 (DFO.exe)，再还原原版资源。')
        server_stopped = False
        if state.get('installed_server_dlls') and core.gateway() is not None:
            if not auto_stop_server:
                raise ValueError('服务端网关 (wireprobe-us100.exe) 正在运行并占用服务端插件 DLL，请先停止服务端后再还原。')
            if progress:
                progress('正在停止服务端网关以清理服务端外接 DLL…')
            core.stop_server()
            server_stopped = True

        baseline_file = self.baseline_dir / 'Script.pvf'
        live_pvf = self._client_pvf_path()
        restored_pvf = False
        if state.get('applied_pvf_sha256'):
            if not baseline_file.is_file():
                raise FileNotFoundError('缺少原版 Script.pvf 基线备份文件，无法自动还原')
            want_sha = state.get('baseline_pvf_sha256', '')
            if want_sha and core.digest(baseline_file) != want_sha:
                raise RuntimeError('原版 Script.pvf 基线备份校验值不匹配')
            if progress:
                progress('正在还原原版 Script.pvf…')
            core.file_copy(baseline_file, live_pvf)
            restored_pvf = True

        self._sync_npks([], state, progress=progress)
        self._sync_dll_target('client', [], state, progress=progress)
        self._sync_dll_target('server', [], state, progress=progress)
        state['applied_pvf_sha256'] = ''
        state['pvf_report'] = None
        state['enabled_ids'] = []
        self._save_state(state)
        # Uncheck all mods in selection while preserving order
        sel = self.load_selection()
        self.save_selection(sel['order'], [])
        tail = '（已自动停止服务端，请重新启动服务端生效）' if server_stopped else ''
        return (
            f'已还原原版 Script.pvf、全部 Mod NPK 与外接 DLL 插件。{tail}'
            if restored_pvf
            else f'已还原全部 Mod NPK 与外接 DLL 插件，当前为原版 Script.pvf。{tail}'
        )
