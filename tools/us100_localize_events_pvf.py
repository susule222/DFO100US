# -*- coding: utf-8 -*-
r"""Fill all blank/missing active-event item names, dialogs, and UI strings in US100 Script.pvf.

Root causes verified from live Script.pvf + DFO_unpacked.exe:
  1. Event 520 (ReverseHotTime / 经验加成狂欢) reward boxes (10333092, 10333113),
     their nested child items (10333089, 10333090, 10333091, 10333114, 10333117),
     and grandchild selection boxes (10333115, 10333116) have EMPTY `name_*` and
     `explain_*` entries in `string/stackable.uv.str`, causing blank tooltips in-game.
  2. Event 619 (TimeAttendanceRandomPick / 时间引导石每日打卡) reward items
     (10326700..10326709, including child material 10326708) also have EMPTY
     `name_*` and `explain_*` entries in `string/stackable.uv.str`.
  3. Event 520 Becky speech bubble keys (`event_520_becky_dialog_start`,
     `event_520_becky_dialog_00..09`, `event_520_becky_fake_text`) and mail keys,
     Event 619 mail keys, and Event 367 help text (`event_367_question_msg`) are
     EMPTY in `string/event.uv.str`, causing Becky's white speech bubble to render blank.
  4. Event 520 / 602 UI keys (`april_fools_day_hot_time_title`,
     `april_fools_day_hot_time_text_0`, `event_602_main_text_0`,
     `event_602_main_text_1`, `event_602_popup_11`) are EMPTY in `string/ui.uv.str`.

This tool overlays all 65 verified Chinese strings across `string/stackable.uv.str`,
`string/event.uv.str`, and `string/ui.uv.str` via `pvfmerge`, performs strict
post-merge read-back verification (checking every key and preserving all existing
keys), backs up live `Script.pvf` with SHA-256 manifest, rewraps to US100 format,
and installs atomically (or stages when DFO.exe is running).
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

STK_STR_ENTRY = 'string/stackable.uv.str'
EV_STR_ENTRY = 'string/event.uv.str'
UI_STR_ENTRY = 'string/ui.uv.str'
TARGET_ENTRIES = (STK_STR_ENTRY, EV_STR_ENTRY, UI_STR_ENTRY)

STAGE_DIR = ROOT / 'runtime' / 'event-pvf-localize-build'
STAGE_MANIFEST = STAGE_DIR / 'pending.json'
BACKUP_DIR = ROOT / 'runtime' / 'event-pvf-localize-backup'
BACKUP_MANIFEST = BACKUP_DIR / 'manifest.json'

# Verified blank item entries in string/stackable.uv.str for active Events 520 & 619
# (including all recursive booster children & grandchildren).
STACKABLE_STRING_PATCHES: dict[str, str] = {
    # Event 520 (经验加成狂欢) — 5M & 10M reward boxes + children + grandchildren
    'name_10333092': '贝奇的狂欢5分钟礼盒',
    'explain_10333092': '开启后，可获得Lv100史诗装备自选礼盒、Lv100满级飞跃胶囊与Lv100史诗武器自选礼盒。',
    'name_10333113': '卢克的狂欢10分钟礼盒',
    'explain_10333113': '开启后，可获得愚人节纪念装扮套装礼盒与愚人节纪念伤害字体。',
    'name_10333090': 'Lv100史诗装备自选礼盒',
    'explain_10333090': '开启后，可从各部位Lv100史诗装备中任选1件获得。',
    'name_10333117': 'Lv100满级飞跃胶囊',
    'explain_10333117': '仅限Lv1~99角色在赛丽亚旅馆内使用，使用后角色直接提升至Lv100。',
    'name_10333114': 'Lv100史诗武器自选礼盒',
    'explain_10333114': '开启后，可从Lv100史诗武器专属自选礼盒或Lv100史诗防具自选礼盒中任选1个获得。',
    'name_10333115': 'Lv100史诗武器专属自选礼盒',
    'explain_10333115': '开启后，可根据自身职业任选1把Lv100史诗武器。',
    'name_10333116': 'Lv100史诗防具自选礼盒',
    'explain_10333116': '开启后，可根据职业类型任选1件Lv100史诗防具。',
    'name_10333089': '愚人节纪念装扮套装礼盒',
    'explain_10333089': '开启后，可获得对应职业的活动纪念装扮套装。',
    'name_10333091': '愚人节纪念伤害字体',
    'explain_10333091': '使用后，可在皮肤仓库中解锁并穿戴活动限定伤害字体。',

    # Event 619 (时间引导石每日打卡) — 10 items (10326700..10326709)
    'name_10326700': '史诗祈愿灵药 (武器/防具)',
    'explain_10326700': '在赛丽亚旅馆或城镇中使用后，激活武器与防具史诗掉落祈愿增益效果。',
    'name_10326701': '史诗祈愿灵药 (首饰)',
    'explain_10326701': '在赛丽亚旅馆或城镇中使用后，激活首饰部位史诗掉落祈愿增益效果。',
    'name_10326702': '史诗祈愿灵药 (特殊装备)',
    'explain_10326702': '在赛丽亚旅馆或城镇中使用后，激活特殊装备史诗掉落祈愿增益效果。',
    'name_10326703': '史诗祈愿灵药 (全部位)',
    'explain_10326703': '在赛丽亚旅馆或城镇中使用后，激活全部位史诗掉落祈愿增益效果。',
    'name_10326704': '史诗祈愿灵药 (神话加成)',
    'explain_10326704': '在赛丽亚旅馆或城镇中使用后，激活神话与史诗掉落祈愿增益效果。',
    'name_10326705': '史诗祈愿灵药随机礼盒',
    'explain_10326705': '开启后，随机获得1个史诗祈愿灵药。',
    'name_10326706': '史诗祈愿灵药自选礼盒',
    'explain_10326706': '开启后，可从5种史诗祈愿灵药中任选1个获得。',
    'name_10326707': '时间引导石礼盒 (300个)',
    'explain_10326707': '开启后，可获得300个活动时间引导石。',
    'name_10326708': '活动时间引导石',
    'explain_10326708': '可用于挑战智慧的引导地下城的活动材料。',
    'name_10326709': '神话装备变换石',
    'explain_10326709': '使用后，可将已拥有的1件Lv100神话装备随机变换为其他神话装备。',
}

# Verified blank keys in string/event.uv.str for active Events 520, 619, and 367
EVENT_STRING_PATCHES: dict[str, str] = {
    'event_520_becky_dialog_start': '欢迎参加经验加成狂欢！累计在线满5分钟和10分钟即可领取丰厚好礼！',
    'event_520_becky_dialog_00': '嘿嘿，在线时长已经达标啦，快点击右侧【领取】按钮拿走奖励吧！',
    'event_520_becky_dialog_01': '今天也要元气满满地刷图升级哦，双倍经验正在火热生效中！',
    'event_520_becky_dialog_02': '5分钟礼盒里装满了Lv100成长好礼，千万别忘记领取啦！',
    'event_520_becky_dialog_03': '10分钟礼盒里还有纪念装扮和限定伤害字体等着你呢！',
    'event_520_becky_dialog_04': '贝奇为你加油打气！快去阿拉德大陆展开冒险吧！',
    'event_520_becky_dialog_05': '听说认真参加活动的冒险家，深渊一定会闪光哦！',
    'event_520_becky_dialog_06': '把鼠标移到礼盒图标上，就可以查看里面包含的全部奖励啦！',
    'event_520_becky_dialog_07': '奖励会直接发放到角色背包中，记得留出足够的消耗品栏位哦！',
    'event_520_becky_dialog_08': '贝奇和卢克一起为大家准备了超值狂欢礼盒！',
    'event_520_becky_dialog_09': '活动长期开放，随时上线都能轻松领取全部奖励！',
    'event_520_becky_fake_text': '经验加成狂欢活动火热进行中！',
    'event_520_fake_mail_title': '经验加成狂欢活动奖励',
    'event_520_fake_mail_msg': '感谢参与经验加成狂欢活动，奖励已发放。',
    'event_520_fake_reward_mail_title': '经验加成狂欢活动礼盒',
    'event_520_fake_reward_mail_msg': '您领取的经验加成狂欢礼盒已送达，请查收！',
    'event_520_reward_mail_title': '经验加成狂欢活动奖励',
    'event_520_reward_mail_msg': '恭喜达成累计在线任务，狂欢礼盒已发放！',
    'event_619_mail_title': '时间引导石每日打卡奖励',
    'event_619_mail_msg_00': '每日在线打卡奖励已发放，祝您深渊大爆！',
    'event_619_mail_msg_01': '累计打卡阶段大奖已发放，请查收！',
    'event_367_question_msg': '每日保持在线即可领取签到好礼，累计天数还可解锁额外奖励！',
}

# Verified blank keys in string/ui.uv.str for active Events 520 & 602
UI_STRING_PATCHES: dict[str, str] = {
    'april_fools_day_hot_time_title': '经验加成狂欢',
    'april_fools_day_hot_time_text_0': '累计在线达到指定时间，即可领取贝奇与卢克的狂欢礼盒！',
    'event_602_main_text_0': '通关推荐地下城为魔盒充能，每日最多搜索3次并保存心仪道具！',
    'event_602_main_text_1': '每周可将已保存的魔盒奖励提取至背包，累计提取还能领取阶段礼盒！',
    'event_602_popup_11': '确定要将当前保存的奖励提取至角色背包吗？',
}

ALL_PATCHES: dict[str, dict[str, str]] = {
    STK_STR_ENTRY: STACKABLE_STRING_PATCHES,
    EV_STR_ENTRY: EVENT_STRING_PATCHES,
    UI_STR_ENTRY: UI_STRING_PATCHES,
}


def render_uv_str_overlay(patches: dict[str, str]) -> str:
    """Render CRLF-delimited key>value overlay lines for pvfmerge."""
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


def compute_missing_patches(
    existing_tables: dict[str, dict[str, str]],
) -> dict[str, dict[str, str]]:
    """Return only entries whose current value differs from target patches."""
    diff: dict[str, dict[str, str]] = {}
    for entry_path, patches in ALL_PATCHES.items():
        cur = existing_tables.get(entry_path, {})
        need = {k: v for k, v in patches.items() if cur.get(k) != v}
        if need:
            diff[entry_path] = need
    return diff


def _extract_tables(inner_pvf: Path, out_dir: Path) -> tuple[dict[str, dict[str, str]], dict[str, bytes]]:
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
        creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=180,
    )
    if res.returncode != 0:
        raise RuntimeError('pvfinspect 提取失败：' + (res.stderr or res.stdout or '')[-600:])
    tables: dict[str, dict[str, str]] = {}
    raws: dict[str, bytes] = {}
    for idx, entry in enumerate(TARGET_ENTRIES):
        bf = out_dir / f'{idx:02d}-{Path(entry).name}.bin'
        if not bf.is_file():
            raise FileNotFoundError(f'PVF 缺少词条表：{entry}')
        raw = bf.read_bytes()
        raws[entry] = raw
        tables[entry] = parse_uv_str_utf16(raw)
    return tables, raws


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


def rollback_live_pvf() -> dict:
    if core.client_up():
        raise RuntimeError('检测到 DFO.exe 正在运行，请先关闭游戏客户端再回滚 Script.pvf')
    if not BACKUP_MANIFEST.is_file():
        raise FileNotFoundError(f'未找到备份清单：{BACKUP_MANIFEST}')
    meta = core.read(BACKUP_MANIFEST)
    backup_pvf = Path(meta['backup_pvf'])
    live_pvf = Path(meta['live_pvf'])
    expected = meta['original_sha256']
    if not backup_pvf.is_file():
        raise FileNotFoundError(f'未找到备份文件：{backup_pvf}')
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
        orig_tables, orig_raws = _extract_tables(inner_source, ext_dir)
        orig_counts = {k: len(v) for k, v in orig_tables.items()}

        needed = compute_missing_patches(orig_tables)
        bad_nul = [
            k for k, raw in orig_raws.items()
            if not raw.endswith(b'\x00\x00\x00\x00') or '\x00' in raw[:-4].decode('utf-16-le')
        ]
        if not needed and not bad_nul and inner_override is None:
            return {
                'status': 'already_localized',
                'sha256': before_sha,
                'table_key_counts': orig_counts,
            }

        layers = []
        for idx, (entry_path, patches) in enumerate(ALL_PATCHES.items()):
            ov = tmp / f'{idx:02d}-{Path(entry_path).name}'
            ov.write_text(render_uv_str_overlay(patches), encoding='utf-8')
            layers.append({
                'path': entry_path,
                'source': str(ov),
                'mod_id': 'event-strings-zh',
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

        # Strict post-merge read-back verification across all 3 .uv.str tables
        verify_dir = tmp / 'verify'
        verify_dir.mkdir()
        verified_tables, verified_raws = _extract_tables(merged_inner, verify_dir)
        verified_counts: dict[str, int] = {}
        for entry_path, patches in ALL_PATCHES.items():
            v_raw = verified_raws[entry_path]
            if not v_raw.endswith(b'\x00\x00\x00\x00') or '\x00' in v_raw[:-4].decode('utf-16-le'):
                raise RuntimeError(f'合并后 {entry_path} UTF-16LE NUL 终止符异常（缺少末尾 NUL 或存在内部 NUL）')
            v_kv = verified_tables[entry_path]
            verified_counts[entry_path] = len(v_kv)
            if len(v_kv) < orig_counts[entry_path]:
                raise RuntimeError(
                    f'合并后 {entry_path} 词条数异常减少：{len(v_kv)} < {orig_counts[entry_path]}'
                )
            for k, expected_v in patches.items():
                if v_kv.get(k) != expected_v:
                    raise RuntimeError(
                        f'合并后 {entry_path} 校验失败 {k}: got={v_kv.get(k)!r}, want={expected_v!r}'
                    )

        staged_pvf = STAGE_DIR / 'Script.pvf'
        if is_inner:
            core.file_copy(merged_inner, staged_pvf)
            after_sha = core.digest(staged_pvf)
        else:
            after_sha = rewrap_inner_to_client_pvf(merged_inner, staged_pvf, client_dir)

    stats = {
        'patched_keys_total': sum(len(p) for p in ALL_PATCHES.values()),
        'newly_updated_keys': sum(len(p) for p in needed.values()),
        'orig_counts': orig_counts,
        'verified_counts': verified_counts,
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
    p = argparse.ArgumentParser(
        description='Fill all blank active-event item names, dialogs, and UI strings in US100 Script.pvf'
    )
    p.add_argument('--stage-only', action='store_true', help='Build and verify without replacing live Script.pvf')
    p.add_argument('--rollback', action='store_true', help='Restore original Script.pvf from backup')
    p.add_argument('--inner-source', type=Path, default=None, help='Optional baseline inner PVF path')
    args = p.parse_args()
    if args.rollback:
        result = rollback_live_pvf()
    else:
        result = build_and_apply(stage_only=args.stage_only, inner_override=args.inner_source)
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
