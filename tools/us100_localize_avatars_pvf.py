# -*- coding: utf-8 -*-
r"""Fill all 68,176 blank avatar name keys in US100 Script.pvf (`string/equipment.uv.str`).

Root cause:
  Original US100 `Script.pvf` contains 134,757 avatar `.equ` items, of which 68,180
  (68,176 unique `[name]` keys in `string/equipment.uv.str`) have empty values —
  typically event/package/untradeable copies (such as Event 520's `10333089` avatar
  box items `101551928` etc.) whose visual `(job, part, variationA, variationB)`
  already has an official Chinese name on another tradeable avatar ID.

Resolution hierarchy (100% deterministic, zero blank avatars remaining):
  1. Exact visual match `(job, part, varA, varB)` -> inherit official Chinese name
     directly from an already-named avatar in `string/equipment.uv.str` (~48.5k items).
  2. Exact sprite match `(job, part, varA)` -> inherit base Chinese name and update
     color variant label `[A型]`..`[H型]` (~2.0k items).
  3. Parent booster box theme match (`boosters.generated.json.gz` + `string/stackable.uv.str`)
     -> `<礼盒主题><部位> [颜色型]`.
  4. Clean structured Chinese fallback -> `<品级><部位> (<职业>) [颜色型]`.

Guardrail:
  Variation inheritance only runs when `varA > 0` (or `part == 'skin' and varB > 0`),
  preventing false-positive matches on `aura` or default `(0, 0)` entries.

Merges via `pvfmerge`, strictly verifies read-back (0 remaining blank avatars,
0 Korean chars, key count monotonic), rewraps to US100 `Script.pvf` with SHA-256
verification, backs up live `Script.pvf`, and installs atomically (or stages safely
while `DFO.exe` is running).
"""
from __future__ import annotations

import argparse
import gzip
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path[:0] = [str(HERE / 'atelier'), str(HERE)]

import core  # noqa: E402
import live_resources as lr  # noqa: E402
from mod_workshop import _ensure_merger_binary, rewrap_inner_to_client_pvf  # noqa: E402

EQ_STR_ENTRY = 'string/equipment.uv.str'
BOOSTERS_CATALOG = ROOT / 'server-us100' / 'configs' / 'us100' / 'boosters.generated.json.gz'

STAGE_DIR = ROOT / 'runtime' / 'avatar-pvf-localize-build'
STAGE_MANIFEST = STAGE_DIR / 'pending.json'
BACKUP_DIR = ROOT / 'runtime' / 'avatar-pvf-localize-backup'
BACKUP_MANIFEST = BACKUP_DIR / 'manifest.json'

PART_ZH: dict[str, str] = {
    'cap': '帽子',
    'hair': '头发',
    'face': '脸部',
    'neck': '胸部',
    'coat': '上衣',
    'pants': '下装',
    'belt': '腰部',
    'shoes': '鞋子',
    'skin': '皮肤',
    'aura': '光环',
    'weapon': '武器装扮',
}

JOB_ZH: dict[str, str] = {
    'swordman': '鬼剑士',
    'fighter': '格斗家',
    'gunner': '神枪手',
    'mage': '魔法师',
    'priest': '圣职者',
    'thief': '暗夜使者',
    'knight': '守护者',
    'demoniclancer': '魔枪士',
    'gunblader': '枪剑士',
    'common': '通用',
}

RARITY_ZH: dict[int, str] = {
    0: '装扮',
    1: '普通装扮',
    2: '高级装扮',
    3: '稀有装扮',
    4: '稀有克隆装扮',
    5: '史诗装扮',
    6: '神器装扮',
}

COLOR_LETTERS = 'ABCDEFGH'
_VARIANT_SUFFIX_RE = re.compile(r'\s*[\[\(（][A-Ha-h][型款色]?[\]\)）]\s*$')
_KO_RE = re.compile(r'[\uac00-\ud7a3]')
_ZH_RE = re.compile(r'[\u4e00-\u9fff]')


def color_tag(vb: int) -> str:
    if 0 <= vb < len(COLOR_LETTERS):
        return f'[{COLOR_LETTERS[vb]}型]'
    return ''


