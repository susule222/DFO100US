# -*- coding: utf-8 -*-
r"""Complete Chinese localization for Event 500 (灵魂之铁砧 / ElfAmplify) UI.

Fixes all three client layers cleanly and reversibly:
  1. ImagePacks2/sprite_live_event_kor_2022_0707_elfamplify.NPK:
     Replaces all baked-in Korean/English UI artwork across 10 IMG v2 frames
     (main forge window banner/headers/stamps/footer, calendar popup banner/
     date/speech bubble, extract popup banner/headers, Complete badges, 4-state
     go-to button, and success/fail banners) with matching Chinese typography.
  2. Script.pvf:
     - Replaces `string="STR:Static Text"` on `base_item_name` in
       `ui/event/20200709_forgeofsouls/upgrade_and_spec.xui`.
     - Replaces machine-translated `branch_of_vandrassil_*` keys in
       `string/ui.uv.str`, fills blank `ElfAmplify_mail_*` keys in
       `string/event.uv.str`, and fixes `50060799`/`50060816`/`50060820` item
       names in `string/stackable.uv.str`.
  3. dstr.dat:
     Updates the 8 Event 500 runtime format strings (66333, 66336, 66337,
     100088591..100088593, 100088822, 100088870) from awkward machine-translated
     "分支增幅/世界树枝" wording to clean "灵魂之石强化" Chinese.
"""
from __future__ import annotations

import argparse
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
import xml.etree.ElementTree as ET
import zlib

from PIL import Image, ImageDraw, ImageFilter, ImageFont

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path[:0] = [str(HERE / 'atelier'), str(HERE)]

import core  # noqa: E402
import live_resources as lr  # noqa: E402
from mod_workshop import _ensure_merger_binary, rewrap_inner_to_client_pvf  # noqa: E402
import us100_dstr_ui as dstr_ui  # noqa: E402

NPK_NAME = 'sprite_live_event_kor_2022_0707_elfamplify.NPK'
NPK_KEY = (b'puchikon@neople dungeon and fighter ' + b'DNF' * 73)[:255] + b'\x00'

MAIN_XUI = 'ui/event/20200709_forgeofsouls/upgrade_and_spec.xui'
UI_STR_ENTRY = 'string/ui.uv.str'
EV_STR_ENTRY = 'string/event.uv.str'
STK_STR_ENTRY = 'string/stackable.uv.str'
PVF_TARGETS = (MAIN_XUI, UI_STR_ENTRY, EV_STR_ENTRY, STK_STR_ENTRY)

BACKUP_DIR = ROOT / 'runtime' / 'ev500-ui-l10n-backup'
PREVIEW_DIR = ROOT / 'runtime' / 'ev500-ui-l10n-preview'

UI_PATCHES: dict[str, str] = {
    'branch_of_vandrassil_title': '灵魂之铁砧',
    'branch_of_vandrassil_00': '灵魂之铁砧',
    'branch_of_vandrassil_01': '强化失败时，灵魂之石的强化等级可能会下降。',
    'branch_of_vandrassil_02': '<第一颗灵魂之石>\\n- 活动期间默认解锁。',
    'branch_of_vandrassil_03': '<第二颗灵魂之石>\\n- 将第一颗灵魂之石强化至 +10 后自动解锁。',
    'branch_of_vandrassil_04': '<第三颗灵魂之石>\\n- 将第二颗灵魂之石强化至 +10 后自动解锁。',
    'branch_of_vandrassil_05': '选择灵魂之石进行强化',
    'branch_of_vandrassil_06': '（仅适用于 +9 及以下灵魂之石）',
    'branch_of_vandrassil_07': '+10 灵魂武器强化券',
    'branch_of_vandrassil_14': '强化 (空格键)',
    'branch_of_vandrassil_15': '累计强化次数 : %d/%d',
    'branch_of_vandrassil_16': '领取',
    'branch_of_vandrassil_17': '每日可领1次',
    'branch_of_vandrassil_18': '每个账号限领1次',
    'branch_of_vandrassil_19': '提取',
    'branch_of_vandrassil_20': '该灵魂之石已完成提取。',
    'branch_of_vandrassil_21': (
        '提取灵魂之石的能量。\\n可获得与当前强化等级对应的武器强化券。\\n\\n'
        '提取完成后，该灵魂之石将无法继续强化，\\n请谨慎确认！\\n\\n如需提取灵魂之石，请在下方输入\\n'
    ),
    'branch_of_vandrassil_22': '开始提取',
    'branch_of_vandrassil_23': '，并点击确认按钮。',
    'branch_of_vandrassil_extract_tooltip': '无法提取。',
    'branch_of_vandrassil_main_tooltip': (
        '* 收集灵魂之石，在灵魂之铁砧上进行强化！\\n'
        '  - 可通过每日领取凯丽的灵魂强化礼盒或通关地下城获取灵魂之石。\\n'
        '  - 强化等级越高，所需消耗的灵魂之石越多。\\n'
        '  - 最高可强化至 +15；达到指定强化等级后即可提取武器强化券与锻造券。\\n\\n'
        '* 解锁更多灵魂之石！\\n'
        '  - 最多可解锁并强化 3 颗灵魂之石。\\n'
        '  - 将当前灵魂之石强化至 +10 后，将自动解锁下一颗灵魂之石。'
    ),
    'branch_of_vandrassil_sub_tooltip': (
        '* 强化灵魂之石，完成累计账号任务。\\n'
        '  - 每次尝试强化灵魂之石，累计强化次数都会增加。\\n'
        '  - 累计强化次数与任务进度按账号共享保存。'
    ),
}

