"""Localize all US100 WorldMap, Dungeon Select, and Minimap NPKs into Simplified Chinese
strictly in-place (preserving 100% of NpkIndex.etc offsets/lengths and NPK SHA-256 to guarantee
zero Red-X missing sprites).

Donor priority:
  1. Official CN100 client (D:\\DNF\\client\\DNF100\\ImagePacks2)
  2. Official CN115 client (D:\\DNF\\115CN\\地下城与勇士：创新世纪\\ImagePacks2) for post-100 maps/frames
  3. Clean localized overlays for the 4 US100-only frames (north_belmire f10 Elvenmere,
     step_3(world) f17 Empyrean, step_3(world)_bg f11, minimap_minimap_town f0 World Map)

Usage:
  python tools/us100_localize_maps_ui.py --build --preview-dir runtime/map-ui-preview
  python tools/us100_localize_maps_ui.py --install
  python tools/us100_localize_maps_ui.py --verify
  python tools/us100_localize_maps_ui.py --rollback
"""
from __future__ import annotations

import argparse
from dataclasses import dataclass
from functools import lru_cache
import hashlib
import json
import os
from pathlib import Path
import shutil
import struct
import sys
import zlib

import numpy as np
from PIL import Image, ImageDraw, ImageFont

sys.path.insert(0, str(Path(__file__).resolve().parent))
import us100_ui_atlas as atlas  # noqa: E402
from us100_localize_events_ui import encode_pixels_native, verify_npk_header_sha256  # noqa: E402

DEFAULT_US_PACKS = Path(r"D:\DNF\client\100US\DFO\ImagePacks2")
DEFAULT_CN100_PACKS = Path(r"D:\DNF\client\DNF100\ImagePacks2")
DEFAULT_CN115_PACKS = Path(r"D:\DNF\115CN\地下城与勇士：创新世纪\ImagePacks2")
DEFAULT_BACKUP_DIR = Path("runtime/map-ui-localize-backup")
DEFAULT_BUILD_DIR = Path("runtime/map-ui-localize-build")
DEFAULT_PREVIEW_DIR = Path("runtime/map-ui-preview")

NPK_KEY = (b"puchikon@neople dungeon and fighter " + b"DNF" * 73)[:255] + b"\x00"

FONT_BOLD_CANDIDATES = (
    Path(r"C:\Windows\Fonts\msyhbd.ttc"),
    Path(r"C:\Windows\Fonts\simhei.ttf"),
    Path(r"C:\Windows\Fonts\msyh.ttc"),
)

# Extra NPKs that also hold non-map UI; only patch members matching the predicate.
EXTRA_SCOPED_NPKS: dict[str, str] = {
    "sprite_interface2.NPK": "worldmap",
    "sprite_interface2_dazzlementtower.NPK": "selectdungeon",
    "sprite_interface2_timetrainingroom.NPK": "selectdungeon",
    "sprite_interface2_gcontents_210128_ilrustrea.NPK": "worldmap",
}

# Skip legacy pre-Metastasis replay-only archive (old 640x480 / replay fallback)
SKIP_NPKS: set[str] = {
    "sprite_oldimageforreplay_interface2_minimap_town.NPK",
}


@lru_cache(maxsize=32)
def load_bold_font(size: int) -> ImageFont.FreeTypeFont:
    for path in FONT_BOLD_CANDIDATES:
        if path.is_file():
            return ImageFont.truetype(str(path), size=max(10, size))
    raise FileNotFoundError("No Simplified Chinese TrueType font found in C:\\Windows\\Fonts")


@dataclass
class V2Frame:
    kind: int  # 14=ARGB1555, 15=ARGB4444, 16=ARGB8888, 17=REF
    comp: int  # 5=raw, 6=zlib, or ref_index when kind==17
    w: int = 0
    h: int = 0
    x: int = 0
    y: int = 0
    cw: int = 0
    ch: int = 0
    raw: bytes = b""

    def is_ref(self) -> bool:
        return self.kind == 17

    def meta_bytes(self) -> bytes:
        if self.is_ref():
            return struct.pack("<II", 17, self.comp)
        return struct.pack(
            "<IIIIIiiII",
            self.kind,
            self.comp,
            self.w,
            self.h,
            len(self.raw),
            self.x,
            self.y,
            self.cw,
            self.ch,
        )

    def to_image(self) -> Image.Image:
        return atlas.Frame(self.meta_bytes(), self.raw).image().convert("RGBA")


