"""Unit tests for US100 event UI strict in-place localization pipeline."""
from __future__ import annotations

import hashlib
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


def _make_v2_img(
    images: list[tuple[Image.Image, tuple[int, int, int, int], int]],
    append_link_to: int | None = None,
) -> bytes:
    """Create a valid v2 IMG blob from (PIL.Image, (x, y, cw, ch), kind) entries."""
    metas = []
    bodies = []
    for im, (x, y, cw, ch), kind in images:
        raw_px = loc.encode_pixels_native(im, kind)
        # Use low compression level so fixture frames have realistic byte slack like native assets
        comp = zlib.compress(raw_px, level=1)
        metas.append(struct.pack("<IIIIIiiII", kind, 6, im.width, im.height, len(comp), x, y, cw, ch))
        bodies.append(comp)
    count = len(images)
    if append_link_to is not None:
        metas.append(struct.pack("<II", 17, append_link_to))
        count += 1
    table = b"".join(metas)
    body = b"".join(bodies)
    return b"Neople Img File\0" + struct.pack("<IIII", len(table), 0, 2, count) + table + body


def _make_npk_with_sha256(members: dict[str, bytes]) -> bytes:
    """Build a valid Neople NPK including the 32-byte header SHA-256 after table_end."""
    count = len(members)
    table_end = 20 + 264 * count
    data_start = table_end + 32
    header = bytearray(b"NeoplePack_Bill\0" + struct.pack("<I", count))
    body = bytearray()
    for name, raw in members.items():
        offset = data_start + len(body)
        body.extend(raw)
        name_bytes = name.encode("cp949")[:255].ljust(256, b"\0")
        enc = bytes(a ^ b for a, b in zip(name_bytes, KEY))
        header.extend(struct.pack("<II", offset, len(raw)))
        header.extend(enc)
    hashed_len = (table_end // 17) * 17
    checksum = hashlib.sha256(bytes(header[:hashed_len])).digest()
    return bytes(header) + checksum + bytes(body)


def _noisy_button(w: int, h: int) -> Image.Image:
    im = Image.new("RGBA", (w, h))
    px = im.load()
    for y in range(h):
        for x in range(w):
            px[x, y] = ((x * 7 + y * 13) % 256, (x * 11 + y * 5) % 256, (x * 3 + y * 17) % 256, 255)
    return im


class TestEventUILocalizer(unittest.TestCase):
    def test_encode_pixels_native_all_kinds_roundtrip(self) -> None:
        im = Image.new("RGBA", (16, 12), (240, 128, 64, 255))
        im.putpixel((2, 3), (0, 255, 0, 0))
        for kind in (14, 15, 16):
            raw = loc.encode_pixels_native(im, kind)
            expected = 16 * 12 * (4 if kind == 16 else 2)
            self.assertEqual(len(raw), expected)

    def test_fit_frame_in_place_exact_size_and_meta_preserved(self) -> None:
        src_im = _noisy_button(64, 28)
        blob = _make_v2_img([(src_im, (11, -3, 128, 64), 16)])
        orig = atlas.split_img(blob)[0]

        mod = loc.make_button_patcher("领取", fill=(255, 255, 255, 255))(src_im.copy())
        fitted, decoded = loc.fit_frame_in_place(orig, mod)

        self.assertEqual(len(fitted), len(orig.raw))
        self.assertEqual(atlas.Frame(orig.meta, fitted).image().tobytes(), decoded.tobytes())
        self.assertNotEqual(decoded.tobytes(), src_im.tobytes())

    def test_patch_npk_in_place_preserves_header_sha256_offsets_and_link_frames(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp) / "test.NPK"
            img_name = "sprite/test/panel.img"
            img_blob = _make_v2_img([(_noisy_button(98, 28), (4, 6, 100, 40), 16)], append_link_to=0)
            orig_npk = _make_npk_with_sha256({img_name: img_blob})
            p.write_bytes(orig_npk)

            self.assertTrue(loc.verify_npk_header_sha256(orig_npk))
            patched_npk, previews = loc.patch_npk_in_place(
                p,
                {img_name: {0: loc.make_button_patcher("购买", keep_left=28)}},
            )
            self.assertEqual(len(patched_npk), len(orig_npk))
            self.assertTrue(loc.verify_npk_header_sha256(patched_npk))
            self.assertEqual(len(previews), 1)

            # Header + 32-byte SHA-256 + IMG header & Frame.meta table must be 100% bit-identical!
            table_end = 20 + 264
            self.assertEqual(patched_npk[: table_end + 32], orig_npk[: table_end + 32])

            out_path = Path(tmp) / "out.NPK"
            out_path.write_bytes(patched_npk)
            self.assertEqual(atlas.index(out_path), atlas.index(p))
            frames = atlas.split_img(atlas.member(out_path, img_name))
            self.assertEqual(len(frames), 2)
            self.assertEqual(frames[1], 0)
            self.assertEqual(frames[0].meta, atlas.split_img(img_blob)[0].meta)

    def test_button_patcher_preserves_left_icon_region(self) -> None:
        im = Image.new("RGBA", (98, 28), (40, 40, 55, 255))
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

    def test_install_and_rollback_roundtrip_on_temp_fixture(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            packs = root / "packs"
            build = root / "build"
            backup = root / "backup"
            packs.mkdir()

            sample_img = _make_v2_img([(_noisy_button(64, 28), (0, 0, 64, 28), 16)])
            npk_name = "test_event.NPK"
            img_name = "sprite/test/btn.img"
            orig_bytes = _make_npk_with_sha256({img_name: sample_img})
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
                self.assertEqual((packs / npk_name).stat().st_size, len(orig_bytes))
                self.assertTrue(loc.verify_npk_header_sha256((packs / npk_name).read_bytes()))

                restored = loc.rollback_all(packs, backup, allow_while_running=True)
                self.assertEqual(restored, [npk_name])
                self.assertEqual(atlas.digest(packs / npk_name), orig_sha)

                # Verify apply_staged_if_pending applies modified build when DFO is not running
                orig_running = loc.is_dfo_running
                try:
                    loc.is_dfo_running = lambda: False
                    msg = loc.apply_staged_if_pending(packs, build, backup)
                    self.assertIsNotNone(msg)
                    self.assertIn("1 staged", msg)
                    self.assertNotEqual(atlas.digest(packs / npk_name), orig_sha)
                    self.assertIsNone(loc.apply_staged_if_pending(packs, build, backup))
                finally:
                    loc.is_dfo_running = orig_running
            finally:
                loc.build_localization_plan = orig_builder

    def test_mining_complete_badges_preserve_ribbon_and_clear_green_edges(self) -> None:
        # #20: transparent 57x15 green badge must clear border pixels completely
        f20 = Image.new("RGBA", (57, 15), (0, 255, 0, 255))
        out20 = loc._patch_512_mining_20(f20)
        self.assertEqual(out20.getpixel((0, 0))[3], 0)
        self.assertEqual(out20.getpixel((56, 14))[3], 0)

        # #21: 73x34 tilted white ribbon must keep opaque white body & orange right tab
        f21 = Image.new("RGBA", (73, 34), (0, 0, 0, 0))
        for x in range(3, 60):
            for y in range(6, 22):
                f21.putpixel((x, y), (235, 235, 235, 255))
        for x in range(61, 68):
            for y in range(4, 20):
                f21.putpixel((x, y), (226, 108, 66, 255))
        out21 = loc._patch_512_mining_21(f21)
        # White ribbon interior remains opaque (not erased to transparent)
        self.assertEqual(out21.getpixel((10, 14))[3], 255)
        # Orange tab on right remains intact
        self.assertEqual(out21.getpixel((64, 10)), (226, 108, 66, 255))
        # Transparent exterior stays transparent
        self.assertEqual(out21.getpixel((0, 0))[3], 0)


if __name__ == "__main__":
    unittest.main()
