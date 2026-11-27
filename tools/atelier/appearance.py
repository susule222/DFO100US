"""Static and animated PVF standing pose renderer with IMG v6 palette, weapon, and aura support."""
import io
import json
import re
import struct
import zlib
from functools import lru_cache
from pathlib import PurePosixPath
from PIL import Image, ImageChops

from item_icons import ItemIcons, reference
from sprites import _pixels, index


def inflate(b, limit):
    d = zlib.decompressobj()
    raw = d.decompress(b, limit + 1)
    if len(raw) > limit or not d.eof:
        raise ValueError('texture data out of bounds')
    return raw


@lru_cache(maxsize=256)
def picture(pack, stamp, size, name, frame, palette=0):
    off, span = index(pack)[name]
    with pack.open('rb') as f:
        f.seek(off)

        def get(n):
            if n < 0 or f.tell() + n > off + span:
                raise ValueError('IMG 超出边界')
            b = f.read(n)
            if len(b) != n:
                raise ValueError('IMG 截断')
            return b

        def ints(n):
            return struct.unpack('<' + 'I' * n, get(n * 4))

        if get(16) != b'Neople Img File\0':
            raise ValueError('未知 IMG')
        length, _, ver, count = ints(4)
        if not 0 <= frame < count <= 65536 or length > count * 64:
            raise ValueError('动作帧不在此图层中')
        pal = None
        tex = []
        if ver in (4, 6):
            palettes = ints(1)[0] if ver == 6 else 1
            if palettes > 512:
                raise ValueError('调色板异常')
            target = (palette % palettes) if palettes else 0
            for i in range(palettes):
                colors = ints(1)[0]
                if colors > 256:
                    raise ValueError('调色板越界')
                buf = get(colors * 4)
                if i == target:
                    pal = buf
        elif ver == 5:
            n, _, palcount = ints(3)
            if n > 8192 or palcount:
                raise ValueError('不支持的 v5 纹理目录')
            tex = [ints(7) for _ in range(n)]
        elif ver != 2:
            raise ValueError(f'未支持 IMG v{ver}')
        meta = get(length)
        base = f.tell()
        pos = base + sum(t[3] for t in tex)
        rows = []
        at = 0
        for _ in range(count):
            kind, comp = struct.unpack_from('<2I', meta, at)
            if kind == 17:
                rows.append(comp)
                at += 8
                continue
            if kind not in (14, 15, 16, 18, 19, 20):
                raise ValueError('未知像素格式')
            w, h, n, x, y, _, _ = struct.unpack_from('<IIIiiII', meta, at + 8)
            uv = struct.unpack_from('<7I', meta, at + 36) if comp == 7 else None
            rows.append((kind, comp, w, h, pos, n, x, y, uv))
            at += 64 if uv else 36
            if not uv:
                pos += n
        if pos > off + span or at != length:
            raise ValueError('图帧目录越界')
        seen = set()
        while 0 <= frame < count and frame not in seen:
            row = rows[frame]
            if not isinstance(row, int):
                break
            seen.add(frame)
            frame = row
        else:
            raise ValueError('无效链接帧')
        kind, comp, w, h, pos, n, x, y, uv = row
        if not (0 < w <= 4096 and 0 < h <= 4096 and w * h <= 4194304):
            raise ValueError('图层尺寸无效')
        if uv:
            tid = uv[1]
            if tid >= len(tex):
                raise ValueError('无效纹理编号')
            _, _, _, compressed, raw, tw, th = tex[tid]
            if raw > 32 << 20 or tw * th > 8388608 or compressed > 32 << 20:
                raise ValueError('纹理过大')
            f.seek(base + sum(t[3] for t in tex[:tid]))
            dds = inflate(get(compressed), raw)
            with Image.open(io.BytesIO(dds), formats=['DDS']) as source:
                sx, sy = uv[2:4]
                if source.size != (tw, th) or sx + w > tw or sy + h > th:
                    raise ValueError('纹理裁切越界')
                im = source.crop((sx, sy, sx + w, sy + h)).convert('RGBA')
        else:
            if n > 20 << 20:
                raise ValueError('图层过大')
            f.seek(pos)
            im = _pixels(kind, comp, w, h, get(n), pal)
        return im, x, y


