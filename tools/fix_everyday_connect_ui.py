# -*- coding: utf-8 -*-
r"""Fix Event 515 (EveryDayConnect / 每日签到活动) XUI display defects in Script.pvf.

Root causes verified from Script.pvf + sprite_interface2_event_everydayconnect.NPK
+ DFO_unpacked.exe:
  1. `ui/event/everydayconnect/main.xui` and `calendar.xui` reference
     `MainBanner.img` / `CalendarBanner.img` with `imageIndex="NUM:7"`, while
     US100's NPK only ships 2 frames (`0` and `1`), producing a blank dark
     header and a red missing-frame X behind the top-left sprout badge.
     Frame `0` is native Chinese artwork ("每日签到盛典！ 长期开放", 519x58 / 261x78).
  2. `<POPUPWINDOW>` in `main.xui` and `calendar.xui` lacks `TitleString`, so
     the window title fell back to English `"Daily Check-in Event"`. Setting
     `TitleString="STR:&lt;20::us100_act_tooltip_515&gt;"` (paired with empty
     NOTI 108 Title for Event 515) displays native UTF-16LE Chinese `"每日签到活动"`.
  3. `ui/event/everydayconnect/slot/slot.xui` (and mini-calendar slot XUIs)
     clamp `dailyText` to `Size="VECTOR:32, 12"` / `stringLenth="NUM:32"`,
     truncating `"第 4 天"`..`"第 28 天"` to `"第 4..."`, `"第 1..."`, `"第 2..."`.
     Widening `dailyText` to the full 52px slot width (`Pos="VECTOR:0, 5"`,
     `Size="VECTOR:52, 12"`, `stringLenth="NUM:52"`) renders every day label
     cleanly centered with zero ellipsis.
  4. `allRecvBtn` ("全部领取", CMD 680 `action=3`) in `main.xui` was hidden
     (`IsVisible="BOOL:false"`). Enabling `IsVisible="BOOL:true"` exposes the
     one-click Receive All button, while hiding the dead Nexon `hyper` link.
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path[:0] = [str(HERE / 'atelier'), str(HERE)]

import core  # noqa: E402
import live_resources as lr  # noqa: E402
from mod_workshop import _ensure_merger_binary, rewrap_inner_to_client_pvf  # noqa: E402

MAIN_XUI = 'ui/event/everydayconnect/main.xui'
CALENDAR_XUI = 'ui/event/everydayconnect/calendar.xui'
SLOT_XUI = 'ui/event/everydayconnect/slot/slot.xui'
SLOT_CAL_XUI = 'ui/event/everydayconnect/slot/slot_calendar.xui'
SLOT_ACC_CAL_XUI = 'ui/event/everydayconnect/slot/slot_acc_calendar.xui'
TARGET_ENTRIES = (MAIN_XUI, CALENDAR_XUI, SLOT_XUI, SLOT_CAL_XUI, SLOT_ACC_CAL_XUI)

TITLE_ATTR = 'TitleString="STR:&lt;20::us100_act_tooltip_515&gt;"'

STAGE_DIR = ROOT / 'runtime' / 'everyday-connect-ui-fix'
STAGE_MANIFEST = STAGE_DIR / 'pending.json'


def _upsert_attr(attrs: str, name: str, value: str) -> str:
    pat = re.compile(rf'\b{re.escape(name)}="[^"]*"')
    if pat.search(attrs):
        return pat.sub(f'{name}="{value}"', attrs, count=1)
    return attrs.rstrip() + f' {name}="{value}"'


def _patch_popup_title(text: str) -> tuple[str, int]:
    changed = 0

    def _repl(m: re.Match) -> str:
        nonlocal changed
        attrs = m.group(1)
        new_attrs = _upsert_attr(attrs, 'TitleString', 'STR:&lt;20::us100_act_tooltip_515&gt;')
        new_tag = f'<POPUPWINDOW {new_attrs.strip()} />'
        if new_tag != m.group(0):
            changed += 1
        return new_tag

    out = re.sub(r'<POPUPWINDOW\s+([^>]+?)\s*/>', _repl, text, count=1)
    if TITLE_ATTR not in out:
        raise ValueError('failed to set POPUPWINDOW TitleString')
    return out, changed


def _hide_hyper(text: str) -> tuple[str, int]:
    changed = 0

    def _repl(m: re.Match) -> str:
        nonlocal changed
        attrs = m.group(1)
        if 'ID="STR:hyper"' not in attrs:
            return m.group(0)
        new_attrs = _upsert_attr(attrs, 'IsVisible', 'BOOL:false')
        new_attrs = _upsert_attr(new_attrs, 'Pos', 'VECTOR:-2000, -2000')
        new_tag = f'<CNLabelHyperLink {new_attrs.strip()}>'
        if new_tag != m.group(0):
            changed += 1
        return new_tag

    out = re.sub(r'<CNLabelHyperLink\s+([^>]+?)>', _repl, text)
    return out, changed


def patch_main_xui_text(text: str) -> tuple[str, dict]:
    out, title_changed = _patch_popup_title(text)
    banner_changed = 0

    def _banner_repl(m: re.Match) -> str:
        nonlocal banner_changed
        attrs = m.group(1)
        if 'MainBanner.img' not in attrs:
            return m.group(0)
        new_attrs = _upsert_attr(attrs, 'imageIndex', 'NUM:0')
        new_attrs = _upsert_attr(new_attrs, 'Size', 'VECTOR:519, 58')
        new_tag = f'<CNUIControlImage {new_attrs.strip()} />'
        if new_tag != m.group(0):
            banner_changed += 1
        return new_tag

    out = re.sub(r'<CNUIControlImage\s+([^>]+?)\s*/>', _banner_repl, out)
    if 'MainBanner.img' not in out or 'imageIndex="NUM:7"' in out:
        raise ValueError('MainBanner.img imageIndex was not patched to 0')

    btn_changed = 0

    def _btn_repl(m: re.Match) -> str:
        nonlocal btn_changed
        attrs = m.group(1)
        if 'ID="STR:allRecvBtn"' not in attrs:
            return m.group(0)
        new_attrs = _upsert_attr(attrs, 'IsVisible', 'BOOL:true')
        new_tag = f'<CNButton {new_attrs.strip()} />'
        if new_tag != m.group(0):
            btn_changed += 1
        return new_tag

    out = re.sub(r'<CNButton\s+([^>]+?)\s*/>', _btn_repl, out)
    out, hyper_changed = _hide_hyper(out)
    ET.fromstring(out.rstrip('\x00').lstrip('\ufeff'))
    return out, {
        'title_changed': title_changed,
        'banner_changed': banner_changed,
        'all_recv_changed': btn_changed,
        'hyper_changed': hyper_changed,
        'changed': bool(title_changed or banner_changed or btn_changed or hyper_changed),
    }


def patch_calendar_xui_text(text: str) -> tuple[str, dict]:
    out, title_changed = _patch_popup_title(text)
    banner_changed = 0

    def _banner_repl(m: re.Match) -> str:
        nonlocal banner_changed
        attrs = m.group(1)
        if 'CalendarBanner.img' not in attrs:
            return m.group(0)
        new_attrs = _upsert_attr(attrs, 'imageIndex', 'NUM:0')
        new_tag = f'<CNUIControlImage {new_attrs.strip()} />'
        if new_tag != m.group(0):
            banner_changed += 1
        return new_tag

    out = re.sub(r'<CNUIControlImage\s+([^>]+?)\s*/>', _banner_repl, out)
    if 'CalendarBanner.img' not in out or 'imageIndex="NUM:7"' in out:
        raise ValueError('CalendarBanner.img imageIndex was not patched to 0')

    out, hyper_changed = _hide_hyper(out)
    ET.fromstring(out.rstrip('\x00').lstrip('\ufeff'))
    return out, {
        'title_changed': title_changed,
        'banner_changed': banner_changed,
        'hyper_changed': hyper_changed,
        'changed': bool(title_changed or banner_changed or hyper_changed),
    }


def patch_slot_daily_text(text: str, pos: str, width: int = 52) -> tuple[str, dict]:
    changed = 0

    def _repl(m: re.Match) -> str:
        nonlocal changed
        attrs = m.group(1)
        if 'ID="STR:dailyText"' not in attrs:
            return m.group(0)
        new_attrs = _upsert_attr(attrs, 'Pos', f'VECTOR:{pos}')
        new_attrs = _upsert_attr(new_attrs, 'Size', f'VECTOR:{width}, 12')
        new_attrs = _upsert_attr(new_attrs, 'stringLenth', f'NUM:{width}')
        new_tag = f'<CNUIControlText {new_attrs.strip()} />'
        if new_tag != m.group(0):
            changed += 1
        return new_tag

    out = re.sub(r'<CNUIControlText\s+([^>]+?)\s*/>', _repl, text)
    if f'stringLenth="NUM:{width}"' not in out:
        raise ValueError('dailyText was not widened')
    ET.fromstring(out.rstrip('\x00').lstrip('\ufeff'))
    return out, {'daily_text_changed': changed, 'changed': bool(changed)}


def patch_entry_bytes(entry_path: str, raw: bytes) -> tuple[bytes, dict]:
    text = raw.decode('utf-16-le').rstrip('\x00')
    if entry_path == MAIN_XUI:
        patched, stats = patch_main_xui_text(text)
    elif entry_path == CALENDAR_XUI:
        patched, stats = patch_calendar_xui_text(text)
    elif entry_path == SLOT_XUI:
        patched, stats = patch_slot_daily_text(text, pos='0, 5', width=52)
    elif entry_path == SLOT_CAL_XUI:
        patched, stats = patch_slot_daily_text(text, pos='-9, -17', width=52)
    elif entry_path == SLOT_ACC_CAL_XUI:
        patched, stats = patch_slot_daily_text(text, pos='-9, -15', width=52)
    else:
        raise ValueError(f'unsupported entry: {entry_path}')
    # Preserve clean UTF-16LE bytes (pvfmerge ensures trailing \x00\x00).
    bad_nul = not raw.endswith(b'\x00\x00') or '\x00' in raw[:-2].decode('utf-16-le')
    stats['bad_nul'] = bad_nul
    stats['changed'] = bool(stats['changed'] or bad_nul)
    return patched.encode('utf-16-le'), stats


def _extract_entries(inner_pvf: Path, out_dir: Path) -> dict[str, bytes]:
    go_bin = core.pre.GO_BIN
    env = dict(os.environ)
    env['GOPATH'] = str(ROOT / 'tools' / 'gopath')
    env['GOCACHE'] = str(ROOT / 'tools' / 'gopath' / 'cache')
    env['PATH'] = str(go_bin.parent) + os.pathsep + env.get('PATH', '')
    res = subprocess.run(
        [
            str(go_bin), 'run', './cmd/pvfinspect',
            '-source', str(inner_pvf),
            '-files', ','.join(TARGET_ENTRIES),
            '-output', str(out_dir),
        ],
        cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
        creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=180,
    )
    if res.returncode != 0:
        raise RuntimeError('pvfinspect 提取失败：' + (res.stderr or res.stdout or '')[-600:])
    out: dict[str, bytes] = {}
    for idx, entry in enumerate(TARGET_ENTRIES):
        p = out_dir / f'{idx:02d}-{Path(entry).name}.bin'
        if not p.is_file():
            raise FileNotFoundError(f'PVF 缺少 {entry}')
        out[entry] = p.read_bytes()
    return out


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
        orig_map = _extract_entries(inner_source, ext_dir)

        patched_map: dict[str, bytes] = {}
        stats_map: dict[str, dict] = {}
        any_changed = False
        for entry, raw in orig_map.items():
            patched_bytes, st = patch_entry_bytes(entry, raw)
            patched_map[entry] = patched_bytes
            stats_map[entry] = st
            if st['changed']:
                any_changed = True

        if not any_changed and inner_override is None:
            return {'status': 'already_fixed', 'sha256': before_sha, 'stats': stats_map}

        layers = []
        for idx, entry in enumerate(TARGET_ENTRIES):
            src = tmp / f'{idx:02d}-{Path(entry).name}'
            src.write_bytes(patched_map[entry])
            layers.append({
                'path': entry,
                'source': str(src),
                'mod_id': 'everyday-connect-ui-fix',
                'priority': idx,
            })
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

        verify_dir = tmp / 'verify'
        verify_dir.mkdir()
        verified_map = _extract_entries(merged_inner, verify_dir)
        for entry, v_raw in verified_map.items():
            if not v_raw.endswith(b'\x00\x00') or '\x00' in v_raw[:-2].decode('utf-16-le'):
                raise RuntimeError(f'合并后 {entry} UTF-16LE NUL 终止符异常')
            _, recheck = patch_entry_bytes(entry, v_raw)
            if recheck['changed']:
                raise RuntimeError(f'合并后 {entry} 回读校验失败：{recheck}')

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
            'stats': stats_map,
        }

    core.atomic(STAGE_MANIFEST, {
        'live_pvf': str(live_pvf),
        'staged_pvf': str(staged_pvf),
        'before_sha256': before_sha,
        'after_sha256': after_sha,
        'stats': stats_map,
    })
    return {
        'status': 'staged',
        'before_sha256': before_sha,
        'after_sha256': after_sha,
        'merge_report': merge_report,
        'stats': stats_map,
        'reason': 'DFO.exe 运行中，已暂存，下次启动客户端自动替换',
    }


def apply_staged_if_pending() -> str | None:
    """Install staged Script.pvf atomically when DFO.exe is not running."""
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
        staged.unlink(missing_ok=True)
        STAGE_MANIFEST.unlink(missing_ok=True)
        return None
    if core.digest(staged) != expected_after:
        raise RuntimeError('暂存的每日签到界面修复 PVF 校验不匹配')
    core.file_copy(staged, live)
    if core.digest(live) != expected_after:
        raise RuntimeError('安装每日签到界面修复 PVF 后校验不匹配')
    staged.unlink(missing_ok=True)
    STAGE_MANIFEST.unlink(missing_ok=True)
    return '已自动应用每日签到界面修复补丁'


def main() -> int:
    parser = argparse.ArgumentParser(description='修复每日签到活动(Event 515) XUI 界面显示错误')
    parser.add_argument('--stage-only', action='store_true', help='仅构建并暂存')
    parser.add_argument('--apply-staged', action='store_true', help='仅在客户端未运行时应用已暂存补丁')
    parser.add_argument('--inner', type=str, default='', help='指定解包后的 inner PVF 路径')
    args = parser.parse_args()
    if args.apply_staged:
        msg = apply_staged_if_pending()
        print(msg or '无待应用的每日签到界面修复补丁')
        return 0
    result = build_and_apply(
        stage_only=args.stage_only,
        inner_override=Path(args.inner) if args.inner else None,
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
