"""Patch the four Act labels in their original US100 NPK byte ranges."""
import argparse
from datetime import datetime, timezone
import hashlib
from pathlib import Path
import struct
import zlib

from us100_quest_ui_install import CLIENT, DSTR, PVF, QUEST, GameFonts, core
from us100_ui_atlas import Frame, digest, index, member, split_img


REL = Path("ImagePacks2") / QUEST
ORIGINAL_PACK = "9db1515117690b559297ab6ae0b01d3932c923b22c8d19a5eb058837defc1bbc"
OUTLINE_FONTS = {
    "calibri.ttf": "9b78f7806d8a9c0b4b441446972e55cb5959b931ad62a97fd9006fa2f8e5b11e",
    "tahoma.ttf": "d79a531c5a96992734c6dd41eb4e94b9c6bca8f9296006efc60637c1f77bee60",
}
ROOT = "sprite/interface/quest/"
FRAMES = {
    ROOT + "quest_tag.img": {39: (39, (16, 2, 38, 14)), 44: (36, (17, 2, 38, 14))},
    ROOT + "questguide.img": {19: (19, (21, 5, 45, 17)), 53: (47, (23, 6, 43, 16))},
}


def inflate_exact(raw, expected):
    d = zlib.decompressobj()
    data = d.decompress(raw, expected + 1)
    if not d.eof or d.unused_data or d.unconsumed_tail or len(data) != expected:
        raise ValueError("unexpected zlib stream length")
    return data


def compress_exact(raw, size, header):
    # Non-final DEFLATE blocks leave space for valid empty blocks before Adler-32.
    check = struct.pack(">I", zlib.adler32(raw))
    for level in (9, 8, 7, 6, 5, 4, 3, 2, 1):
        for memory in (8, 6, 4, 3, 9, 7, 5, 2, 1):
            for strategy in (zlib.Z_DEFAULT_STRATEGY, zlib.Z_FILTERED):
                c = zlib.compressobj(level, zlib.DEFLATED, -15, memory, strategy)
                body = c.compress(raw) + c.flush(zlib.Z_SYNC_FLUSH)
                fill = size - len(header) - len(body) - len(check)
                if fill < 2 or fill % 5 == 1:
                    continue
                count = (4 * fill) // 5
                if (count * 10 + 7) // 8 != fill:
                    raise AssertionError("invalid empty-block size")
                groups, rest = divmod(count - 1, 4)
                empty = sum(2 << (10 * i) for i in range(4)).to_bytes(5, "little")
                bits = sum(2 << (10 * i) for i in range(rest+1)) | (1 << (10 * rest))
                result = header + body + empty * groups + bits.to_bytes(fill-5*groups,"little") + check
                if inflate_exact(result, len(raw)) != raw:
                    raise AssertionError("zlib round trip differs")
                return result
    raise ValueError(f"cannot encode localized pixels in original {size}-byte slot")


def localize_pixels(a, b, rectangle, tag):
    if not isinstance(a, Frame) or not isinstance(b, Frame) or a.geometry != b.geometry:
        raise ValueError("incompatible Act frame")
    if a.meta[:8] != struct.pack("<2I", 16, 6) or b.meta[:8] != a.meta[:8] or a.raw[:2] != b"\x78\x9c":
        raise ValueError("unexpected Act frame encoding")
    w, h = a.geometry[:2]
    source = inflate_exact(a.raw, w * h * 4)
    donor = inflate_exact(b.raw, w * h * 4)
    result = bytearray(source)
    x0, y0, x1, y1 = rectangle
    if not (0 <= x0 < x1 <= w and 0 <= y0 < y1 <= h):
        raise ValueError("label rectangle out of bounds")
    for y in range(y0, y1):
        for x in range(x0, x1):
            p = (y * w + x) * 4
            if tag is None:
                result[p:p+4] = donor[p:p+4]
                continue
            # Keep the US badge and recolor only the donor's glyph mask to fit its small slot.
            blue, green, red, _ = donor[p:p+4]
            luma = (red * 2 + green * 5 + blue) // 8
            if 3 <= y <= 12 and luma >= (83 if tag == 39 else 98):
                c = ((100,211,246,255) if luma > 170 else (44,125,185,255)) if tag == 39 else (166,242,184,255)
                result[p:p+4] = bytes(c)
            else:
                profile = (y * w + (39 if tag == 39 and y == 2 else 38)) * 4
                result[p:p+4] = source[profile:profile+4]
    if source == result:
        raise ValueError("empty Act pixel change")
    return bytes(result)