def _is_opaque_black_glow(im):
    """Detect DNF additive (LinearDodge) aura/effect sprites that use opaque black as transparent."""
    w, h = im.size
    if w < 2 or h < 2:
        return False
    corners = [im.getpixel((0, 0)), im.getpixel((w - 1, 0)), im.getpixel((0, h - 1)), im.getpixel((w - 1, h - 1))]
    black_opaque = sum(1 for r, g, b, a in corners if a >= 240 and r <= 18 and g <= 18 and b <= 18)
    return black_opaque >= 3


def _composite_additive(canvas, src, pos):
    dx, dy = pos
    w, h = src.size
    patch = canvas.crop((dx, dy, dx + w, dy + h))
    pr, pg, pb, pa = patch.split()
    sr, sg, sb, sa = src.split()
    # Scale source RGB by its alpha so any masked edge fades cleanly
    sr = ImageChops.multiply(sr, sa)
    sg = ImageChops.multiply(sg, sa)
    sb = ImageChops.multiply(sb, sa)
    lum = ImageChops.lighter(ImageChops.lighter(sr, sg), sb)
    nr = ImageChops.add(pr, sr)
    ng = ImageChops.add(pg, sg)
    nb = ImageChops.add(pb, sb)
    na = ImageChops.lighter(pa, lum)
    canvas.paste(Image.merge('RGBA', (nr, ng, nb, na)), (dx, dy))


JOB_ALIASES = {
    '[creator mage]': '[mage]',
    '[demonic swordman]': '[swordman]',
}
AVATAR_SLOTS = {'cap', 'coat', 'pants', 'shoes', 'hair', 'neck', 'face', 'belt', 'skin'}
def parse_visual(raw):
    data = json.loads(raw or '[[],[]]')
    spec = data[0] if len(data) > 0 and isinstance(data[0], list) else []
    aura = data[1] if len(data) > 1 and isinstance(data[1], list) else []
    aura_anim = data[2] if len(data) > 2 and isinstance(data[2], list) else []
    return spec, aura, aura_anim


def aura_frame_at(tracks, ms):
    out = []
    for track in tracks:
        frames = track.get('Frames') or []
        if not frames:
            continue
        total = sum(max(40, int(fr.get('Delay') or 100)) for fr in frames)
        if total <= 0:
            out.append(frames[0])
            continue
        t = ms % total
        acc = 0
        chosen = frames[0]
        for fr in frames:
            acc += max(40, int(fr.get('Delay') or 100))
            if t < acc:
                chosen = fr
                break
        out.append(chosen)
    return out