EVENT_PATCHES: dict[str, str] = {
    'ElfAmplify_mail_title': '灵魂之铁砧活动奖励',
    'ElfAmplify_mail_mag_1': '感谢参与灵魂之铁砧活动，您的活动奖励已发放。',
    'ElfAmplify_mail_mag_2': '每日凯丽的灵魂强化礼盒已发放，祝您强化大成功！',
    'ElfAmplify_mail_mag_3': '恭喜达成累计强化次数任务，阶段奖励已发放！',
    'ElfAmplify_mail_mag_4': '灵魂之石提取成功，对应的武器强化券与锻造券已发放！',
    'ElfAmplify_mail_mag_5': '灵魂之铁砧活动奖励已送达，请注意查收。',
    'event_4189_mail_msg': '灵魂之铁砧活动奖励返还已完成。',
}

STACKABLE_PATCHES: dict[str, str] = {
    'name_50060799': '灵魂之石',
    'name_50060816': '凯丽的灵魂强化礼盒',
    'name_50060820': '[活动] 时间引导石礼盒 (500个)',
}

DSTR_PATCHES: dict[str, str] = {
    '66333': '无法与灵魂之铁砧同时使用。',
    '66336': ' %s已成功将灵魂之石强化至 +%d。',
    '66337': ' %s未能将灵魂之石强化至 +%d。',
    '100088591': '灵魂之石强化成功率为 ???\\n强化失败时，强化等级将会降低。',
    '100088592': '灵魂之石强化成功率为 %.0f%%。\\n强化失败时，强化等级将会降低。',
    '100088593': '灵魂之石强化成功率为 %.0f%%。',
    '100088822': '仅可提取 +1 及以上等级的灵魂之石。',
    '100088870': '无法继续使用已提取能量的灵魂之石。',
}


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _load_font(size: int, heavy: bool = False) -> ImageFont.FreeTypeFont:
    fonts_dir = Path(os.environ.get('WINDIR', r'C:\Windows')) / 'Fonts'
    cands = (
        [
            fonts_dir / 'Source Han Serif SC Heavy (TrueType).ttf',
            fonts_dir / 'Noto Sans SC Bold (TrueType).otf',
            fonts_dir / 'msyhbd.ttc',
            fonts_dir / 'simhei.ttf',
        ]
        if heavy
        else [
            fonts_dir / 'Noto Sans SC Bold (TrueType).otf',
            fonts_dir / 'msyhbd.ttc',
            fonts_dir / 'msyh.ttc',
            fonts_dir / 'simhei.ttf',
        ]
    )
    for p in cands:
        if p.exists():
            return ImageFont.truetype(str(p), size)
    return ImageFont.load_default()


def _fill_horizontal_from_edges(im: Image.Image, box: tuple[int, int, int, int], pad: int = 3) -> None:
    """Cleanly erase text on horizontal-banded UI chrome by interpolating from left/right samples."""
    x0, y0, x1, y1 = box
    w, h = im.size
    px = im.load()
    lx = max(0, x0 - pad)
    rx = min(w - 1, x1 + pad)
    span = max(1, x1 - x0)
    for y in range(max(0, y0), min(h, y1)):
        c_l = px[lx, y]
        c_r = px[rx, y]
        for x in range(max(0, x0), min(w, x1)):
            t = (x - x0) / span
            px[x, y] = (
                int(c_l[0] * (1 - t) + c_r[0] * t),
                int(c_l[1] * (1 - t) + c_r[1] * t),
                int(c_l[2] * (1 - t) + c_r[2] * t),
                int(c_l[3] * (1 - t) + c_r[3] * t),
            )


def _fill_vertical_from_edges(im: Image.Image, box: tuple[int, int, int, int], pad: int = 2) -> None:
    """Cleanly erase text on top/bottom-symmetric gradients (e.g. banners/buttons)."""
    x0, y0, x1, y1 = box
    w, h = im.size
    px = im.load()
    ty = max(0, y0 - pad)
    by = min(h - 1, y1 + pad)
    span = max(1, y1 - y0)
    for x in range(max(0, x0), min(w, x1)):
        c_t = px[x, ty]
        c_b = px[x, by]
        for y in range(max(0, y0), min(h, y1)):
            t = (y - y0) / span
            px[x, y] = (
                int(c_t[0] * (1 - t) + c_b[0] * t),
                int(c_t[1] * (1 - t) + c_b[1] * t),
                int(c_t[2] * (1 - t) + c_b[2] * t),
                int(c_t[3] * (1 - t) + c_b[3] * t),
            )