def apply_color_variant(base_name: str, vb: int) -> str:
    cleaned = _VARIANT_SUFFIX_RE.sub('', base_name).strip()
    tag = color_tag(vb)
    if tag and cleaned:
        return f'{cleaned} {tag}'
    return cleaned or base_name.strip()


def clean_box_theme(box_name: str) -> str:
    s = box_name.strip()
    for suffix in (
        '装扮套装自选礼盒', '装扮套装礼盒', '装扮自选礼盒', '装扮礼盒',
        '装扮套装箱', '装扮箱', '套装自选礼盒', '套装礼盒',
        '武器装扮自选礼盒', '武器装扮礼盒', '武器装扮箱',
        '光环装扮自选礼盒', '光环装扮礼盒', '光环礼盒',
        '自选礼盒', '随机礼盒', '礼盒', '箱子',
    ):
        if s.endswith(suffix) and len(s) > len(suffix) + 1:
            s = s[: -len(suffix)].strip()
            break
    return s


def resolve_blank_avatar_names(
    blanks: list[dict],
    var_full_named: dict[str, str],
    var_a_named: dict[str, str],
    tid_to_box: dict[int, str],
) -> tuple[dict[str, str], dict[str, int]]:
    """Resolve every blank avatar key to a clean Chinese name."""
    patches: dict[str, str] = {}
    stats = {'var_full': 0, 'var_a': 0, 'box': 0, 'structured': 0}

    for row in blanks:
        key = row['key']
        if not key or key in patches:
            continue
        tid = int(row['id'])
        job = row.get('job', '')
        part = row.get('part', '')
        va = int(row.get('va', 0))
        vb = int(row.get('vb', 0))
        rarity = int(row.get('rarity', 0))
        has_var = va > 0 or (part == 'skin' and vb > 0)

        name = ''
        if has_var:
            k_full = f'{job}|{part}|{va}|{vb}'
            cand = var_full_named.get(k_full, '').strip()
            if cand and _ZH_RE.search(cand) and not _KO_RE.search(cand):
                name = cand
                stats['var_full'] += 1
            else:
                k_a = f'{job}|{part}|{va}'
                cand_a = var_a_named.get(k_a, '').strip()
                if cand_a and _ZH_RE.search(cand_a) and not _KO_RE.search(cand_a):
                    name = apply_color_variant(cand_a, vb)
                    stats['var_a'] += 1

        if not name and tid in tid_to_box:
            theme = clean_box_theme(tid_to_box[tid])
            if theme and _ZH_RE.search(theme) and not _KO_RE.search(theme):
                part_cn = PART_ZH.get(part, '装扮')
                if part in ('aura', 'weapon'):
                    base = theme if part_cn in theme else f'{theme}{part_cn}'
                else:
                    base = f'{theme}{part_cn}'
                tag = color_tag(vb) if has_var else ''
                name = f'{base} {tag}'.strip()
                stats['box'] += 1

        if not name:
            part_cn = PART_ZH.get(part, '装扮')
            job_cn = JOB_ZH.get(job, '')
            rar_cn = RARITY_ZH.get(rarity, '高级装扮')
            if part == 'weapon':
                prefix = '神器' if rarity >= 6 else ('稀有' if rarity in (3, 4) else '高级')
                base = f'{prefix}武器装扮'
            elif part == 'aura':
                prefix = '至尊' if rarity >= 6 else ('稀有' if rarity in (3, 4) else '纪念')
                base = f'{prefix}光环装扮'
            else:
                base = f'{rar_cn}{part_cn}'
            if job_cn and job_cn != '通用':
                base = f'{base} ({job_cn})'
            tag = color_tag(vb) if has_var else ''
            name = f'{base} {tag}'.strip()
            stats['structured'] += 1

        patches[key] = name

    return patches, stats


def render_uv_str_overlay(patches: dict[str, str]) -> str:
    lines: list[str] = []
    for k, v in sorted(patches.items()):
        if '>' in k or '\n' in k or '\r' in k:
            raise ValueError(f'invalid key: {k!r}')
        if '\n' in v or '\r' in v:
            raise ValueError(f'invalid value for {k}: {v!r}')
        lines.append(f'{k}>{v}')
    return '\r\n'.join(lines) + '\r\n'