def parse_img_v2(data: bytes) -> tuple[int, int, list[V2Frame]]:
    """Parse IMG v2, tolerating trailing zero-padding bytes after the last frame payload."""
    if len(data) < 32 or data[:16] != b"Neople Img File\x00":
        raise ValueError("not a Neople Img File")
    table_len, kz, ver, count = struct.unpack_from("<4I", data, 16)
    if ver != 2 or count > 8192 or 32 + table_len > len(data):
        raise ValueError(f"unsupported IMG version {ver} (count={count})")
    pos = 32
    pixels = 32 + table_len
    frames: list[V2Frame] = []
    for i in range(count):
        if pos + 8 > 32 + table_len:
            raise ValueError(f"frame {i} header truncated")
        kind, comp = struct.unpack_from("<II", data, pos)
        if kind == 17:
            frames.append(V2Frame(kind=17, comp=comp))
            pos += 8
            continue
        if kind not in (14, 15, 16) or pos + 36 > 32 + table_len:
            raise ValueError(f"frame {i} unsupported kind {kind}")
        w, h, sz, x, y, cw, ch = struct.unpack_from("<IIIiiII", data, pos + 8)
        if pixels + sz > len(data):
            raise ValueError(f"frame {i} payload truncated ({pixels + sz} > {len(data)})")
        frames.append(V2Frame(kind=kind, comp=comp, w=w, h=h, x=x, y=y, cw=cw, ch=ch, raw=data[pixels : pixels + sz]))
        pos += 36
        pixels += sz
    if pos != 32 + table_len:
        raise ValueError(f"table length mismatch: parsed {pos - 32} != {table_len}")
    # Verify any trailing bytes are zero padding only
    tail = data[pixels:]
    if tail and any(b != 0 for b in tail):
        raise ValueError(f"non-zero tail ({len(tail)} bytes) after IMG v2 frames")
    return kz, ver, frames


def encode_img_v2(kz: int, ver: int, frames: list[V2Frame]) -> bytes:
    table = b"".join(f.meta_bytes() for f in frames)
    body = b"".join(f.raw for f in frames if not f.is_ref())
    header = b"Neople Img File\x00" + struct.pack("<4I", len(table), kz, ver, len(frames))
    return header + table + body


def resolve_v2_frame(frames: list[V2Frame], idx: int, depth: int = 0) -> V2Frame | None:
    if idx < 0 or idx >= len(frames) or depth > 16:
        return None
    f = frames[idx]
    if f.is_ref():
        return resolve_v2_frame(frames, f.comp, depth + 1)
    return f