def _draw_centered_text(
    im: Image.Image,
    box: tuple[int, int, int, int],
    text: str,
    font: ImageFont.FreeTypeFont,
    fill: tuple[int, int, int, int],
    stroke_width: int = 0,
    stroke_fill: tuple[int, int, int, int] = (0, 0, 0, 255),
    shadow: tuple[int, int, tuple[int, int, int, int]] | None = None,
) -> None:
    draw = ImageDraw.Draw(im)
    x0, y0, x1, y1 = box
    bbox = draw.textbbox((0, 0), text, font=font, stroke_width=stroke_width)
    tw, th = bbox[2] - bbox[0], bbox[3] - bbox[1]
    tx = x0 + (x1 - x0 - tw) // 2 - bbox[0]
    ty = y0 + (y1 - y0 - th) // 2 - bbox[1]
    if shadow is not None:
        sx, sy, sc = shadow
        draw.text((tx + sx, ty + sy), text, font=font, fill=sc, stroke_width=stroke_width, stroke_fill=sc)
    draw.text((tx, ty), text, font=font, fill=fill, stroke_width=stroke_width, stroke_fill=stroke_fill)


def _draw_gradient_title(
    im: Image.Image,
    box: tuple[int, int, int, int],
    text: str,
    font: ImageFont.FreeTypeFont,
    top_rgb: tuple[int, int, int],
    bot_rgb: tuple[int, int, int],
    outline_px: int = 2,
    glow_rgba: tuple[int, int, int, int] | None = (200, 245, 180, 140),
) -> None:
    """Render DNF-style metallic gradient title text with soft outer glow and crisp dark outline."""
    x0, y0, x1, y1 = box
    w, h = x1 - x0, y1 - y0
    tmp = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    td = ImageDraw.Draw(tmp)
    bbox = td.textbbox((0, 0), text, font=font, stroke_width=outline_px)
    tw, th = bbox[2] - bbox[0], bbox[3] - bbox[1]
    tx = (w - tw) // 2 - bbox[0]
    ty = (h - th) // 2 - bbox[1]

    if glow_rgba is not None:
        glow = Image.new('RGBA', (w, h), (0, 0, 0, 0))
        gd = ImageDraw.Draw(glow)
        gd.text((tx, ty), text, font=font, fill=glow_rgba, stroke_width=outline_px + 2, stroke_fill=glow_rgba)
        glow = glow.filter(ImageFilter.GaussianBlur(radius=3))
        im.alpha_composite(glow, (x0, y0))

    # Crisp outline + drop shadow
    outline_layer = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    od = ImageDraw.Draw(outline_layer)
    od.text((tx, ty + 1), text, font=font, fill=(6, 12, 10, 235), stroke_width=outline_px + 1, stroke_fill=(6, 12, 10, 235))
    od.text((tx, ty), text, font=font, fill=(10, 18, 15, 255), stroke_width=outline_px, stroke_fill=(10, 18, 15, 255))
    im.alpha_composite(outline_layer, (x0, y0))

    # Gradient fill masked by text glyphs
    mask = Image.new('L', (w, h), 0)
    md = ImageDraw.Draw(mask)
    md.text((tx, ty), text, font=font, fill=255)
    grad = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    gp = grad.load()
    y_start = max(0, ty + bbox[1])
    y_end = min(h - 1, ty + bbox[3])
    span = max(1, y_end - y_start)
    for y in range(y_start, y_end + 1):
        t = (y - y_start) / span
        r = int(top_rgb[0] * (1 - t) + bot_rgb[0] * t)
        g = int(top_rgb[1] * (1 - t) + bot_rgb[1] * t)
        b = int(top_rgb[2] * (1 - t) + bot_rgb[2] * t)
        for x in range(w):
            gp[x, y] = (r, g, b, 255)
    grad.putalpha(mask)
    im.alpha_composite(grad, (x0, y0))


def _paint_emerald_banner_bg(im: Image.Image, box: tuple[int, int, int, int], feather: int = 18) -> None:
    """Paint a smooth emerald-teal atmospheric gradient feathered cleanly into the existing banner edges."""
    x0, y0, x1, y1 = box
    w, h = x1 - x0, y1 - y0
    patch = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    px = patch.load()
    for y in range(h):
        ty = y / max(1, h - 1)
        # Top deep forest -> middle emerald -> lower warm teal
        r_base = int(8 + 26 * ty)
        g_base = int(38 + 48 * ty)
        b_base = int(34 + 34 * ty)
        for x in range(w):
            # Radial warm-emerald glow toward center
            dx = (x - w * 0.5) / max(1.0, w * 0.5)
            dy = (y - h * 0.58) / max(1.0, h * 0.6)
            glow = max(0.0, 1.0 - (dx * dx * 0.85 + dy * dy))
            r = min(255, int(r_base + 54 * glow))
            g = min(255, int(g_base + 72 * glow))
            b = min(255, int(b_base + 34 * glow))
            # Soft horizontal feathering at left/right edges so wings blend seamlessly
            edge = min(x, w - 1 - x)
            a = 255 if edge >= feather else int(255 * (edge / max(1, feather)))
            px[x, y] = (r, g, b, a)
    im.alpha_composite(patch, (x0, y0))