def build(source, donor, out):
    from PIL import Image, ImageDraw

    out = out.resolve()
    if out.is_relative_to(CLIENT) or digest(source) != ORIGINAL_PACK:
        raise ValueError("require the known no-red-X US100 baseline and an offline output directory")
    base = source.read_bytes()
    candidate = bytearray(base)
    names = index(source)
    spans = []
    panel = Image.new("RGB", (790, 268), "#222323")
    draw = ImageDraw.Draw(panel)
    draw.text((12, 7), "US100 original", fill="white")
    draw.text((400, 7), "US100 Act localization", fill="white")
    row = 0
    for name, mapping in FRAMES.items():
        start, _ = names[name]
        orig = member(source, name)
        frames = split_img(orig)
        cn = split_img(member(donor, name))
        start += 32 + struct.unpack_from("<I", orig, 16)[0]
        at = start
        for frame_no, frame in enumerate(frames):
            if not isinstance(frame, Frame):
                continue
            if frame_no in mapping:
                cn_no, rect = mapping[frame_no]
                pix = localize_pixels(frame, cn[cn_no], rect, frame_no if name.endswith("quest_tag.img") else None)
                blob = compress_exact(pix, len(frame.raw), frame.raw[:2])
                assert base[at:at+len(blob)] == frame.raw
                candidate[at:at+len(blob)] = blob
                spans.append((at, at+len(blob), name, frame_no, rect))
                for col, f in enumerate((frame, Frame(frame.meta, blob))):
                    img = f.image()
                    img = img.resize((img.width*2, img.height*2), Image.Resampling.NEAREST)
                    panel.paste(img, (12+col*388, 25+row*60), img)
                row += 1
            at += len(frame.raw)
    if len(spans) != 4 or len(base) != len(candidate):
        raise ValueError("unexpected Act patch footprint")
    pos = 0
    for left, right, *_ in sorted(spans):
        if left < pos or candidate[pos:left] != base[pos:left]:
            raise ValueError("unexpected byte changes outside Act frame data")
        pos = right
    if candidate[pos:] != base[pos:]:
        raise ValueError("unexpected NPK trailer change")
    out.mkdir(parents=True)
    target = out / QUEST
    target.write_bytes(candidate)
    if list(index(target).items()) != list(names.items()):
        raise ValueError("NPK index layout differs")
    for name in names:
        old, new = member(source, name), member(target, name)
        if name not in FRAMES:
            if old != new:
                raise ValueError(f"unrelated NPK member changed: {name}")
            continue
        a, b = split_img(old), split_img(new)
        if len(a) != len(b) or any((f != g if i not in FRAMES[name] else f.meta != g.meta)
                                   for i, (f,g) in enumerate(zip(a,b))):
            raise ValueError(f"non-target frames or frame metadata changed: {name}")
    panel.save(out/"comparison.png")
    core.atomic(out/"manifest.json", {"before": ORIGINAL_PACK, "after": digest(target),
                "donor": digest(donor), "size": len(base), "spans": spans, "in_game_verified": False})
    print("Four Act frames validated; offsets, lengths, and other bytes are unchanged.")


def deploy(bundle, rollback):
    bundle = bundle.resolve()
    if bundle.is_relative_to(CLIENT):
        raise ValueError("bundle must be outside the live client")
    record = core.read(bundle/"manifest.json")
    candidate, backup, live = bundle/QUEST, bundle/"baseline.npk", CLIENT/REL
    if record["before"] != ORIGINAL_PACK or digest(candidate) != record["after"]:
        raise ValueError("candidate hash differs")
    fonts = GameFonts(CLIENT/"Fonts")
    with fonts.locked():
        if not rollback and (fonts.hashes() != OUTLINE_FONTS or digest(CLIENT/"Script.pvf") != PVF
                             or digest(CLIENT/"dstr.dat") != DSTR):
            raise ValueError("the verified client baseline has changed")
        if not rollback and not backup.exists() and digest(live) == ORIGINAL_PACK:
            core.file_copy(live, backup)
        if digest(backup) != ORIGINAL_PACK or backup.stat().st_size != record["size"]:
            raise ValueError("no verified original backup")
        if digest(live) not in {ORIGINAL_PACK, record["after"]} or core.client_up():
            raise ValueError("the client reopened or quest pack changed")
        old = backup.read_bytes()
        new = candidate.read_bytes()
        pos = 0
        if len(record["spans"]) != sum(map(len, FRAMES.values())):
            raise ValueError("unexpected Act frame count")
        for left, right, *_ in sorted(record["spans"]):
            if not pos <= left < right <= len(old) or old[pos:left] != new[pos:left]:
                raise ValueError("candidate changed bytes outside verified ranges")
            pos = right
        if len(old) != len(new) or old[pos:] != new[pos:]:
            raise ValueError("candidate changed NPK length or tail")
        expected = ORIGINAL_PACK if rollback else record["after"]
        if digest(live) != expected:
            if core.client_up():
                raise ValueError("client reopened before replacement")
            core.file_copy(backup if rollback else candidate, live)
        if digest(live) != expected:
            raise ValueError("deployed file hash differs")
        core.atomic(bundle/"deployment.json", {"utc": datetime.now(timezone.utc).isoformat(),
                    "status": "rolled_back" if rollback else "installed_pending_game_check",
                    "path": str(live), "sha256": expected, "unchanged_fonts": fonts.hashes()})
        print(f"Verified {live.name}: {expected}")


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    cmd = p.add_subparsers(dest="mode", required=True)
    b = cmd.add_parser("build")
    for key in ("source", "donor", "out"):
        b.add_argument("--"+key, type=Path, required=True)
    for mode in ("install", "rollback"):
        cmd.add_parser(mode).add_argument("--bundle", type=Path, required=True)
    a = p.parse_args()
    if a.mode == "build":
        build(a.source, a.donor, a.out)
    else:
        deploy(a.bundle, rollback=a.mode == "rollback")
