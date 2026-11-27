# -*- coding: utf-8 -*-
"""Fix broken ChannelBase.img frame references in US100 selectchannelver2/3.xui.

Root cause:
  US100 v2.24.0.124 ships a newer 17-frame ChannelBase.img (0..16) designed for
  the in-game channelchange window, while ui/channel/selectchannelver3.xui (and
  selectchannelver2.xui) still reference old ChannelBase.img indices:
    - ID 1221 -> index 21 (OOB >= 17) -> renders a red X under the tab bar
    - CNCheckBox CheckEffect/OverEffect -> indices 24/25 (OOB >= 17) -> renders
      a red X over the selected/hovered channel slot
    - ID 1212 -> index 0 (594x446 dark panel) -> giant dark box at top-right
    - ID 1213 -> index 1 (136x446 dark sidebar) -> tall vertical dark strip
    - ID -1   -> index 8 (477x150 dark panel) -> giant dark box at bottom
    - ID 1210 -> index 9 (stray 67x18 badge at top-right)
    - ID 2002 -> UpImg 3 (192x44 yellow border) -> floating yellow box at bottom-right

This script patches both XUI files cleanly inside Script.pvf (preserving every
control ID so DFO.exe C++ lookups never fail), rewraps to US100 client format
with full SHA-256 verification, and either installs immediately (when DFO.exe is
closed) or stages the verified PVF for automatic swap on next launcher start.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path[:0] = [str(HERE / 'atelier'), str(HERE)]

import core  # noqa: E402
import live_resources as lr  # noqa: E402
from mod_workshop import _ensure_merger_binary, rewrap_inner_to_client_pvf  # noqa: E402

TARGET_ENTRIES = (
    'ui/channel/selectchannelver3.xui',
    'ui/channel/selectchannelver2.xui',
)
STAGE_DIR = ROOT / 'runtime' / 'channel-ui-fix'
STAGE_MANIFEST = STAGE_DIR / 'pending.json'

_HIDE_IMAGE_IDS = {'1210', '1212', '1213', '1221', '-1'}

_REFRESH_BUTTON_XML = (
    '<CNButton ButtonDrawType="STR:PATTERN" ButtonName="STR:刷新" '
    'ClickSound="STR:CLICK_BUTTON1" DisableImg="NUM:0" DownImg="NUM:0" '
    'ID="STR:2002" IsFontShadow="BOOL:true" OverImg="NUM:0" '
    'PatternImgWidth="NUM:64" '
    'PatternLeftImgIndex_Disable="NUM:181" PatternLeftImgIndex_Down="NUM:178" '
    'PatternLeftImgIndex_Over="NUM:175" PatternLeftImgIndex_Up="NUM:172" '
    'PatternMiddleImgIndex_Disable="NUM:182" PatternMiddleImgIndex_Down="NUM:179" '
    'PatternMiddleImgIndex_Over="NUM:176" PatternMiddleImgIndex_Up="NUM:173" '
    'PatternRightImgIndex_Disable="NUM:183" PatternRightImgIndex_Down="NUM:180" '
    'PatternRightImgIndex_Over="NUM:177" PatternRightImgIndex_Up="NUM:174" '
    'Pos="VECTOR:619, 466" Size="VECTOR:64, 20" UpImg="NUM:0" '
    'addImageScale="VECTOR2F:0.000000, 0.000000" '
    'patternImgPack="STR:Interface/WindowCommon.img" />'
)


def _patch_control_tag(match: re.Match) -> str:
    full = match.group(0)
    tag = match.group(1)
    attrs = match.group(2)

    id_m = re.search(r'ID="(?:STR:)?([^"]+)"', attrs)
    cid = id_m.group(1) if id_m else ''

    # 1. Hide broken/misaligned ChannelBase.img overlay controls safely off-screen
    #    while keeping their IDs intact for C++ GetControl() lookups.
    if tag == 'CNUIControlImage' and cid in _HIDE_IMAGE_IDS and 'channelbase.img' in attrs.lower():
        patched = attrs
        patched = re.sub(r'Pos="VECTOR:[^"]+"', 'Pos="VECTOR:-2000, -2000"', patched)
        if 'Pos=' not in patched:
            patched += ' Pos="VECTOR:-2000, -2000"'
        patched = re.sub(r'Size="VECTOR:[^"]+"', 'Size="VECTOR:0, 0"', patched)
        patched = re.sub(
            r'ImagePack="STR:[^"]*"',
            'ImagePack="STR:Interface2/Channel/channelselect/ChannelSlot/ChannelSlot2.img"',
            patched,
        )
        patched = re.sub(r'imageIndex="NUM:-?\d+"', 'imageIndex="NUM:0"', patched)
        if 'IsShow=' in patched:
            patched = re.sub(r'IsShow="BOOL:(?:true|false)"', 'IsShow="BOOL:false"', patched)
        else:
            patched += ' IsShow="BOOL:false"'
        suffix = ' />' if full.endswith('/>') else '>'
        return f'<{tag} {patched.strip()}{suffix}'

    # 2. Replace out-of-bounds ChannelBase.img[24/25] check/hover overlays on all
    #    channel slots with the valid 166x40 glow sprite in ChannelSlot2.img[25].
    if tag == 'CNCheckBox' and 'channelbase.img' in attrs.lower():
        patched = attrs
        patched = re.sub(
            r'CheckEffectImgPackName="STR:[^"]*ChannelBase\.img"',
            'CheckEffectImgPackName="STR:Interface2/Channel/channelselect/ChannelSlot/ChannelSlot2.img"',
            patched,
            flags=re.I,
        )
        patched = re.sub(
            r'OverEffectImgPackName="STR:[^"]*ChannelBase\.img"',
            'OverEffectImgPackName="STR:Interface2/Channel/channelselect/ChannelSlot/ChannelSlot2.img"',
            patched,
            flags=re.I,
        )
        patched = re.sub(r'CheckEffectImgIndex="NUM:24"', 'CheckEffectImgIndex="NUM:25"', patched)
        patched = re.sub(r'OverEffectImgIndex="NUM:25"', 'OverEffectImgIndex="NUM:25"', patched)
        patched = re.sub(r'OverEffectImgAlpha="NUM:\d+"', 'OverEffectImgAlpha="NUM:140"', patched)
        suffix = ' />' if full.endswith('/>') else '>'
        return f'<{tag} {patched.strip()}{suffix}'

    # 3. Replace Refresh button (ID 2002) whose UpImg=3 drew a 192x44 floating yellow box.
    if tag == 'CNButton' and cid == '2002' and 'channelbase.img' in attrs.lower():
        return _REFRESH_BUTTON_XML

    return full


def patch_channel_xui_text(text: str) -> tuple[str, dict]:
    """Patch selectchannelver2/3.xui XML text and return (new_text, stats)."""
    before_oob21 = text.count('imageIndex="NUM:21"')
    before_ck24 = text.count('CheckEffectImgIndex="NUM:24"')
    before_ov25 = text.count('OverEffectImgIndex="NUM:25"')
    patched = re.sub(r'<(CNUIControlImage|CNCheckBox|CNButton)\s+([^>]+?)/?>', _patch_control_tag, text)
    after_oob = len(re.findall(
        r'(?:ChannelBase|channelbase)\.img[^>]*(?:imageIndex|CheckEffectImgIndex|OverEffectImgIndex)="NUM:(?:1[7-9]|[2-9]\d)"',
        patched,
        flags=re.I,
    ))
    after_cb_refs = len(re.findall(r'channelbase\.img', patched, flags=re.I))
    stats = {
        'before_oob21': before_oob21,
        'before_ck24': before_ck24,
        'before_ov25': before_ov25,
        'after_oob': after_oob,
        'after_channelbase_refs': after_cb_refs,
    }
    return patched, stats


def patch_channel_xui_bytes(raw: bytes) -> tuple[bytes, dict]:
    text = raw.decode('utf-16-le')
    patched, stats = patch_channel_xui_text(text)
    if stats['after_oob'] != 0:
        raise ValueError(f'residual out-of-bounds ChannelBase.img refs: {stats}')
    if stats['after_channelbase_refs'] != 0:
        raise ValueError(f'unexpected residual ChannelBase.img refs: {stats}')
    return patched.encode('utf-16-le'), stats


def _extract_entries(inner_pvf: Path, out_dir: Path) -> dict[str, Path]:
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
            '-output', str(out_dir),
        ],
        cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
        creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=120,
    )
    if res.returncode != 0:
        raise RuntimeError('pvfinspect 提取失败：' + (res.stderr or res.stdout or '')[-600:])
    found = {}
    for idx, entry in enumerate(TARGET_ENTRIES):
        cand = out_dir / f'{idx:02d}-{Path(entry).name}.bin'
        if not cand.is_file():
            raise FileNotFoundError(f'未在 PVF 中找到 {entry}')
        found[entry] = cand
    return found


def apply_staged_if_pending() -> str | None:
    """Install staged fixed Script.pvf atomically when DFO.exe is not running."""
    if not STAGE_MANIFEST.is_file():
        return None
    if core.client_up():
        return None
    meta = core.read(STAGE_MANIFEST)
    staged = Path(meta['staged_pvf'])
    live = Path(meta['live_pvf'])
    expected_before = meta['before_sha256']
    expected_after = meta['after_sha256']
    if not staged.is_file() or not live.is_file():
        STAGE_MANIFEST.unlink(missing_ok=True)
        return None
    cur_sha = core.digest(live)
    if cur_sha == expected_after:
        staged.unlink(missing_ok=True)
        STAGE_MANIFEST.unlink(missing_ok=True)
        return None
    if cur_sha != expected_before:
        # User switched Script.pvf manually; drop stale staged file cleanly.
        staged.unlink(missing_ok=True)
        STAGE_MANIFEST.unlink(missing_ok=True)
        return None
    if core.digest(staged) != expected_after:
        raise RuntimeError('暂存的频道 UI 修复 PVF 校验不匹配')
    core.file_copy(staged, live)
    if core.digest(live) != expected_after:
        raise RuntimeError('安装频道 UI 修复 PVF 后校验不匹配')
    staged.unlink(missing_ok=True)
    STAGE_MANIFEST.unlink(missing_ok=True)
    return '已自动应用频道选择界面修复补丁（消除红叉与错位框）'


def build_and_apply(stage_only: bool = False) -> dict:
    client_dir = core.pre.client_exe().parent
    live_pvf = client_dir / 'Script.pvf'
    if not live_pvf.is_file():
        raise FileNotFoundError(f'未找到客户端 Script.pvf：{live_pvf}')

    before_sha = core.digest(live_pvf)
    fp = lr.pvf_fingerprint(live_pvf)
    is_inner = bool(fp and fp.get('inner'))
    inner_source = live_pvf if is_inner else lr.ensure_inner_pvf(live_pvf, fp)
    merger = _ensure_merger_binary()

    STAGE_DIR.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=STAGE_DIR, prefix='.work-') as tmpdir:
        tmp = Path(tmpdir)
        ext_dir = tmp / 'extracted'
        ext_dir.mkdir()
        extracted = _extract_entries(inner_source, ext_dir)

        layers = []
        patch_stats = {}
        changed = False
        for seq, entry in enumerate(TARGET_ENTRIES):
            raw = extracted[entry].read_bytes()
            fixed, st = patch_channel_xui_bytes(raw)
            patch_stats[entry] = st
            if fixed != raw:
                changed = True
            out_f = tmp / f'{seq:02d}_{Path(entry).name}'
            out_f.write_bytes(fixed)
            layers.append({
                'path': entry,
                'source': str(out_f),
                'mod_id': 'channel-ui-fix',
                'priority': seq,
            })

        if not changed:
            return {'status': 'already_fixed', 'sha256': before_sha, 'stats': patch_stats}

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

        staged_pvf = STAGE_DIR / 'Script.pvf'
        if is_inner:
            core.file_copy(merged_inner, staged_pvf)
            after_sha = core.digest(staged_pvf)
        else:
            after_sha = rewrap_inner_to_client_pvf(merged_inner, staged_pvf, client_dir)

    if not stage_only and not core.client_up():
        core.file_copy(staged_pvf, live_pvf)
        if core.digest(live_pvf) != after_sha:
            raise RuntimeError('写入 Script.pvf 校验失败')
        staged_pvf.unlink(missing_ok=True)
        STAGE_MANIFEST.unlink(missing_ok=True)
        return {
            'status': 'installed',
            'before_sha256': before_sha,
            'after_sha256': after_sha,
            'merge_report': merge_report,
            'stats': patch_stats,
        }

    core.atomic(STAGE_MANIFEST, {
        'live_pvf': str(live_pvf),
        'staged_pvf': str(staged_pvf),
        'before_sha256': before_sha,
        'after_sha256': after_sha,
        'stats': patch_stats,
    })
    return {
        'status': 'staged_pending_client_close',
        'staged_pvf': str(staged_pvf),
        'before_sha256': before_sha,
        'after_sha256': after_sha,
        'merge_report': merge_report,
        'stats': patch_stats,
    }


def main():
    p = argparse.ArgumentParser(description='Fix US100 Channel Select UI red-X and layout glitches in Script.pvf')
    p.add_argument('--stage-only', action='store_true', help='Build & stage patched Script.pvf without replacing live file')
    args = p.parse_args()
    result = build_and_apply(stage_only=args.stage_only)
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