def _patch_elfampify_main_bg(im: Image.Image) -> Image.Image:
    """Patch 0618_elfampify.img[0] (380x507): banner, headers, 6x done stamps, footer."""
    out = im.copy().convert('RGBA')

    # 1. Top banner (preserve left/right wings; full-opaque cover over old glyphs)
    _paint_emerald_banner_bg(out, (50, 8, 330, 56), feather=1)
    _draw_gradient_title(
        out, (52, 8, 328, 55), '强化！灵魂之铁砧',
        _load_font(27, heavy=True),
        top_rgb=(255, 252, 220), bot_rgb=(172, 238, 198),
        outline_px=2, glow_rgba=(215, 250, 175, 130),
    )

    # 2. Section header 1: 필요 재료 -> 所需材料 (bar spans y=254..277)
    _fill_horizontal_from_edges(out, (130, 257, 250, 275), pad=6)
    _draw_centered_text(
        out, (120, 255, 260, 276), '所需材料',
        _load_font(13, heavy=False),
        fill=(224, 196, 132, 255), stroke_width=1, stroke_fill=(14, 12, 10, 255),
    )

    # 3. Section header 2: 누적 계정 미션 -> 累计账号任务 (bar spans y=341..362)
    _fill_horizontal_from_edges(out, (115, 343, 265, 360), pad=6)
    _draw_centered_text(
        out, (110, 341, 270, 362), '累计账号任务',
        _load_font(13, heavy=False),
        fill=(224, 196, 132, 255), stroke_width=1, stroke_fill=(14, 12, 10, 255),
    )

    # 4. 6 completed card labels: 완료 -> 已完成 (y=428..446 inside the 6 mission cards)
    f_done = _load_font(12, heavy=False)
    for idx in range(6):
        cx = 40 + idx * 60
        _fill_horizontal_from_edges(out, (cx - 20, 428, cx + 20, 446), pad=2)
        _draw_centered_text(out, (cx - 24, 427, cx + 24, 447), '已完成', f_done, fill=(104, 106, 108, 255))

    # 5. Bottom footer: preserve envelope icon at x=82..98, paint solid dark footer & clean Chinese text
    ImageDraw.Draw(out).rectangle((100, 483, 330, 500), fill=(6, 7, 7, 255))
    _draw_centered_text(
        out, (100, 482, 314, 501), '账号任务奖励将直接发放至角色背包。',
        _load_font(11, heavy=False),
        fill=(146, 148, 150, 255),
    )
    return out


def _patch_elfampify_cal_0(im: Image.Image) -> Image.Image:
    """Patch 0618_elfampify_cal.img[0] (287x370): calendar popup banner, date, speech bubble."""
    out = im.copy().convert('RGBA')

    # 1. Top banner (preserve wings at x<22 and x>265)
    _paint_emerald_banner_bg(out, (22, 3, 265, 39), feather=1)
    _draw_gradient_title(
        out, (22, 3, 265, 39), '强化！灵魂之铁砧',
        _load_font(22, heavy=True),
        top_rgb=(255, 252, 220), bot_rgb=(172, 238, 198),
        outline_px=2, glow_rgba=(215, 250, 175, 120),
    )

    # 2. Date ribbon (y=39..54)
    _fill_horizontal_from_edges(out, (28, 39, 259, 54), pad=4)
    _draw_centered_text(
        out, (24, 38, 263, 55), '活动时间：长期开放',
        _load_font(11, heavy=False),
        fill=(145, 232, 188, 255), stroke_width=1, stroke_fill=(12, 32, 26, 220),
    )

    # 3. Speech bubble interior (x=14..157, y=75..165) — warm parchment fill
    draw = ImageDraw.Draw(out)
    draw.rectangle((14, 75, 157, 165), fill=(217, 209, 193, 255))
    f_bub = _load_font(11, heavy=False)
    lines = [
        ('灵魂之石中蕴含着', (38, 32, 26, 255)),
        ('强化装备的神秘力量！', (178, 38, 32, 255)),
        ('将灵魂之石强化后，', (38, 32, 26, 255)),
        ('随时可提取为对应等级的', (178, 38, 32, 255)),
        ('武器强化券与锻造券哦！', (38, 32, 26, 255)),
    ]
    y = 80
    for text, color in lines:
        draw.text((19, y), text, font=f_bub, fill=color)
        y += 16
    return out


def _patch_elfampify_cal_1(im: Image.Image) -> Image.Image:
    """Patch 0618_elfampify_cal.img[1] (284x299): extract popup banner, subtitle, date, 3 headers."""
    out = im.copy().convert('RGBA')

    # 1. Top banner (preserve wings at x<21 and x>263)
    _paint_emerald_banner_bg(out, (21, 1, 263, 36), feather=1)
    _draw_gradient_title(
        out, (20, 1, 264, 36), '强化！灵魂之铁砧',
        _load_font(21, heavy=True),
        top_rgb=(255, 252, 220), bot_rgb=(172, 238, 198),
        outline_px=2, glow_rgba=(215, 250, 175, 120),
    )

    # 2. Sub-banner: 세계수의 기운 추출 -> 灵魂之石提取 (keep neon brackets at x~75 and x~209)
    _paint_emerald_banner_bg(out, (84, 36, 200, 54), feather=3)
    _draw_centered_text(
        out, (82, 35, 202, 54), '灵魂之石提取',
        _load_font(13, heavy=False),
        fill=(242, 252, 246, 255), stroke_width=1, stroke_fill=(12, 36, 28, 240),
    )

    # 3. Date bar (y=55..70)
    _fill_horizontal_from_edges(out, (26, 55, 258, 70), pad=4)
    _draw_centered_text(
        out, (22, 54, 262, 71), '活动时间：长期开放',
        _load_font(11, heavy=False),
        fill=(145, 232, 188, 255), stroke_width=1, stroke_fill=(12, 32, 26, 220),
    )

    # 4. Three extraction row headers
    f_row = _load_font(12, heavy=False)
    rows = [
        ((34, 80, 250, 99), '第一颗灵魂之石提取', (198, 176, 118, 255)),
        ((34, 152, 250, 171), '第二颗灵魂之石提取', (130, 178, 194, 255)),
        ((34, 224, 250, 243), '第三颗灵魂之石提取', (196, 112, 112, 255)),
    ]
    for box, label, color in rows:
        _fill_horizontal_from_edges(out, box, pad=4)
        _draw_centered_text(out, box, label, f_row, fill=color, stroke_width=1, stroke_fill=(8, 8, 8, 255))
    return out