def parse_uv_str_utf16(raw: bytes) -> dict[str, str]:
    txt = raw.decode('utf-16-le').lstrip('\ufeff')
    kv: dict[str, str] = {}
    for line in txt.splitlines():
        if '>' in line:
            k, v = line.split('>', 1)
            if k:
                kv[k] = v
    return kv


def _scan_pvf_avatars(inner_pvf: Path) -> dict:
    go_bin = core.pre.GO_BIN
    env = dict(os.environ)
    env['GOPATH'] = str(ROOT / 'tools' / 'gopath')
    env['GOCACHE'] = str(ROOT / 'tools' / 'gocache')
    env['PATH'] = str(go_bin.parent) + os.pathsep + env.get('PATH', '')

    go_src = r'''package main

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"strings"
	"unicode/utf16"
)

func decodeUTF16LE(b []byte) string {
	if len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe {
		b = b[2:]
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[i*2]) | uint16(b[i*2+1])<<8
	}
	return string(utf16.Decode(u))
}

func parseUVStr(b []byte) map[string]string {
	txt := decodeUTF16LE(b)
	m := map[string]string{}
	for _, line := range strings.Split(txt, "\n") {
		line = strings.TrimRight(line, "\r")
		if idx := strings.IndexByte(line, '>'); idx > 0 {
			m[line[:idx]] = line[idx+1:]
		}
	}
	return m
}

func extractKey(ref string) string {
	if strings.HasPrefix(ref, "<") && strings.HasSuffix(ref, ">") {
		if idx := strings.Index(ref, "::"); idx >= 0 {
			return ref[idx+2 : len(ref)-1]
		}
	}
	return ""
}

func main() {
	src := flag.String("source", "", "")
	flag.Parse()
	a, err := pvf.LoadArchive(pvf.Options{Path: *src, MaxBytes: 1024 * 1024 * 1024})
	if err != nil {
		panic(err)
	}
	eqRaw, err := a.ReadRaw("string/equipment.uv.str")
	if err != nil {
		panic(err)
	}
	eqKV := parseUVStr(eqRaw)
	stkRaw, err := a.ReadRaw("string/stackable.uv.str")
	if err != nil {
		panic(err)
	}
	stkKV := parseUVStr(stkRaw)
	eqIdx, err := catalog.ResolveScript(a, "list/equipment.lst")
	if err != nil {
		panic(err)
	}
	rows, err := catalog.ParseIndex(eqIdx.Cells)
	if err != nil {
		panic(err)
	}

	type blankRow struct {
		ID     uint32 `json:"id"`
		Key    string `json:"key"`
		Job    string `json:"job"`
		Part   string `json:"part"`
		VA     int32  `json:"va"`
		VB     int32  `json:"vb"`
		Rarity int32  `json:"rarity"`
	}
	var blanks []blankRow
	varFullNamed := map[string]string{}
	varANamed := map[string]string{}
	totalAvatars := 0

	for _, r := range rows {
		lp := strings.ToLower(strings.ReplaceAll(r.Path, "\\", "/"))
		if !strings.Contains(lp, "/avatar/") && !strings.HasPrefix(lp, "avatar/") {
			continue
		}
		totalAvatars++
		parts := strings.Split(lp, "/")
		job, part := "", ""
		for i, seg := range parts {
			if seg == "avatar" {
				if i > 0 {
					job = parts[i-1]
				}
				if i+1 < len(parts)-1 {
					part = parts[i+1]
				}
				break
			}
		}
		p := r.Path
		if !strings.HasPrefix(lp, "equipment/") {
			p = path.Join("equipment", p)
		}
		var rarity, va, vb int32
		key := fmt.Sprintf("name_%d", r.ID)
		if s, err := catalog.ResolveScript(a, p); err == nil {
			for i := 0; i < len(s.Cells); i++ {
				if s.Cells[i].Type == 3 {
					switch s.Cells[i].Text {
					case "[name]":
						if i+1 < len(s.Cells) && s.Cells[i+1].Type == 8 {
							if k := extractKey(s.Cells[i+1].Reference); k != "" {
								key = k
							}
						}
					case "[rarity]":
						if i+1 < len(s.Cells) && s.Cells[i+1].Type == 0 {
							rarity = s.Cells[i+1].Value
						}
					case "[variation]":
						if i+1 < len(s.Cells) && s.Cells[i+1].Type == 0 {
							va = s.Cells[i+1].Value
						}
						if i+2 < len(s.Cells) && s.Cells[i+2].Type == 0 {
							vb = s.Cells[i+2].Value
						}
					}
				}
			}
		}
		nm := strings.TrimSpace(eqKV[key])
		if nm != "" {
			if va > 0 || (part == "skin" && vb > 0) {
				kFull := fmt.Sprintf("%s|%s|%d|%d", job, part, va, vb)
				kA := fmt.Sprintf("%s|%s|%d", job, part, va)
				if _, ok := varFullNamed[kFull]; !ok {
					varFullNamed[kFull] = nm
				}
				if _, ok := varANamed[kA]; !ok {
					varANamed[kA] = nm
				}
			}
			continue
		}
		blanks = append(blanks, blankRow{
			ID: r.ID, Key: key, Job: job, Part: part, VA: va, VB: vb, Rarity: rarity,
		})
	}
	out := map[string]any{
		"total_avatars":  totalAvatars,
		"blanks":         blanks,
		"var_full_named": varFullNamed,
		"var_a_named":    varANamed,
		"stk_names":      stkKV,
		"eq_key_count":   len(eqKV),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out)
}
'''
    tmp_go = core.SERVER / 'cmd' / 'avatarscan_tmp.go'
    tmp_go.write_text(go_src, encoding='utf-8')
    try:
        res = subprocess.run(
            [str(go_bin), 'run', './cmd/avatarscan_tmp.go', '-source', str(inner_pvf)],
            cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
            creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=240,
        )
        if res.returncode != 0:
            raise RuntimeError('扫描装扮失败：' + (res.stderr or res.stdout or '')[-600:])
        return json.loads(res.stdout)
    finally:
        tmp_go.unlink(missing_ok=True)