class Stage:
    def __init__(self, ward, source):
        self.ward = ward
        self.source = source
        self.assets = ItemIcons(ward.packs, getattr(ward, 'extra_packs', ()))

    def sync_sources(self, ward, source):
        self.ward = ward
        self.source = source
        self.assets.set_packs(ward.packs, getattr(ward, 'extra_packs', ()))

    def sprite(self, path, num, palette=0):
        ref = reference(path, num)
        if not ref:
            raise ValueError('图层路径无效')
        pack = self.assets.locate(ref)
        s = pack.stat()
        return picture(pack, s.st_mtime_ns, s.st_size, ref.img, num, palette)

    def has_aura_animation(self, chosen):
        r = chosen.get('aura')
        if not r:
            return False
        _, _, aura_anim = parse_visual(r['visual'])
        return any(len(t.get('Frames') or []) > 1 for t in aura_anim)

    def render(self, job, chosen, show_aura=True, show_weapon=True, show_defaults=True, aura_ms=None):
        pose = self.source.poses[job]
        template = pose['Image'].lower()
        base = PurePosixPath(template)
        prefix = base.name.split('_body')[0]
        root = str(base.parent.parent)
        layers = []
        errors = []
        seen = set()

        def add(path, num, z, dx, dy, optional=False, palette=0, is_aura=False):
            key = (path, num, z, dx, dy, palette, is_aura)
            if key in seen:
                return True
            try:
                im, x, y = self.sprite(path, num, palette)
                seen.add(key)
                additive = is_aura and _is_opaque_black_glow(im)
                if additive:
                    sr, sg, sb, _ = im.split()
                    lum = ImageChops.lighter(ImageChops.lighter(sr, sg), sb)
                    bb = lum.getbbox()
                else:
                    bb = im.getbbox()
                if bb:
                    layers.append((z, im.crop(bb), x + dx + bb[0], y + dy + bb[1], additive))
                return True
            except (OSError, ValueError, KeyError, IndexError, struct.error, zlib.error) as exc:
                if not optional:
                    errors.append(f'{PurePosixPath(path).name}: {exc}')
                return False

        def stand(path, z, optional=False, palette=0):
            return add(path, pose['Frame'], z, pose['X'], pose['Y'], optional, palette)

        def try_candidates(candidates, z, err_label):
            for path, pal in candidates:
                if stand(path, z, True, pal):
                    return True
            # Emit detailed error from primary candidate
            if candidates:
                stand(candidates[0][0], z, False, candidates[0][1])
            elif err_label:
                errors.append(err_label)
            return False

        skin = False
        if show_defaults:
            # Unselected core slots use the client's default clothing rather than floating pieces.
            for slot, z in [('shoes', 1400), ('pants', 1500), ('coat', 1800), ('hair', 2000)]:
                if slot not in chosen:
                    stand(f'{root}/{slot}/{prefix}_{slot}0000a.img', z, True)

        for slot, r in chosen.items():
            if not r:
                continue
            if slot == 'aura' and not show_aura:
                continue
            if slot == 'weapon' and not show_weapon:
                continue
            spec, aura, aura_anim = parse_visual(r['visual'])
            if slot == 'aura':
                active_aura = aura_frame_at(aura_anim, aura_ms) if (aura_ms is not None and aura_anim) else aura
                if not active_aura:
                    errors.append('光环没有可解析的首帧特效')
                for part in active_aura:
                    add(part['Image'], part['Frame'], 10000 if part.get('Front') else -10000, part['X'], part['Y'], is_aura=True)
                continue
            alias_job = JOB_ALIASES.get(job, '')
            parts = [s for s in spec if s['Job'] in ('', job, alias_job, '[all]')]
            if not parts and 'bald' not in r['path']:
                errors.append(r['name'] + '：未找到该职业的外观图层')
            for s in parts:
                v_num = int(s['V'])
                c_num = int(s['C'])
                var = f'{v_num:02}{c_num:02}'
                base_var = f'{v_num:02}00'
                tag = s['Layer'].lower()
                if slot == 'weapon':
                    m = re.fullmatch(r'([a-z0-9]+)([a-z][0-9]*)', tag.split('_')[-1])
                    if not m:
                        errors.append('未识别的武器图层 ' + tag)
                        continue
                    stem, suffix = m.groups()
                    weapon_root = f"{root.rsplit('/avatar', 1)[0]}/weapon"
                    folders = [f'{weapon_root}/{stem}']
                    expand = (s.get('Path') or '').replace('\\', '/').strip('/').lower()
                    if '/weapon/' in expand:
                        sub = expand.split('/weapon/', 1)[1].split('/')[0]
                        if sub and f'{weapon_root}/{sub}' not in folders:
                            folders.append(f'{weapon_root}/{sub}')
                    candidates = []
                    for folder in folders:
                        for bname in (f'{prefix}_{stem}{var}{suffix}.img', f'{stem}{var}{suffix}.img',
                                      f'(tn){prefix}_{stem}{var}{suffix}.img', f'(tn){stem}{var}{suffix}.img'):
                            candidates.append((f'{folder}/{bname}', 0))
                        if c_num > 0:
                            for bname in (f'{prefix}_{stem}{base_var}{suffix}.img', f'{stem}{base_var}{suffix}.img',
                                          f'(tn){prefix}_{stem}{base_var}{suffix}.img', f'(tn){stem}{base_var}{suffix}.img'):
                                candidates.append((f'{folder}/{bname}', c_num))
                    if not any(stand(p, s['Z'], True, pal) for p, pal in candidates):
                        errors.append(f'{stem}{var}{suffix}.img：未找到或无法读取此职业的武器图层')
                    continue
                elif slot == 'skin' and tag == 'body':
                    candidates = [
                        (template % (100 * v_num + c_num), 0),
                    ]
                    if c_num > 0:
                        candidates.append((template % (100 * v_num), c_num))
                    if v_num == 0 and c_num > 0:
                        candidates.append((template.replace('%04d', '0000'), c_num))
                    if try_candidates(candidates, s['Z'], ''):
                        skin = True
                    continue
                else:
                    target_slot = slot
                    if '_' in tag:
                        head_part, suffix = tag.rsplit('_', 1)
                        if head_part in AVATAR_SLOTS:
                            target_slot = head_part
                    else:
                        suffix = tag[len(slot):] if tag.startswith(slot) else ''
                    stem = 'body' if target_slot == 'skin' else target_slot
                    folder = f'{root}/{target_slot}'
                    candidates = [
                        (f'{folder}/{prefix}_{stem}{var}{suffix}.img', 0),
                        (f'{folder}/(tn){prefix}_{stem}{var}{suffix}.img', 0),
                    ]
                    if c_num > 0:
                        candidates.append((f'{folder}/{prefix}_{stem}{base_var}{suffix}.img', c_num))
                        candidates.append((f'{folder}/(tn){prefix}_{stem}{base_var}{suffix}.img', c_num))
                    try_candidates(candidates, s['Z'], '')

        if not skin:
            stand(template.replace('%04d', '0000'), 1000)
        if not layers:
            raise ValueError('没有能绘制的站姿图层')
        body_layers = [v for v in layers if not v[4] and abs(v[0]) < 10000]
        ref_layers = body_layers or layers
        bleft = min(x for _, im, x, y, _ in ref_layers)
        btop = min(y for _, im, x, y, _ in ref_layers)
        bright = max(x + im.width for _, im, x, y, _ in ref_layers)
        bbottom = max(y + im.height for _, im, x, y, _ in ref_layers)

        left = min(x for _, im, x, y, _ in layers)
        top = min(y for _, im, x, y, _ in layers)
        right = max(x + im.width for _, im, x, y, _ in layers)
        bottom = max(y + im.height for _, im, x, y, _ in layers)
        # Keep oversized aura particle bounds from shrinking the character preview
        if body_layers:
            left = max(left, bleft - 90)
            top = max(top, btop - 90)
            right = min(right, bright + 90)
            bottom = min(bottom, bbottom + 55)
        if right - left > 4096 or bottom - top > 4096:
            raise ValueError('站姿图层的坐标跨度异常')
        out = Image.new('RGBA', (max(1, right - left), max(1, bottom - top)))
        ow, oh = out.size
        for z, im, x, y, additive in sorted(layers, key=lambda v: v[0]):
            dx, dy = x - left, y - top
            # Clip layer to output canvas if aura extended past clamped margin
            cx0 = max(0, -dx)
            cy0 = max(0, -dy)
            cx1 = min(im.width, ow - dx)
            cy1 = min(im.height, oh - dy)
            if cx1 <= cx0 or cy1 <= cy0:
                continue
            part_im = im if (cx0 == 0 and cy0 == 0 and cx1 == im.width and cy1 == im.height) else im.crop((cx0, cy0, cx1, cy1))
            px, py = dx + cx0, dy + cy0
            if additive:
                _composite_additive(out, part_im, (px, py))
            else:
                out.alpha_composite(part_im, (px, py))
        return out, errors