def _patch_complete_badge(im: Image.Image, text: str, font_size: int) -> Image.Image:
    """Replace English Complete! badge with matching golden glowing Chinese text."""
    w, h = im.size
    if w < 120:
        # Transparent badge (0618_elfampify_cal.img[2], 79x30)
        out = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    else:
        # Framed dark badge (0618_elfampify_cal.img[3], 262x43) — preserve border, clear interior
        out = im.copy().convert('RGBA')
        draw = ImageDraw.Draw(out)
        draw.rectangle((4, 4, w - 5, h - 5), fill=(36, 36, 36, 255))
    _draw_gradient_title(
        out, (2, 1, w - 2, h - 1), text,
        _load_font(font_size, heavy=False),
        top_rgb=(255, 238, 128), bot_rgb=(245, 196, 78),
        outline_px=1, glow_rgba=(245, 148, 72, 165),
    )
    return out


def _patch_goto_button(im: Image.Image, disabled: bool = False) -> Image.Image:
    """Replace Korean button label (210x23) while preserving exact button chrome & chevron."""
    out = im.copy().convert('RGBA')
    # Sample inner button vertical gradient cleanly across x=22..188, y=3..20
    _fill_horizontal_from_edges(out, (22, 3, 188, 20), pad=6)
    fill = (160, 160, 160, 255) if disabled else (238, 224, 174, 255)
    _draw_centered_text(
        out, (12, 1, 198, 22), '前往凯丽的强化铁砧 >',
        _load_font(12, heavy=False),
        fill=fill, stroke_width=1, stroke_fill=(12, 34, 26, 255) if not disabled else (22, 22, 22, 255),
    )
    return out


def _patch_result_banner(im: Image.Image, text: str, top_rgb: tuple[int, int, int], bot_rgb: tuple[int, int, int]) -> Image.Image:
    """Replace Korean success/fail popup text banner (167x20) cleanly on transparent canvas."""
    w, h = im.size
    out = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    _draw_gradient_title(
        out, (0, 0, w, h), text,
        _load_font(16, heavy=True),
        top_rgb=top_rgb, bot_rgb=bot_rgb,
        outline_px=2, glow_rgba=None,
    )
    return out


def _decode_img_v2_frames(buf: bytes) -> tuple[int, int, list[dict]]:
    if not buf.startswith(b'Neople Img File\x00'):
        raise ValueError('unsupported IMG header')
    idx_sz, keep_zero, ver, fcnt = struct.unpack_from('<IIII', buf, 16)
    if ver != 2:
        raise ValueError(f'expected IMG v2, got v{ver}')
    pos = 32
    frames: list[dict] = []
    for _ in range(fcnt):
        fmt = struct.unpack_from('<I', buf, pos)[0]
        pos += 4
        if fmt == 0x11:
            link = struct.unpack_from('<I', buf, pos)[0]
            pos += 4
            frames.append({'fmt': 0x11, 'link': link})
        else:
            comp, w, h, sz, x, y, mw, mh = struct.unpack_from('<IIIIIIII', buf, pos)
            pos += 32
            frames.append({
                'fmt': fmt, 'comp': comp, 'w': w, 'h': h, 'sz': sz,
                'x': x, 'y': y, 'mw': mw, 'mh': mh,
            })
    data_pos = 32 + idx_sz
    for f in frames:
        if f['fmt'] == 0x11:
            continue
        f['data'] = buf[data_pos:data_pos + f['sz']]
        data_pos += f['sz']
    return keep_zero, ver, frames


def _frame_to_pil(f: dict) -> Image.Image:
    raw = zlib.decompress(f['data']) if f['comp'] == 6 else f['data']
    w, h, fmt = f['w'], f['h'], f['fmt']
    if fmt == 0x10:
        return Image.frombytes('RGBA', (w, h), raw, 'raw', 'BGRA')
    raise ValueError(f'expected ARGB8888 (0x10) frame, got {fmt:#x}')


def _pil_to_frame(f: dict, im: Image.Image) -> dict:
    rgba = im.convert('RGBA')
    w, h = rgba.size
    bgra = rgba.tobytes('raw', 'BGRA')
    comp_data = zlib.compress(bgra, level=6)
    nf = dict(f)
    nf['fmt'] = 0x10
    nf['comp'] = 6
    nf['w'] = w
    nf['h'] = h
    nf['sz'] = len(comp_data)
    nf['data'] = comp_data
    return nf


