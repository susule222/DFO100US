"""Unit & invariant tests for zero-red-X in-place map NPK localization."""
from __future__ import annotations

import json
from pathlib import Path
import struct
import sys
import unittest
import zlib

sys.path.insert(0, str(Path(__file__).resolve().parent))
import us100_localize_maps_ui as mod  # noqa: E402
from us100_localize_events_ui import verify_npk_header_sha256  # noqa: E402


class TestUS100LocalizeMapsUI(unittest.TestCase):
    def test_v2_roundtrip_and_exact_padding(self):
        # Synthetic 2-frame IMG v2 (1 real ARGB8888 frame + 1 ref frame)
        px = bytes([10, 20, 30, 255] * 16)
        raw = zlib.compress(px, level=6)
        f0 = mod.V2Frame(kind=16, comp=6, w=4, h=4, x=1, y=2, cw=8, ch=8, raw=raw)
        f1 = mod.V2Frame(kind=17, comp=0)
        blob = mod.encode_img_v2(0, 2, [f0, f1])
        target = len(blob) + 17
        fitted = mod.fit_img_v2_exact_size(0, 2, [f0, f1], target, {0}, {0: 16})
        self.assertEqual(len(fitted), target)
        kz, ver, parsed = mod.parse_img_v2(fitted)
        self.assertEqual((kz, ver, len(parsed)), (0, 2, 2))
        self.assertFalse(parsed[0].is_ref())
        self.assertTrue(parsed[1].is_ref())
        self.assertEqual(parsed[1].comp, 0)
        self.assertEqual(zlib.decompress(parsed[0].raw), px)

    def test_blank_cn_placeholder_is_never_transplanted(self):
        real_us = mod.V2Frame(kind=16, comp=6, w=200, h=120, x=10, y=20, cw=800, ch=567, raw=b"")
        blank_cn = mod.V2Frame(kind=16, comp=6, w=1, h=1, x=0, y=0, cw=1, ch=1, raw=b"")
        self.assertIsNone(mod._fit_donor_to_us_canvas(real_us, blank_cn))

    def test_built_map_npks_preserve_exact_npkindex_offsets_and_sha256(self):
        manifest_path = mod.DEFAULT_BUILD_DIR / "manifest.json"
        if not manifest_path.exists():
            self.skipTest(f"{manifest_path} not built yet")
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        self.assertGreaterEqual(manifest["npks_patched"], 30)
        self.assertEqual(manifest["skipped_imgs"], [])

        # Spot-check the critical WorldMap + Minimap + SelectDungeon NPKs against clean backup/live baseline
        must_check = [
            "sprite_interface2_worldmap_step_1(town).NPK",
            "sprite_interface2_worldmap_step_2(area).NPK",
            "sprite_interface2_worldmap_step_2(area)_arad.NPK",
            "sprite_interface2_worldmap_step_2(area)_devildom.NPK",
            "sprite_interface2_worldmap_step_2(area)_heaven.NPK",
            "sprite_interface2_worldmap_step_3(world).NPK",
            "sprite_interface2_minimap_town.NPK",
            "sprite_worldmap.NPK",
            "sprite_worldmap_selectdungeonslot.NPK",
        ]
        for name in must_check:
            built = mod.DEFAULT_BUILD_DIR / name
            self.assertTrue(built.exists(), f"missing built NPK: {name}")
            clean = (mod.DEFAULT_BACKUP_DIR / name) if (mod.DEFAULT_BACKUP_DIR / name).exists() else (mod.DEFAULT_US_PACKS / name)
            orig = clean.read_bytes()
            patched = built.read_bytes()
            self.assertEqual(len(patched), len(orig), f"{name} size drifted")
            entries = mod._npk_member_table(orig)
            first_off = min(o for o, _, _ in entries)
            self.assertEqual(patched[:first_off], orig[:first_off], f"{name} header/NpkIndex table drifted")
            if verify_npk_header_sha256(orig):
                self.assertTrue(verify_npk_header_sha256(patched), f"{name} SHA-256 invalid")
            # Verify every IMG v2 frame inside parses cleanly with exact original frame count
            seen = set()
            for off, sz, img in entries:
                if (off, sz) in seen:
                    continue
                seen.add((off, sz))
                om = orig[off : off + sz]
                pm = patched[off : off + sz]
                if len(om) >= 32 and om[:16] == b"Neople Img File\x00" and struct.unpack_from("<I", om, 24)[0] == 2:
                    _, _, ufr = mod.parse_img_v2(om)
                    _, _, pfr = mod.parse_img_v2(pm)
                    self.assertEqual(len(pfr), len(ufr), f"{name}:{img} frame count changed")


if __name__ == "__main__":
    unittest.main()
