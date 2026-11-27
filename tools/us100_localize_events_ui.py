"""Localize US100 event/activity UI v2 IMG frames inside ImagePacks2 NPKs into Simplified Chinese.

Usage:
  python tools/us100_localize_events_ui.py --build --preview-dir runtime/event-ui-preview/after
  python tools/us100_localize_events_ui.py --install
  python tools/us100_localize_events_ui.py --rollback
"""
from __future__ import annotations

import argparse
from dataclasses import dataclass
from functools import lru_cache
import hashlib
import json
from pathlib import Path
import shutil
import struct
import subprocess
import sys
from typing import Callable
import zlib

from PIL import Image, ImageDraw, ImageFont
import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parent))
import us100_ui_atlas as atlas  # noqa: E402
from us100_quest_act import compress_exact, inflate_exact  # noqa: E402

DEFAULT_CLIENT_PACKS = Path(r"D:\DNF\client\100US\DFO\ImagePacks2")
DEFAULT_BACKUP_DIR = Path("runtime/event-ui-localize-backup")
DEFAULT_BUILD_DIR = Path("runtime/event-ui-localize-build")

FONT_BOLD_CANDIDATES = (
    Path(r"C:\Windows\Fonts\msyhbd.ttc"),
    Path(r"C:\Windows\Fonts\simhei.ttf"),
    Path(r"C:\Windows\Fonts\msyh.ttc"),
)
FONT_REG_CANDIDATES = (
    Path(r"C:\Windows\Fonts\msyh.ttc"),
    Path(r"C:\Windows\Fonts\msyhbd.ttc"),
    Path(r"C:\Windows\Fonts\simhei.ttf"),
)
FONT_COMPACT_CANDIDATES = (
    Path(r"C:\Windows\Fonts\simsun.ttc"),
    Path(r"C:\Windows\Fonts\msyh.ttc"),
    Path(r"C:\Windows\Fonts\simhei.ttf"),
)


@lru_cache(maxsize=64)
def load_font(size: int, bold: bool = True) -> ImageFont.FreeTypeFont:
    candidates = FONT_BOLD_CANDIDATES if bold else FONT_REG_CANDIDATES
    for path in candidates:
        if path.is_file():
            return ImageFont.truetype(str(path), size=max(8, size))
    raise FileNotFoundError("No Simplified Chinese TrueType font found in C:\\Windows\\Fonts")


@lru_cache(maxsize=32)
def load_compact_font(size: int) -> ImageFont.FreeTypeFont:
    for path in FONT_COMPACT_CANDIDATES:
        if path.is_file():
            return ImageFont.truetype(str(path), size=max(8, size))
    return load_font(size, bold=False)


def encode_pixels_native(im: Image.Image, kind: int) -> bytes:
    """Encode PIL RGBA image into the frame's exact native Neople pixel layout (kind 14/15/16)."""
    rgba = im.convert("RGBA")
    if kind == 16:
        return rgba.tobytes("raw", "BGRA")
    arr = np.asarray(rgba, dtype=np.uint16)
    if kind == 15:
        # ARGB4444
        val = (
            ((arr[:, :, 3] >> 4) << 12)
            | ((arr[:, :, 0] >> 4) << 8)
            | ((arr[:, :, 1] >> 4) << 4)
            | (arr[:, :, 2] >> 4)
        )
        return val.astype("<u2").tobytes()
    if kind == 14:
        # ARGB1555
        val = (
            ((arr[:, :, 3] >= 128).astype(np.uint16) << 15)
            | ((arr[:, :, 0] >> 3) << 10)
            | ((arr[:, :, 1] >> 3) << 5)
            | (arr[:, :, 2] >> 3)
        )
        return val.astype("<u2").tobytes()
    raise ValueError(f"unsupported v2 IMG pixel kind: {kind}")


def draw_compact_button(
    im: Image.Image,
    text: str,
    fill: tuple[int, int, int, int],
    *,
    bg_mode: str = "solid",
    bg_color: tuple[int, int, int, int] | None = None,
    keep: tuple[int, int, int, int] = (4, 4, 4, 4),
    size: int = 12,
) -> Image.Image:
    """Render crisp 1-bit CJK text onto a flat sampled interior so tight zlib slots fit effortlessly."""
    out = im.copy().convert("RGBA")
    kl, kt, kr, kb = keep
    box = (kl, kt, max(kl + 4, out.width - kr), max(kt + 4, out.height - kb))
    fill_sampled_bg(out, box, mode=bg_mode, color=bg_color)
    x0, y0, x1, y1 = box
    font = load_compact_font(size)
    mask = Image.new("1", out.size, 0)
    d = ImageDraw.Draw(mask)
    bb = d.textbbox((0, 0), text, font=font)
    lw, lh = bb[2] - bb[0], bb[3] - bb[1]
    tx = x0 + (x1 - x0 - lw) // 2 - bb[0]
    ty = y0 + (y1 - y0 - lh) // 2 - bb[1]
    d.text((tx, ty), text, font=font, fill=1)
    out.paste(fill, (0, 0), mask)
    return out