def _encode_img_v2(keep_zero: int, ver: int, frames: list[dict]) -> bytes:
    idx = bytearray()
    payload = bytearray()
    for f in frames:
        if f['fmt'] == 0x11:
            idx += struct.pack('<II', 0x11, f['link'])
        else:
            idx += struct.pack(
                '<IIIIIIIII',
                f['fmt'], f['comp'], f['w'], f['h'], len(f['data']),
                f['x'], f['y'], f['mw'], f['mh'],
            )
            payload += f['data']
    hdr = b'Neople Img File\x00' + struct.pack('<IIII', len(idx), keep_zero, ver, len(frames))
    return bytes(hdr + idx + payload)


def parse_npk(raw: bytes) -> list[tuple[str, bytes]]:
    if not raw.startswith(b'NeoplePack_Bill\x00'):
        raise ValueError('invalid NPK header')
    count = struct.unpack_from('<I', raw, 16)[0]
    entries: list[tuple[str, bytes]] = []
    pos = 20
    for _ in range(count):
        off, sz = struct.unpack_from('<II', raw, pos)
        enc = raw[pos + 8:pos + 264]
        name = bytes(enc[j] ^ NPK_KEY[j] for j in range(256)).split(b'\x00', 1)[0].decode('cp949')
        entries.append((name, raw[off:off + sz]))
        pos += 264
    return entries


