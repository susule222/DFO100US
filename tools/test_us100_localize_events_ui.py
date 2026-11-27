"""Unit tests for US100 event UI localization pipeline."""
from __future__ import annotations

from pathlib import Path
import struct
import sys
import tempfile
import unittest
import zlib

from PIL import Image

sys.path.insert(0, str(Path(__file__).resolve().parent))
import us100_localize_events_ui as loc  # noqa: E402
import us100_ui_atlas as atlas  # noqa: E402
from atelier.sprites import KEY  # noqa: E402


def _make_v2_img(images: list[tuple[Image.Image, tuple[int, int, int, int]]]) -> bytes:
    """Create a valid v2 IMG blob from (PIL.Image, (x, y, cw, ch)) entries."""
    metas = []
    bodies = []
    for im, (x, y, cw, ch) in images:
        rgba = im.convert("RGBA")
        bgra = rgba.tobytes("raw", "BGRA")
        comp = zlib.compress(bgra, level=6)
        metas.append(struct.pack("<IIIIIiiII", 16, 6, rgba.width, rgba.height, len(comp), x, y, cw, ch))
        bodies.append(comp)
    table = b"".join(metas)
    body = b"".join(bodies)
    return b"Neople Img File\0" + struct.pack("<IIII", len(table), 0, 2, len(images)) + table + body


def _make_npk(members: dict[str, bytes]) -> bytes:
    count = len(members)
    header = bytearray(b"NeoplePack_Bill\0" + struct.pack("<I", count))
    body = bytearray()
    table_end = 20 + 264 * count
    for name, raw in members.items():
        offset = table_end + len(body)
        body.extend(raw)
        name_bytes = name.encode("cp949")[:255].ljust(256, b"\0")
        enc = bytes(a ^ b for a, b in zip(name_bytes, KEY))
        header.extend(struct.pack("<II", offset, len(raw)))
        header.extend(enc)
    return bytes(header + body)


class TestEventUILocalizer(unittest.TestCase):
    def test_encode_v2_frame_roundtrip_preserves_geometry_and_pixels(self) -> None:
        src_im = Image.new("RGBA", (64, 28), (12, 34, 56, 255))
        src_im.putpixel((5, 7), (250, 10, 20, 180))
        blob = _make_v2_img([(src_im, (11, -3, 128, 64))])
        orig = atlas.split_img(blob)[0]

        mod = src_im.copy()
        mod.putpixel((10, 12), (20, 220, 240, 255))
        encoded = loc.encode_v2_frame(orig, mod)

        self.assertEqual(encoded.geometry, (64, 28, 11, -3, 128, 64))
        self.assertEqual(encoded.image().tobytes(), mod.tobytes())

    def test_encode_v2_frame_rejects_dimension_mismatch(self) -> None:
        src_im = Image.new("RGBA", (32, 16), (0, 0, 0, 255))
        orig = atlas.split_img(_make_v2_img([(src_im, (0, 0, 32, 16))]))[0]
        with self.assertRaises(ValueError):
            loc.encode_v2_frame(orig, Image.new("RGBA", (30, 16)))

    def test_patch_img_bytes_preserves_link_frames_and_repaints_target(self) -> None:
        im0 = Image.new("RGBA", (80, 26), (30, 50, 90, 255))
        raw_v2 = _make_v2_img([(im0, (4, 6, 100, 40))])
        # Append a link frame (kind=17, target=0)
        table_len, _, ver, _ = struct.unpack_from("<4I", raw_v2, 16)
        link_meta = struct.pack("<II", 17, 0)
        old_table = raw_v2[32 : 32 + table_len]
        old_body = raw_v2[32 + table_len :]
        with_link = (
            raw_v2[:16]
            + struct.pack("<IIII", len(old_table) + len(link_meta), 0, ver, 2)
            + old_table
            + link_meta
            + old_body
        )

        patched, rendered = loc.patch_img_bytes(
            with_link,
            {0: loc.make_button_patcher("领取", fill=(255, 255, 255, 255))},
        )
        frames = atlas.split_img(patched)
        self.assertEqual(len(frames), 2)
        self.assertEqual(frames[1], 0)
        self.assertEqual(frames[0].geometry, (80, 26, 4, 6, 100, 40))
        self.assertEqual(frames[0].image().tobytes(), rendered[0].tobytes())
        # Ensure text actually drew visible pixels different from uniform background
        self.assertNotEqual(frames[0].image().tobytes(), im0.tobytes())

    def test_button_patcher_preserves_left_icon_region(self) -> None:
        im = Image.new("RGBA", (98, 28), (40, 40, 55, 255))
        # Paint distinctive gold coin pixel in left icon zone
        im.putpixel((10, 14), (255, 200, 0, 255))
        out = loc.make_button_patcher("购买", keep_left=28, keep_right=6, keep_top=4, keep_bottom=4)(im.copy())
        self.assertEqual(out.getpixel((10, 14)), (255, 200, 0, 255))

    def test_plan_covers_all_16_event_npks_with_valid_specs(self) -> None:
        plan = loc.build_localization_plan()
        self.assertEqual(len(plan), 16)
        self.assertIn("sprite_live_event_kor_2022_0401_reversehottime.NPK", plan)
        self.assertIn("sprite_live_else_univ_2022_0712_danji.NPK", plan)
        self.assertIn("sprite_interface2_ui_eventcalender.NPK", plan)
        self.assertIn("sprite_interface2_ui_eventlist.NPK", plan)
        for npk, imgs in plan.items():
            self.assertTrue(npk.endswith(".NPK"))
            self.assertGreater(len(imgs), 0)
            for img_name, frame_map in imgs.items():
                self.assertTrue(img_name.endswith(".img"))
                self.assertGreater(len(frame_map), 0)

    def test_install_and_rollback_roundtrip_on_temp_fixture(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            packs = root / "packs"
            build = root / "build"
            backup = root / "backup"
            packs.mkdir()

            sample_img = _make_v2_img([(Image.new("RGBA", (56, 24), (24, 48, 82, 255)), (0, 0, 56, 24))])
            npk_name = "test_event.NPK"
            img_name = "sprite/test/btn.img"
            orig_bytes = _make_npk({img_name: sample_img})
            (packs / npk_name).write_bytes(orig_bytes)
            orig_sha = atlas.digest(packs / npk_name)

            fake_plan = {npk_name: {img_name: {0: loc.make_button_patcher("领取")}}}
            orig_builder = loc.build_localization_plan
            try:
                loc.build_localization_plan = lambda: fake_plan  # type: ignore[assignment]
                reports = loc.install_all(packs, build, backup, allow_while_running=True)
                self.assertEqual(len(reports), 1)
                self.assertNotEqual(atlas.digest(packs / npk_name), orig_sha)
                self.assertEqual(atlas.digest(backup / npk_name), orig_sha)

                restored = loc.rollback_all(packs, backup, allow_while_running=True)
                self.assertEqual(restored, [npk_name])
                self.assertEqual(atlas.digest(packs / npk_name), orig_sha)
            finally:
                loc.build_localization_plan = orig_builder


if __name__ == "__main__":
    unittest.main()