def fit_frame_in_place(
    orig_frame: atlas.Frame,
    updated_im: Image.Image,
    compact_candidates: list[Image.Image] | None = None,
) -> tuple[bytes, Image.Image]:
    """Compress `updated_im` into `orig_frame`'s EXACT original byte slot (`len(orig_frame.raw)`),
    preserving 100% of original NPK header, 32-byte SHA-256, NpkIndex.etc offsets, and Frame.meta."""
    kind, comp, w, h, orig_sz, _x, _y, _cw, _ch = struct.unpack("<IIIIIiiII", orig_frame.meta)
    if comp != 6:
        raise ValueError(f"unsupported frame compression {comp} (expected 6/zlib)")
    if (updated_im.width, updated_im.height) != (w, h):
        raise ValueError(f"frame size mismatch: expected {(w, h)}, got {(updated_im.width, updated_im.height)}")
    hdr = orig_frame.raw[:2]
    expected_len = w * h * (4 if kind == 16 else 2)
    orig_rgba = orig_frame.image().convert("RGBA")
    orig_arr = np.asarray(orig_rgba)
    upd_rgba = updated_im.convert("RGBA")
    upd_arr = np.asarray(upd_rgba)
    delta_mask = np.any(orig_arr != upd_arr, axis=2)

    def _try(cand_im: Image.Image) -> tuple[bytes, Image.Image] | None:
        raw = encode_pixels_native(cand_im, kind)
        try:
            blob = compress_exact(raw, orig_sz, hdr)
        except ValueError:
            return None
        if len(blob) != orig_sz or inflate_exact(blob, expected_len) != raw:
            raise AssertionError("in-place zlib slot verification failed")
        return blob, atlas.Frame(orig_frame.meta, blob).image().convert("RGBA")

    # 1. Exact lossless encoding first
    res = _try(upd_rgba)
    if res is not None:
        return res

    # 2. Gentle progressive quantization strictly on changed pixels (preserves untouched art bit-for-bit)
    for step in (4, 8, 16, 24, 32, 48, 64):
        arr = upd_arr.copy()
        arr[delta_mask, :3] = np.minimum(
            255, ((arr[delta_mask, :3].astype(np.int16) + step // 2) // step) * step
        )
        res = _try(Image.fromarray(arr, "RGBA"))
        if res is not None:
            return res

    # 3. Palette quantization (16/8 colors) strictly on changed pixels
    for colors in (16, 8):
        q = upd_rgba.quantize(colors=colors, method=Image.Quantize.FASTOCTREE).convert("RGBA")
        q_arr = np.asarray(q)
        arr = orig_arr.copy()
        arr[delta_mask] = q_arr[delta_mask]
        res = _try(Image.fromarray(arr, "RGBA"))
        if res is not None:
            return res

    # 4. Crisp 1-bit compact button fallback for tight button slots (cleaner than 4-color quantize!)
    if compact_candidates:
        for cand_im in compact_candidates:
            res = _try(cand_im.convert("RGBA"))
            if res is not None:
                return res
            for colors in (16, 8, 4):
                q = cand_im.convert("RGBA").quantize(colors=colors, method=Image.Quantize.FASTOCTREE).convert("RGBA")
                res = _try(q)
                if res is not None:
                    return res

    # 5. Last resort: 4-color quantization on changed pixels
    q = upd_rgba.quantize(colors=4, method=Image.Quantize.FASTOCTREE).convert("RGBA")
    q_arr = np.asarray(q)
    arr = orig_arr.copy()
    arr[delta_mask] = q_arr[delta_mask]
    res = _try(Image.fromarray(arr, "RGBA"))
    if res is not None:
        return res

    best = len(zlib.compress(encode_pixels_native(upd_rgba, kind), 9))
    raise ValueError(f"cannot fit localized frame ({w}x{h}, kind={kind}) in original {orig_sz}-byte slot (min={best})")


def verify_npk_header_sha256(data: bytes) -> bool:
    """Verify Neople NPK 32-byte header SHA-256 located right after the member index table."""
    if len(data) < 20 or data[:16] != b"NeoplePack_Bill\0":
        return False
    count, = struct.unpack_from("<I", data, 16)
    table_end = 20 + 264 * count
    if table_end + 32 > len(data):
        return False
    first_off = min(struct.unpack_from("<I", data, 20 + 264 * i)[0] for i in range(count))
    if first_off - table_end < 32:
        return True  # legacy NPK without 32-byte checksum gap
    hashed_len = (table_end // 17) * 17
    expected = hashlib.sha256(data[:hashed_len]).digest()
    return data[table_end : table_end + 32] == expected


def patch_npk_in_place(
    src_path: Path,
    img_specs: dict[str, dict[int, Callable[[Image.Image], Image.Image]]],
) -> tuple[bytes, list[tuple[str, int, Image.Image]]]:
    """Patch target v2 IMG frames strictly inside their original byte spans inside `src_path`.

    Guarantees ZERO changes to NPK file size, NPK header, 32-byte NPK SHA-256,
    NpkIndex.etc member offsets/lengths, IMG headers, or 36-byte Frame.meta records.
    """
    orig_bytes = src_path.read_bytes()
    buf = bytearray(orig_bytes)
    rows = atlas.index(src_path)
    spans: list[tuple[int, int]] = []
    preview_items: list[tuple[str, int, Image.Image]] = []
    seen_member_offsets: set[int] = set()

    for img_name, frame_patchers in img_specs.items():
        key = img_name.casefold()
        if key not in rows:
            raise KeyError(f"{img_name} not found in {src_path.name}")
        member_off, member_len = rows[key]
        if member_off in seen_member_offsets:
            # Shared NPK member (e.g. resource.img and 210902_lialab2_event.img share the same bytes)
            continue
        seen_member_offsets.add(member_off)

        raw_img = orig_bytes[member_off : member_off + member_len]
        table_len, = struct.unpack_from("<I", raw_img, 16)
        frames = atlas.split_img(raw_img)
        cursor = member_off + 32 + table_len
        for idx, frame in enumerate(frames):
            if not isinstance(frame, atlas.Frame):
                continue
            start = cursor
            end = start + len(frame.raw)
            cursor = end
            if idx not in frame_patchers:
                continue
            if orig_bytes[start:end] != frame.raw:
                raise AssertionError(f"byte offset mismatch for {img_name} #{idx}")
            fn = frame_patchers[idx]
            orig_im = frame.image().convert("RGBA")
            updated_im = fn(orig_im.copy())
            compact_fn = getattr(fn, "compact_fallback", None)
            compact_cands = compact_fn(orig_im.copy()) if compact_fn else None
            fitted_blob, decoded_im = fit_frame_in_place(frame, updated_im, compact_cands)
            buf[start:end] = fitted_blob
            spans.append((start, end))
            preview_items.append((img_name, idx, decoded_im))
        if cursor != member_off + member_len:
            raise AssertionError(f"IMG tail mismatch for {img_name}")

    # Strict byte-level invariance check outside patched frame payload spans
    if len(buf) != len(orig_bytes):
        raise AssertionError("NPK total length changed during in-place patch")
    pos = 0
    for left, right in sorted(spans):
        if left < pos:
            raise AssertionError("overlapping frame spans")
        if buf[pos:left] != orig_bytes[pos:left]:
            raise AssertionError(f"unexpected byte mutation outside frame spans at {pos}..{left}")
        pos = right
    if buf[pos:] != orig_bytes[pos:]:
        raise AssertionError(f"unexpected trailing byte mutation at {pos}..")
    result = bytes(buf)
    if not verify_npk_header_sha256(result):
        raise AssertionError(f"NPK header SHA-256 verification failed for {src_path.name}")
    return result, preview_items


# ---------------------------------------------------------------------------
# Background sampling & crisp CJK typography helpers
# ---------------------------------------------------------------------------

def _median_rgba(pixels: list[tuple[int, int, int, int]]) -> tuple[int, int, int, int]:
    if not pixels:
        return (0, 0, 0, 255)
    n = len(pixels)
    mid = n // 2
    return (
        sorted(p[0] for p in pixels)[mid],
        sorted(p[1] for p in pixels)[mid],
        sorted(p[2] for p in pixels)[mid],
        sorted(p[3] for p in pixels)[mid],
    )


def _clamp_box(im: Image.Image, box: tuple[int, int, int, int]) -> tuple[int, int, int, int]:
    x0, y0, x1, y1 = box
    x0 = max(0, min(im.width - 1, int(x0)))
    y0 = max(0, min(im.height - 1, int(y0)))
    x1 = max(x0 + 1, min(im.width, int(x1)))
    y1 = max(y0 + 1, min(im.height, int(y1)))
    return (x0, y0, x1, y1)


def fill_sampled_bg(
    im: Image.Image,
    box: tuple[int, int, int, int],
    mode: str = "edge_v",
    color: tuple[int, int, int, int] | None = None,
    pad: int = 2,
    radius: int = 0,
) -> None:
    """Cleanly erase existing text in `box` while blending into the native UI background."""
    x0, y0, x1, y1 = _clamp_box(im, box)
    w, h = x1 - x0, y1 - y0
    if mode == "clear":
        clear_patch = Image.new("RGBA", (w, h), (0, 0, 0, 0))
        im.paste(clear_patch, (x0, y0))
        return
    if mode == "solid":
        if color is None:
            pts = []
            for sx in range(x0, x1, max(1, w // 12)):
                pts.append(im.getpixel((sx, max(0, y0 - 1))))
                pts.append(im.getpixel((sx, min(im.height - 1, y1))))
            color = _median_rgba(pts)
        if radius > 0:
            draw = ImageDraw.Draw(im)
            draw.rounded_rectangle([x0, y0, x1 - 1, y1 - 1], radius=radius, fill=color)
        else:
            im.paste(Image.new("RGBA", (w, h), color), (x0, y0))
        return

    px = im.load()
    if mode == "edge_v":
        top_y = max(0, y0 - pad)
        bot_y = min(im.height - 1, y1 - 1 + pad)
        top_c = _median_rgba([px[x, top_y] for x in range(x0, x1)])
        bot_c = _median_rgba([px[x, bot_y] for x in range(x0, x1)])
        for y in range(y0, y1):
            t = (y - y0) / max(1, h - 1)
            c = tuple(int(round(top_c[k] * (1 - t) + bot_c[k] * t)) for k in range(4))
            for x in range(x0, x1):
                px[x, y] = c
        return

    if mode == "edge_h":
        left_x = max(0, x0 - pad)
        right_x = min(im.width - 1, x1 - 1 + pad)
        left_c = _median_rgba([px[left_x, y] for y in range(y0, y1)])
        right_c = _median_rgba([px[right_x, y] for y in range(y0, y1)])
        for x in range(x0, x1):
            t = (x - x0) / max(1, w - 1)
            c = tuple(int(round(left_c[k] * (1 - t) + right_c[k] * t)) for k in range(4))
            for y in range(y0, y1):
                px[x, y] = c
        return

    if mode == "row_lr":
        # Per-row smooth interpolation between left & right boundary pixels (preserves horizontal bands/gradients!)
        lx = max(0, x0 - pad)
        rx = min(im.width - 1, x1 - 1 + pad)
        for y in range(y0, y1):
            lc = px[lx, y]
            rc = px[rx, y]
            for x in range(x0, x1):
                t = (x - x0) / max(1, w - 1)
                px[x, y] = tuple(int(round(lc[k] * (1 - t) + rc[k] * t)) for k in range(4))
        return

    if mode == "col_tb":
        # Per-column smooth interpolation between top & bottom boundary pixels
        ty = max(0, y0 - pad)
        by = min(im.height - 1, y1 - 1 + pad)
        for x in range(x0, x1):
            tc = px[x, ty]
            bc = px[x, by]
            for y in range(y0, y1):
                t = (y - y0) / max(1, h - 1)
                px[x, y] = tuple(int(round(tc[k] * (1 - t) + bc[k] * t)) for k in range(4))
        return

    raise ValueError(f"unsupported fill mode: {mode}")


def _wrap_lines(
    draw: ImageDraw.ImageDraw,
    text: str,
    font: ImageFont.FreeTypeFont,
    max_w: int,
    stroke_width: int,
) -> list[str]:
    out: list[str] = []
    for para in text.split("\n"):
        if not para:
            out.append("")
            continue
        cur = ""
        for ch in para:
            cand = cur + ch
            bbox = draw.textbbox((0, 0), cand, font=font, stroke_width=stroke_width)
            if cur and (bbox[2] - bbox[0]) > max_w:
                out.append(cur)
                cur = ch
            else:
                cur = cand
        if cur:
            out.append(cur)
    return out


def draw_fitted_text(
    im: Image.Image,
    box: tuple[int, int, int, int],
    text: str,
    *,
    max_size: int = 16,
    min_size: int = 9,
    fill: tuple[int, int, int, int] = (255, 255, 255, 255),
    stroke_fill: tuple[int, int, int, int] | None = None,
    stroke_width: int = 0,
    shadow: tuple[int, int, tuple[int, int, int, int]] | None = None,
    align: str = "center",
    valign: str = "center",
    bold: bool = True,
    line_spacing: int = 2,
    auto_wrap: bool = False,
) -> None:
    """Render Simplified Chinese text strictly inside `box` with automatic sizing so it never clips."""
    x0, y0, x1, y1 = _clamp_box(im, box)
    avail_w = max(4, (x1 - x0) - 2)
    avail_h = max(4, (y1 - y0) - 2)
    draw = ImageDraw.Draw(im)

    chosen_font = load_font(min_size, bold=bold)
    chosen_lines = text.split("\n")
    chosen_metrics: list[tuple[int, int, int, int]] = []
    chosen_total_h = 0

    for size in range(max_size, min_size - 1, -1):
        font = load_font(size, bold=bold)
        lines = (
            _wrap_lines(draw, text, font, avail_w, stroke_width)
            if auto_wrap
            else text.split("\n")
        )
        metrics = []
        max_line_w = 0
        total_h = 0
        for idx, line in enumerate(lines):
            probe = line if line else "测"
            bb = draw.textbbox((0, 0), probe, font=font, stroke_width=stroke_width)
            lw = 0 if not line else (bb[2] - bb[0])
            lh = max(1, bb[3] - bb[1])
            metrics.append((bb[0], bb[1], lw, lh))
            max_line_w = max(max_line_w, lw)
            total_h += lh + (line_spacing if idx > 0 else 0)
        chosen_font = font
        chosen_lines = lines
        chosen_metrics = metrics
        chosen_total_h = total_h
        if max_line_w <= avail_w and total_h <= avail_h:
            break

    if valign == "top":
        cur_y = y0 + 1
    elif valign == "bottom":
        cur_y = y1 - 1 - chosen_total_h
    else:
        cur_y = y0 + (y1 - y0 - chosen_total_h) // 2

    for line, (bx0, by0, lw, lh) in zip(chosen_lines, chosen_metrics):
        if align == "left":
            tx = x0 + 2 - bx0
        elif align == "right":
            tx = x1 - 2 - lw - bx0
        else:
            tx = x0 + (x1 - x0 - lw) // 2 - bx0
        ty = cur_y - by0
        if line:
            if shadow is not None:
                dx, dy, sc = shadow
                draw.text((tx + dx, ty + dy), line, font=chosen_font, fill=sc)
            draw.text(
                (tx, ty),
                line,
                font=chosen_font,
                fill=fill,
                stroke_width=stroke_width if stroke_fill else 0,
                stroke_fill=stroke_fill,
            )
        cur_y += lh + line_spacing


def patch_rect(
    im: Image.Image,
    box: tuple[int, int, int, int],
    text: str,
    *,
    bg_mode: str = "edge_v",
    bg_color: tuple[int, int, int, int] | None = None,
    bg_pad: int = 2,
    bg_radius: int = 0,
    max_size: int = 16,
    min_size: int = 9,
    fill: tuple[int, int, int, int] = (255, 255, 255, 255),
    stroke_fill: tuple[int, int, int, int] | None = None,
    stroke_width: int = 0,
    shadow: tuple[int, int, tuple[int, int, int, int]] | None = None,
    align: str = "center",
    valign: str = "center",
    bold: bool = True,
    line_spacing: int = 2,
    auto_wrap: bool = False,
) -> Image.Image:
    fill_sampled_bg(im, box, mode=bg_mode, color=bg_color, pad=bg_pad, radius=bg_radius)
    draw_fitted_text(
        im,
        box,
        text,
        max_size=max_size,
        min_size=min_size,
        fill=fill,
        stroke_fill=stroke_fill,
        stroke_width=stroke_width,
        shadow=shadow,
        align=align,
        valign=valign,
        bold=bold,
        line_spacing=line_spacing,
        auto_wrap=auto_wrap,
    )
    return im


def _is_desaturated(im: Image.Image) -> bool:
    """Detect disabled/grayed-out button frames automatically."""
    w, h = im.size
    cx0, cy0, cx1, cy1 = w // 4, h // 4, max(w // 4 + 1, 3 * w // 4), max(h // 4 + 1, 3 * h // 4)
    diffs = []
    for y in range(cy0, cy1, max(1, (cy1 - cy0) // 6)):
        for x in range(cx0, cx1, max(1, (cx1 - cx0) // 6)):
            r, g, b, a = im.getpixel((x, y))
            if a > 128:
                diffs.append(max(r, g, b) - min(r, g, b))
    return bool(diffs) and (sum(diffs) / len(diffs)) < 14


def make_button_patcher(
    text: str,
    *,
    fill: tuple[int, int, int, int] = (245, 245, 245, 255),
    disabled_fill: tuple[int, int, int, int] = (155, 155, 155, 255),
    stroke_fill: tuple[int, int, int, int] | None = (20, 20, 25, 230),
    stroke_width: int = 1,
    max_size: int = 14,
    keep_left: int = 4,
    keep_right: int = 4,
    keep_top: int = 4,
    keep_bottom: int = 4,
    bg_mode: str = "edge_v",
    bg_color: tuple[int, int, int, int] | None = None,
    bg_pad: int = 1,
    bold: bool = True,
) -> Callable[[Image.Image], Image.Image]:
    def _patch(im: Image.Image) -> Image.Image:
        w, h = im.size
        box = (keep_left, keep_top, max(keep_left + 4, w - keep_right), max(keep_top + 4, h - keep_bottom))
        text_color = disabled_fill if _is_desaturated(im) else fill
        return patch_rect(
            im,
            box,
            text,
            bg_mode=bg_mode,
            bg_color=bg_color,
            bg_pad=bg_pad,
            max_size=max_size,
            min_size=9,
            fill=text_color,
            stroke_fill=stroke_fill,
            stroke_width=stroke_width,
            bold=bold,
        )

    def _compact_fallback(im: Image.Image) -> list[Image.Image]:
        text_color = disabled_fill if _is_desaturated(im) else fill
        keep = (keep_left, keep_top, keep_right, keep_bottom)
        cands = []
        # Try flat solid interior + crisp 1-bit text, and clear-background variant for overlay labels
        for c_mode in ("solid", "clear"):
            if c_mode == "clear" and keep_left > 4:
                continue
            for sz in (min(max_size, 13), 12, 11, 10, 9):
                cands.append(
                    draw_compact_button(
                        im, text, text_color, bg_mode=c_mode, bg_color=bg_color, keep=keep, size=sz
                    )
                )
        return cands

    _patch.compact_fallback = _compact_fallback  # type: ignore[attr-defined]
    return _patch


def button_group(
    indices: tuple[int, ...] | range,
    text: str,
    **kwargs,
) -> dict[int, Callable[[Image.Image], Image.Image]]:
    fn = make_button_patcher(text, **kwargs)
    return {idx: fn for idx in indices}


# ---------------------------------------------------------------------------
# Per-event localization specs (all 16 US100 event/activity NPKs)
# ---------------------------------------------------------------------------

def _patch_520_reversehottime_0(im: Image.Image) -> Image.Image:
    # Event 520: EXP Boost Event (0401_reversehottime.img #0, 260x355)
    # Keep character portrait on right (x >= 198) intact!
    patch_rect(
        im, (10, 12, 198, 56), "经验加成狂欢",
        bg_mode="edge_v", bg_pad=2, max_size=26,
        fill=(255, 255, 255, 255), stroke_fill=(130, 18, 12, 255), stroke_width=2,
        shadow=(2, 2, (90, 10, 8, 220)),
    )
    patch_rect(
        im, (22, 65, 238, 82), "活动期间：长期开放",
        bg_mode="solid", bg_color=(48, 16, 12, 255), max_size=12,
        fill=(242, 198, 120, 255), bold=True,
    )
    patch_rect(
        im, (120, 96, 232, 114), "[累计任务]",
        bg_mode="solid", bg_color=(32, 14, 10, 255), max_size=13,
        fill=(240, 200, 136, 255), bold=True,
    )
    patch_rect(
        im, (124, 224, 174, 250), "已完成!",
        bg_mode="row_lr", bg_pad=2, max_size=12,
        fill=(246, 196, 84, 255), stroke_fill=(35, 16, 8, 220), stroke_width=1,
    )
    patch_rect(
        im, (192, 224, 242, 250), "已完成!",
        bg_mode="row_lr", bg_pad=2, max_size=12,
        fill=(246, 196, 84, 255), stroke_fill=(35, 16, 8, 220), stroke_width=1,
    )
    patch_rect(
        im, (72, 272, 188, 290), "[累计时间]",
        bg_mode="solid", bg_color=(36, 16, 12, 255), max_size=13,
        fill=(240, 200, 136, 255), bold=True,
    )
    # Keep envelope icon at x=44..61 untouched
    patch_rect(
        im, (64, 336, 238, 353), "奖励将通过邮件发放",
        bg_mode="solid", bg_color=(34, 15, 12, 255), max_size=12,
        fill=(206, 172, 148, 255), align="left", bold=False,
    )
    return im


def _patch_danji_cal_0(im: Image.Image) -> Image.Image:
    # 0712_danji_calendar.img #0 (288x359)
    patch_rect(
        im, (10, 10, 162, 48), "丹吉神秘罐",
        bg_mode="solid", bg_color=(30, 16, 10, 255), max_size=26,
        fill=(255, 218, 128, 255), stroke_fill=(85, 32, 8, 255), stroke_width=2,
    )
    patch_rect(
        im, (10, 48, 154, 65), "长期开放",
        bg_mode="solid", bg_color=(30, 16, 10, 255), max_size=12,
        fill=(230, 196, 140, 255), align="left",
    )
    patch_rect(
        im, (82, 74, 206, 91), "第 1 赛季",
        bg_mode="row_lr", bg_pad=2, max_size=12,
        fill=(255, 236, 190, 255),
    )
    patch_rect(
        im, (86, 97, 278, 164),
        "丹吉神秘罐全新开启！\n每次开启丹吉罐可累计积分，\n达到指定进度即可领取珍稀奖励。",
        bg_mode="solid", bg_color=(26, 18, 14, 255), max_size=11,
        fill=(242, 224, 182, 255), align="left", bold=False, line_spacing=3,
    )
    patch_rect(
        im, (76, 169, 212, 191), "活动说明",
        bg_mode="row_lr", bg_pad=2, max_size=12,
        fill=(220, 248, 255, 255),
    )
    patch_rect(
        im, (8, 192, 280, 328),
        "• 每次开启丹吉罐均可累计丹吉积分。\n"
        "• 累计积分在赛季结束前不会重置\n"
        "  （领取全部阶段奖励后将重置重新累计）。\n"
        "• 丹吉特别奖励领取次数不设上限。\n"
        "• 活动期间长期开放，欢迎参与！",
        bg_mode="solid", bg_color=(24, 16, 12, 255), max_size=11,
        fill=(235, 212, 168, 255), align="left", bold=False, line_spacing=4,
    )
    return im


def _patch_danji_cal_1(im: Image.Image) -> Image.Image:
    # 0712_danji_calendar.img #1 (278x367)
    patch_rect(
        im, (10, 10, 160, 48), "丹吉神秘罐",
        bg_mode="solid", bg_color=(28, 16, 12, 255), max_size=24,
        fill=(255, 218, 128, 255), stroke_fill=(85, 32, 8, 255), stroke_width=2,
    )
    patch_rect(
        im, (10, 48, 152, 68), "长期开放",
        bg_mode="solid", bg_color=(28, 16, 12, 255), max_size=12,
        fill=(230, 196, 140, 255), align="left",
    )
    patch_rect(
        im, (72, 74, 206, 98), "第 3 赛季",
        bg_mode="solid", bg_color=(72, 42, 70, 255), max_size=12,
        fill=(255, 236, 190, 255),
    )
    patch_rect(
        im, (82, 99, 270, 174),
        "丹吉神秘罐全新赛季开启！\n开启罐子累计进度即可兑换\n丰厚限定装扮与珍稀道具。",
        bg_mode="solid", bg_color=(24, 18, 14, 255), max_size=11,
        fill=(242, 224, 182, 255), align="left", bold=False, line_spacing=3,
    )
    patch_rect(
        im, (72, 175, 206, 198), "活动说明",
        bg_mode="solid", bg_color=(72, 42, 70, 255), max_size=12,
        fill=(220, 248, 255, 255),
    )
    patch_rect(
        im, (8, 198, 270, 338),
        "• 每次开启丹吉罐均可获得丹吉积分。\n"
        "• 集齐全部阶段奖励后进度重置，\n"
        "  可继续开启并重复领取奖励。\n"
        "• 奖励道具将直接发放或通过邮件发送。",
        bg_mode="solid", bg_color=(22, 16, 12, 255), max_size=11,
        fill=(235, 212, 168, 255), align="left", bold=False, line_spacing=4,
    )
    return im


def _patch_danji_month_cal_0(im: Image.Image) -> Image.Image:
    # danji_month_cal.img #0 (248x300)
    patch_rect(
        im, (52, 8, 196, 48), "丹吉",
        bg_mode="edge_v", bg_pad=2, max_size=30,
        fill=(255, 214, 118, 255), stroke_fill=(110, 30, 10, 255), stroke_width=2,
    )
    patch_rect(
        im, (18, 214, 230, 238), "丹吉本月精选",
        bg_mode="row_lr", bg_pad=2, max_size=15,
        fill=(246, 214, 148, 255), stroke_fill=(60, 22, 8, 230), stroke_width=1,
    )
    return im


def _patch_everyday_0(im: Image.Image) -> Image.Image:
    # everydayconnect.img #0 (519x400)
    patch_rect(
        im, (388, 72, 505, 112), "累计登录\n额外奖励！",
        bg_mode="solid", bg_color=(28, 32, 48, 255), max_size=14,
        fill=(250, 214, 130, 255), stroke_fill=(15, 16, 26, 240), stroke_width=1,
    )
    return im


def _patch_everyday_cal_0(im: Image.Image) -> Image.Image:
    # everydayconnect_calender.img #0 (261x364)
    patch_rect(
        im, (10, 92, 251, 142),
        "每日登录即可领取丰厚好礼！\n坚持每日签到，还可解锁\n累计登录额外奖励！",
        bg_mode="solid", bg_color=(28, 25, 24, 255), max_size=12,
        fill=(245, 236, 218, 255), bold=False, line_spacing=3,
    )
    patch_rect(
        im, (24, 184, 102, 236), "每日登录\n奖励",
        bg_mode="solid", bg_color=(22, 50, 28, 255), max_size=14,
        fill=(152, 246, 138, 255), stroke_fill=(10, 28, 12, 240), stroke_width=1,
    )
    patch_rect(
        im, (24, 258, 102, 316), "累计登录\n额外奖励",
        bg_mode="solid", bg_color=(32, 30, 56, 255), max_size=14,
        fill=(245, 198, 126, 255), stroke_fill=(16, 14, 30, 240), stroke_width=1,
    )
    return im


def _patch_everyday_mainbanner(title: str) -> Callable[[Image.Image], Image.Image]:
    def _fn(im: Image.Image) -> Image.Image:
        w, h = im.size
        patch_rect(
            im, (24, 4, w - 24, h - 22), title,
            bg_mode="edge_v", bg_pad=2, max_size=24,
            fill=(255, 236, 145, 255), stroke_fill=(24, 24, 32, 255), stroke_width=2,
        )
        patch_rect(
            im, (140, h - 22, w - 140, h - 4), "长期开放",
            bg_mode="edge_v", bg_pad=2, max_size=12,
            fill=(245, 242, 228, 255), stroke_fill=(20, 18, 16, 220), stroke_width=1,
        )
        return im
    return _fn


def _patch_everyday_calbanner(title: str) -> Callable[[Image.Image], Image.Image]:
    def _fn(im: Image.Image) -> Image.Image:
        w, h = im.size
        patch_rect(
            im, (10, 2, w - 10, h - 24), title,
            bg_mode="edge_v", bg_pad=1, max_size=24,
            fill=(255, 236, 145, 255), stroke_fill=(24, 24, 32, 255), stroke_width=2,
        )
        patch_rect(
            im, (38, h - 24, w - 38, h - 5), "活动期间：长期开放",
            bg_mode="edge_v", bg_pad=1, max_size=12,
            fill=(245, 242, 228, 255), stroke_fill=(20, 18, 16, 220), stroke_width=1,
        )
        return im
    return _fn


def _patch_619_dailycheck_ui_0(im: Image.Image) -> Image.Image:
    # 1202_dailycheck_ui.img #0 (358x460)
    patch_rect(
        im, (34, 10, 326, 48), "引导石大人的礼物！",
        bg_mode="solid", bg_color=(46, 32, 28, 255), max_size=26,
        fill=(255, 240, 165, 255), stroke_fill=(60, 24, 16, 255), stroke_width=2,
    )
    patch_rect(
        im, (62, 51, 298, 69), "活动期间：长期开放",
        bg_mode="solid", bg_color=(46, 32, 28, 255), max_size=12,
        fill=(248, 206, 98, 255),
    )
    patch_rect(
        im, (20, 70, 340, 120),
        "每天在线60分钟即可领取每日好礼！\n通关推荐地下城还可额外累计5分钟在线时间！",
        bg_mode="solid", bg_color=(46, 32, 28, 255), max_size=11,
        fill=(246, 238, 226, 255), bold=False, line_spacing=3,
    )
    # Keep small golden stone mascot at x=22..76 untouched
    patch_rect(
        im, (80, 140, 294, 168), "小引导石的每日礼物！",
        bg_mode="solid", bg_color=(96, 56, 32, 255), max_size=18,
        fill=(255, 244, 210, 255), stroke_fill=(56, 28, 12, 255), stroke_width=2,
    )
    patch_rect(
        im, (14, 184, 72, 204), "在线时间",
        bg_mode="solid", bg_color=(96, 56, 32, 255), max_size=12,
        fill=(255, 245, 225, 255),
    )
    patch_rect(
        im, (88, 326, 272, 354), "引导石大人的累计礼物！",
        bg_mode="solid", bg_color=(62, 34, 18, 255), max_size=17,
        fill=(255, 244, 210, 255), stroke_fill=(40, 18, 8, 255), stroke_width=2,
    )
    patch_rect(
        im, (12, 358, 352, 380), "多次领取每日礼物即可解锁累计豪华大奖！",
        bg_mode="solid", bg_color=(62, 34, 18, 255), max_size=11,
        fill=(242, 226, 200, 255), bold=False,
    )
    patch_rect(
        im, (118, 381, 216, 400), "已领取次数：",
        bg_mode="solid", bg_color=(62, 34, 18, 255), max_size=12,
        fill=(255, 232, 168, 255),
    )
    patch_rect(
        im, (32, 418, 76, 438), "5次",
        bg_mode="solid", bg_color=(62, 34, 18, 255), max_size=15,
        fill=(246, 170, 92, 255),
    )
    patch_rect(
        im, (176, 418, 228, 438), "10次",
        bg_mode="solid", bg_color=(62, 34, 18, 255), max_size=15,
        fill=(246, 170, 92, 255),
    )
    return im


def _patch_619_dailycheck_cal_0(im: Image.Image) -> Image.Image:
    # 1202_dailycheck_calendar.img #0 (282x426)
    patch_rect(
        im, (46, 4, 236, 43), "每天在线60分钟！\n畅玩地下城领取好礼！",
        bg_mode="solid", bg_color=(48, 34, 28, 255), max_size=11,
        fill=(246, 236, 218, 255),
    )
    patch_rect(
        im, (54, 42, 230, 98), "引导石大人的\n丰厚礼物！",
        bg_mode="solid", bg_color=(48, 34, 28, 255), max_size=22,
        fill=(255, 224, 130, 255), stroke_fill=(56, 20, 14, 255), stroke_width=2,
    )
    patch_rect(
        im, (26, 106, 256, 124), "活动期间：长期开放",
        bg_mode="solid", bg_color=(96, 56, 32, 255), max_size=12,
        fill=(255, 220, 112, 255),
    )
    patch_rect(
        im, (18, 128, 264, 194),
        "引导石大人为大家准备了每日好礼！\n每天在线满60分钟即可轻松领取，\n通关推荐地下城额外累计5分钟！",
        bg_mode="solid", bg_color=(96, 56, 32, 255), max_size=11,
        fill=(248, 238, 220, 255), bold=False, line_spacing=3,
    )
    patch_rect(
        im, (48, 202, 234, 222), "小引导石的礼物列表",
        bg_mode="solid", bg_color=(96, 56, 32, 255), max_size=14,
        fill=(255, 245, 220, 255),
    )
    patch_rect(
        im, (60, 338, 222, 358), "引导石大人的累计礼物",
        bg_mode="solid", bg_color=(62, 34, 18, 255), max_size=14,
        fill=(255, 245, 220, 255),
    )
    patch_rect(
        im, (52, 380, 92, 416), "签到\n5次",
        bg_mode="solid", bg_color=(62, 34, 18, 255), max_size=13,
        fill=(246, 174, 96, 255),
    )
    patch_rect(
        im, (140, 380, 180, 416), "签到\n10次",
        bg_mode="solid", bg_color=(62, 34, 18, 255), max_size=13,
        fill=(246, 174, 96, 255),
    )
    return im


def _patch_525_security_ev_0(im: Image.Image) -> Image.Image:
    # 0804_securityevent_eventui.img #0 (268x330)
    patch_rect(
        im, (110, 4, 260, 72), "守护安全！\n领取好礼！",
        bg_mode="solid", bg_color=(26, 52, 122, 255), max_size=24,
        fill=(255, 255, 255, 255), stroke_fill=(14, 34, 92, 255), stroke_width=2,
    )
    patch_rect(
        im, (108, 70, 260, 116), "守护您的珍贵账号！\n致敬坚持安全防护的冒险家",
        bg_mode="solid", bg_color=(36, 62, 134, 255), max_size=11,
        fill=(228, 242, 255, 255), bold=False, line_spacing=3,
    )
    patch_rect(
        im, (50, 130, 232, 168), "根据您的账号安全等级\n准备了【安全印章】好礼！",
        bg_mode="edge_v", bg_pad=2, max_size=12,
        fill=(24, 52, 110, 255), bold=True, line_spacing=3,
    )
    patch_rect(
        im, (76, 176, 206, 198), "当前我的安全等级",
        bg_mode="edge_v", bg_pad=2, max_size=14,
        fill=(255, 255, 255, 255), stroke_fill=(20, 72, 140, 255), stroke_width=2,
    )
    patch_rect(
        im, (14, 284, 254, 322), "领取的印章可在NPC赛丽亚【安全印章商店】\n查看并兑换丰厚奖励！",
        bg_mode="edge_v", bg_pad=2, max_size=11,
        fill=(255, 255, 255, 255), stroke_fill=(18, 62, 120, 230), stroke_width=1,
        bold=False, line_spacing=3,
    )
    return im


def _patch_525_security_cal_0(im: Image.Image) -> Image.Image:
    # 0804_securityevent_calendarui.img #0 (268x330)
    patch_rect(
        im, (110, 4, 260, 72), "守护安全！\n领取好礼！",
        bg_mode="solid", bg_color=(26, 52, 122, 255), max_size=24,
        fill=(255, 255, 255, 255), stroke_fill=(14, 34, 92, 255), stroke_width=2,
    )
    patch_rect(
        im, (108, 70, 260, 116), "开启账号安全防护服务，\n让您的冒险之旅更加安心！",
        bg_mode="solid", bg_color=(36, 62, 134, 255), max_size=11,
        fill=(228, 242, 255, 255), bold=False, line_spacing=3,
    )
    fill_sampled_bg(im, (214, 128, 244, 178), mode="edge_v", pad=2)
    patch_rect(
        im, (28, 128, 218, 224),
        "开启安全防护提升安全等级，\n每日可领取15个【安全印章】！\n在NPC赛丽亚【安全印章商店】\n即可兑换各类珍稀奖励！",
        bg_mode="edge_v", bg_pad=2, max_size=11,
        fill=(18, 46, 102, 255), bold=True, line_spacing=4,
    )
    patch_rect(
        im, (62, 236, 220, 256), "安全印章商店主要奖励",
        bg_mode="edge_v", bg_pad=2, max_size=13,
        fill=(255, 255, 255, 255), stroke_fill=(20, 72, 140, 255), stroke_width=2,
    )
    return im


def _patch_607_newbi_0(im: Image.Image) -> Image.Image:
    # 0317_newbi.img #0 (268x356)
    card_bg = (218, 220, 224, 255)
    patch_rect(
        im, (8, 16, 206, 46), "新手/回归冒险家福利",
        bg_mode="edge_v", bg_pad=2, max_size=19,
        fill=(255, 255, 255, 255), stroke_fill=(26, 78, 148, 255), stroke_width=2,
    )
    patch_rect(
        im, (8, 48, 206, 66), "活动期间：长期开放",
        bg_mode="edge_v", bg_pad=2, max_size=11,
        fill=(24, 52, 92, 255), align="left",
    )
    # Card 1
    patch_rect(
        im, (14, 80, 206, 102), "全新冒险之路 · 长期开放",
        bg_mode="solid", bg_color=card_bg, max_size=13,
        fill=(30, 62, 118, 255), align="left",
    )
    patch_rect(
        im, (14, 104, 218, 144), "通关推荐地下城收集冒险图鉴，\n掉落概率大幅提升！",
        bg_mode="solid", bg_color=card_bg, max_size=11,
        fill=(55, 62, 74, 255), align="left", bold=False, line_spacing=3,
    )
    # Card 2 (keep characters on right x >= 186)
    patch_rect(
        im, (14, 158, 206, 180), "精锐队员租借 · 长期开放",
        bg_mode="solid", bg_color=card_bg, max_size=13,
        fill=(30, 62, 118, 255), align="left",
    )
    patch_rect(
        im, (14, 182, 186, 224), "小队模式人手不足？\n免费租借精锐队员助战！",
        bg_mode="solid", bg_color=card_bg, max_size=11,
        fill=(55, 62, 74, 255), align="left", bold=False, line_spacing=3,
    )
    # Card 3 (keep green check stamp on right x >= 198)
    patch_rect(
        im, (14, 246, 214, 268), "每日签到福利 · 长期开放",
        bg_mode="solid", bg_color=card_bg, max_size=13,
        fill=(30, 62, 118, 255), align="left",
    )
    patch_rect(
        im, (14, 270, 196, 312), "参与每日签到活动，\n指定天数获赠额外好礼！",
        bg_mode="solid", bg_color=card_bg, max_size=11,
        fill=(55, 62, 74, 255), align="left", bold=False, line_spacing=3,
    )
    return im


def _patch_602_lialab_cal_0(im: Image.Image) -> Image.Image:
    # 210902_lialab2_calendar.img #0 (324x440)
    patch_rect(
        im, (22, 10, 302, 74), "利亚的特别\n魔盒实验室 II",
        bg_mode="edge_v", bg_pad=2, max_size=24,
        fill=(235, 248, 255, 255), stroke_fill=(14, 48, 92, 255), stroke_width=2,
    )
    patch_rect(
        im, (84, 78, 240, 96), "长期开放",
        bg_mode="edge_v", bg_pad=2, max_size=12,
        fill=(140, 226, 255, 255),
    )
    patch_rect(
        im, (42, 128, 282, 148), "▪ 利亚的特别魔盒商店 ▪",
        bg_mode="solid", bg_color=(14, 40, 74, 255), max_size=14,
        fill=(140, 236, 255, 255),
    )
    patch_rect(
        im, (14, 150, 310, 248),
        "“成功实验的魔盒正在上架，快来看看吧！”\n"
        "将各类玩法的不可交易道具变为账号绑定！\n"
        "可在飞空艇左侧的利亚特别魔盒商店购买\n"
        "各类材料魔盒，助力角色快速毕业。",
        bg_mode="solid", bg_color=(14, 40, 74, 255), max_size=11,
        fill=(216, 240, 255, 255), bold=False, line_spacing=4,
    )
    patch_rect(
        im, (42, 266, 282, 286), "▪ 利亚的特别魔盒实验室 ▪",
        bg_mode="solid", bg_color=(14, 40, 74, 255), max_size=14,
        fill=(140, 236, 255, 255),
    )
    patch_rect(
        im, (14, 286, 310, 350),
        "“新魔盒研究正在进行中，急需探索数据！”\n"
        "通关指定地下城后可探索3次，\n"
        "每周可从已保存道具中领取1件心仪奖励。",
        bg_mode="solid", bg_color=(14, 40, 74, 255), max_size=11,
        fill=(216, 240, 255, 255), bold=False, line_spacing=4,
    )
    return im


def _patch_602_lialab_shop_0(im: Image.Image) -> Image.Image:
    # 210902_lialab2_shop.img #0 (348x78) — keep Ria's face on right x >= 264!
    patch_rect(
        im, (6, 8, 264, 42), "利亚的特别魔盒实验室 II",
        bg_mode="edge_v", bg_pad=2, max_size=19,
        fill=(240, 250, 255, 255), stroke_fill=(10, 42, 86, 255), stroke_width=2,
    )
    patch_rect(
        im, (6, 44, 258, 74), "将各类玩法的不可交易道具\n转换为账号绑定的特别机会！",
        bg_mode="edge_v", bg_pad=2, max_size=11,
        fill=(165, 235, 255, 255), bold=False, line_spacing=2,
    )
    return im


def _patch_602_lialab_ev_0(im: Image.Image) -> Image.Image:
    # 210902_lialab2_event.img & resource.img #0 (726x480)
    patch_rect(
        im, (140, 101, 244, 118), "魔盒实验室",
        bg_mode="row_lr", bg_pad=2, max_size=12,
        fill=(160, 240, 255, 255),
    )
    patch_rect(
        im, (476, 101, 610, 118), "魔盒研究记录",
        bg_mode="row_lr", bg_pad=2, max_size=12,
        fill=(160, 240, 255, 255),
    )
    patch_rect(
        im, (406, 195, 496, 214), "已保存道具",
        bg_mode="row_lr", bg_pad=2, max_size=12,
        fill=(150, 232, 255, 255),
    )
    patch_rect(
        im, (472, 274, 620, 292), "跨界装置说明",
        bg_mode="row_lr", bg_pad=2, max_size=12,
        fill=(160, 240, 255, 255),
    )
    patch_rect(
        im, (472, 357, 514, 372), "1次",
        bg_mode="solid", bg_color=(28, 32, 38, 255), max_size=11,
        fill=(220, 236, 248, 255),
    )
    patch_rect(
        im, (562, 357, 604, 372), "2次",
        bg_mode="solid", bg_color=(28, 32, 38, 255), max_size=11,
        fill=(220, 236, 248, 255),
    )
    return im


def _patch_pickandkeep_cal_0(im: Image.Image) -> Image.Image:
    # pickandkeepevent/calendar.img #0 (324x440)
    patch_rect(
        im, (14, 8, 310, 44), "利亚的特别魔盒实验室",
        bg_mode="edge_v", bg_pad=2, max_size=22,
        fill=(235, 248, 255, 255), stroke_fill=(12, 46, 90, 255), stroke_width=2,
    )
    patch_rect(
        im, (42, 46, 282, 66), "活动期间：长期开放",
        bg_mode="edge_v", bg_pad=2, max_size=12,
        fill=(145, 230, 255, 255),
    )
    patch_rect(
        im, (16, 152, 308, 266),
        "每日在线15分钟即可为魔盒充能，\n最多可使用3次魔盒探索功能！\n"
        "探索到的道具可自由选择排除或保存，\n每周坚持研究还可获得特别回礼！",
        bg_mode="solid", bg_color=(14, 24, 38, 230), max_size=12,
        fill=(228, 242, 255, 255), align="left", bold=False, line_spacing=5,
    )
    patch_rect(
        im, (92, 274, 232, 294), "主要奖励",
        bg_mode="solid", bg_color=(24, 42, 58, 255), max_size=13,
        fill=(170, 244, 255, 255),
    )
    return im


def _patch_614_melody_main_0(im: Image.Image) -> Image.Image:
    # musicnote16t_main.img #0 (720x445)
    patch_rect(
        im, (30, 10, 276, 72), "冒险家之旋律",
        bg_mode="edge_v", bg_pad=2, max_size=28,
        fill=(255, 255, 255, 255), stroke_fill=(44, 72, 150, 255), stroke_width=2,
    )
    patch_rect(
        im, (106, 78, 204, 97), "长期开放",
        bg_mode="solid", bg_color=(32, 60, 92, 255), max_size=12,
        fill=(240, 246, 255, 255), bg_radius=6,
    )
    patch_rect(
        im, (16, 102, 290, 148),
        "庆贺全新冒险篇章开启！\n分享你的故事，为你谱写专属新曲！",
        bg_mode="edge_v", bg_pad=2, max_size=12,
        fill=(32, 50, 76, 255), stroke_fill=(255, 255, 255, 220), stroke_width=1,
        line_spacing=3,
    )
    patch_rect(
        im, (72, 152, 234, 174), "◆ 每周任务 ◆",
        bg_mode="edge_v", bg_pad=2, max_size=14,
        fill=(44, 118, 92, 255), stroke_fill=(255, 255, 255, 220), stroke_width=1,
    )
    for idx, (bx0, bx1) in enumerate([(18, 76), (194, 252), (372, 430), (548, 606)], start=1):
        patch_rect(
            im, (bx0, 386, bx1, 418), f"第{idx}行\n奖励",
            bg_mode="edge_v", bg_pad=2, max_size=13,
            fill=(255, 255, 255, 255), stroke_fill=(32, 72, 130, 220), stroke_width=1,
        )
    patch_rect(
        im, (272, 424, 448, 442), "奖励将通过邮件发放。",
        bg_mode="edge_v", bg_pad=2, max_size=11,
        fill=(60, 72, 92, 255), bold=False,
    )
    return im


def _patch_614_melody_cal_0(im: Image.Image) -> Image.Image:
    # musicnote16t_calendar.img #0 (280x345)
    patch_rect(
        im, (18, 10, 262, 70), "冒险家之旋律",
        bg_mode="edge_v", bg_pad=2, max_size=26,
        fill=(255, 255, 255, 255), stroke_fill=(44, 68, 148, 255), stroke_width=2,
    )
    patch_rect(
        im, (88, 76, 192, 95), "长期开放",
        bg_mode="solid", bg_color=(32, 60, 92, 255), max_size=12,
        fill=(240, 246, 255, 255), bg_radius=6,
    )
    patch_rect(
        im, (24, 112, 256, 254),
        "冒险家，听说了吗？\n盛大的冒险庆典即将开启！\n分享你的冒险故事，\n我将为你谱写专属新曲，\n完成旋律即可领取丰厚好礼！",
        bg_mode="solid", bg_color=(24, 46, 56, 255), max_size=12,
        fill=(242, 248, 255, 255), bold=False, line_spacing=5,
    )
    patch_rect(
        im, (20, 288, 62, 326), "主要\n奖励",
        bg_mode="edge_v", bg_pad=2, max_size=13,
        fill=(255, 255, 255, 255), stroke_fill=(32, 68, 128, 220), stroke_width=1,
    )
    return im


def _patch_614_shop_cal_0(im: Image.Image) -> Image.Image:
    # 16th_shopcalender.img #0 (280x335)
    patch_rect(
        im, (8, 8, 272, 54), "周年纪念商店开启！",
        bg_mode="edge_v", bg_pad=2, max_size=24,
        fill=(255, 255, 255, 255), stroke_fill=(28, 88, 148, 255), stroke_width=2,
    )
    patch_rect(
        im, (36, 62, 244, 80), "活动期间：长期开放",
        bg_mode="solid", bg_color=(32, 66, 86, 255), max_size=11,
        fill=(232, 246, 255, 255), bg_radius=6,
    )
    patch_rect(
        im, (26, 102, 254, 214),
        "快来看看吧！\n赫顿玛尔开启周年纪念商店！\n携带周年纪念硬币即可兑换\n各类特别道具！参与活动\n即可轻松获得纪念硬币！",
        bg_mode="solid", bg_color=(20, 44, 54, 255), max_size=12,
        fill=(242, 248, 255, 255), bold=False, line_spacing=4,
    )
    patch_rect(
        im, (26, 246, 66, 284), "主要\n奖励",
        bg_mode="solid", bg_color=(42, 162, 206, 255), max_size=13,
        fill=(255, 255, 255, 255),
    )
    return im


def _patch_512_mining_1(im: Image.Image) -> Image.Image:
    # dungeonmining_evnet.img #1 (597x87)
    patch_rect(
        im, (8, 2, 590, 56), "地下城挖矿大作战！",
        bg_mode="clear", max_size=40,
        fill=(210, 246, 92, 255), stroke_fill=(40, 26, 10, 255), stroke_width=3,
        shadow=(2, 3, (20, 14, 6, 220)),
    )
    patch_rect(
        im, (228, 61, 382, 81), "长期开放",
        bg_mode="solid", bg_color=(230, 152, 74, 255), max_size=14,
        fill=(48, 26, 12, 255),
    )
    return im


def _patch_512_mining_2(im: Image.Image) -> Image.Image:
    # dungeonmining_evnet.img #2 (723x134) — keep pickaxe icons untouched!
    patch_rect(
        im, (254, 18, 386, 44), "当前持有",
        bg_mode="solid", bg_color=(26, 20, 14, 255), max_size=18,
        fill=(252, 186, 86, 255), align="right",
    )
    patch_rect(
        im, (104, 62, 242, 88), "登录 3 次",
        bg_mode="solid", bg_color=(26, 20, 14, 255), max_size=16,
        fill=(242, 198, 122, 255), align="left",
    )
    patch_rect(
        im, (104, 100, 242, 128), "在线 30 分钟",
        bg_mode="solid", bg_color=(26, 20, 14, 255), max_size=16,
        fill=(242, 198, 122, 255), align="left",
    )
    patch_rect(
        im, (366, 56, 518, 90), "推荐地下城 20 次",
        bg_mode="solid", bg_color=(26, 20, 14, 255), max_size=16,
        fill=(242, 198, 122, 255), align="left",
    )
    patch_rect(
        im, (366, 100, 518, 128), "通关团本 1 次",
        bg_mode="solid", bg_color=(26, 20, 14, 255), max_size=16,
        fill=(242, 198, 122, 255), align="left",
    )
    return im


def _patch_512_mining_cal_0(im: Image.Image) -> Image.Image:
    # dungeonmining_calendar.img #0 (278x389)
    patch_rect(
        im, (8, 4, 270, 74), "地下城挖矿\n大作战！",
        bg_mode="edge_v", bg_pad=2, max_size=26,
        fill=(216, 248, 96, 255), stroke_fill=(46, 28, 10, 255), stroke_width=3,
    )
    patch_rect(
        im, (66, 82, 212, 102), "长期开放",
        bg_mode="row_lr", bg_pad=2, max_size=13,
        fill=(48, 26, 12, 255),
    )
    # Keep character art on left (x <= 94)
    patch_rect(
        im, (94, 114, 272, 214),
        "冒险家，我们在地下城\n发现了神秘藏宝图！\n完成每周任务守护矿工，\n一起挖掘丰厚宝藏吧！",
        bg_mode="edge_v", bg_pad=2, max_size=12,
        fill=(48, 30, 16, 255), bold=True, line_spacing=4,
    )
    return im


def _patch_367_kitty_0(im: Image.Image) -> Image.Image:
    # 210422_kittykingdomattendance.img #0 (300x452)
    patch_rect(
        im, (30, 8, 270, 78), "猫咪王国\n签到与燃烧！",
        bg_mode="edge_v", bg_pad=2, max_size=25,
        fill=(255, 242, 118, 255), stroke_fill=(24, 92, 108, 255), stroke_width=3,
    )
    patch_rect(
        im, (24, 82, 276, 97), "活动期间：长期开放",
        bg_mode="edge_v", bg_pad=2, max_size=11,
        fill=(255, 255, 255, 255), stroke_fill=(14, 78, 94, 230), stroke_width=1,
    )
    patch_rect(
        im, (12, 98, 288, 115), "周末及节假日在线10分钟即可领取好礼！",
        bg_mode="edge_v", bg_pad=2, max_size=11,
        fill=(255, 248, 170, 255), stroke_fill=(14, 78, 94, 230), stroke_width=1,
    )
    patch_rect(
        im, (88, 121, 212, 141), "在线任务",
        bg_mode="solid", bg_color=(248, 198, 86, 255), max_size=14,
        fill=(96, 48, 18, 255),
    )
    patch_rect(
        im, (88, 329, 212, 349), "累计任务",
        bg_mode="solid", bg_color=(248, 198, 86, 255), max_size=14,
        fill=(96, 48, 18, 255),
    )
    for row_y0, row_y1 in [(360, 380), (396, 416)]:
        patch_rect(
            im, (52, row_y0, 144, row_y1), "领取在线奖励",
            bg_mode="solid", bg_color=(252, 244, 234, 255), max_size=12,
            fill=(108, 56, 24, 255), align="left",
        )
        patch_rect(
            im, (176, row_y0, 198, row_y1), "次",
            bg_mode="solid", bg_color=(252, 244, 234, 255), max_size=12,
            fill=(116, 86, 66, 255),
        )
        patch_rect(
            im, (212, row_y0, 234, row_y1), "次",
            bg_mode="solid", bg_color=(252, 244, 234, 255), max_size=12,
            fill=(116, 86, 66, 255),
        )
    # Keep envelope icon at x=78..92 untouched
    patch_rect(
        im, (94, 433, 242, 449), "奖励将通过邮件发放",
        bg_mode="edge_v", bg_pad=2, max_size=11,
        fill=(255, 255, 255, 255), stroke_fill=(16, 84, 100, 220), stroke_width=1,
        align="left",
    )
    return im


def _patch_367_kitty_13(im: Image.Image) -> Image.Image:
    # 210422_kittykingdomattendance.img #13 (300x452)
    patch_rect(
        im, (30, 8, 270, 78), "猫咪王国\n签到与燃烧！",
        bg_mode="edge_v", bg_pad=2, max_size=25,
        fill=(255, 242, 118, 255), stroke_fill=(24, 92, 108, 255), stroke_width=3,
    )
    patch_rect(
        im, (20, 82, 280, 114), "活动期间：长期开放\n周末及节假日专属福利",
        bg_mode="edge_v", bg_pad=2, max_size=11,
        fill=(255, 255, 255, 255), stroke_fill=(14, 78, 94, 230), stroke_width=1,
    )
    patch_rect(
        im, (10, 116, 290, 144), "猫咪王国准备了签到与燃烧双重好礼！\n在线满10分钟即可领取奖励与增益！",
        bg_mode="edge_v", bg_pad=2, max_size=11,
        fill=(20, 62, 74, 255), bold=True, line_spacing=2,
    )
    patch_rect(
        im, (82, 150, 218, 170), "猫咪王国燃烧",
        bg_mode="solid", bg_color=(248, 198, 86, 255), max_size=14,
        fill=(96, 48, 18, 255),
    )
    patch_rect(
        im, (52, 176, 248, 234), "技能冷却 ：-20%\n1~85级技能：+1\n抗魔值 ：+100",
        bg_mode="solid", bg_color=(252, 244, 234, 255), max_size=13,
        fill=(186, 52, 34, 255), bold=True, line_spacing=3,
    )
    patch_rect(
        im, (82, 254, 218, 274), "猫咪王国签到",
        bg_mode="solid", bg_color=(248, 198, 86, 255), max_size=14,
        fill=(96, 48, 18, 255),
    )
    patch_rect(
        im, (142, 326, 254, 358), "周末及节假日\n最多领取10次喵！",
        bg_mode="solid", bg_color=(252, 244, 234, 255), max_size=11,
        fill=(96, 52, 24, 255),
    )
    for (rx0, rx1), label, col in [
        ((18, 76), "节日礼", (255, 255, 255, 255)),
        ((82, 154), "特别签到", (255, 255, 255, 255)),
        ((162, 218), "累计3次", (255, 255, 255, 255)),
        ((228, 286), "累计6次", (255, 255, 255, 255)),
    ]:
        patch_rect(
            im, (rx0, 413, rx1, 431), label,
            bg_mode="row_lr", bg_pad=2, max_size=11,
            fill=col, stroke_fill=(35, 35, 45, 220), stroke_width=1,
        )
    return im


def _patch_367_kitty_rental_0(im: Image.Image) -> Image.Image:
    # 210422_kittykingdomrental.img #0 (300x452)
    patch_rect(
        im, (30, 8, 270, 78), "猫咪王国\n装备租借与折扣！",
        bg_mode="edge_v", bg_pad=2, max_size=24,
        fill=(255, 246, 130, 255), stroke_fill=(44, 96, 24, 255), stroke_width=3,
    )
    patch_rect(
        im, (24, 82, 276, 98), "活动期间：长期开放",
        bg_mode="edge_v", bg_pad=2, max_size=11,
        fill=(255, 255, 255, 255), stroke_fill=(34, 78, 18, 220), stroke_width=1,
    )
    patch_rect(
        im, (10, 102, 290, 134), "猫咪王国开启装备租借与团本门票折扣！\n每日免费租借装备，半价挑战各大团本！",
        bg_mode="edge_v", bg_pad=2, max_size=11,
        fill=(28, 66, 18, 255), bold=True, line_spacing=2,
    )
    patch_rect(
        im, (80, 140, 220, 160), "猫咪王国折扣",
        bg_mode="solid", bg_color=(248, 198, 86, 255), max_size=14,
        fill=(96, 48, 18, 255),
    )
    patch_rect(
        im, (28, 166, 272, 236), "超时空漩涡 | 普雷·伊希斯 | 希洛克\n团本门票 50% 折扣！\n（包含单人模式）",
        bg_mode="solid", bg_color=(252, 244, 234, 255), max_size=13,
        fill=(196, 48, 30, 255), bold=True, line_spacing=3,
    )
    patch_rect(
        im, (80, 246, 220, 266), "猫咪王国租借",
        bg_mode="solid", bg_color=(248, 198, 86, 255), max_size=14,
        fill=(96, 48, 18, 255),
    )
    patch_rect(
        im, (118, 278, 282, 338), "选择一名角色，即可免费\n租借100级传说全套装备\n以及Buff强化换装！",
        bg_mode="solid", bg_color=(246, 236, 222, 255), max_size=11,
        fill=(92, 52, 26, 255), align="left", line_spacing=3,
    )
    patch_rect(
        im, (14, 348, 286, 382), "前往赛丽亚房间的“露娜”处租借装备！",
        bg_mode="solid", bg_color=(252, 244, 234, 255), max_size=12,
        fill=(28, 112, 118, 255),
    )
    return im


def _patch_500_forge_cal_0(im: Image.Image) -> Image.Image:
    # 20200709_forgeofsouls_calendar.img #0 (332x473)
    patch_rect(
        im, (38, 12, 294, 56), "灵魂之铁砧",
        bg_mode="edge_v", bg_pad=2, max_size=30,
        fill=(255, 206, 68, 255), stroke_fill=(92, 24, 8, 255), stroke_width=3,
    )
    patch_rect(
        im, (96, 58, 236, 76), "长期开放",
        bg_mode="solid", bg_color=(42, 24, 18, 255), max_size=12,
        fill=(248, 226, 178, 255),
    )
    patch_rect(
        im, (144, 102, 314, 198),
        "听说从天而降的巨大\n灵魂之铁砧了吗？\n伴随铁砧降落的灵魂石\n能让武器变得更强！\n要不要来试试看？",
        bg_mode="solid", bg_color=(22, 16, 14, 255), max_size=12,
        fill=(236, 198, 168, 255), align="left", bold=False, line_spacing=3,
    )
    patch_rect(
        im, (162, 230, 296, 264), "凯丽的灵魂强化礼盒",
        bg_mode="solid", bg_color=(24, 18, 14, 255), max_size=13,
        fill=(236, 198, 116, 255),
    )
    patch_rect(
        im, (156, 322, 304, 342), "每日限领 1 次",
        bg_mode="solid", bg_color=(24, 18, 14, 255), max_size=12,
        fill=(226, 210, 188, 255),
    )
    patch_rect(
        im, (24, 432, 310, 464), "提取灵魂之石并应用于武器！",
        bg_mode="solid", bg_color=(20, 16, 14, 255), max_size=13,
        fill=(128, 232, 118, 255),
    )
    return im


def _patch_500_forge_cal_1(im: Image.Image) -> Image.Image:
    # 20200709_forgeofsouls_calendar.img #1 (333x380)
    patch_rect(
        im, (38, 12, 294, 56), "灵魂之铁砧",
        bg_mode="edge_v", bg_pad=2, max_size=30,
        fill=(255, 206, 68, 255), stroke_fill=(92, 24, 8, 255), stroke_width=3,
    )
    patch_rect(
        im, (96, 58, 236, 76), "长期开放",
        bg_mode="solid", bg_color=(42, 24, 18, 255), max_size=12,
        fill=(248, 226, 178, 255),
    )
    patch_rect(
        im, (86, 100, 316, 160),
        "通过不断锻造可提取变换之灵魂石，\n可自由选择强化或锻造作为提取方式。",
        bg_mode="solid", bg_color=(22, 16, 14, 255), max_size=12,
        fill=(236, 198, 168, 255), align="left", bold=False, line_spacing=4,
    )
    return im


def _patch_500_forge_main_0(im: Image.Image) -> Image.Image:
    # 20200709_forgeofsouls.img #0 (382x441) — keep Kiri portrait x <= 72!
    patch_rect(
        im, (76, 10, 336, 52), "灵魂之铁砧",
        bg_mode="edge_v", bg_pad=2, max_size=30,
        fill=(255, 206, 68, 255), stroke_fill=(92, 24, 8, 255), stroke_width=3,
    )
    patch_rect(
        im, (100, 356, 282, 374), "合成灵魂",
        bg_mode="solid", bg_color=(28, 24, 20, 255), max_size=13,
        fill=(232, 214, 184, 255),
    )
    return im


def _patch_500_forge_main_2(im: Image.Image) -> Image.Image:
    # 20200709_forgeofsouls.img #2 (382x530)
    patch_rect(
        im, (76, 10, 336, 52), "灵魂之铁砧",
        bg_mode="edge_v", bg_pad=2, max_size=30,
        fill=(255, 206, 68, 255), stroke_fill=(92, 24, 8, 255), stroke_width=3,
    )
    patch_rect(
        im, (100, 252, 282, 270), "所需材料",
        bg_mode="solid", bg_color=(28, 24, 20, 255), max_size=13,
        fill=(232, 214, 184, 255),
    )
    patch_rect(
        im, (100, 312, 282, 330), "活动任务",
        bg_mode="solid", bg_color=(28, 24, 20, 255), max_size=13,
        fill=(232, 214, 184, 255),
    )
    patch_rect(
        im, (62, 424, 320, 444), "重复任务（强化）",
        bg_mode="solid", bg_color=(28, 24, 20, 255), max_size=13,
        fill=(232, 214, 184, 255),
    )
    patch_rect(
        im, (20, 502, 362, 524), "完成重复任务后，奖励将通过邮件发放。",
        bg_mode="solid", bg_color=(22, 18, 16, 255), max_size=11,
        fill=(216, 198, 174, 255), bold=False,
    )
    return im


def _patch_392_onedayplan_0(im: Image.Image) -> Image.Image:
    # 0906_onedayplan.img #0 (352x490) — keep girl portrait x <= 60!
    patch_rect(
        im, (62, 6, 338, 40), "每日冒险计划",
        bg_mode="edge_v", bg_pad=2, max_size=24,
        fill=(255, 242, 132, 255), stroke_fill=(24, 62, 36, 255), stroke_width=2,
    )
    patch_rect(
        im, (108, 42, 256, 58), "长期开放",
        bg_mode="edge_v", bg_pad=2, max_size=12,
        fill=(152, 248, 148, 255),
    )
    patch_rect(
        im, (8, 62, 344, 80), "今日任务清单！ 选择今日目标领取每日奖励。",
        bg_mode="row_lr", bg_pad=2, max_size=12,
        fill=(62, 34, 14, 255), align="left",
    )
    patch_rect(
        im, (10, 212, 336, 230), "未完成的任务可以重新选择并继续进行。",
        bg_mode="solid", bg_color=(36, 26, 20, 255), max_size=11,
        fill=(218, 200, 176, 255), align="left", bold=False,
    )
    patch_rect(
        im, (8, 240, 344, 258), "累计奖励任务！ 累计完成任务领取额外好礼。",
        bg_mode="row_lr", bg_pad=2, max_size=12,
        fill=(20, 56, 48, 255), align="left",
    )
    patch_rect(
        im, (10, 464, 342, 482), "活动期间每个账号最多可完成2次累计任务。",
        bg_mode="solid", bg_color=(34, 34, 38, 255), max_size=11,
        fill=(212, 216, 222, 255), bold=False,
    )
    return im


def _patch_367_prepare_cal_0(im: Image.Image) -> Image.Image:
    # 0628_prepare_cal.img #0 (288x334) — keep character portrait on right x >= 198!
    patch_rect(
        im, (14, 6, 194, 76), "110级\n备战计划",
        bg_mode="edge_v", bg_pad=2, max_size=26,
        fill=(196, 248, 255, 255), stroke_fill=(14, 48, 86, 255), stroke_width=2,
    )
    patch_rect(
        im, (76, 82, 212, 98), "长期开放",
        bg_mode="solid", bg_color=(18, 26, 52, 255), max_size=12,
        fill=(255, 196, 206, 255),
    )
    patch_rect(
        im, (14, 108, 138, 138), "维护前可用",
        bg_mode="solid", bg_color=(24, 134, 178, 255), max_size=13,
        fill=(236, 252, 255, 255),
    )
    patch_rect(
        im, (150, 108, 274, 138), "维护后可用",
        bg_mode="solid", bg_color=(64, 116, 184, 255), max_size=13,
        fill=(236, 248, 255, 255),
    )
    patch_rect(
        im, (100, 150, 188, 186), "今日\n奖励",
        bg_mode="solid", bg_color=(138, 62, 168, 255), max_size=14,
        fill=(255, 255, 255, 255), stroke_fill=(68, 22, 96, 230), stroke_width=1,
    )
    patch_rect(
        im, (100, 196, 188, 232), "明日\n奖励",
        bg_mode="solid", bg_color=(72, 46, 104, 255), max_size=14,
        fill=(224, 206, 242, 255),
    )
    patch_rect(
        im, (82, 314, 240, 330), "奖励将通过邮件发放。",
        bg_mode="solid", bg_color=(12, 22, 46, 255), max_size=11,
        fill=(186, 206, 232, 255), align="left", bold=False,
    )
    return im


def build_localization_plan() -> dict[str, dict[str, dict[int, Callable[[Image.Image], Image.Image]]]]:
    """Return nested mapping: npk_filename -> img_member_name -> {frame_idx: patch_fn}."""
    lialab_ev_patchers: dict[int, Callable[[Image.Image], Image.Image]] = {
        0: _patch_602_lialab_ev_0,
        1: make_button_patcher("探索范围", fill=(180, 242, 255, 255), max_size=12, keep_left=4, keep_right=4, keep_top=3, keep_bottom=3),
        **button_group(range(2, 6), "探索", fill=(230, 250, 255, 255), max_size=14),
        **button_group(range(6, 10), "保存", fill=(230, 250, 255, 255), max_size=13),
        **button_group(range(10, 14), "排除", fill=(230, 250, 255, 255), max_size=13),
        **button_group(range(14, 18), "跨界", fill=(230, 250, 255, 255), max_size=13),
        **button_group(range(18, 22), "领取", fill=(230, 250, 255, 255), max_size=13),
        25: make_button_patcher("排除", fill=(255, 210, 210, 255), max_size=12, keep_left=3, keep_right=3, keep_top=3, keep_bottom=3),
        26: make_button_patcher("保存", fill=(210, 255, 220, 255), max_size=12, keep_left=3, keep_right=3, keep_top=3, keep_bottom=3),
    }

    return {
        "sprite_live_event_kor_2022_0401_reversehottime.NPK": {
            "sprite/live/event/kor/2022/0401_reversehottime/0401_reversehottime.img": {
                0: _patch_520_reversehottime_0,
                **button_group(range(3, 7), "领取", fill=(236, 246, 255, 255), max_size=13),
            },
        },
        "sprite_interface2_ui_eventcalender.NPK": {
            "sprite/interface2/ui/eventcalender/eventcalender.img": {
                13: make_button_patcher("新", fill=(255, 255, 255, 255), max_size=10, keep_left=2, keep_right=2, keep_top=1, keep_bottom=1, bg_mode="solid"),
                14: make_button_patcher("查看", fill=(255, 255, 255, 255), max_size=10, keep_left=2, keep_right=2, keep_top=1, keep_bottom=1, bg_mode="solid"),
                # Keep Seria portrait on left completely uncovered!
                22: lambda im: patch_rect(
                    im, (27, 5, 335, 29), "查看当前账号的活动材料！",
                    bg_mode="edge_v", bg_pad=2, max_size=13, fill=(248, 232, 184, 255),
                ),
                27: lambda im: patch_rect(
                    im, (36, 10, 335, 53), "查看当前账号的活动材料！",
                    bg_mode="edge_v", bg_pad=2, max_size=14, fill=(248, 232, 184, 255),
                ),
            },
        },
        "sprite_interface2_ui_eventlist.NPK": {
            "sprite/interface2/ui/eventlist/event_window.img": {
                8: make_button_patcher("点击", fill=(255, 255, 255, 255), max_size=10, keep_left=2, keep_right=2, keep_top=1, keep_bottom=1, bg_mode="solid"),
                10: make_button_patcher("点击", fill=(255, 255, 255, 255), max_size=10, keep_left=2, keep_right=2, keep_top=1, keep_bottom=1, bg_mode="solid"),
                # Keep CERA coin icon on left (x=0..28)!
                **button_group(range(11, 15), "购买", fill=(255, 236, 170, 255), max_size=13, keep_left=28, keep_right=6, keep_top=4, keep_bottom=4),
                **button_group(range(20, 24), "积分商城", fill=(255, 236, 180, 255), max_size=13, keep_left=6, keep_right=6, keep_top=4, keep_bottom=4),
            },
        },
        "sprite_live_else_univ_2022_0712_danji.NPK": {
            "sprite/live/else/univ/2022/0712_danji/0712_danji.img": {
                **button_group(range(13, 17), "开启(空格)", fill=(255, 242, 198, 255), max_size=13, keep_left=6, keep_right=6, keep_top=4, keep_bottom=4),
            },
            "sprite/live/else/univ/2022/0712_danji/0712_danji_calendar.img": {
                0: _patch_danji_cal_0,
                1: _patch_danji_cal_1,
            },
            "sprite/live/else/univ/2022/0712_danji/danji_month_cal.img": {
                0: _patch_danji_month_cal_0,
            },
        },
        "sprite_interface2_event_everydayconnect.NPK": {
            "sprite/interface2/event/everydayconnect/everydayconnect.img": {
                0: _patch_everyday_0,
                7: make_button_patcher("已完成", fill=(255, 242, 148, 255), max_size=11, keep_left=2, keep_right=2, keep_top=2, keep_bottom=2, bg_mode="solid"),
            },
            "sprite/interface2/event/everydayconnect/everydayconnect_calender.img": {
                0: _patch_everyday_cal_0,
                **button_group(range(4, 8), "立即签到！", fill=(255, 248, 210, 255), max_size=13),
            },
            "sprite/interface2/event/everydayconnect/mainbanner.img": {
                0: _patch_everyday_mainbanner("每日签到盛典！"),
                1: _patch_everyday_mainbanner("金星签到盛典！"),
            },
            "sprite/interface2/event/everydayconnect/calendarbanner.img": {
                0: _patch_everyday_calbanner("每日签到盛典！"),
                1: _patch_everyday_calbanner("金星签到盛典！"),
            },
        },
        "sprite_live_event_kor_2021_1202_dailycheck.NPK": {
            "sprite/live/event/kor/2021/1202_dailycheck/1202_dailycheck_ui.img": {
                0: _patch_619_dailycheck_ui_0,
                **button_group(range(7, 11), "领取礼物", fill=(255, 248, 224, 255), max_size=13),
                **button_group(range(11, 15), "重置礼物", fill=(242, 246, 255, 255), max_size=13),
                **button_group(range(15, 19), "领取", fill=(255, 248, 224, 255), max_size=13),
            },
            "sprite/live/event/kor/2021/1202_dailycheck/1202_dailycheck_calendar.img": {
                0: _patch_619_dailycheck_cal_0,
            },
        },
        "sprite_live_event_kor_2022_0804_securityevent.NPK": {
            "sprite/live/event/kor/2022/0804_securityevent/0804_securityevent_eventui.img": {
                0: _patch_525_security_ev_0,
                **button_group(range(1, 5), "领取", fill=(255, 255, 255, 255), max_size=13),
            },
            "sprite/live/event/kor/2022/0804_securityevent/0804_securityevent_calendarui.img": {
                0: _patch_525_security_cal_0,
                **button_group(range(1, 5), "开通安全防护", fill=(255, 255, 255, 255), max_size=13),
            },
        },
        "sprite_live_event_kor_2022_0317_newbi.NPK": {
            "sprite/live/event/kor/2022/0317_newbi/0317_newbi.img": {
                0: _patch_607_newbi_0,
                1: lambda im: patch_rect(im, (14, 10, 238, 47), "活动已结束。", bg_mode="solid", bg_color=(36, 40, 48, 215), max_size=14, fill=(240, 244, 250, 255)),
                2: lambda im: patch_rect(im, (14, 14, 238, 56), "活动已结束。", bg_mode="solid", bg_color=(36, 40, 48, 215), max_size=14, fill=(240, 244, 250, 255)),
                **button_group(range(3, 7), "查看全部福利 >", fill=(255, 255, 255, 255), max_size=13),
            },
        },
        "sprite_live_event_kor_2021_0902_lialab2.NPK": {
            "sprite/live/event/kor/2021/0902_lialab2/210902_lialab2_calendar.img": {
                0: _patch_602_lialab_cal_0,
                **button_group(range(1, 5), "前往商店", fill=(235, 250, 255, 255), max_size=13),
                **button_group(range(5, 9), "前往魔盒实验", fill=(235, 250, 255, 255), max_size=13),
            },
            "sprite/live/event/kor/2021/0902_lialab2/210902_lialab2_shop.img": {
                0: _patch_602_lialab_shop_0,
            },
        },
        "sprite_interface2_event_20200903_pickandkeepevent.NPK": {
            "sprite/live/event/kor/2021/0902_lialab2/210902_lialab2_event.img": lialab_ev_patchers,
            "sprite/interface2/event/20200903_pickandkeepevent/resource.img": lialab_ev_patchers,
            "sprite/interface2/event/20200903_pickandkeepevent/calendar.img": {
                0: _patch_pickandkeep_cal_0,
                **button_group(range(1, 5), "前往实验室", fill=(240, 255, 245, 255), max_size=12),
            },
        },
        "sprite_live_event_kor_2021_0805_melodyforadventurers.NPK": {
            "sprite/live/event/kor/2021/0805_melodyforadventurers/musicnote16t_main.img": {
                0: _patch_614_melody_main_0,
                **button_group(range(1, 5), "领取", fill=(255, 255, 255, 255), max_size=13),
                5: make_button_patcher("已完成", fill=(255, 248, 175, 255), max_size=12, keep_left=3, keep_right=3, keep_top=3, keep_bottom=3),
                36: make_button_patcher("已完成", fill=(255, 248, 175, 255), max_size=15, keep_left=6, keep_right=6, keep_top=6, keep_bottom=6),
            },
            "sprite/live/event/kor/2021/0805_melodyforadventurers/musicnote16t_calendar.img": {
                0: _patch_614_melody_cal_0,
            },
            "sprite/live/event/kor/2021/0805_melodyforadventurers/16th_shopcalender.img": {
                0: _patch_614_shop_cal_0,
                **button_group(range(1, 5), "前往周年纪念商店！", fill=(42, 36, 16, 255), stroke_fill=None, stroke_width=0, max_size=14),
            },
        },
        "sprite_live_event_kor_2021_1014_dungeonmining.NPK": {
            "sprite/live/event/kor/2021/1014_dungeonmining/dungeonmining_evnet.img": {
                1: _patch_512_mining_1,
                2: _patch_512_mining_2,
                **button_group(range(12, 16), "挖矿", fill=(255, 244, 210, 255), max_size=11, keep_left=2, keep_right=2, keep_top=2, keep_bottom=2),
                **button_group(range(16, 20), "开采", fill=(255, 244, 210, 255), max_size=12, keep_left=3, keep_right=3, keep_top=2, keep_bottom=2),
                20: make_button_patcher("已完成", fill=(255, 242, 150, 255), max_size=12, keep_left=3, keep_right=3, keep_top=3, keep_bottom=3),
                21: make_button_patcher("已完成", fill=(255, 242, 150, 255), max_size=12, keep_left=3, keep_right=3, keep_top=3, keep_bottom=3),
            },
            "sprite/live/event/kor/2021/1014_dungeonmining/dungeonmining_calendar.img": {
                0: _patch_512_mining_cal_0,
            },
        },
        "sprite_interface2_event_20210422_kittykingdom.NPK": {
            "sprite/interface2/event/20210422_kittykingdom/210422_kittykingdomattendance.img": {
                0: _patch_367_kitty_0,
                **button_group(range(1, 5), "领取", fill=(255, 255, 255, 255), max_size=12, keep_left=3, keep_right=3, keep_top=2, keep_bottom=2),
                8: make_button_patcher("奖励", fill=(255, 248, 175, 255), max_size=11, keep_left=2, keep_right=2, keep_top=1, keep_bottom=1, bg_mode="solid"),
                12: lambda im: patch_rect(im, (14, 18, 260, 66), "今天不是活动参与日喵！", bg_mode="solid", max_size=15, fill=(255, 246, 224, 255)),
                13: _patch_367_kitty_13,
            },
            "sprite/interface2/event/20210422_kittykingdom/210422_kittykingdomrental.img": {
                0: _patch_367_kitty_rental_0,
                **button_group(range(1, 5), "前往露娜处", fill=(255, 255, 255, 255), max_size=14),
            },
        },
        "sprite_interface2_event_20200709_forgeofsouls.NPK": {
            "sprite/interface2/event/20200709_forgeofsouls/20200709_forgeofsouls_calendar.img": {
                0: _patch_500_forge_cal_0,
                1: _patch_500_forge_cal_1,
            },
            "sprite/interface2/event/20200709_forgeofsouls/20200709_forgeofsouls.img": {
                0: _patch_500_forge_main_0,
                2: _patch_500_forge_main_2,
            },
        },
        "sprite_live_event_univ_2022_0906_onedayplan.NPK": {
            "sprite/live/event/univ/2022/0906_onedayplan/0906_onedayplan.img": {
                0: _patch_392_onedayplan_0,
                4: lambda im: patch_rect(im, (4, 46, 76, 72), "已完成", bg_mode="edge_v", bg_pad=2, max_size=15, fill=(255, 255, 255, 255), stroke_fill=(130, 82, 16, 230), stroke_width=1),
                7: lambda im: patch_rect(im, (124, 6, 228, 36), "已完成", bg_mode="row_lr", bg_pad=2, max_size=16, fill=(255, 255, 255, 255), stroke_fill=(14, 118, 66, 230), stroke_width=1),
            },
        },
        "sprite_live_event_univ_2022_0628_prepare.NPK": {
            "sprite/live/event/univ/2022/0628_prepare/0628_prepare_cal.img": {
                0: _patch_367_prepare_cal_0,
                **button_group(range(1, 5), "每日签到", fill=(255, 255, 255, 255), max_size=14),
                5: lambda im: patch_rect(im, (10, 6, 262, 36), "新版本 · 全新征程即将开启！", bg_mode="row_lr", bg_pad=2, max_size=15, fill=(224, 210, 255, 255), stroke_fill=(44, 22, 82, 230), stroke_width=1),
            },
        },
    }


@dataclass(frozen=True)
class BuildReport:
    npk_name: str
    img_count: int
    frame_count: int
    out_path: Path
    sha256: str


def _save_preview_sheet(
    title: str,
    rendered_frames: list[tuple[str, int, Image.Image]],
    out_png: Path,
) -> None:
    if not rendered_frames:
        return
    max_w = max(920, max(im.width for _, _, im in rendered_frames) + 40)
    x, y, row_h = 12, 28, 0
    placed = []
    for img_name, idx, im in rendered_frames:
        if x + im.width + 12 > max_w and x > 12:
            x = 12
            y += row_h + 24
            row_h = 0
        placed.append((x, y, img_name, idx, im))
        x += im.width + 12
        row_h = max(row_h, im.height)
    canvas = Image.new("RGBA", (max_w, y + row_h + 28), (26, 28, 36, 255))
    d = ImageDraw.Draw(canvas)
    d.text((12, 6), title, fill=(255, 220, 120))
    for px, py, img_name, idx, im in placed:
        d.rectangle([px - 1, py - 1, px + im.width, py + im.height], outline=(90, 145, 210))
        canvas.alpha_composite(im, (px, py))
        short = Path(img_name).name
        d.text((px, py + im.height + 2), f"{short} #{idx} ({im.width}x{im.height})", fill=(215, 220, 230))
    out_png.parent.mkdir(parents=True, exist_ok=True)
    canvas.save(out_png)


def build_all(
    source_packs_dir: Path,
    build_dir: Path,
    preview_dir: Path | None = None,
    backup_dir: Path | None = None,
) -> list[BuildReport]:
    """Build localized copies of all planned NPKs using clean original sources (backup_dir preferred if present)."""
    plan = build_localization_plan()
    build_dir.mkdir(parents=True, exist_ok=True)
    reports: list[BuildReport] = []
    for npk_name, img_specs in plan.items():
        # Always read from original backup if it already exists so repeated runs stay idempotent!
        src = (backup_dir / npk_name) if (backup_dir and (backup_dir / npk_name).is_file()) else (source_packs_dir / npk_name)
        if not src.is_file():
            raise FileNotFoundError(f"Missing source NPK: {src}")
        rebuilt_npk, preview_items = patch_npk_in_place(src, img_specs)
        frame_total = len(preview_items)
        out_path = build_dir / npk_name
        out_path.write_bytes(rebuilt_npk)
        # Verify rebuilt NPK matches original index layout 100% & every patched IMG decodes cleanly
        if atlas.index(out_path) != atlas.index(src):
            raise ValueError(f"NPK index table drifted for {npk_name}")
        for img_name in img_specs:
            atlas.split_img(atlas.member(out_path, img_name))
        if preview_dir is not None:
            _save_preview_sheet(npk_name, preview_items, preview_dir / f"{Path(npk_name).stem}.png")
        reports.append(
            BuildReport(
                npk_name=npk_name,
                img_count=len(img_specs),
                frame_count=frame_total,
                out_path=out_path,
                sha256=atlas.digest(out_path),
            )
        )
    return reports


def is_dfo_running() -> bool:
    if sys.platform != "win32":
        return False
    res = subprocess.run(
        ["tasklist", "/FI", "IMAGENAME eq DFO.exe", "/NH"],
        capture_output=True,
        text=True,
        check=False,
    )
    return "DFO.exe" in res.stdout


def install_all(
    packs_dir: Path,
    build_dir: Path,
    backup_dir: Path,
    allow_while_running: bool = False,
) -> list[BuildReport]:
    if not allow_while_running and is_dfo_running():
        raise RuntimeError("DFO.exe is currently running; close the client before installing localized NPKs.")
    backup_dir.mkdir(parents=True, exist_ok=True)
    manifest_path = backup_dir / "manifest.json"
    manifest: dict[str, dict[str, str]] = (
        json.loads(manifest_path.read_text(encoding="utf-8"))
        if manifest_path.is_file()
        else {}
    )
    # 1. Backup untouched originals first (never overwrite existing backup!)
    plan = build_localization_plan()
    for npk_name in plan:
        live = packs_dir / npk_name
        bak = backup_dir / npk_name
        if not live.is_file():
            raise FileNotFoundError(f"Live NPK missing: {live}")
        if not bak.is_file():
            shutil.copy2(live, bak)
            manifest[npk_name] = {
                "original_sha256": atlas.digest(bak),
                "installed_from": str(live),
            }
    manifest_path.write_text(json.dumps(manifest, indent=2, ensure_ascii=False), encoding="utf-8")

    # 2. Build fresh localized NPKs strictly from clean backups
    reports = build_all(packs_dir, build_dir, preview_dir=None, backup_dir=backup_dir)

    # 3. Install atomically and verify SHA-256
    for rep in reports:
        dest = packs_dir / rep.npk_name
        shutil.copy2(rep.out_path, dest)
        if atlas.digest(dest) != rep.sha256:
            raise IOError(f"SHA-256 verification failed after installing {dest}")
        manifest.setdefault(rep.npk_name, {})["localized_sha256"] = rep.sha256
    manifest_path.write_text(json.dumps(manifest, indent=2, ensure_ascii=False), encoding="utf-8")
    return reports


def rollback_all(packs_dir: Path, backup_dir: Path, allow_while_running: bool = False) -> list[str]:
    if not allow_while_running and is_dfo_running():
        raise RuntimeError("DFO.exe is currently running; close the client before rolling back NPKs.")
    manifest_path = backup_dir / "manifest.json"
    if not manifest_path.is_file():
        raise FileNotFoundError(f"Backup manifest not found: {manifest_path}")
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    restored = []
    for npk_name, meta in manifest.items():
        bak = backup_dir / npk_name
        dest = packs_dir / npk_name
        if not bak.is_file():
            raise FileNotFoundError(f"Backup NPK missing: {bak}")
        shutil.copy2(bak, dest)
        if atlas.digest(dest) != meta["original_sha256"]:
            raise IOError(f"SHA-256 mismatch restoring {dest}")
        restored.append(npk_name)
    return restored


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--packs-dir", type=Path, default=DEFAULT_CLIENT_PACKS)
    parser.add_argument("--build-dir", type=Path, default=DEFAULT_BUILD_DIR)
    parser.add_argument("--backup-dir", type=Path, default=DEFAULT_BACKUP_DIR)
    parser.add_argument("--preview-dir", type=Path, default=None)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--build", action="store_true", help="Build localized NPKs into --build-dir without modifying client.")
    mode.add_argument("--install", action="store_true", help="Backup originals and install localized NPKs into --packs-dir.")
    mode.add_argument("--rollback", action="store_true", help="Restore original NPKs from --backup-dir.")
    args = parser.parse_args(argv)

    if args.build:
        reports = build_all(args.packs_dir, args.build_dir, preview_dir=args.preview_dir, backup_dir=args.backup_dir)
        total_imgs = sum(r.img_count for r in reports)
        total_frames = sum(r.frame_count for r in reports)
        print(f"Built {len(reports)} NPKs ({total_imgs} IMGs, {total_frames} frames) into {args.build_dir}")
        for r in reports:
            print(f"  {r.npk_name}: {r.img_count} imgs, {r.frame_count} frames, sha256={r.sha256[:12]}")
        return 0
    if args.install:
        reports = install_all(args.packs_dir, args.build_dir, args.backup_dir)
        total_imgs = sum(r.img_count for r in reports)
        total_frames = sum(r.frame_count for r in reports)
        print(f"Installed {len(reports)} localized NPKs ({total_imgs} IMGs, {total_frames} frames) into {args.packs_dir}")
        print(f"Originals backed up in {args.backup_dir}")
        return 0
    restored = rollback_all(args.packs_dir, args.backup_dir)
    print(f"Restored {len(restored)} original NPKs from {args.backup_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