def _build_tid_to_box_map(blank_ids: set[int], stk_names: dict[str, str]) -> dict[int, str]:
    if not BOOSTERS_CATALOG.is_file():
        return {}
    with gzip.open(BOOSTERS_CATALOG, 'rt', encoding='utf-8') as f:
        boxes = (json.load(f).get('boxes') or {})
    tid_to_box: dict[int, str] = {}
    for bid, box in boxes.items():
        bn = stk_names.get(f'name_{bid}', '').strip()
        if not bn or bn == '测试' or 'test' in bn.lower() or not _ZH_RE.search(bn) or _KO_RE.search(bn):
            continue
        all_items = [it for lst in (box.get('c') or {}).values() for it in lst] + [
            it for grp in (box.get('g') or []) for it in grp
        ]
        for it in all_items:
            tid = int(it.get('t', 0))
            if tid in blank_ids and tid not in tid_to_box:
                tid_to_box[tid] = bn
    return tid_to_box


def _extract_eq_str(inner_pvf: Path, out_dir: Path) -> dict[str, str]:
    go_bin = core.pre.GO_BIN
    env = dict(os.environ)
    env['GOPATH'] = str(ROOT / 'tools' / 'gopath')
    env['GOCACHE'] = str(ROOT / 'tools' / 'gocache')
    env['PATH'] = str(go_bin.parent) + os.pathsep + env.get('PATH', '')
    res = subprocess.run(
        [
            str(go_bin), 'run', './cmd/pvfinspect',
            '-source', str(inner_pvf),
            '-files', EQ_STR_ENTRY,
            '-output', str(out_dir),
        ],
        cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
        creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=180,
    )
    if res.returncode != 0:
        raise RuntimeError('pvfinspect 提取失败：' + (res.stderr or res.stdout or '')[-600:])
    bf = out_dir / '00-equipment.uv.str.bin'
    if not bf.is_file():
        raise FileNotFoundError(f'PVF 缺少 {EQ_STR_ENTRY}')
    return parse_uv_str_utf16(bf.read_bytes())


def _backup_live_pvf(live_pvf: Path, before_sha: str) -> Path:
    BACKUP_DIR.mkdir(parents=True, exist_ok=True)
    backup_pvf = BACKUP_DIR / 'Script.pvf.bak'
    if not backup_pvf.is_file():
        core.file_copy(live_pvf, backup_pvf)
        core.atomic(BACKUP_MANIFEST, {
            'live_pvf': str(live_pvf),
            'backup_pvf': str(backup_pvf),
            'original_sha256': before_sha,
            'original_size': live_pvf.stat().st_size,
        })
    return backup_pvf


