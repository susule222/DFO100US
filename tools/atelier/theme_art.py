"""Official Lv.100 DNF HD wallpaper and character illustration loader from ImagePacks2 NPKs."""
from __future__ import annotations

from functools import lru_cache
from pathlib import Path
from PIL import Image, ImageEnhance, ImageFilter

from appearance import picture
from sprites import index

# Curated official 1600x900 HD Lv.100 artworks directly inside client ImagePacks2
WALLPAPERS = [
    (
        'wisdom',
        '智慧的引导 · Lv.100',
        'sprite_map_cutscene.NPK',
        'sprite/map/cutscene/leadofwisdom_1600.img',
        0,
    ),
    (
        'next_journey',
        '第7季 · 阿拉德新征程',
        'sprite_interface2_nowloading.NPK',
        'sprite/interface2/nowloading/(18)nowloading.img',
        0,
    ),
    (
        'stormlane',
        '风暴航路 · 次元雷暴',
        'sprite_map_cutscene.NPK',
        'sprite/map/cutscene/stormlane_1600.img',
        0,
    ),
    (
        'basilica',
        '雷米迪亚大圣堂',
        'sprite_map_cutscene.NPK',
        'sprite/map/cutscene/lemiedia_basilica_1600.img',
        0,
    ),
    (
        'revelation',
        '启示之夜 · 暗黑圣殿',
        'sprite_map_cutscene.NPK',
        'sprite/map/cutscene/nightofrevelation_1600.img',
        0,
    ),
    (
        'purgatory',
        '黑鸦之境 · 混沌炼狱',
        'sprite_map_cutscene.NPK',
        'sprite/map/cutscene/black_purgatory_1600.img',
        0,
    ),
    (
        'bakal',
        '机械崛起 · 史诗开战',
        'sprite_interface2_nowloading.NPK',
        'sprite/interface2/nowloading/nowloading.img',
        0,
    ),
]

WALLPAPER_MAP = {item[0]: item for item in WALLPAPERS}

# High-res Lv.100 class/awakening hero cut-ins (transparent RGBA)
HERO_ILLUSTS = [
    ('sprite_interface2_charactercreatever2.NPK', 'sprite/interface2/charactercreatever2/(tn)classillust.img', 6),
    ('sprite_interface2_charactercreatever2.NPK', 'sprite/interface2/charactercreatever2/classillust.img', 6),
    ('sprite_interface2_charactercreatever2.NPK', 'sprite/interface2/charactercreatever2/(tn)classillust.img', 3),
    ('sprite_interface_skillcutscene.NPK', 'sprite/interface/skillcutscene/ghost_berserker.img', 0),
]


def _find_pack(packs: Path, extra_packs: tuple[Path, ...], name: str) -> Path | None:
    for root in (*extra_packs, packs):
        if not root or not root.is_dir():
            continue
        p = root / name
        if p.is_file():
            return p
    return None


@lru_cache(maxsize=12)
def _load_npk_frame(pack_str: str, mtime_ns: int, size: int, img_name: str, frame_idx: int):
    pack = Path(pack_str)
    if img_name not in index(pack):
        return None
    im, _, _ = picture(pack, mtime_ns, size, img_name, frame_idx)
    return im.convert('RGBA')


def load_wallpaper(packs: Path, key: str = 'wisdom', extra_packs: tuple[Path, ...] = ()) -> Image.Image | None:
    """Load an official Lv.100 1600x900 wallpaper from the live NPK directory."""
    order = [WALLPAPER_MAP[key]] if key in WALLPAPER_MAP else []
    order.extend(w for w in WALLPAPERS if w[0] != key)
    for _, _, npk_name, img_name, frame_idx in order:
        pack = _find_pack(packs, extra_packs, npk_name)
        if not pack:
            continue
        try:
            st = pack.stat()
            im = _load_npk_frame(str(pack), st.st_mtime_ns, st.st_size, img_name, frame_idx)
            if im is not None:
                return im
        except Exception:
            continue
    return None


def load_hero_illust(packs: Path, extra_packs: tuple[Path, ...] = ()) -> Image.Image | None:
    """Load a high-res Lv.100 character illustration for the home hero showcase."""
    for npk_name, img_name, frame_idx in HERO_ILLUSTS:
        pack = _find_pack(packs, extra_packs, npk_name)
        if not pack:
            continue
        try:
            st = pack.stat()
            im = _load_npk_frame(str(pack), st.st_mtime_ns, st.st_size, img_name, frame_idx)
            if im is not None:
                bb = im.getbbox()
                return im.crop(bb) if bb else im
        except Exception:
            continue
    return None


def make_scenic_backdrop(im: Image.Image, width: int, height: int) -> Image.Image:
    """Compose a rich, atmospheric dark-gold Lv.100 window backdrop with optimal UI contrast."""
    w = max(320, int(width))
    h = max(240, int(height))
    iw, ih = im.size
    scale = max(w / iw, h / ih)
    nw, nh = max(w, int(round(iw * scale))), max(h, int(round(ih * scale)))
    resized = im.resize((nw, nh), Image.Resampling.LANCZOS)
    x0 = (nw - w) // 2
    y0 = max(0, (nh - h) // 3)
    cropped = resized.crop((x0, y0, x0 + w, y0 + h)).convert('RGBA')
    # Subtle cinematic richness and soft background depth so foreground glass cards pop cleanly
    cropped = ImageEnhance.Color(cropped).enhance(1.12)
    cropped = ImageEnhance.Brightness(cropped).enhance(0.54)
    cropped = cropped.filter(ImageFilter.GaussianBlur(radius=1.2))
    return cropped