def build_npk(entries: list[tuple[str, bytes]]) -> bytes:
    count = len(entries)
    header_len = 20 + count * 264
    # Include the standard 32-byte NPK SHA-256 verification digest after the directory
    table = bytearray(b'NeoplePack_Bill\x00' + struct.pack('<I', count))
    data_offset = header_len + 32
    blobs = bytearray()
    for name, blob in entries:
        nb = name.encode('cp949')
        padded = nb + b'\x00' * (256 - len(nb))
        enc = bytes(padded[j] ^ NPK_KEY[j] for j in range(256))
        table += struct.pack('<II', data_offset + len(blobs), len(blob)) + enc
        blobs += blob
    digest_input = bytes(table[: (len(table) // 17) * 17])
    digest = hashlib.sha256(digest_input).digest()
    return bytes(table) + digest + bytes(blobs)


def patch_elfamplify_npk_bytes(raw: bytes, preview_dir: Path | None = None) -> bytes:
    entries = parse_npk(raw)
    if preview_dir is not None:
        preview_dir.mkdir(parents=True, exist_ok=True)

    new_entries: list[tuple[str, bytes]] = []
    patched_count = 0
    for name, img_raw in entries:
        low = name.lower()
        if low.endswith('/0618_elfampify.img'):
            kz, ver, frames = _decode_img_v2_frames(img_raw)
            im0 = _patch_elfampify_main_bg(_frame_to_pil(frames[0]))
            frames[0] = _pil_to_frame(frames[0], im0)
            if preview_dir is not None:
                im0.save(preview_dir / '0618_elfampify_00.png')
            new_entries.append((name, _encode_img_v2(kz, ver, frames)))
            patched_count += 1
        elif low.endswith('/0618_elfampify_cal.img'):
            kz, ver, frames = _decode_img_v2_frames(img_raw)
            im0 = _patch_elfampify_cal_0(_frame_to_pil(frames[0]))
            im1 = _patch_elfampify_cal_1(_frame_to_pil(frames[1]))
            im2 = _patch_complete_badge(_frame_to_pil(frames[2]), '已完成', 15)
            im3 = _patch_complete_badge(_frame_to_pil(frames[3]), '已全部完成！', 18)
            im4 = _patch_goto_button(_frame_to_pil(frames[4]), disabled=False)
            im5 = _patch_goto_button(_frame_to_pil(frames[5]), disabled=False)
            im6 = _patch_goto_button(_frame_to_pil(frames[6]), disabled=False)
            im7 = _patch_goto_button(_frame_to_pil(frames[7]), disabled=True)
            for idx, im in ((0, im0), (1, im1), (2, im2), (3, im3), (4, im4), (5, im5), (6, im6), (7, im7)):
                frames[idx] = _pil_to_frame(frames[idx], im)
                if preview_dir is not None:
                    im.save(preview_dir / f'0618_elfampify_cal_{idx:02d}.png')
            new_entries.append((name, _encode_img_v2(kz, ver, frames)))
            patched_count += 8
        elif low.endswith('/0618_elfampify_ui_ani.img'):
            kz, ver, frames = _decode_img_v2_frames(img_raw)
            im10 = _patch_result_banner(_frame_to_pil(frames[10]), '强化成功！', (255, 246, 178), (118, 240, 255))
            im11 = _patch_result_banner(_frame_to_pil(frames[11]), '强化失败！', (255, 224, 178), (255, 118, 56))
            frames[10] = _pil_to_frame(frames[10], im10)
            frames[11] = _pil_to_frame(frames[11], im11)
            if preview_dir is not None:
                im10.save(preview_dir / '0618_elfampify_ui_ani_10.png')
                im11.save(preview_dir / '0618_elfampify_ui_ani_11.png')
            new_entries.append((name, _encode_img_v2(kz, ver, frames)))
            patched_count += 2
        else:
            new_entries.append((name, img_raw))
    if patched_count != 11:
        raise RuntimeError(f'expected 11 patched frames in {NPK_NAME}, got {patched_count}')
    return build_npk(new_entries)


def patch_upgrade_and_spec_xui(text: str) -> tuple[str, bool]:
    """Replace literal `Static Text` placeholder on `base_item_name` with Chinese UI string key."""
    pat = re.compile(r'(<CNUIControlText\b[^>]*\bID="STR:base_item_name"[^>]*\bstring=")STR:Static Text(")')
    new_text, n = pat.subn(r'\1STR:&lt;20::branch_of_vandrassil_05&gt;\2', text, count=1)
    if n == 0 and 'STR:&lt;20::branch_of_vandrassil_05&gt;' not in text:
        raise ValueError('failed to locate base_item_name Static Text in upgrade_and_spec.xui')
    ET.fromstring(new_text.rstrip('\x00').lstrip('\ufeff'))
    return new_text, n > 0


def render_uv_str_overlay(patches: dict[str, str]) -> str:
    return '\r\n'.join(f'{k}>{v}' for k, v in sorted(patches.items())) + '\r\n'


def patch_dstr_plain(plain: str) -> tuple[str, int]:
    """Replace the 8 Event 500 strings in decoded UTF-8 dstr.dat text, preserving exact line endings."""
    changed = 0
    lines = plain.splitlines(keepends=True)
    out: list[str] = []
    seen: set[str] = set()
    for line in lines:
        if '>' in line:
            k, rest = line.split('>', 1)
            if k in DSTR_PATCHES:
                eol = '\r\n' if line.endswith('\r\n') else ('\n' if line.endswith('\n') else '')
                new_line = f'{k}>{DSTR_PATCHES[k]}{eol}'
                if new_line != line:
                    changed += 1
                out.append(new_line)
                seen.add(k)
                continue
        out.append(line)
    missing = set(DSTR_PATCHES) - seen
    if missing:
        raise ValueError(f'missing expected dstr.dat keys: {sorted(missing)}')
    return ''.join(out), changed


def _extract_pvf_targets(inner_pvf: Path, out_dir: Path) -> dict[str, bytes]:
    go_bin = core.pre.GO_BIN
    env = dict(os.environ)
    env['GOPATH'] = str(ROOT / 'tools' / 'gopath')
    env['GOCACHE'] = str(ROOT / 'tools' / 'gocache')
    env['PATH'] = str(go_bin.parent) + os.pathsep + env.get('PATH', '')
    res = subprocess.run(
        [
            str(go_bin), 'run', './cmd/pvfinspect',
            '-source', str(inner_pvf),
            '-files', ','.join(PVF_TARGETS),
            '-output', str(out_dir),
        ],
        cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
        creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=180,
    )
    if res.returncode != 0:
        raise RuntimeError('pvfinspect failed: ' + (res.stderr or res.stdout or '')[-500:])
    manifest = json.loads((out_dir / 'exports.json').read_text(encoding='utf-8'))
    out: dict[str, bytes] = {}
    for row in manifest:
        ap = row['entry']['archive_path'] if 'entry' in row else row['archive_path']
        out[ap] = (out_dir / row['raw_file']).read_bytes()
    return out


def apply_all(preview_only: bool = False) -> dict:
    client_dir = core.pre.client_exe().parent
    npk_path = client_dir / 'ImagePacks2' / NPK_NAME
    pvf_path = client_dir / 'Script.pvf'
    dstr_path = client_dir / 'dstr.dat'
    exe_path = client_dir / 'DFO.exe'

    # 1. Patch NPK sprites (and save previews)
    orig_npk = npk_path.read_bytes()
    new_npk = patch_elfamplify_npk_bytes(orig_npk, preview_dir=PREVIEW_DIR)
    # Round-trip sanity check on patched NPK
    rt_entries = parse_npk(new_npk)
    if len(rt_entries) != len(parse_npk(orig_npk)):
        raise RuntimeError('NPK entry count mismatch after rebuild')

    if preview_only:
        return {
            'preview_only': True,
            'preview_dir': str(PREVIEW_DIR),
            'npk_before_sha': _sha256(orig_npk),
            'npk_after_sha': _sha256(new_npk),
        }

    if core.client_up():
        raise RuntimeError('DFO.exe is currently running; close game before applying client asset patches')

    BACKUP_DIR.mkdir(parents=True, exist_ok=True)
    # Back up originals once (never overwrite first clean backup)
    for src, bname in ((npk_path, NPK_NAME), (dstr_path, 'dstr.dat'), (pvf_path, 'Script.pvf')):
        dst = BACKUP_DIR / bname
        if not dst.exists():
            shutil.copy2(src, dst)

    # 2. Patch dstr.dat with round-trip verification
    keys = dstr_ui.key_pair(exe_path)
    orig_dstr = dstr_path.read_bytes()
    plain_dstr = dstr_ui.unpack(orig_dstr, keys).decode('utf-8')
    patched_dstr_txt, dstr_changed = patch_dstr_plain(plain_dstr)
    new_dstr = dstr_ui.pack(patched_dstr_txt.encode('utf-8'), keys)
    if dstr_ui.unpack(new_dstr, keys).decode('utf-8') != patched_dstr_txt:
        raise RuntimeError('dstr.dat round-trip verification failed')

    # 3. Patch Script.pvf (upgrade_and_spec.xui + string/ui.uv.str + event.uv.str + stackable.uv.str)
    fp = lr.pvf_fingerprint(pvf_path)
    is_inner = bool(fp and fp.get('inner'))
    inner_pvf = pvf_path if is_inner else lr.ensure_inner_pvf(pvf_path, fp)
    with tempfile.TemporaryDirectory(prefix='ev500-pvf-') as td:
        tmp = Path(td)
        extracted = _extract_pvf_targets(inner_pvf, tmp / 'before')
        raw_xui = extracted[MAIN_XUI]
        has_bom = raw_xui.startswith(b'\xff\xfe')
        xui_txt = raw_xui.decode('utf-16-le')
        new_xui_txt, xui_changed = patch_upgrade_and_spec_xui(xui_txt)
        new_xui_bytes = (b'\xff\xfe' if has_bom and not new_xui_txt.startswith('\ufeff') else b'') + new_xui_txt.encode('utf-16-le')

        xui_file = tmp / 'upgrade_and_spec.xui'
        xui_file.write_bytes(new_xui_bytes)
        layers = [
            {'path': MAIN_XUI, 'source': str(xui_file), 'mod_id': 'ev500-ui-l10n', 'priority': 0},
        ]
        for idx, (entry, patches) in enumerate((
            (UI_STR_ENTRY, UI_PATCHES),
            (EV_STR_ENTRY, EVENT_PATCHES),
            (STK_STR_ENTRY, STACKABLE_PATCHES),
        ), start=1):
            ov = tmp / f'{idx:02d}-{Path(entry).name}'
            ov.write_text(render_uv_str_overlay(patches), encoding='utf-8', newline='')
            layers.append({'path': entry, 'source': str(ov), 'mod_id': 'ev500-ui-l10n', 'priority': idx})

        plan_file = tmp / 'plan.json'
        plan_file.write_text(json.dumps({'layers': layers}, ensure_ascii=False), encoding='utf-8')
        merged_inner = tmp / 'merged.inner.pvf'
        merger = _ensure_merger_binary()
        env = dict(os.environ)
        env['GOGC'] = '10'
        res = subprocess.run(
            [str(merger), '-source', str(inner_pvf), '-plan', str(plan_file), '-output', str(merged_inner)],
            cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
            creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=600,
        )
        if res.returncode != 0:
            raise RuntimeError('pvfmerge failed: ' + (res.stderr or res.stdout or '')[-600:])

        # Read-back verify every patched entry in merged_inner
        after = _extract_pvf_targets(merged_inner, tmp / 'after')
        after_xui = after[MAIN_XUI].decode('utf-16-le')
        if 'STR:Static Text' in after_xui:
            raise RuntimeError('upgrade_and_spec.xui still contains Static Text after merge')
        for entry, patches in (
            (UI_STR_ENTRY, UI_PATCHES),
            (EV_STR_ENTRY, EVENT_PATCHES),
            (STK_STR_ENTRY, STACKABLE_PATCHES),
        ):
            v_raw = after[entry]
            if not v_raw.endswith(b'\x00\x00\x00\x00') or '\x00' in v_raw[:-4].decode('utf-16-le'):
                raise RuntimeError(f'post-merge UTF-16LE NUL check failed for {entry}')
            txt = v_raw.decode('utf-16-le').lstrip('\ufeff')
            kv = dict(line.split('>', 1) for line in txt.splitlines() if '>' in line)
            for k, want in patches.items():
                if kv.get(k) != want:
                    raise RuntimeError(f'post-merge mismatch in {entry} key {k}: got {kv.get(k)!r}, want {want!r}')

        wrapped_pvf = tmp / 'Script.pvf.new'
        if is_inner:
            core.file_copy(merged_inner, wrapped_pvf)
        else:
            rewrap_inner_to_client_pvf(merged_inner, wrapped_pvf, client_dir)

        # Atomic swaps for NPK, dstr.dat, and Script.pvf
        for target, src_bytes in ((npk_path, new_npk), (dstr_path, new_dstr)):
            tmp_out = target.with_suffix(target.suffix + '.tmp')
            tmp_out.write_bytes(src_bytes)
            os.replace(tmp_out, target)
        tmp_pvf = pvf_path.with_suffix('.pvf.tmp')
        core.file_copy(wrapped_pvf, tmp_pvf)
        os.replace(tmp_pvf, pvf_path)

    # Refresh inner PVF cache so launcher/tools see the new Script.pvf immediately
    new_fp = lr.pvf_fingerprint(pvf_path)
    if new_fp and not new_fp.get('inner'):
        lr.ensure_inner_pvf(pvf_path, new_fp)

    manifest = {
        'npk': {'path': str(npk_path), 'before': _sha256(orig_npk), 'after': _sha256(new_npk)},
        'dstr': {'path': str(dstr_path), 'before': _sha256(orig_dstr), 'after': _sha256(new_dstr), 'changed_rows': dstr_changed},
        'pvf': {'path': str(pvf_path), 'after': core.digest(pvf_path), 'xui_changed': xui_changed},
    }
    (BACKUP_DIR / 'applied.json').write_text(json.dumps(manifest, indent=2, ensure_ascii=False), encoding='utf-8')
    return manifest


def main() -> int:
    ap = argparse.ArgumentParser(description='Localize Event 500 (灵魂之铁砧 / ElfAmplify) UI assets')
    ap.add_argument('--preview-only', action='store_true', help='Only render NPK sprite previews without touching live client files')
    args = ap.parse_args()
    result = apply_all(preview_only=args.preview_only)
    print(json.dumps(result, indent=2, ensure_ascii=False))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