def apply_staged() -> dict:
    if core.client_up():
        raise RuntimeError('检测到 DFO.exe 正在运行，请先关闭游戏客户端再应用装扮汉化补丁')
    if not STAGE_MANIFEST.is_file():
        raise FileNotFoundError(f'未找到待安装清单：{STAGE_MANIFEST}')
    meta = core.read(STAGE_MANIFEST)
    staged = Path(meta['staged_pvf'])
    live = Path(meta['live_pvf'])
    expected_before = meta['before_sha256']
    expected_after = meta['after_sha256']
    if not staged.is_file():
        raise FileNotFoundError(f'未找到暂存文件：{staged}')
    cur_sha = core.digest(live)
    if cur_sha == expected_after:
        staged.unlink(missing_ok=True)
        STAGE_MANIFEST.unlink(missing_ok=True)
        return {'status': 'already_installed', 'sha256': cur_sha}
    if cur_sha != expected_before:
        raise RuntimeError('当前 Script.pvf 已变更，请重新运行本工具构建最新补丁')
    if core.digest(staged) != expected_after:
        raise RuntimeError('暂存 Script.pvf 校验和不匹配')
    backup_pvf = _backup_live_pvf(live, cur_sha)
    core.file_copy(staged, live)
    if core.digest(live) != expected_after:
        raise RuntimeError('安装写入后校验和不匹配')
    staged.unlink(missing_ok=True)
    STAGE_MANIFEST.unlink(missing_ok=True)
    return {
        'status': 'installed',
        'backup_pvf': str(backup_pvf),
        'before_sha256': cur_sha,
        'after_sha256': expected_after,
    }


def rollback_live_pvf() -> dict:
    if core.client_up():
        raise RuntimeError('检测到 DFO.exe 正在运行，请先关闭游戏客户端再回滚 Script.pvf')
    if not BACKUP_MANIFEST.is_file():
        raise FileNotFoundError(f'未找到备份清单：{BACKUP_MANIFEST}')
    meta = core.read(BACKUP_MANIFEST)
    backup_pvf = Path(meta['backup_pvf'])
    live_pvf = Path(meta['live_pvf'])
    expected = meta['original_sha256']
    if core.digest(backup_pvf) != expected:
        raise RuntimeError('备份 Script.pvf 校验和不匹配，中止回滚')
    core.file_copy(backup_pvf, live_pvf)
    if core.digest(live_pvf) != expected:
        raise RuntimeError('回滚写入后校验和不匹配')
    return {'status': 'rolled_back', 'live_pvf': str(live_pvf), 'sha256': expected}


