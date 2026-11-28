# -*- coding: utf-8 -*-
r"""Restore missing active event NPCs in US100 Seria's Room (map/common/(r)gate_seria.map).

Root causes verified from live US100 Script.pvf + DFO_unpacked.exe:
  1. Event 614 (Melody for Adventurers / 希曼·斯特拉的准备阶段！) rewards Music Notes
     (50052385) and Golden Music Notes (50052386), whose tooltip directs players to
     exchange them at Himan Stella in Seria's Room. However, NPC 100000827
     (Npc/100000827_event_himan_seria.npc, shop 100000399 -> event_21himan_seria_KDNF.shp)
     was removed from map/common/(r)gate_seria.map in the 2023-02 US100 snapshot,
     gated on inactive `[open visible event] 426` instead of active Event 614, and
     had empty speech-bubble keys (`dialog_100000827_All_1/2`) in string/npc.uv.str.
  2. Event 602 (Ria's Special Cube Lab II / 莉亚的特别魔盒实验室) is active on the server,
     but its Seria's Room NPC 500000169 (Npc/500000169_univ_lia_lab.npc, shop 500000097,
     `[open visible event] 602`) was also absent from map/common/(r)gate_seria.map and
     had blank localization keys (`name_100001400`, `field_role_name_100001400`,
     `role_name1_100001400`, `role_name2_100001400`) in string/npc.uv.str.

This tool patches all three PVF entries cleanly via `pvfmerge` (preserving exact 5-byte
binary script cells and UTF-16LE NUL terminators), verifies every token and string on
read-back, backs up live Script.pvf with SHA-256 manifest, rewraps to US100 format, and
installs atomically (or stages for auto-apply on next client launch if DFO.exe is open).
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import struct
import subprocess
import sys
import tempfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path[:0] = [str(HERE / 'atelier'), str(HERE)]

import core  # noqa: E402
import live_resources as lr  # noqa: E402
from mod_workshop import _ensure_merger_binary, rewrap_inner_to_client_pvf  # noqa: E402

MAP_ENTRY = 'map/common/(r)gate_seria.map'
HIMAN_NPC_ENTRY = 'npc/100000827_event_himan_seria.npc'
NPC_STR_ENTRY = 'string/npc.uv.str'
TARGET_ENTRIES = (MAP_ENTRY, HIMAN_NPC_ENTRY, NPC_STR_ENTRY)

HIMAN_NPC_ID = 100000827
HIMAN_TARGET_EVENT_ID = 614
LIA_LAB_NPC_ID = 500000169

# Non-overlapping, walkable positions inside map/common/(r)gate_seria.map:
#   Seria (2) at (555,146), Storage (22) at (367,200), PostBox (32) at (866,190),
#   AvatarCloset (20543) at (100,300), CoinShop (20673) at (340,0), EnchantBook (100001774) at (630,11).
SERIA_EVENT_NPCS: tuple[tuple[int, str, int, int, int], ...] = (
    (HIMAN_NPC_ID, '[left]', 715, 225, 0),
    (LIA_LAB_NPC_ID, '[right]', 250, 265, 0),
)

NPC_STRING_PATCHES: dict[str, str] = {
    'dialog_100000827_All_1': '把收集到的音乐音符带给我，就可以兑换各种丰厚奖励哦！',
    'dialog_100000827_All_2': '还可以用音乐音符兑换更珍贵的金色音乐音符！',
    'name_100001400': '莉亚的特别魔盒实验室',
    'field_role_name_100001400': '活动商店',
    'role_name1_100001400': '特别魔盒实验室',
    'role_name2_100001400': '活动说明',
}

STAGE_DIR = ROOT / 'runtime' / 'seria-npc-pvf-build'
STAGE_MANIFEST = STAGE_DIR / 'pending.json'
BACKUP_DIR = ROOT / 'runtime' / 'seria-npc-pvf-backup'
BACKUP_MANIFEST = BACKUP_DIR / 'manifest.json'


def _pack_cell(token_type: int, value: int) -> bytes:
    return struct.pack('<Bi', token_type, value)


def patch_gate_seria_map_bytes(raw: bytes, tokens: list[dict]) -> tuple[bytes, list[int]]:
    """Insert missing event NPC rows right before [/NPC] in (r)gate_seria.map."""
    if len(raw) != len(tokens) * 5:
        raise ValueError(f'map binary size mismatch: {len(raw)} vs {len(tokens) * 5}')

    npc_start = -1
    npc_end = -1
    dir_offsets: dict[str, int] = {}
    existing_ids: set[int] = set()

    for i, t in enumerate(tokens):
        if t.get('type') == 3 and t.get('text') == '[NPC]':
            npc_start = i
            j = i + 1
            while j < len(tokens):
                tj = tokens[j]
                if tj.get('type') == 3 and tj.get('text') == '[/NPC]':
                    npc_end = j
                    break
                if j + 4 < len(tokens) and tj.get('type') == 0 and tokens[j + 1].get('type') == 6:
                    existing_ids.add(int(tj['value']))
                    dtxt = str(tokens[j + 1].get('text') or '')
                    if dtxt in ('[left]', '[right]'):
                        dir_offsets[dtxt] = int(tokens[j + 1]['value'])
                    j += 5
                else:
                    j += 1
            break

    if npc_start < 0 or npc_end < 0:
        raise ValueError('map/common/(r)gate_seria.map missing [NPC]..[/NPC] section')
    for needed_dir in ('[left]', '[right]'):
        if needed_dir not in dir_offsets:
            raise ValueError(f'map/common/(r)gate_seria.map missing direction string {needed_dir}')

    insert_cells = bytearray()
    added: list[int] = []
    for npc_id, facing, x, y, flag in SERIA_EVENT_NPCS:
        if npc_id in existing_ids:
            continue
        insert_cells += _pack_cell(0, npc_id)
        insert_cells += _pack_cell(6, dir_offsets[facing])
        insert_cells += _pack_cell(0, x)
        insert_cells += _pack_cell(0, y)
        insert_cells += _pack_cell(0, flag)
        added.append(npc_id)

    if not insert_cells:
        return raw, []

    split_off = npc_end * 5
    return raw[:split_off] + bytes(insert_cells) + raw[split_off:], added


def patch_himan_npc_visible_event(raw: bytes, tokens: list[dict], target_event_id: int = HIMAN_TARGET_EVENT_ID) -> tuple[bytes, bool]:
    """Update [open visible event] value in 100000827_event_himan_seria.npc to active Event 614."""
    if len(raw) != len(tokens) * 5:
        raise ValueError(f'npc binary size mismatch: {len(raw)} vs {len(tokens) * 5}')

    for i, t in enumerate(tokens):
        if t.get('type') == 3 and t.get('text') == '[open visible event]':
            if i + 1 >= len(tokens) or tokens[i + 1].get('type') != 0:
                raise ValueError('malformed [open visible event] value token')
            if int(tokens[i + 1]['value']) == target_event_id:
                return raw, False
            out = bytearray(raw)
            off = (i + 1) * 5
            out[off:off + 5] = _pack_cell(0, target_event_id)
            return bytes(out), True
    raise ValueError('100000827_event_himan_seria.npc missing [open visible event]')


def render_uv_str_overlay(patches: dict[str, str]) -> str:
    lines: list[str] = []
    for k, v in sorted(patches.items()):
        if '>' in k or '\n' in k or '\r' in k:
            raise ValueError(f'invalid key: {k!r}')
        if '\n' in v or '\r' in v:
            raise ValueError(f'invalid value with raw newline for {k}: {v!r}')
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


def _extract_entries(inner_pvf: Path, out_dir: Path) -> tuple[dict[str, bytes], dict[str, list[dict]]]:
    go_bin = core.pre.GO_BIN
    env = dict(os.environ)
    env['GOPATH'] = str(ROOT / 'tools' / 'gopath')
    env['GOCACHE'] = str(ROOT / 'tools' / 'gocache')
    env['PATH'] = str(go_bin.parent) + os.pathsep + env.get('PATH', '')
    res = subprocess.run(
        [
            str(go_bin), 'run', './cmd/pvfinspect',
            '-source', str(inner_pvf),
            '-files', ','.join(TARGET_ENTRIES),
            '-tokens',
            '-output', str(out_dir),
        ],
        cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
        creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=180,
    )
    if res.returncode != 0:
        raise RuntimeError('pvfinspect 提取失败：' + (res.stderr or res.stdout or '')[-600:])
    raws: dict[str, bytes] = {}
    toks: dict[str, list[dict]] = {}
    for idx, entry in enumerate(TARGET_ENTRIES):
        base = f'{idx:02d}-{Path(entry).name}'
        bf = out_dir / f'{base}.bin'
        if not bf.is_file():
            raise FileNotFoundError(f'PVF 缺少条目：{entry}')
        raws[entry] = bf.read_bytes()
        tf = out_dir / f'{base}.tokens.json'
        if tf.is_file():
            toks[entry] = json.loads(tf.read_text(encoding='utf-8'))
    return raws, toks


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


def build_and_apply(stage_only: bool = False, inner_override: Path | None = None) -> dict:
    client_dir = core.pre.client_exe().parent
    live_pvf = client_dir / 'Script.pvf'
    if not live_pvf.is_file():
        raise FileNotFoundError(f'未找到客户端 Script.pvf：{live_pvf}')

    before_sha = core.digest(live_pvf)
    fp = lr.pvf_fingerprint(live_pvf)
    is_inner = bool(fp and fp.get('inner'))
    if inner_override is not None:
        inner_source = Path(inner_override).resolve()
    else:
        inner_source = live_pvf if is_inner else lr.ensure_inner_pvf(live_pvf, fp)
    merger = _ensure_merger_binary()

    STAGE_DIR.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=STAGE_DIR, prefix='.work-') as tmpdir:
        tmp = Path(tmpdir)
        ext_dir = tmp / 'extracted'
        ext_dir.mkdir()
        raws, toks = _extract_entries(inner_source, ext_dir)

        new_map_raw, added_npcs = patch_gate_seria_map_bytes(raws[MAP_ENTRY], toks[MAP_ENTRY])
        new_himan_raw, himan_changed = patch_himan_npc_visible_event(raws[HIMAN_NPC_ENTRY], toks[HIMAN_NPC_ENTRY])
        orig_npc_str = parse_uv_str_utf16(raws[NPC_STR_ENTRY])
        missing_str = {k: v for k, v in NPC_STRING_PATCHES.items() if orig_npc_str.get(k) != v}
        bad_nul = (
            not raws[NPC_STR_ENTRY].endswith(b'\x00\x00\x00\x00')
            or '\x00' in raws[NPC_STR_ENTRY][:-4].decode('utf-16-le')
        )

        if not added_npcs and not himan_changed and not missing_str and not bad_nul and inner_override is None:
            return {
                'status': 'already_patched',
                'sha256': before_sha,
            }

        layers = []
        map_ov = tmp / '00-(r)gate_seria.map'
        map_ov.write_bytes(new_map_raw)
        layers.append({'path': MAP_ENTRY, 'source': str(map_ov), 'mod_id': 'seria-event-npcs', 'priority': 0})

        himan_ov = tmp / '01-100000827_event_himan_seria.npc'
        himan_ov.write_bytes(new_himan_raw)
        layers.append({'path': HIMAN_NPC_ENTRY, 'source': str(himan_ov), 'mod_id': 'seria-event-npcs', 'priority': 1})

        str_ov = tmp / '02-npc.uv.str'
        str_ov.write_text(render_uv_str_overlay(NPC_STRING_PATCHES), encoding='utf-8')
        layers.append({'path': NPC_STR_ENTRY, 'source': str(str_ov), 'mod_id': 'seria-event-npcs', 'priority': 2})

        plan_file = tmp / 'plan.json'
        plan_file.write_text(json.dumps({'layers': layers}, ensure_ascii=False), encoding='utf-8')
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

        # Strict post-merge read-back verification across all 3 patched entries.
        verify_dir = tmp / 'verify'
        verify_dir.mkdir()
        v_raws, v_toks = _extract_entries(merged_inner, verify_dir)

        _, still_missing_npcs = patch_gate_seria_map_bytes(v_raws[MAP_ENTRY], v_toks[MAP_ENTRY])
        if still_missing_npcs:
            raise RuntimeError(f'合并后 (r)gate_seria.map 仍缺少活动 NPC: {still_missing_npcs}')
        _, still_wrong_ev = patch_himan_npc_visible_event(v_raws[HIMAN_NPC_ENTRY], v_toks[HIMAN_NPC_ENTRY])
        if still_wrong_ev:
            raise RuntimeError('合并后 100000827_event_himan_seria.npc [open visible event] 校验失败')

        v_str_raw = v_raws[NPC_STR_ENTRY]
        if not v_str_raw.endswith(b'\x00\x00\x00\x00') or '\x00' in v_str_raw[:-4].decode('utf-16-le'):
            raise RuntimeError('合并后 string/npc.uv.str UTF-16LE NUL 终止符异常')
        v_kv = parse_uv_str_utf16(v_str_raw)
        if len(v_kv) < len(orig_npc_str):
            raise RuntimeError(f'合并后 string/npc.uv.str 词条数异常减少：{len(v_kv)} < {len(orig_npc_str)}')
        for k, expected_v in NPC_STRING_PATCHES.items():
            if v_kv.get(k) != expected_v:
                raise RuntimeError(f'合并后 string/npc.uv.str 校验失败 {k}: got={v_kv.get(k)!r}')

        staged_pvf = STAGE_DIR / 'Script.pvf'
        if is_inner:
            core.file_copy(merged_inner, staged_pvf)
            staged_sha = core.digest(staged_pvf)
        else:
            staged_sha = rewrap_inner_to_client_pvf(merged_inner, staged_pvf, client_dir)

    _backup_live_pvf(live_pvf, before_sha)

    if stage_only or core.client_up():
        core.atomic(STAGE_MANIFEST, {
            'staged_pvf': str(staged_pvf),
            'target_pvf': str(live_pvf),
            'staged_sha256': staged_sha,
            'base_sha256': before_sha,
            'added_npcs': added_npcs,
            'himan_event_id': HIMAN_TARGET_EVENT_ID,
            'npc_strings_patched': len(NPC_STRING_PATCHES),
        })
        return {
            'status': 'staged',
            'reason': 'DFO.exe is running' if core.client_up() else 'stage_only',
            'staged_pvf': str(staged_pvf),
            'sha256': staged_sha,
            'added_npcs': added_npcs,
            'merge_report': merge_report,
        }

    core.file_copy(staged_pvf, live_pvf)
    after_sha = core.digest(live_pvf)
    if after_sha != staged_sha:
        raise RuntimeError(f'写入 Script.pvf 校验和不匹配：{after_sha} != {staged_sha}')
    STAGE_MANIFEST.unlink(missing_ok=True)
    staged_pvf.unlink(missing_ok=True)
    return {
        'status': 'applied',
        'live_pvf': str(live_pvf),
        'before_sha256': before_sha,
        'after_sha256': after_sha,
        'added_npcs': added_npcs,
        'merge_report': merge_report,
    }


def apply_staged_if_pending() -> str | None:
    """Install staged Seria room event NPC Script.pvf patch when DFO.exe is not running."""
    if not STAGE_MANIFEST.is_file():
        return None
    if core.client_up():
        return None
    meta = core.read(STAGE_MANIFEST)
    staged_pvf = Path(meta['staged_pvf'])
    target_pvf = Path(meta['target_pvf'])
    expected_sha = meta['staged_sha256']
    if not staged_pvf.is_file():
        STAGE_MANIFEST.unlink(missing_ok=True)
        return None
    if core.digest(staged_pvf) != expected_sha:
        STAGE_MANIFEST.unlink(missing_ok=True)
        return None
    if target_pvf.is_file() and core.digest(target_pvf) == expected_sha:
        STAGE_MANIFEST.unlink(missing_ok=True)
        staged_pvf.unlink(missing_ok=True)
        return None
    core.file_copy(staged_pvf, target_pvf)
    if core.digest(target_pvf) != expected_sha:
        raise RuntimeError('应用暂存的赛丽亚房间活动 NPC Script.pvf 补丁后校验和不匹配')
    STAGE_MANIFEST.unlink(missing_ok=True)
    staged_pvf.unlink(missing_ok=True)
    return '已自动应用赛丽亚房间活动 NPC 补丁至 Script.pvf（希曼·斯特拉 + 莉亚魔盒实验室）'


def main() -> int:
    ap = argparse.ArgumentParser(description='Restore US100 Seria Room event NPCs in Script.pvf')
    ap.add_argument('--stage-only', action='store_true', help='Stage patched Script.pvf without replacing live file')
    ap.add_argument('--apply-staged', action='store_true', help='Apply previously staged Script.pvf if DFO.exe is closed')
    args = ap.parse_args()

    if args.apply_staged:
        msg = apply_staged_if_pending()
        print(msg or 'no pending staged patch')
        return 0

    result = build_and_apply(stage_only=args.stage_only)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
