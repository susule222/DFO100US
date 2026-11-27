"""Stage local UI fonts with missing Chinese glyphs. No live paths are written."""
import argparse
import hashlib
import json
from pathlib import Path

from fontTools.pens.recordingPen import DecomposingRecordingPen
from fontTools.pens.transformPen import TransformPen
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.ttLib import TTFont


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def is_display_char(n):
    return (0x2e80 <= n <= 0x312f or 0x3400 <= n <= 0x9fff
            or 0xf900 <= n <= 0xfaff or 0xff01 <= n <= 0xffef)


def main():
    ap = argparse.ArgumentParser()
    for name in ("fonts", "donor", "tables", "out"):
        ap.add_argument("--" + name, type=Path, required=True)
    a = ap.parse_args()
    if a.out.resolve().is_relative_to(a.fonts.resolve()):
        ap.error("choose a separate staging directory")
    needed = set()
    for file in a.tables.rglob("*"):
        if file.is_file() and file.suffix.lower() in {".str", ".xui"}:
            needed.update(n for n in map(ord, set(file.read_bytes().decode("utf-16-le"))) if is_display_char(n))
    if len(needed) < 100:
        ap.error("missing localized text corpus")
    a.out.mkdir(parents=True, exist_ok=False)
    donor = TTFont(a.donor)
    glyphs = donor.getGlyphSet(location={"wght": 400} if "fvar" in donor else None)
    dc = donor.getBestCmap()
    receipt = {"donor": a.donor.name, "donor_sha256": sha(a.donor), "needed": len(needed), "fonts": {}}
    # Both shipped US fonts use the same UPM. Reuse the outlines across them.
    cache = {}
    for name in ("tahoma.ttf", "calibri.ttf"):
        src, dest = a.fonts / name, a.out / name
        base = TTFont(src, recalcTimestamp=False)
        original = dict(base.getBestCmap())
        order = list(base.getGlyphOrder())
        scale = base["head"].unitsPerEm / donor["head"].unitsPerEm
        mappings = {}
        for n in sorted(needed.intersection(dc).difference(original)):
            key = (scale, dc[n])
            if key not in cache:
                rec = DecomposingRecordingPen(glyphs)
                glyphs[dc[n]].draw(rec)
                pen = TTGlyphPen(None)
                rec.replay(TransformPen(pen, (scale, 0, 0, scale, 0, 0)))
                g = pen.glyph()
                g.recalcBounds({})
                cache[key] = g, round(glyphs[dc[n]].width * scale)
            g, width = cache[key]
            label = f"localCJK{n:04X}"
            if label in base["glyf"]:
                raise ValueError(f"glyph name collision: {label}")
            base["glyf"][label] = g
            base["hmtx"].metrics[label] = (width, getattr(g, "xMin", 0))
            order.append(label)
            mappings[n] = label
        base.setGlyphOrder(order)
        for t in base["cmap"].tables:
            if t.isUnicode() and t.format in (4, 12):
                t.cmap.update(mappings)
        base["OS/2"].ulCodePageRange1 |= 1 << 18  # Simplified Chinese
        base["OS/2"].recalcUnicodeRanges(base)
        if "DSIG" in base:
            del base["DSIG"]
        base.save(dest)
        with TTFont(dest) as built:
            cmap = built.getBestCmap()
            if any(cmap.get(n) != v for n, v in original.items()) or not mappings.keys() <= cmap.keys():
                raise ValueError("cmap round trip failed")
            sample = "\u529b\u91cf\u5192\u9669\u5bb6\u540d\u671b"
            if not all(built["glyf"][cmap[ord(ch)]].numberOfContours != 0 for ch in sample):
                raise ValueError("sample text missing real outlines")
            receipt["fonts"][name] = {"before": sha(src), "after": sha(dest), "added": len(mappings),
                                      "missing": [f"U+{n:04X}" for n in sorted(needed.difference(cmap))]}
    (a.out / "manifest.json").write_text(json.dumps(receipt, indent=2), encoding="utf-8")
    print(json.dumps(receipt))


if __name__ == "__main__":
    main()