def build_and_apply(stage_only: bool = False, inner_override: Path | None = None) -> dict:
    client_dir = core.pre.client_exe().parent
    live_pvf = client_dir / 'Script.pvf'
    if not live_pvf.is_file():
        raise FileNotFoundError(f'未找到客户端 Script.pvf：{live_pvf}')

    before_sha = core.digest(live_pvf)
    fp = lr.pvf_fingerprint(live_pvf)
    is_inner = bool(fp and fp.get('inner'))
    inner_source = (
        Path(inner_override).resolve()
        if inner_override is not None
        else (live_pvf if is_inner else lr.ensure_inner_pvf(live_pvf, fp))
    )

    scan = _scan_pvf_avatars(inner_source)
    blanks = scan.get('blanks') or []
    orig_eq_count = int(scan.get('eq_key_count') or 0)
    if not blanks and inner_override is None:
        return {
            'status': 'already_localized',
            'sha256': before_sha,
            'total_avatars': int(scan.get('total_avatars') or 0),
            'blank_avatars': 0,
        }

    blank_ids = {int(r['id']) for r in blanks}
    tid_to_box = _build_tid_to_box_map(blank_ids, scan.get('stk_names') or {})
    patches, res_stats = resolve_blank_avatar_names(
        blanks,
        scan.get('var_full_named') or {},
        scan.get('var_a_named') or {},
        tid_to_box,
    )

    merger = _ensure_merger_binary()
    STAGE_DIR.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=STAGE_DIR, prefix='.work-') as tmpdir:
        tmp = Path(tmpdir)
        ov = tmp / 'equipment.uv.str'
        ov.write_text(render_uv_str_overlay(patches), encoding='utf-8')
        plan_file = tmp / 'plan.json'
        plan_file.write_text(
            json.dumps({
                'layers': [{
                    'path': EQ_STR_ENTRY,
                    'source': str(ov),
                    'mod_id': 'avatar-names-zh',
                    'priority': 0,
                }]
            }, ensure_ascii=False),
            encoding='utf-8',
        )
        merged_inner = tmp / 'merged.inner.pvf'
        env = dict(os.environ)
        env['GOGC'] = '10'
        res = subprocess.run(
            [str(merger), '-source', str(inner_source), '-plan', str(plan_file), '-output', str(merged_inner)],
            cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
            creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=600,
        )
        if res.returncode != 0:
            raise RuntimeError('pvfmerge 失败：' + (res.stderr or res.stdout or '')[-800:])
        merge_report = json.loads((res.stdout or '{}').strip().splitlines()[-1])

        # Strict post-merge read-back verification across all patched keys
        verify_dir = tmp / 'verify'
        verify_dir.mkdir()
        v_eq = _extract_eq_str(merged_inner, verify_dir)
        if len(v_eq) < orig_eq_count:
            raise RuntimeError(f'合并后 equipment.uv.str 词条数减少：{len(v_eq)} < {orig_eq_count}')
        for k, want in patches.items():
            if v_eq.get(k) != want:
                raise RuntimeError(f'回读校验失败 {k}: got={v_eq.get(k)!r}, want={want!r}')

        staged_pvf = STAGE_DIR / 'Script.pvf'
        if is_inner:
            core.file_copy(merged_inner, staged_pvf)
            after_sha = core.digest(staged_pvf)
        else:
            after_sha = rewrap_inner_to_client_pvf(merged_inner, staged_pvf, client_dir)

    stats = {
        'total_avatars': int(scan.get('total_avatars') or 0),
        'patched_avatar_keys': len(patches),
        'resolution': res_stats,
        'orig_eq_keys': orig_eq_count,
        'verified_eq_keys': len(v_eq),
        'sample_101551928': patches.get('name_101551928'),
    }

    if not stage_only and not core.client_up():
        backup_pvf = _backup_live_pvf(live_pvf, before_sha)
        core.file_copy(staged_pvf, live_pvf)
        if core.digest(live_pvf) != after_sha:
            raise RuntimeError('写入 Script.pvf 校验失败')
        staged_pvf.unlink(missing_ok=True)
        STAGE_MANIFEST.unlink(missing_ok=True)
        return {
            'status': 'installed',
            'backup_pvf': str(backup_pvf),
            'before_sha256': before_sha,
            'after_sha256': after_sha,
            'merge_report': merge_report,
            'stats': stats,
        }

    core.atomic(STAGE_MANIFEST, {
        'live_pvf': str(live_pvf),
        'staged_pvf': str(staged_pvf),
        'before_sha256': before_sha,
        'after_sha256': after_sha,
        'stats': stats,
    })
    return {
        'status': 'staged_pending_client_close',
        'staged_pvf': str(staged_pvf),
        'before_sha256': before_sha,
        'after_sha256': after_sha,
        'merge_report': merge_report,
        'stats': stats,
    }


def main():
    p = argparse.ArgumentParser(description='Fill all blank avatar names in US100 Script.pvf')
    p.add_argument('--stage-only', action='store_true', help='Build and stage without replacing live Script.pvf')
    p.add_argument('--apply-staged', action='store_true', help='Install staged Script.pvf after closing DFO.exe')
    p.add_argument('--rollback', action='store_true', help='Restore original Script.pvf from backup')
    p.add_argument('--inner-source', type=Path, default=None, help='Optional baseline inner PVF path')
    args = p.parse_args()
    if args.rollback:
        result = rollback_live_pvf()
    elif args.apply_staged:
        result = apply_staged()
    else:
        result = build_and_apply(stage_only=args.stage_only, inner_override=args.inner_source)
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
