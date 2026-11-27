"""90JP-style unified PVF + NPK Mod Workshop: collection ZIP scanning, priority ordering,
PVF merging (with .lst/.str merge, read-back verification, AES rewrap), NPK sync, and rollback."""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import shutil
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
    """Manages 90JP-style MOD/*.zip (collection -> sub-modules with PVF/ and NPK/) and standalone .npk packs."""

    def __init__(self, client_dir: Path | None = None, mod_dir: Path | None = None, cache_root: Path | None = None):
        self.client_dir = Path(client_dir) if client_dir else (core.pre.client_dir() or core.pre.client_exe().parent)
        self.mod_dir = Path(mod_dir) if mod_dir else (self.client_dir / 'MOD')
        self.cache_root = Path(cache_root) if cache_root else CACHE_ROOT
        self.baseline_dir = self.cache_root / 'baseline'
        self.npk_backup_dir = self.cache_root / 'npk-backups'
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
        """Import a 90JP collection .zip, flat PVF/NPK .zip, or standalone .npk into MOD/."""
        src = Path(src_path)
        ext = src.suffix.lower()
        if not src.is_file() or ext not in ('.zip', '.npk'):
            raise ValueError('仅支持导入 Mod 压缩包 (.zip) 或资源包 (.npk)')
        if ext == '.npk':
            npk_index(src)
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
        """Scan MOD/ for 90JP collection ZIPs (version.json), flat PVF/NPK ZIPs, and standalone NPKs."""
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
                        pvf_entries, npk_entries, err = self._extract_component_lists(members, prefix=top + '/')
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
                            'error': err,
                        })
                    order_map = {mid: i for i, mid in enumerate(mod_order)}
                    sub_mods.sort(key=lambda m: (order_map.get(m['id'], 10**6), m['name']))
                    out.extend(sub_mods)
                else:
                    # Flat / simple PVF or NPK ZIP without 90JP collection version.json
                    mod_id = f'zip.{zip_path.stem.lower()}'
                    if mod_id not in seen_ids:
                        seen_ids.add(mod_id)
                        pvf_entries, npk_entries, err = self._extract_component_lists(names, prefix='')
                        if not pvf_entries and not npk_entries and not err:
                            err = '压缩包内未发现 PVF/ 脚本或 NPK 资源'
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
                'error': str(exc),
            })
        return out

    def _extract_component_lists(self, members: list[str], prefix: str) -> tuple[list[dict], list[dict], str]:
        pvf_entries = []
        npk_entries = []
        plen = len(prefix)
        for m in members:
            norm = _safe_rel(m)
            if not norm:
                return [], [], f'检测到非法路径：{m}'
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
            elif not prefix and rl.startswith('imagepacks2/') and rl.endswith('.npk'):
                npk_entries.append({'member': m, 'target_rel': 'ImagePacks2/' + Path(rel).name})
            elif not prefix and rl.startswith('soundpacks/') and rl.endswith('.npk'):
                npk_entries.append({'member': m, 'target_rel': 'SoundPacks/' + Path(rel).name})
            elif not prefix and rl.endswith('.npk') and '/' not in rl:
                npk_entries.append({'member': m, 'target_rel': 'ImagePacks2/' + Path(rel).name})
        return pvf_entries, npk_entries, ''

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

    def apply_merge(self, progress=None) -> dict:
        """Save & merge all effective enabled PVF + NPK mods into the client Script.pvf and ImagePacks2."""
        if core.client_up():
            raise ValueError('请先完全退出游戏客户端 (DFO.exe)，再执行 Mod 合并。')
        modules = self.scan()
        effective = [m for m in modules if m['effective']]
        state = self.load_state()
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
        state['enabled_ids'] = [m['id'] for m in effective]
        self._save_state(state)
        return {
            'pvf_report': pvf_report,
            'npk_count': npk_res['npk_count'],
            'effective_mods': len(effective),
            'pvf_layers': len(pvf_items),
        }

    def restore_original(self, progress=None) -> str:
        """Restore exact original Script.pvf and remove/restore all Mod NPKs."""
        if core.client_up():
            raise ValueError('请先完全退出游戏客户端 (DFO.exe)，再还原原版资源。')
        state = self.load_state()
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
        state['applied_pvf_sha256'] = ''
        state['pvf_report'] = None
        state['enabled_ids'] = []
        self._save_state(state)
        # Uncheck all mods in selection while preserving order
        sel = self.load_selection()
        self.save_selection(sel['order'], [])
        return '已还原原版 Script.pvf 与全部 Mod NPK。' if restored_pvf else '已还原全部 Mod NPK，当前为原版 Script.pvf。'
