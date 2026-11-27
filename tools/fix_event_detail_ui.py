# -*- coding: utf-8 -*-
r"""Fix US100 detailed event popup window (EventDesignWindow.xui) and localize
Event Calendar + Event Detail panels into native UTF-16LE Chinese in Script.pvf.

Root causes verified in DFO.exe:
  1. `ui/event/eventlistpopupwindow/eventdesignwindow.xui` ships a stray Nexon
     placeholder control `<CNUIControlImage ID="STR:bg" ImagePack="STR:Interface2/PVP/PVP_Fair/PVP_Fair.img" imageIndex="NUM:22" />`
     that renders a cropped Korean placeholder face ("초붕") below the popup.
     We hide `ID="STR:bg"` off-screen while preserving its ID so
     `EventDesignWindow::Init` (`0x1438bcff8..0x1438bd028`) still succeeds.
  2. `EventDesignWindow::SetEventInfo` (`0x1438bd637..0x1438bd667`) always calls
     `m_textControl1->SetVisible(false)` (`0x1438bd641: xor edx, edx`) on the
     static `CNUIControlText ID="1"` box, and `SetText` (`0x144f8def0`) never
     re-enables visibility. Only dynamic `[text_control]` items parsed by
     `ParseMarkup` (`0x1438be7a0`) call `SetVisible(true)` (`0x1438c0329`).
     Trailing newline spacers left in `Notice` drive `LayoutWindowSize`
     (`0x1438c0ce0`) and `LayoutText1` (`0x1438c0710`) so the popup frame and
     inner `CNBoardImage ID="0"` auto-size cleanly around the `[text_control]`
     rows.
  3. `EventCalendar` Loop 1 (`0x14383d288..0x14383d449`) checks each active
     `eventID` from NOTI 108 against `event/eventlistwindow.evt` (resolved
     against UTF-16LE `string/event.uv.str`, table 23). Adding all 13 US100
     server activity entries with `[popupwindow design]` -> `[title]` +
     `[explain]` gives 100% native UTF-16LE Chinese in BOTH the left calendar
     list AND the right detailed event window, while keeping dynamic launcher
     ON/OFF control intact and hiding the dead external webpage hyperlink.
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

EVT_ENTRY = 'event/eventlistwindow.evt'
STR_ENTRY = 'string/event.uv.str'
XUI_ENTRY = 'ui/event/eventlistpopupwindow/eventdesignwindow.xui'
TARGET_ENTRIES = (EVT_ENTRY, STR_ENTRY, XUI_ENTRY)

STAGE_DIR = ROOT / 'runtime' / 'event-detail-ui-fix'
STAGE_MANIFEST = STAGE_DIR / 'pending.json'

DETAIL_POPUP_WIDTH = 300
EVENT_START_DATE = '20200101'
EVENT_END_DATE = '20301231'

# Valid DFO color constants verified from 0x14504c430
VALID_COLORS = {
    'COLOR_YELLOW01',
    'COLOR_YELLOW02',
    'COLOR_WHITE',
    'COLOR_BEIGE',
    'COLOR_GREEN_LIGHT',
    'COLOR_BLUE_SKY',
    'COLOR_ORANGE_YELLOW01',
}

# Active US100 event definitions:
#   event_id -> {
#       'color_type': 1..4 (calendar badge category),
#       'title': Chinese title (shown in EventCalendar & EventDesignWindow titlebar),
#       'subtitle': Chinese short category,
#       'lines': list of (text, color_name) rendered via [text_control],
#   }
US100_EVENT_DETAILS: dict[int, dict] = {
    4183: {
        'color_type': 4,
        'title': '丹吉袖珍罐活动',
        'subtitle': '丹吉秘宝',
        'lines': [
            ('【活动简介】丹吉的神秘袖珍罐限时开启', 'COLOR_YELLOW01'),
            ('开启罐子可获得强化增幅券、稀有材料与礼盒', 'COLOR_WHITE'),
            ('累计开启达到阶段层数可领取额外保底大奖', 'COLOR_WHITE'),
            ('契约类道具开启后自动激活，无需手动使用', 'COLOR_BEIGE'),
            ('参与方式：点击右下角丹吉图标直接打开界面', 'COLOR_BLUE_SKY'),
            ('生效状态：全天候常驻开启', 'COLOR_GREEN_LIGHT'),
        ],
    },
    520: {
        'color_type': 1,
        'title': '经验加成热力活动',
        'subtitle': '经验热力',
        'lines': [
            ('【活动简介】全服战斗与通关经验倍率提升', 'COLOR_YELLOW01'),
            ('击杀怪物经验与地下城通关结算经验同步加成', 'COLOR_WHITE'),
            ('默认开启双倍经验，助力角色快速满级成长', 'COLOR_WHITE'),
            ('自定义调节：可在启动器活动面板随时调整倍率', 'COLOR_BEIGE'),
            ('调整后实时推送生效，无需重启客户端', 'COLOR_BLUE_SKY'),
            ('生效状态：当前已开启', 'COLOR_GREEN_LIGHT'),
        ],
    },
    602: {
        'color_type': 4,
        'title': '爆率与金币加成活动',
        'subtitle': '掉落金币',
        'lines': [
            ('【活动简介】地下城掉落爆率与金币收益加倍', 'COLOR_YELLOW01'),
            ('怪物装备与材料掉率提升，金币掉落同步加成', 'COLOR_WHITE'),
            ('通关翻牌金币奖励同步享受活动倍率加成', 'COLOR_WHITE'),
            ('自定义调节：可在启动器随时调整各项倍率', 'COLOR_BEIGE'),
            ('调整后即时热更新生效，适合刷图搬砖打宝', 'COLOR_BLUE_SKY'),
            ('生效状态：当前已开启', 'COLOR_GREEN_LIGHT'),
        ],
    },
    619: {
        'color_type': 1,
        'title': '燃烧疲劳活动',
        'subtitle': '疲劳加成',
        'lines': [
            ('【活动简介】燃烧疲劳与额外疲劳上限福利', 'COLOR_YELLOW01'),
            ('支持燃烧阶段加成、周末双倍疲劳与零消耗模式', 'COLOR_WHITE'),
            ('可与黑钻契约上限完美叠加，每日畅快刷图', 'COLOR_WHITE'),
            ('自定义调节：可在启动器勾选各项疲劳增益', 'COLOR_BEIGE'),
            ('切换开关后立即同步最新疲劳值上限', 'COLOR_BLUE_SKY'),
            ('生效状态：当前已开启', 'COLOR_GREEN_LIGHT'),
        ],
    },
    367: {
        'color_type': 3,
        'title': '积分商城活动',
        'subtitle': '积分商城',
        'lines': [
            ('【活动简介】通关地下城累计获取商城积分', 'COLOR_YELLOW01'),
            ('每次通关地下城自动发放积分至账号余额', 'COLOR_WHITE'),
            ('可在游戏内商城积分专区兑换各类实用道具', 'COLOR_WHITE'),
            ('配合每日挑战与周常活动可获额外不限上限积分', 'COLOR_BEIGE'),
            ('自定义调节：可在启动器调整单次积分与日上限', 'COLOR_BLUE_SKY'),
            ('生效状态：当前已开启', 'COLOR_GREEN_LIGHT'),
        ],
    },
    500: {
        'color_type': 4,
        'title': '深渊派对活动',
        'subtitle': '随机深渊',
        'lines': [
            ('【活动简介】地下城高概率触发随机深渊派对', 'COLOR_YELLOW01'),
            ('进入地下城时按配置概率自动刷出深渊房间', 'COLOR_WHITE'),
            ('击败深渊怪物与柱子可掉落史诗与神话装备', 'COLOR_WHITE'),
            ('自定义调节：可在启动器自由设定深渊触发概率', 'COLOR_BEIGE'),
            ('无需消耗邀请函，随时体验畅爽深渊刷宝', 'COLOR_BLUE_SKY'),
            ('生效状态：当前已开启', 'COLOR_GREEN_LIGHT'),
        ],
    },
    392: {
        'color_type': 2,
        'title': '每日挑战活动',
        'subtitle': '每日挑战',
        'lines': [
            ('【活动简介】完成每日击杀与通关目标领好礼', 'COLOR_YELLOW01'),
            ('达成每日击杀数或地下城通关次数自动结算', 'COLOR_WHITE'),
            ('奖励内容：大量金币、强化材料与额外商城积分', 'COLOR_WHITE'),
            ('奖励直接发放至背包并实时刷新余额', 'COLOR_BEIGE'),
            ('重置时间：每日零点自动重置挑战进度', 'COLOR_BLUE_SKY'),
            ('生效状态：当前已开启', 'COLOR_GREEN_LIGHT'),
        ],
    },
    614: {
        'color_type': 2,
        'title': '周常副本活动',
        'subtitle': '周常奖励',
        'lines': [
            ('【活动简介】每周通关地下城领取额外周常奖励', 'COLOR_YELLOW01'),
            ('每周前数次通关自动追加金币、材料与积分', 'COLOR_WHITE'),
            ('奖励自动到账背包，无需手动前往界面领取', 'COLOR_WHITE'),
            ('自定义调节：可在启动器修改每周次数与奖励', 'COLOR_BEIGE'),
            ('重置时间：每周自动刷新周常奖励计数', 'COLOR_BLUE_SKY'),
            ('生效状态：当前已开启', 'COLOR_GREEN_LIGHT'),
        ],
    },
    515: {
        'color_type': 3,
        'title': '每日签到活动',
        'subtitle': '每日签到',
        'lines': [
            ('【活动简介】每日登录即可自动领取签到补给', 'COLOR_YELLOW01'),
            ('每日奖励：100,000金币 + 20炉岩核 + 10积分', 'COLOR_WHITE'),
            ('进入城镇或开启活动时自动发放至角色背包', 'COLOR_WHITE'),
            ('同日内自动防重复发放，安全可靠', 'COLOR_BEIGE'),
            ('重置时间：每日刷新，登录即领', 'COLOR_BLUE_SKY'),
            ('生效状态：当前已开启', 'COLOR_GREEN_LIGHT'),
        ],
    },
    512: {
        'color_type': 3,
        'title': '赛丽亚的欢迎活动',
        'subtitle': '欢迎礼遇',
        'lines': [
            ('【活动简介】赛丽亚为勇士准备的专属见面礼', 'COLOR_YELLOW01'),
            ('欢迎奖励：300,000金币 + 30雷米援助 + 20积分', 'COLOR_WHITE'),
            ('角色首次触发活动时自动发放至背包与余额', 'COLOR_WHITE'),
            ('助您轻松度过前期开荒与副本挑战', 'COLOR_BEIGE'),
            ('领取条件：每个角色自动领取一次', 'COLOR_BLUE_SKY'),
            ('生效状态：当前已开启', 'COLOR_GREEN_LIGHT'),
        ],
    },
    607: {
        'color_type': 2,
        'title': '回归勇士关怀活动',
        'subtitle': '每周补给',
        'lines': [
            ('【活动简介】每周自动发放回归勇士成长物资', 'COLOR_YELLOW01'),
            ('每周奖励：500,000金币 + 50协调结晶 + 30积分', 'COLOR_WHITE'),
            ('登录游戏即可自动到账，助力装备打造提升', 'COLOR_WHITE'),
            ('每周同周期自动防重，无需繁琐操作', 'COLOR_BEIGE'),
            ('重置时间：每周自动刷新一次领取资格', 'COLOR_BLUE_SKY'),
            ('生效状态：当前已开启', 'COLOR_GREEN_LIGHT'),
        ],
    },
    525: {
        'color_type': 2,
        'title': '安全认证保护奖励',
        'subtitle': '安全保护',
        'lines': [
            ('【活动简介】账号安全保护每日专属福利回馈', 'COLOR_YELLOW01'),
            ('每日奖励：50,000金币 + 10品级调整箱 + 5积分', 'COLOR_WHITE'),
            ('登录角色自动发放，助力调整装备最上级品级', 'COLOR_WHITE'),
            ('可在启动器随时开启或关闭本项安全福利', 'COLOR_BEIGE'),
            ('重置时间：每日自动刷新', 'COLOR_BLUE_SKY'),
            ('生效状态：当前已开启', 'COLOR_GREEN_LIGHT'),
        ],
    },
    306: {
        'color_type': 4,
        'title': '网吧特权活动',
        'subtitle': '网吧特权',
        'lines': [
            ('【活动简介】激活专属网吧特权双重属性增益', 'COLOR_YELLOW01'),
            ('特权一：每日疲劳值上限额外增加 +30 点', 'COLOR_WHITE'),
            ('特权二：地下城战斗与通关经验额外 +20%', 'COLOR_WHITE'),
            ('可与双倍经验、燃烧疲劳、黑钻特权完美叠加', 'COLOR_BEIGE'),
            ('自定义调节：可在启动器一键开关实时生效', 'COLOR_BLUE_SKY'),
            ('生效状态：当前已开启', 'COLOR_GREEN_LIGHT'),
        ],
    },
}


def build_text_control_markup(lines: list[tuple[str, str]]) -> str:
    """Build exact EventDesignWindow ParseMarkup (0x1438be7a0) [text_control] string.

    Each line is wrapped in `` ```text ``` `` with strictly single-space token
    separators outside backticks, followed by literal ``\\n``-separated invisible
    spacer lines so LayoutWindowSize (0x1438c0ce0) and LayoutText1 (0x1438c0710)
    size the popup frame and inner CNBoardImage cleanly around all text rows.
    """
    if not lines:
        raise ValueError('lines must not be empty')
    parts = [f'[text_control] {len(lines)}']
    y = 14
    for text, color in lines:
        if color not in VALID_COLORS:
            raise ValueError(f'unsupported DFO color constant: {color!r}')
        if '`' in text or '\n' in text or '\r' in text:
            raise ValueError(f'forbidden backtick or newline in line: {text!r}')
        if '  ' in text:
            raise ValueError(f'double space inside line breaks tokenizer: {text!r}')
        parts.append(f'```{text} ``` 14 {y} {color}')
        y += 24
    # 6 rows * 24px + 14px top = 158px bottom -> 13 spacer lines gives
    # CNBoardImage height = (1 + int(13*6.7))*2 + 16 = 192px & popup height = 18*13 = 234px.
    spacer_count = max(len(lines) * 2 + 1, 11)
    spacers = '\\n'.join('.' for _ in range(spacer_count))
    parts.append(spacers)
    return ' '.join(parts)


def simulate_parse_markup(raw_str_value: str) -> tuple[list[tuple[str, int, int, str]], str]:
    """Exact Python mirror of DFO's EventDesignWindow::ParseMarkup (0x1438be7a0).

    Verifies that every generated detail string tokenizes and parses with zero
    off-by-one errors before merging into Script.pvf.
    """
    runtime_text = raw_str_value.replace('\\n', '\n')
    tokens: list[str] = []
    cur: list[str] = []
    for ch in runtime_text:
        if ch in (' ', '\n'):
            if cur:
                tokens.append(''.join(cur))
                cur = []
            tokens.append(ch)
        else:
            cur.append(ch)
    if cur:
        tokens.append(''.join(cur))

    parsed_controls: list[tuple[str, int, int, str]] = []
    consumed = [False] * len(tokens)
    i = 0
    n = len(tokens)
    while i < n:
        if tokens[i] == '[text_control]':
            consumed[i] = True
            i += 1
            if i >= n:
                raise ValueError('unexpected EOF after [text_control]')
            # skip 1 delimiter token
            consumed[i] = True
            i += 1
            if i >= n:
                raise ValueError('unexpected EOF reading count')
            count = int(tokens[i])
            consumed[i] = True
            for _ in range(count):
                i += 1
                if i >= n:
                    raise ValueError('unexpected EOF before delimiter')
                consumed[i] = True
                opened = False
                closed = False
                buf: list[str] = []
                while not closed:
                    i += 1
                    if i >= n:
                        raise ValueError('unterminated ``` block')
                    tok = tokens[i]
                    if '```' in tok:
                        if not opened:
                            opened = True
                        else:
                            closed = True
                    if opened:
                        buf.append(tok.replace('`', ''))
                        consumed[i] = True
                # skip delim, read x, skip delim, read y, skip delim, read color
                i += 1
                consumed[i] = True
                i += 1
                x = int(tokens[i])
                consumed[i] = True
                i += 1
                consumed[i] = True
                i += 1
                y = int(tokens[i])
                consumed[i] = True
                i += 1
                consumed[i] = True
                i += 1
                color = tokens[i]
                consumed[i] = True
                if color not in VALID_COLORS:
                    raise ValueError(f'invalid color {color!r}')
                parsed_controls.append((''.join(buf).rstrip(' '), x, y, color))
        i += 1
    remaining = ''.join(tok for tok, c in zip(tokens, consumed) if not c)
    return parsed_controls, remaining


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


def _build_entry_tokens(event_id: int, spec: dict) -> list[dict]:
    sub_key = f'<23::us100_ev_sub_{event_id}>'
    title_key = f'<23::us100_ev_title_{event_id}>'
    detail_key = f'<23::us100_ev_detail_{event_id}>'
    return [
        {'type': 3, 'text': '[entry]'},
        {'type': 3, 'text': '[event index]'},
        {'type': 0, 'value': int(event_id)},
        {'type': 3, 'text': '[event info]'},
        {'type': 0, 'value': 1},
        {'type': 0, 'value': int(spec['color_type'])},
        {'type': 8, 'reference': sub_key},
        {'type': 8, 'reference': title_key},
        {'type': 6, 'text': EVENT_START_DATE},
        {'type': 6, 'text': EVENT_END_DATE},
        {'type': 3, 'text': '[/event info]'},
        {'type': 3, 'text': '[popupwindow design]'},
        {'type': 3, 'text': '[title]'},
        {'type': 8, 'reference': title_key},
        {'type': 3, 'text': '[explain]'},
        {'type': 8, 'reference': detail_key},
        {'type': 0, 'value': DETAIL_POPUP_WIDTH},
        {'type': 3, 'text': '[/popupwindow design]'},
        {'type': 3, 'text': '[/entry]'},
    ]


def _token_key(tok: dict) -> tuple:
    tp = int(tok['type'])
    if tp == 0:
        return (0, int(tok['value']))
    if tp == 2:
        return (2, float(tok.get('number', 0.0)))
    if tp in (3, 6):
        return (tp, str(tok.get('text', '')))
    if tp == 8:
        return (8, str(tok.get('reference', '')))
    return (tp, str(tok))


def patch_eventlistwindow_tokens(tokens: list[dict]) -> tuple[list[dict], dict]:
    """Idempotently upsert all 13 US100 event entries into event/eventlistwindow.evt."""
    preserved: list[dict] = []
    existing_us100: dict[int, list[dict]] = {}
    stats = {
        'preserved_entries': 0,
        'upserted_events': sorted(US100_EVENT_DETAILS.keys()),
        'changed': False,
    }
    i = 0
    n = len(tokens)
    while i < n:
        tok = tokens[i]
        if tok.get('type') == 3 and tok.get('text') == '[entry]':
            end = i + 1
            while end < n and not (tokens[end].get('type') == 3 and tokens[end].get('text') == '[/entry]'):
                end += 1
            if end >= n:
                raise ValueError(f'unterminated [entry] block at token {i}')
            block = tokens[i:end + 1]
            ev_id = None
            for k in range(len(block) - 1):
                if block[k].get('type') == 3 and block[k].get('text') == '[event index]' and block[k + 1].get('type') == 0:
                    ev_id = int(block[k + 1]['value'])
                    break
            if ev_id in US100_EVENT_DETAILS:
                existing_us100[ev_id] = block
            else:
                preserved.extend(block)
                stats['preserved_entries'] += 1
            i = end + 1
            continue
        preserved.append(tok)
        i += 1

    out = list(preserved)
    for ev_id in sorted(US100_EVENT_DETAILS.keys()):
        wanted = _build_entry_tokens(ev_id, US100_EVENT_DETAILS[ev_id])
        prev = existing_us100.get(ev_id)
        if prev is None or [_token_key(t) for t in prev] != [_token_key(t) for t in wanted]:
            stats['changed'] = True
        out.extend(wanted)
    return out, stats


def render_evt_script(tokens: list[dict]) -> str:
    parts: list[str] = []
    for tok in tokens:
        s = _format_token(tok)
        if tok['type'] == 3:
            parts.append('\n' + s + '\n')
        else:
            parts.append(s + ' ')
    return ''.join(parts).lstrip('\n') + '\n'


def build_event_str_map() -> dict[str, str]:
    kv: dict[str, str] = {}
    for ev_id, spec in sorted(US100_EVENT_DETAILS.items()):
        detail = build_text_control_markup(spec['lines'])
        parsed, rem = simulate_parse_markup(detail)
        if len(parsed) != len(spec['lines']):
            raise RuntimeError(f'event {ev_id} ParseMarkup mismatch: {len(parsed)} != {len(spec["lines"])}')
        if rem.count('\n') < len(spec['lines']) * 2:
            raise RuntimeError(f'event {ev_id} spacer newlines too small: {rem!r}')
        kv[f'us100_ev_sub_{ev_id}'] = spec['subtitle']
        kv[f'us100_ev_title_{ev_id}'] = spec['title']
        kv[f'us100_ev_detail_{ev_id}'] = detail
    return kv


def render_event_str_overlay() -> str:
    kv = build_event_str_map()
    lines = [f'{k}>{v}' for k, v in kv.items()]
    return '\r\n'.join(lines) + '\r\n'


def patch_eventdesignwindow_xui_text(text: str) -> tuple[str, dict]:
    """Hide stray Nexon placeholder ID="STR:bg" (PVP_Fair.img[22] face) safely off-screen.

    Preserves `ID="STR:bg"` so `EventDesignWindow::Init` (0x1438bcff8) still finds
    all 5 required controls (`0`, `1`, `bg`, `4`, `5`).
    """
    replaced = 0

    def _repl(match: re.Match) -> str:
        nonlocal replaced
        attrs = match.group(1)
        if 'ID="STR:bg"' not in attrs:
            return match.group(0)
        patched = attrs
        patched = re.sub(r'ImagePack="STR:[^"]*"', 'ImagePack="STR:"', patched)
        patched = re.sub(r'Pos="VECTOR:[^"]*"', 'Pos="VECTOR:-2000, -2000"', patched)
        patched = re.sub(r'Size="VECTOR:[^"]*"', 'Size="VECTOR:0, 0"', patched)
        patched = re.sub(r'imageIndex="NUM:-?\d+"', 'imageIndex="NUM:-1"', patched)
        if 'IsShow=' in patched:
            patched = re.sub(r'IsShow="BOOL:(?:true|false)"', 'IsShow="BOOL:false"', patched)
        else:
            patched += ' IsShow="BOOL:false"'
        if 'IsVisible=' in patched:
            patched = re.sub(r'IsVisible="BOOL:(?:true|false)"', 'IsVisible="BOOL:false"', patched)
        else:
            patched += ' IsVisible="BOOL:false"'
        new_tag = f'<CNUIControlImage {patched.strip()} />'
        if new_tag != match.group(0):
            replaced += 1
        return new_tag

    patched_text = re.sub(r'<CNUIControlImage\s+([^>]+?)\s*/>', _repl, text)
    if 'PVP_Fair.img' in patched_text:
        raise ValueError('residual PVP_Fair.img placeholder remained in eventdesignwindow.xui')
    if 'ID="STR:bg"' not in patched_text:
        raise ValueError('ID="STR:bg" missing after patch')
    ET.fromstring(patched_text.rstrip('\x00').lstrip('\ufeff'))
    return patched_text, {'bg_patched': replaced}


def patch_eventdesignwindow_xui_bytes(raw: bytes) -> tuple[bytes, dict]:
    text = raw.decode('utf-16-le')
    patched, stats = patch_eventdesignwindow_xui_text(text)
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
            '-tokens',
            '-output', str(out_dir),
        ],
        cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
        creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=180,
    )
    if res.returncode != 0:
        raise RuntimeError('pvfinspect 提取失败：' + (res.stderr or res.stdout or '')[-600:])
    evt_tokens = out_dir / f'00-{Path(EVT_ENTRY).name}.tokens.json'
    str_bin = out_dir / f'01-{Path(STR_ENTRY).name}.bin'
    xui_bin = out_dir / f'02-{Path(XUI_ENTRY).name}.bin'
    if not evt_tokens.is_file() or not str_bin.is_file() or not xui_bin.is_file():
        raise FileNotFoundError(f'未在 PVF 中找到目标文件：{TARGET_ENTRIES}')
    return {
        'evt_tokens': evt_tokens,
        'str_bin': str_bin,
        'xui_bin': xui_bin,
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
        raise RuntimeError('暂存的活动详情界面修复 PVF 校验不匹配')
    core.file_copy(staged, live)
    if core.digest(live) != expected_after:
        raise RuntimeError('安装活动详情界面修复 PVF 后校验不匹配')
    staged.unlink(missing_ok=True)
    STAGE_MANIFEST.unlink(missing_ok=True)
    return '已自动应用活动详情界面修复补丁'


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

    wanted_str_kv = build_event_str_map()
    STAGE_DIR.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=STAGE_DIR, prefix='.work-') as tmpdir:
        tmp = Path(tmpdir)
        ext_dir = tmp / 'extracted'
        ext_dir.mkdir()
        extracted = _extract_entries(inner_source, ext_dir)

        orig_evt_tokens = json.loads(extracted['evt_tokens'].read_text(encoding='utf-8'))
        orig_str_raw = extracted['str_bin'].read_bytes()
        orig_str_kv = _parse_str_table_utf16(orig_str_raw)
        orig_xui_raw = extracted['xui_bin'].read_bytes()

        patched_evt_tokens, evt_stats = patch_eventlistwindow_tokens(orig_evt_tokens)
        patched_xui_bytes, xui_stats = patch_eventdesignwindow_xui_bytes(orig_xui_raw)
        str_needs_update = (
            not orig_str_raw.endswith(b'\x00\x00\x00\x00')
            or '\x00' in orig_str_raw[:-4].decode('utf-16-le')
            or any(orig_str_kv.get(k) != v for k, v in wanted_str_kv.items())
        )
        xui_needs_update = (
            xui_stats['bg_patched'] > 0
            or not orig_xui_raw.endswith(b'\x00\x00')
            or '\x00' in orig_xui_raw[:-2].decode('utf-16-le')
        )

        if not evt_stats['changed'] and not str_needs_update and not xui_needs_update and inner_override is None:
            return {'status': 'already_fixed', 'sha256': before_sha, 'evt_stats': evt_stats, 'xui_stats': xui_stats}

        evt_src = tmp / 'eventlistwindow.evt'
        evt_src.write_text(render_evt_script(patched_evt_tokens), encoding='utf-8')

        str_src = tmp / 'event.uv.str'
        str_src.write_text(render_event_str_overlay(), encoding='utf-8')

        xui_src = tmp / 'eventdesignwindow.xui'
        xui_src.write_bytes(patched_xui_bytes)

        layers = [
            {'path': EVT_ENTRY, 'source': str(evt_src), 'mod_id': 'event-detail-ui-fix', 'priority': 0},
            {'path': STR_ENTRY, 'source': str(str_src), 'mod_id': 'event-detail-ui-fix', 'priority': 1},
            {'path': XUI_ENTRY, 'source': str(xui_src), 'mod_id': 'event-detail-ui-fix', 'priority': 2},
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

        # Strict post-merge read-back verification across all 3 entries.
        verify_dir = tmp / 'verify'
        verify_dir.mkdir()
        verified = _extract_entries(merged_inner, verify_dir)
        v_evt_tokens = json.loads(verified['evt_tokens'].read_text(encoding='utf-8'))
        v_str_raw = verified['str_bin'].read_bytes()
        if not v_str_raw.endswith(b'\x00\x00\x00\x00') or '\x00' in v_str_raw[:-4].decode('utf-16-le'):
            raise RuntimeError(f'合并后 {STR_ENTRY} UTF-16LE NUL 终止符异常（缺少末尾 NUL 或存在内部 NUL）')
        v_str_kv = _parse_str_table_utf16(v_str_raw)
        v_xui_raw = verified['xui_bin'].read_bytes()
        if not v_xui_raw.endswith(b'\x00\x00') or '\x00' in v_xui_raw[:-2].decode('utf-16-le'):
            raise RuntimeError(f'合并后 {XUI_ENTRY} UTF-16LE NUL 终止符异常（缺少末尾 NUL 或存在内部 NUL）')
        v_xui_text = v_xui_raw.decode('utf-16-le')

        if len(v_evt_tokens) != len(patched_evt_tokens):
            raise RuntimeError(f'合并后 {EVT_ENTRY} token 数不匹配：{len(v_evt_tokens)} != {len(patched_evt_tokens)}')
        _, recheck_evt = patch_eventlistwindow_tokens(v_evt_tokens)
        if recheck_evt['changed']:
            raise RuntimeError(f'合并后 {EVT_ENTRY} 回读校验失败：{recheck_evt}')

        for k, v in wanted_str_kv.items():
            if v_str_kv.get(k) != v:
                raise RuntimeError(f'合并后 {STR_ENTRY} 缺少或错写 {k}: {v_str_kv.get(k)!r}')
            if k.startswith('us100_ev_detail_'):
                parsed, _ = simulate_parse_markup(v_str_kv[k])
                if not parsed:
                    raise RuntimeError(f'合并后 {k} ParseMarkup 解析为空')

        if 'PVP_Fair.img' in v_xui_text or 'ID="STR:bg"' not in v_xui_text:
            raise RuntimeError(f'合并后 {XUI_ENTRY} 回读校验失败')
        ET.fromstring(v_xui_text.rstrip('\x00').lstrip('\ufeff'))

        stats = {
            'preserved_entries': evt_stats['preserved_entries'],
            'upserted_events': len(US100_EVENT_DETAILS),
            'verified_evt_tokens': len(v_evt_tokens),
            'verified_str_keys': len(v_str_kv),
            'xui_bg_patched': xui_stats['bg_patched'],
        }

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
        'status': 'staged',
        'before_sha256': before_sha,
        'after_sha256': after_sha,
        'merge_report': merge_report,
        'stats': stats,
        'reason': 'DFO.exe 运行中，已暂存，下次启动客户端自动替换',
    }


def main() -> int:
    parser = argparse.ArgumentParser(description='修复 US100 活动详情界面并汉化活动日历与详情内容')
    parser.add_argument('--stage-only', action='store_true', help='仅构建并暂存，不立即覆盖客户端 Script.pvf')
    parser.add_argument('--apply-staged', action='store_true', help='仅在客户端未运行时应用已暂存补丁')
    parser.add_argument('--inner', type=str, default='', help='指定解包后的 inner PVF 路径')
    args = parser.parse_args()
    if args.apply_staged:
        msg = apply_staged_if_pending()
        print(msg or '无待应用的活动详情界面修复补丁')
        return 0
    result = build_and_apply(
        stage_only=args.stage_only,
        inner_override=Path(args.inner) if args.inner else None,
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
