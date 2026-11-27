# -*- coding: utf-8 -*-
r"""Localize US100 HUD event notification floating tooltips into Chinese in Script.pvf.

Root cause:
  DFO US100's NOTI 108 wire string decoder (0x144e6eec0 -> 0x144f1d8a0) uses
  MultiByteToWideChar with global codepage 0x14a3ef854 = 437 (CP437), which
  cannot decode UTF-8/GBK Chinese bytes on an unmodified DFO.exe.

  However, HUDNotificationManager (0x1437a2ec3 / 0x1437aeb0e) natively reads
  UTF-16LE Chinese tooltips from PVF `ui/event/informationbuttoninfo.evn`
  (`[tooltip] \`enable\` <20::key>` / `[tooltip] \`disable\` <20::key>`) resolved
  against `string/ui.uv.str` (table 20 in `list/n_string.lst`). Because the
  active event icon blocks lacked `[tooltip]` entries, the client fell back to
  NOTI 108's ASCII titles (e.g. "Burning Fatigue Event").

This tool adds UTF-16LE Chinese tooltip strings to `string/ui.uv.str` and
attaches `[tooltip]` enable/disable references to all active US100 event icon
blocks in `ui/event/informationbuttoninfo.evn`, compiles and merges via
`pvfmerge`, verifies every token and string post-merge, rewraps to US100 client
Script.pvf format with SHA-256 verification, and installs atomically (or stages
when DFO.exe is running).
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path[:0] = [str(HERE / 'atelier'), str(HERE)]

import core  # noqa: E402
import live_resources as lr  # noqa: E402
from mod_workshop import _ensure_merger_binary, rewrap_inner_to_client_pvf  # noqa: E402

EVN_ENTRY = 'ui/event/informationbuttoninfo.evn'
STR_ENTRY = 'string/ui.uv.str'
TARGET_ENTRIES = (EVN_ENTRY, STR_ENTRY)
STAGE_DIR = ROOT / 'runtime' / 'event-tooltip-zh'
STAGE_MANIFEST = STAGE_DIR / 'pending.json'

# Active US100 event IDs -> (string/ui.uv.str key, Chinese HUD floating tooltip)
EVENT_TOOLTIPS: dict[int, tuple[str, str]] = {
    306: ('us100_act_tooltip_306', '网吧特权活动'),
    367: ('us100_act_tooltip_367', '积分商城活动'),
    392: ('us100_act_tooltip_392', '每日挑战活动'),
    500: ('us100_act_tooltip_500', '深渊派对活动'),
    512: ('us100_act_tooltip_512', '赛丽亚的欢迎活动'),
    515: ('us100_act_tooltip_515', '每日签到活动'),
    520: ('us100_act_tooltip_520', '经验加成热力活动'),
    602: ('us100_act_tooltip_602', '爆率与金币加成活动'),
    614: ('us100_act_tooltip_614', '周常副本活动'),
    615: ('us100_act_tooltip_615', '丹吉袖珍罐活动'),
    619: ('us100_act_tooltip_619', '燃烧疲劳活动'),
}


def _format_token(tok: dict) -> str:
    tp = tok['type']
    if tp == 0:
        return str(int(tok['value']))
    if tp == 2:
        return repr(float(tok.get('number', 0.0)))
    if tp == 3:
        text = tok.get('text', '')
        if not (text.startswith('[') and text.endswith(']')):
            raise ValueError(f'unexpected type-3 tag token: {tok!r}')
        return text
    if tp == 6:
        text = tok.get('text', '')
        if '`' in text:
            raise ValueError(f'backtick inside type-6 token: {tok!r}')
        return f'`{text}`'
    if tp == 8:
        ref = tok.get('reference', '')
        if not (ref.startswith('<') and ref.endswith('>') and '::' in ref):
            raise ValueError(f'unexpected type-8 reference token: {tok!r}')
        return ref
    raise ValueError(f'unsupported PVF script token type {tp}: {tok!r}')


def patch_evn_tokens(tokens: list[dict]) -> tuple[list[dict], dict]:
    """Attach Chinese [tooltip] enable/disable refs to all active event blocks.

    Idempotent: replaces any existing [tooltip] entries on target event blocks
    while preserving every other block and token untouched.
    """
    out: list[dict] = []
    stats = {
        'total_blocks': 0,
        'patched_blocks': 0,
        'patched_events': [],
        'already_matching': 0,
    }
    i = 0
    n = len(tokens)
    while i < n:
        tok = tokens[i]
        if tok.get('type') == 3 and tok.get('text') == '[info]':
            end = i + 1
            while end < n and not (tokens[end].get('type') == 3 and tokens[end].get('text') == '[/info]'):
                end += 1
            if end >= n:
                raise ValueError(f'unterminated [info] block at token {i}')
            stats['total_blocks'] += 1
            body = tokens[i + 1:end]
            ev_id = None
            for k in range(len(body) - 1):
                if body[k].get('type') == 3 and body[k].get('text') == '[event]' and body[k + 1].get('type') == 0:
                    ev_id = int(body[k + 1]['value'])
                    break

            if ev_id in EVENT_TOOLTIPS:
                key, _ = EVENT_TOOLTIPS[ev_id]
                ref = f'<20::{key}>'
                wanted_tail = [
                    {'type': 3, 'text': '[tooltip]'},
                    {'type': 6, 'text': 'enable'},
                    {'type': 8, 'reference': ref},
                    {'type': 3, 'text': '[tooltip]'},
                    {'type': 6, 'text': 'disable'},
                    {'type': 8, 'reference': ref},
                ]
                cleaned: list[dict] = []
                existing_tooltips: list[tuple[str, str]] = []
                k = 0
                while k < len(body):
                    if (
                        body[k].get('type') == 3
                        and body[k].get('text') == '[tooltip]'
                        and k + 2 < len(body)
                    ):
                        existing_tooltips.append((
                            body[k + 1].get('text', ''),
                            body[k + 2].get('reference', body[k + 2].get('text', '')),
                        ))
                        k += 3
                        continue
                    cleaned.append(body[k])
                    k += 1
                if existing_tooltips == [('enable', ref), ('disable', ref)]:
                    stats['already_matching'] += 1
                else:
                    stats['patched_blocks'] += 1
                stats['patched_events'].append(ev_id)
                out.append(tokens[i])
                out.extend(cleaned)
                out.extend(wanted_tail)
                out.append(tokens[end])
            else:
                out.extend(tokens[i:end + 1])
            i = end + 1
            continue
        out.append(tok)
        i += 1
    return out, stats


def render_evn_script(tokens: list[dict]) -> str:
    parts: list[str] = []
    for tok in tokens:
        s = _format_token(tok)
        if tok['type'] == 3:
            parts.append('\n' + s + '\n')
        else:
            parts.append(s + ' ')
    return ''.join(parts).lstrip('\n') + '\n'


def render_ui_str_overlay() -> str:
    lines = [f'{key}>{label}' for _, (key, label) in sorted(EVENT_TOOLTIPS.items())]
    return '\r\n'.join(lines) + '\r\n'


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
            '-tokens',
            '-output', str(out_dir),
        ],
        cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
        creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=180,
    )
    if res.returncode != 0:
        raise RuntimeError('pvfinspect 提取失败：' + (res.stderr or res.stdout or '')[-600:])
    evn_tokens = out_dir / f'00-{Path(EVN_ENTRY).name}.tokens.json'
    str_bin = out_dir / f'01-{Path(STR_ENTRY).name}.bin'
    if not evn_tokens.is_file() or not str_bin.is_file():
        raise FileNotFoundError(f'未在 PVF 中找到目标文件：{TARGET_ENTRIES}')
    return {
        'evn_tokens': evn_tokens,
        'str_bin': str_bin,
    }


def _parse_str_table_utf16(raw: bytes) -> dict[str, str]:
    txt = raw.decode('utf-16-le').lstrip('\ufeff')
    kv: dict[str, str] = {}
    for line in txt.splitlines():
        if '>' in line:
            k, v = line.split('>', 1)
            if k:
                kv[k] = v
    return kv


def apply_staged_if_pending() -> str | None:
    """Install staged localized Script.pvf atomically when DFO.exe is not running."""
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
        raise RuntimeError('暂存的活动浮窗汉化 PVF 校验不匹配')
    core.file_copy(staged, live)
    if core.digest(live) != expected_after:
        raise RuntimeError('安装活动浮窗汉化 PVF 后校验不匹配')
    staged.unlink(missing_ok=True)
    STAGE_MANIFEST.unlink(missing_ok=True)
    return '已自动应用活动浮窗名称汉化补丁'


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
        extracted = _extract_entries(inner_source, ext_dir)

        orig_tokens = json.loads(extracted['evn_tokens'].read_text(encoding='utf-8'))
        orig_str_kv = _parse_str_table_utf16(extracted['str_bin'].read_bytes())

        patched_tokens, stats = patch_evn_tokens(orig_tokens)
        str_needs_update = any(
            orig_str_kv.get(key) != label for _, (key, label) in EVENT_TOOLTIPS.items()
        )
        if stats['patched_blocks'] == 0 and not str_needs_update and inner_override is None:
            return {'status': 'already_localized', 'sha256': before_sha, 'stats': stats}

        evn_src = tmp / 'informationbuttoninfo.evn'
        evn_src.write_text(render_evn_script(patched_tokens), encoding='utf-8')

        str_src = tmp / 'ui.uv.str'
        str_src.write_text(render_ui_str_overlay(), encoding='utf-8')

        layers = [
            {'path': EVN_ENTRY, 'source': str(evn_src), 'mod_id': 'event-tooltip-zh', 'priority': 0},
            {'path': STR_ENTRY, 'source': str(str_src), 'mod_id': 'event-tooltip-zh', 'priority': 1},
        ]
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

        # Strict post-merge read-back verification: check every token & UTF-16LE tooltip string.
        verify_dir = tmp / 'verify'
        verify_dir.mkdir()
        verified = _extract_entries(merged_inner, verify_dir)
        v_tokens = json.loads(verified['evn_tokens'].read_text(encoding='utf-8'))
        v_str_kv = _parse_str_table_utf16(verified['str_bin'].read_bytes())

        if len(v_tokens) != len(patched_tokens):
            raise RuntimeError(f'合并后 token 数不匹配：{len(v_tokens)} != {len(patched_tokens)}')
        for ev_id, (key, label) in EVENT_TOOLTIPS.items():
            if v_str_kv.get(key) != label:
                raise RuntimeError(f'合并后 {STR_ENTRY} 缺少或错写 {key}: {v_str_kv.get(key)!r}')
        _, recheck = patch_evn_tokens(v_tokens)
        if recheck['total_blocks'] != stats['total_blocks'] or recheck['patched_blocks'] != 0:
            raise RuntimeError(f'合并后 {EVN_ENTRY} 回读校验失败：{recheck}')
        stats['verified_tokens'] = len(v_tokens)
        stats['verified_str_keys'] = len(v_str_kv)

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
    p = argparse.ArgumentParser(description='Localize US100 HUD event floating tooltip names to Chinese in Script.pvf')
    p.add_argument('--stage-only', action='store_true', help='Build & stage patched Script.pvf without replacing live file')
    p.add_argument('--inner-source', type=Path, default=None, help='Optional baseline inner PVF path')
    args = p.parse_args()
    result = build_and_apply(stage_only=args.stage_only, inner_override=args.inner_source)
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
