"""Read and compare UI atlas frames without modifying installed NPKs."""
import argparse
from itertools import groupby
from dataclasses import dataclass
import hashlib
from pathlib import Path
import struct
import sys

from PIL import Image, ImageDraw

sys.path.insert(0, str(Path(__file__).resolve().parent / "atelier"))
from sprites import KEY, _pixels, index


@dataclass(frozen=True)
class Frame:
    meta: bytes
    raw: bytes

    @property
    def geometry(self):
        return struct.unpack_from("<II", self.meta, 8) + struct.unpack_from("<iiII", self.meta, 20)

    def image(self):
        kind, comp, w, h = struct.unpack_from("<4I", self.meta)
        if w * h > 12_000_000 or not w or not h:
            raise ValueError("invalid frame dimensions")
        return _pixels(kind, comp, w, h, self.raw)


def member(pack, name):
    offset, count = index(pack)[name.casefold()]
    with pack.open("rb") as file:
        file.seek(offset)
        raw = file.read(count)
    if len(raw) != count:
        raise ValueError("truncated NPK member")
    return raw


def split_img(data):
    if data[:16] != b"Neople Img File\0" or len(data) < 32:
        raise ValueError("not a Neople image")
    table, _, version, count = struct.unpack_from("<4I", data, 16)
    if version != 2 or count > 4096 or table > 36 * count or 32 + table > len(data):
        raise ValueError("not a supported v2 image")
    pos, pixels, frames = 32, 32 + table, []
    for i in range(count):
        if pos + 8 > 32 + table:
            raise ValueError(f"frame {i} metadata truncated")
        kind, comp = struct.unpack_from("<II", data, pos)
        if kind == 17:
            frames.append(comp)
            pos += 8
            continue
        if kind not in (14, 15, 16) or pos + 36 > 32 + table:
            raise ValueError(f"frame {i} format: {kind}")
        size, = struct.unpack_from("<I", data, pos + 16)
        if size > len(data) - pixels:
            raise ValueError(f"frame {i} payload truncated")
        frames.append(Frame(data[pos:pos+36], data[pixels:pixels+size]))
        pos += 36
        pixels += size
    if pos != 32 + table or pixels != len(data):
        raise ValueError("unrecognized image tail")
    return frames


def rebuild_img(source, donor, frame_map):
    left, right = split_img(source), split_img(donor)
    result = left.copy()
    for to, origin in frame_map.items():
        a, b = left[to], right[origin]
        if not isinstance(a, Frame) or not isinstance(b, Frame) or a.geometry[4:] != b.geometry[4:]:
            raise ValueError(f"incompatible sprites: {to} <- {origin}")
        if max(abs(x-y) for x, y in zip(a.geometry[:4], b.geometry[:4])) > 130:
            raise ValueError(f"implausible sprite geometry change: {to} <- {origin}")
        b.image()
        result[to] = b
    table = b"".join(f.meta if isinstance(f, Frame) else struct.pack("<II",17,f) for f in result)
    body = b"".join(f.raw for f in result if isinstance(f, Frame))
    blob = source[:16] + struct.pack("<I",len(table)) + source[20:32] + table + body
    if split_img(blob) != result:
        raise ValueError("image frame round trip failed")
    return blob


def rebuild_npk(source, replacements):
    old = source.read_bytes()
    rows = index(source)
    if not replacements or not replacements.keys() <= rows.keys():
        raise ValueError("replacement needs existing named members")
    count, = struct.unpack_from("<I",old,16)
    table_end = 20 + 264 * count
    header, body, records = bytearray(old[:table_end]), bytearray(), []
    for i in range(count):
        p = 20 + 264*i
        start, size = struct.unpack_from("<II",header,p)
        encoded = header[p+8:p+264]
        name = bytes(a ^ b for a, b in zip(encoded, KEY)).split(b"\0")[0].decode("cp949","replace").lower()
        records.append((start,size,p,name))
    cursor = table_end
    used = set()
    for (start,size), group in groupby(sorted(records), key=lambda row:row[:2]):
        if start < cursor or start+size > len(old):
            raise ValueError("partially overlapping source NPK members")
        body.extend(old[cursor:start])
        original = old[start:start+size]
        slots = {}
        for _,_,p,name in group:
            if name in used:
                raise ValueError("duplicate NPK name")
            used.add(name)
            raw = replacements.get(name, original)
            if raw not in slots:
                slots[raw] = table_end+len(body)
                body.extend(raw)
            struct.pack_into("<II",header,p,slots[raw],len(raw))
        cursor = start+size
    body.extend(old[cursor:])
    return bytes(header + body)


def digest(path):
    with path.open("rb") as f:
        return hashlib.file_digest(f,"sha256").hexdigest()


def gallery(packs, names, output):
    art = []
    for pack, name in zip(packs, names):
        art.append(split_img(member(pack, name)))
    rows = max(map(len, art))
    w, h = 410, 109
    panel = Image.new("RGB", (w * len(art), h * rows), "#292929")
    draw = ImageDraw.Draw(panel)
    for col, frames in enumerate(art):
        for i, frame in enumerate(frames):
            x, y = col*w, i*h
            if isinstance(frame, int):
                draw.text((x+5,y+3), f"{i}: link {frame}", fill="white")
                continue
            draw.text((x+5,y+3), f"{i}: {frame.geometry}", fill="white")
            im = frame.image()
            im.thumbnail((w-12,h-20))
            panel.paste(im, (x+6,y+17), im)
    with output.open("xb") as file:
        panel.save(file, format="PNG")


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--pack", type=Path, action="append", required=True)
    p.add_argument("--name", action="append", required=True)
    p.add_argument("--out", type=Path, required=True)
    a = p.parse_args()
    if len(a.pack) != len(a.name):
        p.error("name each pack member")
    gallery(a.pack, a.name, a.out)