def reencode_frame(
    f: V2Frame,
    im: Image.Image | None = None,
    *,
    target_kind: int | None = None,
    quant_step: int = 0,
    level: int = 9,
) -> V2Frame:
    kind = target_kind if target_kind is not None else f.kind
    if im is None:
        im = f.to_image()
    rgba = im.convert("RGBA")
    arr = np.asarray(rgba).copy()
    # Zero out invisible RGB channels so zlib achieves significantly better lossless compression
    zero_a = arr[:, :, 3] == 0
    if np.any(zero_a):
        arr[zero_a] = 0
    if quant_step > 1:
        half = quant_step // 2
        vis = ~zero_a
        rgb = arr[vis, :3].astype(np.int16)
        arr[vis, :3] = np.minimum(255, ((rgb + half) // quant_step) * quant_step).astype(np.uint8)
    raw_pixels = encode_pixels_native(Image.fromarray(arr, "RGBA"), kind)
    comp_bytes = zlib.compress(raw_pixels, level=level)
    return V2Frame(
        kind=kind,
        comp=6,
        w=rgba.width,
        h=rgba.height,
        x=f.x,
        y=f.y,
        cw=f.cw,
        ch=f.ch,
        raw=comp_bytes,
    )


def fit_img_v2_exact_size(
    kz: int,
    ver: int,
    frames: list[V2Frame],
    target_size: int,
    replaced_indices: set[int],
    orig_kinds: dict[int, int],
) -> bytes:
    """Encode IMG v2 into EXACT `target_size` bytes so NPK member offsets/sizes stay 100% unchanged."""
    work = list(frames)
    blob = encode_img_v2(kz, ver, work)

    # Pass 1: Lossless zlib-9 + invisible-RGB cleanup on replaced frames (largest first)
    if len(blob) > target_size:
        order = sorted(
            (i for i in replaced_indices if not work[i].is_ref()),
            key=lambda i: len(work[i].raw),
            reverse=True,
        )
        for i in order:
            cand = reencode_frame(work[i], level=9)
            if len(cand.raw) < len(work[i].raw):
                work[i] = cand
                blob = encode_img_v2(kz, ver, work)
                if len(blob) <= target_size:
                    break

    # Pass 2: If a replaced frame uses 32-bit ARGB8888 (kind 16) while US used 16-bit (14/15), match US native kind
    if len(blob) > target_size:
        order = sorted(
            (i for i in replaced_indices if not work[i].is_ref() and work[i].kind == 16 and orig_kinds.get(i) in (14, 15)),
            key=lambda i: len(work[i].raw),
            reverse=True,
        )
        for i in order:
            cand = reencode_frame(work[i], target_kind=orig_kinds[i], level=9)
            if len(cand.raw) < len(work[i].raw):
                work[i] = cand
                blob = encode_img_v2(kz, ver, work)
                if len(blob) <= target_size:
                    break

    # Pass 3: Losslessly recompress untouched frames at zlib-9 (largest first) to reclaim slack
    if len(blob) > target_size:
        order = sorted(
            (i for i, f in enumerate(work) if not f.is_ref() and i not in replaced_indices),
            key=lambda i: len(work[i].raw),
            reverse=True,
        )
        for i in order:
            cand = reencode_frame(work[i], level=9)
            if len(cand.raw) < len(work[i].raw):
                work[i] = cand
                blob = encode_img_v2(kz, ver, work)
                if len(blob) <= target_size:
                    break

    # Pass 4: Gentle progressive bit-rounding on replaced frames only (preserves crisp text)
    if len(blob) > target_size:
        cached_im = {i: work[i].to_image() for i in replaced_indices if not work[i].is_ref()}
        for step in (4, 8, 12, 16, 24, 32):
            order = sorted(cached_im.keys(), key=lambda i: len(work[i].raw), reverse=True)
            for i in order:
                cand = reencode_frame(work[i], cached_im[i], quant_step=step, level=9)
                if len(cand.raw) < len(work[i].raw):
                    work[i] = cand
                    blob = encode_img_v2(kz, ver, work)
                    if len(blob) <= target_size:
                        break
            if len(blob) <= target_size:
                break

    # Pass 5: Convert large replaced ARGB8888 frames to ARGB4444 (kind 15) if still over budget
    if len(blob) > target_size:
        cached_im = {i: work[i].to_image() for i in replaced_indices if not work[i].is_ref() and work[i].kind == 16}
        for step in (0, 8, 16):
            order = sorted(cached_im.keys(), key=lambda i: len(work[i].raw), reverse=True)
            for i in order:
                cand = reencode_frame(work[i], cached_im[i], target_kind=15, quant_step=step, level=9)
                if len(cand.raw) < len(work[i].raw):
                    work[i] = cand
                    blob = encode_img_v2(kz, ver, work)
                    if len(blob) <= target_size:
                        break
            if len(blob) <= target_size:
                break

    # Pass 6: Gentle rounding on untouched frames inside the same IMG as last resort
    if len(blob) > target_size:
        untouched = [i for i, f in enumerate(work) if not f.is_ref() and i not in replaced_indices]
        cached_u = {i: work[i].to_image() for i in untouched}
        for step in (8, 16, 24):
            order = sorted(cached_u.keys(), key=lambda i: len(work[i].raw), reverse=True)
            for i in order:
                cand = reencode_frame(work[i], cached_u[i], quant_step=step, level=9)
                if len(cand.raw) < len(work[i].raw):
                    work[i] = cand
                    blob = encode_img_v2(kz, ver, work)
                    if len(blob) <= target_size:
                        break
            if len(blob) <= target_size:
                break

    if len(blob) > target_size:
        raise RuntimeError(f"IMG v2 still exceeds target_size: {len(blob)} > {target_size}")
    if len(blob) < target_size:
        blob += b"\x00" * (target_size - len(blob))
    return blob


def _draw_outlined_text(
    im: Image.Image,
    text: str,
    cx: int,
    cy: int,
    size: int,
    fill: tuple[int, int, int, int],
    outline: tuple[int, int, int, int] = (18, 10, 6, 255),
    stroke_w: int = 2,
) -> None:
    draw = ImageDraw.Draw(im)
    font = load_bold_font(size)
    bb = draw.textbbox((0, 0), text, font=font, stroke_width=stroke_w)
    tw, th = bb[2] - bb[0], bb[3] - bb[1]
    x = cx - tw // 2 - bb[0]
    y = cy - th // 2 - bb[1]
    draw.text((x, y), text, font=font, fill=fill, stroke_width=stroke_w, stroke_fill=outline)


def _patch_elvenmere_frame(uf: V2Frame) -> V2Frame:
    """Localize north_belmire.img frame 10 ('Elvenmere' -> '艾尔文泪湖') without CN115's blank placeholder."""
    im = uf.to_image()
    arr = np.asarray(im).copy()
    # Erase 'Elvenmere' text inside the parchment circle (x: 16..98, y: 94..119) by sampling vertical parchment rows
    x0, y0, x1, y1 = 16, 94, 98, 118
    for x in range(x0, x1):
        # Average clean parchment rows directly above (y=90..93) and below (y=119..121)
        top = arr[89:93, x, :].astype(np.float32).mean(axis=0)
        bot = arr[119:122, x, :].astype(np.float32).mean(axis=0)
        for y in range(y0, y1):
            if arr[y, x, 3] > 20:
                t = (y - y0) / max(1, (y1 - y0 - 1))
                blended = top * (1.0 - t) + bot * t
                arr[y, x, :3] = np.clip(blended[:3], 0, 255).astype(np.uint8)
    out = Image.fromarray(arr, "RGBA")
    _draw_outlined_text(
        out,
        "艾尔文泪湖",
        cx=56,
        cy=105,
        size=15,
        fill=(248, 244, 232, 255),
        outline=(20, 12, 8, 255),
        stroke_w=2,
    )
    return reencode_frame(uf, out, level=9)


def _patch_step3_world_f17(uf: V2Frame, cn100_frames: list[V2Frame]) -> V2Frame:
    """Transplant CN100's official '天界' corner label from step_3(world).img f15 onto US f17."""
    im = uf.to_image()
    ref_im = cn100_frames[15].to_image()
    # Bottom-right label region ('Empyrean' -> '天界'): x: 660..795, y: 278..334
    box = (660, 278, 795, 334)
    im.paste(ref_im.crop(box), box[:2])
    return reencode_frame(uf, im, level=9)


def _patch_step3_world_bg_f11(uf: V2Frame, cn100_frames: list[V2Frame]) -> V2Frame:
    """Transplant CN100's official '魔界' / '天界' / '阿拉德' labels from step_3(world)_bg.img f10 onto US f11."""
    im = uf.to_image()
    ref_im = cn100_frames[10].to_image()
    for box in (
        (615, 42, 795, 86),   # Pandemonium -> 魔界
        (655, 290, 795, 342), # Empyrean -> 天界
        (705, 505, 795, 558), # Arad -> 阿拉德
    ):
        im.paste(ref_im.crop(box), box[:2])
    return reencode_frame(uf, im, level=9)


def _patch_minimap_town_header(uf: V2Frame) -> V2Frame:
    """Localize minimap_minimap_town.img frame 0 top-left 'World Map' emblem -> '世界地图'."""
    im = uf.to_image()
    arr = np.asarray(im).copy()
    # Erase 'World Map' text at x: 14..122, y: 3..29 by copying clean dark header bar strip from x: 130..238
    w_patch = 108
    arr[3:29, 14 : 14 + w_patch, :] = arr[3:29, 130 : 130 + w_patch, :]
    out = Image.fromarray(arr, "RGBA")
    _draw_outlined_text(
        out,
        "世界地图",
        cx=62,
        cy=15,
        size=16,
        fill=(228, 198, 132, 255),
        outline=(22, 14, 8, 255),
        stroke_w=2,
    )
    return reencode_frame(uf, out, level=9)


def _fit_donor_to_us_canvas(uf: V2Frame, cf: V2Frame) -> V2Frame | None:
    """Return a V2Frame using CN donor pixels that is geometry-safe inside US frame `uf`."""
    if uf.w > 4 and uf.h > 4 and cf.w <= 2 and cf.h <= 2:
        return None  # never overwrite a real US sprite with a 1x1 CN blank placeholder

    if (uf.cw, uf.ch) == (cf.cw, cf.ch):
        if abs(cf.x - uf.x) > 180 or abs(cf.y - uf.y) > 180:
            return None
        return V2Frame(
            kind=cf.kind,
            comp=cf.comp,
            w=cf.w,
            h=cf.h,
            x=cf.x,
            y=cf.y,
            cw=uf.cw,
            ch=uf.ch,
            raw=cf.raw,
        )

    # Allow small UI badge/label canvas differences (e.g. selectdungeon02 difficulty icons 20x19 vs 19x19)
    # by centering CN pixels onto US's exact (ucw, uch) canvas so XUI bounds never shift.
    if abs(cf.cw - uf.cw) <= 28 and abs(cf.ch - uf.ch) <= 16 and cf.w <= uf.cw + 12 and cf.h <= uf.ch + 8:
        src_im = cf.to_image()
        if src_im.width > uf.cw or src_im.height > uf.ch:
            scale = min(uf.cw / max(1, src_im.width), uf.ch / max(1, src_im.height))
            nw = max(1, int(round(src_im.width * scale)))
            nh = max(1, int(round(src_im.height * scale)))
            src_im = src_im.resize((nw, nh), Image.Resampling.LANCZOS)
        nx = max(0, min(uf.cw - src_im.width, (uf.cw - src_im.width) // 2))
        ny = max(0, min(uf.ch - src_im.height, (uf.ch - src_im.height) // 2))
        stub = V2Frame(kind=cf.kind, comp=6, w=src_im.width, h=src_im.height, x=nx, y=ny, cw=uf.cw, ch=uf.ch)
        return reencode_frame(stub, src_im, level=9)

    return None


def discover_target_npks(us_packs: Path) -> list[tuple[str, str | None]]:
    """Return sorted list of (npk_name, optional_img_substring_filter)."""
    out: list[tuple[str, str | None]] = []
    for f in sorted(us_packs.iterdir()):
        if f.suffix.lower() != ".npk":
            continue
        name = f.name
        if name in SKIP_NPKS:
            continue
        fl = name.lower()
        if "worldmap" in fl or "minimap" in fl or "selectdungeon" in fl:
            out.append((name, None))
    for name, sub in sorted(EXTRA_SCOPED_NPKS.items()):
        if (us_packs / name).exists() and all(x[0] != name for x in out):
            out.append((name, sub))
    return out


def _npk_member_table(raw: bytes) -> list[tuple[int, int, str]]:
    if len(raw) < 20 or raw[:16] != b"NeoplePack_Bill\x00":
        raise ValueError("invalid NPK header")
    count = struct.unpack_from("<I", raw, 16)[0]
    entries: list[tuple[int, int, str]] = []
    pos = 20
    for _ in range(count):
        off, sz = struct.unpack_from("<II", raw, pos)
        enc = raw[pos + 8 : pos + 264]
        name = bytes(enc[j] ^ NPK_KEY[j] for j in range(256)).split(b"\x00", 1)[0].decode("cp949", "replace").lower()
        entries.append((off, sz, name))
        pos += 264
    return entries


def _v5_frame_count(raw: bytes) -> int | None:
    if len(raw) < 40 or raw[:16] != b"Neople Img File\x00":
        return None
    ver = struct.unpack_from("<I", raw, 24)[0]
    if ver != 5:
        return None
    return struct.unpack_from("<I", raw, 28)[0]


def patch_map_npk_in_place(
    us_npk: Path,
    cn100_npk: Path | None,
    cn115_npk: Path | None,
    *,
    img_filter: str | None = None,
) -> tuple[bytes, dict]:
    """Patch all eligible map IMGs inside `us_npk` strictly in-place, preserving every member offset/size."""
    orig_bytes = us_npk.read_bytes()
    buf = bytearray(orig_bytes)
    u_entries = _npk_member_table(orig_bytes)

    c100_raw_bytes = cn100_npk.read_bytes() if cn100_npk and cn100_npk.exists() else None
    c115_raw_bytes = cn115_npk.read_bytes() if cn115_npk and cn115_npk.exists() else None
    c100_map = {n: (o, s) for o, s, n in _npk_member_table(c100_raw_bytes)} if c100_raw_bytes else {}
    c115_map = {n: (o, s) for o, s, n in _npk_member_table(c115_raw_bytes)} if c115_raw_bytes else {}

    stats = {
        "npk": us_npk.name,
        "imgs_patched": 0,
        "frames_replaced": 0,
        "frames_custom": 0,
        "v5_patched": 0,
        "skipped_imgs": [],
        "patched_img_names": [],
    }

    seen_spans: set[tuple[int, int]] = set()
    for off, sz, img in u_entries:
        if img_filter and img_filter not in img:
            continue
        if (off, sz) in seen_spans:
            continue
        seen_spans.add((off, sz))

        u_raw = orig_bytes[off : off + sz]
        if len(u_raw) < 32 or u_raw[:16] != b"Neople Img File\x00":
            continue
        ver = struct.unpack_from("<I", u_raw, 24)[0]

        c100_raw = c100_raw_bytes[c100_map[img][0] : c100_map[img][0] + c100_map[img][1]] if img in c100_map else None
        c115_raw = c115_raw_bytes[c115_map[img][0] : c115_map[img][0] + c115_map[img][1]] if img in c115_map else None

        # Safe IMG v5 in-place replacement only when CN100 has exact same v5 version, exact same frame count, and fits <= sz.
        if ver == 5:
            u_cnt = _v5_frame_count(u_raw)
            for cand in (c100_raw, c115_raw):
                if cand is None or cand == u_raw:
                    continue
                if _v5_frame_count(cand) == u_cnt and len(cand) <= sz:
                    padded = cand + b"\x00" * (sz - len(cand))
                    buf[off : off + sz] = padded
                    stats["imgs_patched"] += 1
                    stats["v5_patched"] += 1
                    stats["patched_img_names"].append(img)
                    break
            continue

        if ver != 2:
            continue
        if (c100_raw is None and c115_raw is None) or c100_raw == u_raw:
            continue

        kz, u_ver, u_frames = parse_img_v2(u_raw)
        c100_frames: list[V2Frame] | None = None
        c115_frames: list[V2Frame] | None = None
        if c100_raw is not None:
            try:
                _, _, c100_frames = parse_img_v2(c100_raw)
            except ValueError:
                c100_frames = None
        if c115_raw is not None:
            try:
                _, _, c115_frames = parse_img_v2(c115_raw)
            except ValueError:
                c115_frames = None

        if c100_frames is None and c115_frames is None:
            continue

        new_frames = list(u_frames)
        orig_kinds = {i: f.kind for i, f in enumerate(u_frames) if not f.is_ref()}
        replaced_indices: set[int] = set()
        custom_count = 0

        for i, uf in enumerate(u_frames):
            if uf.is_ref():
                continue

            # Custom high-precision patches for the 4 US100-only frames lacking CN donors
            if img.endswith("/north_belmire.img") and i == 10:
                new_frames[i] = _patch_elvenmere_frame(uf)
                replaced_indices.add(i)
                custom_count += 1
                continue
            if img.endswith("/step_3(world).img") and i == 17 and c100_frames and len(c100_frames) > 15:
                new_frames[i] = _patch_step3_world_f17(uf, c100_frames)
                replaced_indices.add(i)
                custom_count += 1
                continue
            if img.endswith("/step_3(world)_bg.img") and i == 11 and c100_frames and len(c100_frames) > 10:
                new_frames[i] = _patch_step3_world_bg_f11(uf, c100_frames)
                replaced_indices.add(i)
                custom_count += 1
                continue
            if img.endswith("/minimap_minimap_town.img") and i == 0:
                new_frames[i] = _patch_minimap_town_header(uf)
                replaced_indices.add(i)
                custom_count += 1
                continue

            donor_frame: V2Frame | None = None
            for c_frames in (c100_frames, c115_frames):
                if c_frames is None or i >= len(c_frames):
                    continue
                cf = resolve_v2_frame(c_frames, i)
                if cf is None:
                    continue
                fitted = _fit_donor_to_us_canvas(uf, cf)
                if fitted is not None:
                    donor_frame = fitted
                    break

            if donor_frame is None:
                continue
            if donor_frame.meta_bytes() == uf.meta_bytes() and donor_frame.raw == uf.raw:
                continue
            new_frames[i] = donor_frame
            replaced_indices.add(i)

        if not replaced_indices:
            continue

        try:
            patched_img = fit_img_v2_exact_size(kz, u_ver, new_frames, sz, replaced_indices, orig_kinds)
        except RuntimeError as exc:
            stats["skipped_imgs"].append(f"{img}: {exc}")
            continue

        # Round-trip sanity check on every patched IMG v2 before committing bytes
        _, rt_ver, rt_frames = parse_img_v2(patched_img)
        if rt_ver != 2 or len(rt_frames) != len(u_frames) or len(patched_img) != sz:
            raise AssertionError(f"round-trip structural drift on {img}")
        for idx in replaced_indices:
            f = rt_frames[idx]
            if f.is_ref():
                raise AssertionError(f"replaced frame {idx} unexpectedly became ref in {img}")
            bpp = 4 if f.kind == 16 else 2
            dec = zlib.decompress(f.raw) if f.comp == 6 else f.raw
            if len(dec) != f.w * f.h * bpp:
                raise AssertionError(f"decompressed pixel length mismatch on {img}[{idx}]")

        buf[off : off + sz] = patched_img
        stats["imgs_patched"] += 1
        stats["frames_replaced"] += len(replaced_indices)
        stats["frames_custom"] += custom_count
        stats["patched_img_names"].append(img)

    patched_bytes = bytes(buf)
    if len(patched_bytes) != len(orig_bytes):
        raise AssertionError(f"NPK size drift on {us_npk.name}")
    first_off = min(o for o, _, _ in u_entries) if u_entries else len(orig_bytes)
    if patched_bytes[:first_off] != orig_bytes[:first_off]:
        raise AssertionError(f"NPK header/SHA-256 modified on {us_npk.name}")
    if verify_npk_header_sha256(orig_bytes) and not verify_npk_header_sha256(patched_bytes):
        raise AssertionError(f"NPK SHA-256 verification failed on {us_npk.name}")
    return patched_bytes, stats


def render_previews(build_dir: Path, us_packs: Path, backup_dir: Path, preview_dir: Path) -> None:
    preview_dir.mkdir(parents=True, exist_ok=True)

    def _src(name: str) -> Path:
        b = backup_dir / name
        return b if b.exists() else us_packs / name

    def _dst(name: str) -> Path:
        p = build_dir / name
        return p if p.exists() else us_packs / name

    # 1. Composite South Bel Myre (exact screenshot region!) on top of step_1(town)_bg frame 1
    npk1 = "sprite_interface2_worldmap_step_1(town).NPK"
    if (build_dir / npk1).exists():
        dst_raw = (build_dir / npk1).read_bytes()
        dmap = {n: (o, s) for o, s, n in _npk_member_table(dst_raw)}
        bg_o, bg_s = dmap["sprite/interface2/worldmap/step_1(town)/step_1(town)_bg.img"]
        sb_o, sb_s = dmap["sprite/interface2/worldmap/step_1(town)/south_belmere.img"]
        _, _, bg_fr = parse_img_v2(dst_raw[bg_o : bg_o + bg_s])
        _, _, sb_fr = parse_img_v2(dst_raw[sb_o : sb_o + sb_s])
        canvas = Image.new("RGBA", (800, 567), (12, 16, 22, 255))
        bg0 = bg_fr[1].to_image()
        canvas.alpha_composite(bg0, (bg_fr[1].x, bg_fr[1].y))
        for f in sb_fr:
            if not f.is_ref():
                canvas.alpha_composite(f.to_image(), (f.x, f.y))
        # Overlay minimap_town header & footer!
        npk_m = "sprite_interface2_minimap_town.NPK"
        if (build_dir / npk_m).exists():
            mraw = (build_dir / npk_m).read_bytes()
            mmap = {n: (o, s) for o, s, n in _npk_member_table(mraw)}
            mo, ms = mmap["sprite/interface2/minimap/town/minimap_minimap_town.img"]
            _, _, mfr = parse_img_v2(mraw[mo : mo + ms])
            full = Image.new("RGBA", (800, 600), (12, 16, 22, 255))
            full.alpha_composite(canvas, (0, 31))
            full.alpha_composite(mfr[0].to_image(), (0, 0))
            full.alpha_composite(mfr[54].to_image(), (mfr[54].x, 574))
            full.save(preview_dir / "after_south_belmere_full.png")
        else:
            canvas.save(preview_dir / "after_south_belmere_full.png")

        # Also save north_belmire f10 (Elvenmere -> 艾尔文泪湖)
        nb_o, nb_s = dmap["sprite/interface2/worldmap/step_1(town)/north_belmire.img"]
        _, _, nb_fr = parse_img_v2(dst_raw[nb_o : nb_o + nb_s])
        nb_fr[10].to_image().save(preview_dir / "after_north_belmire_f10.png")

    # 2. step_3(world) f17 & bg f11
    npk3 = "sprite_interface2_worldmap_step_3(world).NPK"
    if (build_dir / npk3).exists():
        dst_raw = (build_dir / npk3).read_bytes()
        dmap = {n: (o, s) for o, s, n in _npk_member_table(dst_raw)}
        w_o, w_s = dmap["sprite/interface2/worldmap/step_3(world)/step_3(world).img"]
        b_o, b_s = dmap["sprite/interface2/worldmap/step_3(world)/step_3(world)_bg.img"]
        _, _, wfr = parse_img_v2(dst_raw[w_o : w_o + w_s])
        _, _, bfr = parse_img_v2(dst_raw[b_o : b_o + b_s])
        wfr[17].to_image().save(preview_dir / "after_step3_world_f17.png")
        bfr[11].to_image().save(preview_dir / "after_step3_world_bg_f11.png")


def build_all(
    us_packs: Path,
    cn100_packs: Path,
    cn115_packs: Path,
    backup_dir: Path,
    build_dir: Path,
    preview_dir: Path | None = None,
) -> dict:
    build_dir.mkdir(parents=True, exist_ok=True)
    targets = discover_target_npks(us_packs)
    summary = {
        "targets_scanned": len(targets),
        "npks_patched": 0,
        "imgs_patched": 0,
        "frames_replaced": 0,
        "frames_custom": 0,
        "v5_patched": 0,
        "skipped_imgs": [],
        "files": [],
    }
    for name, img_filter in targets:
        clean_src = (backup_dir / name) if (backup_dir / name).exists() else (us_packs / name)
        cn100 = cn100_packs / name if cn100_packs.exists() else None
        cn115 = cn115_packs / name if cn115_packs.exists() else None
        patched, st = patch_map_npk_in_place(clean_src, cn100, cn115, img_filter=img_filter)
        if st["imgs_patched"] > 0:
            out_path = build_dir / name
            out_path.write_bytes(patched)
            summary["npks_patched"] += 1
            summary["imgs_patched"] += st["imgs_patched"]
            summary["frames_replaced"] += st["frames_replaced"]
            summary["frames_custom"] += st["frames_custom"]
            summary["v5_patched"] += st["v5_patched"]
            summary["files"].append(
                {
                    "npk": name,
                    "size": len(patched),
                    "sha256": hashlib.sha256(patched).hexdigest(),
                    "imgs_patched": st["imgs_patched"],
                    "frames_replaced": st["frames_replaced"],
                    "v5_patched": st["v5_patched"],
                }
            )
        if st["skipped_imgs"]:
            summary["skipped_imgs"].extend(st["skipped_imgs"])

    (build_dir / "manifest.json").write_text(json.dumps(summary, indent=2, ensure_ascii=False), encoding="utf-8")
    if preview_dir is not None:
        render_previews(build_dir, us_packs, backup_dir, preview_dir)
    return summary


def install_all(us_packs: Path, backup_dir: Path, build_dir: Path) -> dict:
    manifest_path = build_dir / "manifest.json"
    if not manifest_path.exists():
        raise FileNotFoundError(f"build manifest missing: {manifest_path} (run --build first)")
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    backup_dir.mkdir(parents=True, exist_ok=True)
    installed = 0
    for row in manifest["files"]:
        name = row["npk"]
        src = build_dir / name
        dst = us_packs / name
        bak = backup_dir / name
        if not bak.exists():
            shutil.copy2(dst, bak)
        # Final pre-install guard: exact size + exact header + valid SHA-256 vs clean backup
        orig = bak.read_bytes()
        cand = src.read_bytes()
        if len(cand) != len(orig):
            raise RuntimeError(f"refusing to install {name}: size mismatch {len(cand)} != {len(orig)}")
        u_entries = _npk_member_table(orig)
        first_off = min(o for o, _, _ in u_entries) if u_entries else len(orig)
        if cand[:first_off] != orig[:first_off] or (verify_npk_header_sha256(orig) and not verify_npk_header_sha256(cand)):
            raise RuntimeError(f"refusing to install {name}: header/SHA-256 guard failed")
        tmp = dst.with_suffix(dst.suffix + ".tmp")
        tmp.write_bytes(cand)
        os.replace(tmp, dst)
        installed += 1
    return {"installed_npks": installed, "backup_dir": str(backup_dir)}


def verify_installed(us_packs: Path, backup_dir: Path, build_dir: Path) -> dict:
    manifest = json.loads((build_dir / "manifest.json").read_text(encoding="utf-8"))
    verified = 0
    for row in manifest["files"]:
        name = row["npk"]
        live = (us_packs / name).read_bytes()
        bak = (backup_dir / name).read_bytes()
        if len(live) != len(bak):
            raise RuntimeError(f"{name}: live size {len(live)} != backup {len(bak)}")
        entries = _npk_member_table(bak)
        first_off = min(o for o, _, _ in entries) if entries else len(bak)
        if live[:first_off] != bak[:first_off]:
            raise RuntimeError(f"{name}: directory table or SHA-256 drifted from NpkIndex.etc baseline")
        if verify_npk_header_sha256(bak) and not verify_npk_header_sha256(live):
            raise RuntimeError(f"{name}: invalid 32-byte NPK SHA-256")
        if hashlib.sha256(live).hexdigest() != row["sha256"]:
            raise RuntimeError(f"{name}: live sha256 mismatch vs built manifest")
        # Parse every IMG v2 member in live NPK to guarantee zero corrupt sprites
        seen: set[tuple[int, int]] = set()
        for off, sz, img in entries:
            if (off, sz) in seen:
                continue
            seen.add((off, sz))
            m = live[off : off + sz]
            if len(m) >= 32 and m[:16] == b"Neople Img File\x00":
                ver = struct.unpack_from("<I", m, 24)[0]
                if ver == 2:
                    _, _, frames = parse_img_v2(m)
                    for idx, f in enumerate(frames):
                        if f.is_ref():
                            if f.comp < 0 or f.comp >= len(frames):
                                raise RuntimeError(f"{name}:{img}[{idx}] out-of-range ref {f.comp}")
                        elif f.comp == 6:
                            dec = zlib.decompress(f.raw)
                            bpp = 4 if f.kind == 16 else 2
                            if len(dec) != f.w * f.h * bpp:
                                raise RuntimeError(f"{name}:{img}[{idx}] bad pixel size")
        verified += 1
    return {"verified_npks": verified, "status": "OK"}


def rollback_all(us_packs: Path, backup_dir: Path) -> dict:
    if not backup_dir.exists():
        return {"restored_npks": 0}
    restored = 0
    for bak in sorted(backup_dir.glob("*.NPK")):
        dst = us_packs / bak.name
        shutil.copy2(bak, dst)
        restored += 1
    return {"restored_npks": restored}


def main() -> int:
    ap = argparse.ArgumentParser(description="In-place zero-red-X Chinese localization for all US100 map NPKs")
    ap.add_argument("--us-packs", type=Path, default=DEFAULT_US_PACKS)
    ap.add_argument("--cn100-packs", type=Path, default=DEFAULT_CN100_PACKS)
    ap.add_argument("--cn115-packs", type=Path, default=DEFAULT_CN115_PACKS)
    ap.add_argument("--backup-dir", type=Path, default=DEFAULT_BACKUP_DIR)
    ap.add_argument("--build-dir", type=Path, default=DEFAULT_BUILD_DIR)
    ap.add_argument("--preview-dir", type=Path, default=DEFAULT_PREVIEW_DIR)
    ap.add_argument("--build", action="store_true")
    ap.add_argument("--install", action="store_true")
    ap.add_argument("--verify", action="store_true")
    ap.add_argument("--rollback", action="store_true")
    args = ap.parse_args()

    if args.rollback:
        print(json.dumps(rollback_all(args.us_packs, args.backup_dir), indent=2, ensure_ascii=False))
        return 0
    if args.build:
        res = build_all(args.us_packs, args.cn100_packs, args.cn115_packs, args.backup_dir, args.build_dir, args.preview_dir)
        print(json.dumps(res, indent=2, ensure_ascii=False))
    if args.install:
        res = install_all(args.us_packs, args.backup_dir, args.build_dir)
        print(json.dumps(res, indent=2, ensure_ascii=False))
    if args.verify:
        res = verify_installed(args.us_packs, args.backup_dir, args.build_dir)
        print(json.dumps(res, indent=2, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
