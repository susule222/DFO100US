"""Tests for real-time PVF/NPK source selection, IMG v6 palettes, aura animation, and visual Fitting Room."""
import os
from pathlib import Path
import struct
import sys
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
sys.path[:0] = [str(HERE.parents[1] / 'runtime' / 'atelier-qt'), str(HERE), str(HERE.parent)]
os.environ.setdefault('QT_QPA_PLATFORM', 'offscreen')

from PySide6.QtCore import Qt
from PySide6.QtWidgets import QApplication

import core
import epic
import live_resources as lr
from sprites import KEY


def _write_dummy_npk(path: Path, body: bytes):
    name = b'sprite/fixture.img'.ljust(256, b'\0')
    path.write_bytes(
        b'NeoplePack_Bill\0'
        + struct.pack('<III', 1, 284, len(body))
        + bytes(a ^ b for a, b in zip(name, KEY))
        + body
    )


class LiveResourcesAndVisualChecks(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = QApplication.instance() or QApplication([])
        epic.apply_theme(cls.app)
        cls.w = epic.EpicWindow()

    @classmethod
    def tearDownClass(cls):
        cls.w.close()

    def test_auto_pvf_picks_live_client_not_old_copy(self):
        auto_pvf = lr.auto_pvf_path()
        self.assertIsNotNone(auto_pvf)
        self.assertEqual(auto_pvf.name.lower(), 'script.pvf')
        self.assertEqual(auto_pvf.parent.resolve(), core.pre.client_dir().resolve())
        self.assertFalse(lr.is_inner_pvf(auto_pvf))
        fp = lr.pvf_fingerprint(auto_pvf)
        self.assertTrue(lr.is_catalog_synced(fp), 'Active catalog should be synced to live client Script.pvf')

    def test_config_roundtrip_and_npk_realtime_snapshot(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            cfg_path = base / 'resource_sources.json'
            npk_dir = base / 'packs'
            npk_dir.mkdir()
            p1 = npk_dir / 'a.NPK'
            _write_dummy_npk(p1, b'one')
            s1 = lr.npk_snapshot(npk_dir)
            self.assertEqual(s1['count'], 1)
            # Modify NPK in-place and verify snapshot token changes immediately
            _write_dummy_npk(p1, b'one-updated-longer')
            s2 = lr.npk_snapshot(npk_dir)
            self.assertNotEqual(s1['token'], s2['token'])
            # Add a second NPK
            _write_dummy_npk(npk_dir / 'b.NPK', b'two')
            s3 = lr.npk_snapshot(npk_dir)
            self.assertEqual(s3['count'], 2)
            self.assertNotEqual(s2['token'], s3['token'])

            # Config save/load roundtrip
            saved = lr.save_config({'pvf_mode': 'manual', 'pvf_path': str(p1), 'npk_mode': 'manual', 'npk_dir': str(npk_dir)}, cfg_path)
            loaded = lr.load_config(cfg_path)
            self.assertEqual(saved, loaded)
            self.assertEqual(loaded['npk_mode'], 'manual')

    def test_visual_fitting_room_11_slots_cards_weapon_aura_and_palettes(self):
        w = self.w
        w.switch_page(3)
        v = w.fitting
        # All 11 slots present in visual paper-doll (weapon + aura + 9 body slots)
        self.assertEqual(set(v.slot_tiles.keys()), {
            'weapon', 'aura', 'cap', 'hair', 'face', 'neck', 'coat', 'belt', 'pants', 'shoes', 'skin'
        })
        # Quick filter pills switch mode/slot and render visual cards with widgets
        v._on_pill_clicked('single:weapon')
        self.assertEqual(v.mode.currentData(), 'single')
        self.assertEqual(v.slot.currentData(), 'weapon')
        self.assertGreater(v.rows.count(), 0)
        self.assertIsNotNone(v.rows.itemWidget(v.rows.item(0)))
        # First card should be a real visual weapon (sorted ahead of clone/empty items)
        v.rows.setCurrentRow(0)
        v.try_on()
        self.assertIn('weapon', v.choice)
        self.assertTrue(v.slot_tiles['weapon'].equipped)
        self.assertEqual(v.figure.toolTip(), '')

        # Switch to aura via paper-doll tile click
        v._select_slot_pill('aura')
        self.assertEqual(v.slot.currentData(), 'aura')
        self.assertGreater(v.rows.count(), 0)
        v.rows.setCurrentRow(0)
        v.try_on()
        self.assertIn('aura', v.choice)
        self.assertTrue(v.slot_tiles['aura'].equipped)

        # Verify aura animation frame rendering if aura has multiple frames
        if v.stage.has_aura_animation(v.choice):
            im0, e0 = v.stage.render(v.job.currentData(), v.choice, aura_ms=0)
            im1, e1 = v.stage.render(v.job.currentData(), v.choice, aura_ms=360)
            self.assertFalse(e0)
            self.assertFalse(e1)
            self.assertTrue(im0.getbbox() and im1.getbbox())

        # Verify IMG v6 palette fallback works for fighter skin 46202 (ft_body0000.img palette 2)
        vis_skin = v.source.visual('equipment', 46202)
        if vis_skin is not None:
            im_skin, errs = v.stage.render('[fighter]', {'skin': vis_skin})
            self.assertFalse(errs, f'Expected v6 palette skin to render cleanly, got {errs}')
            self.assertTrue(im_skin.getbbox())

        # Unequip via slot tile callback
        v._unequip_slot('weapon')
        self.assertNotIn('weapon', v.choice)
        self.assertFalse(v.slot_tiles['weapon'].equipped)

        # Random outfit populates multiple slots cleanly
        v.random_outfit()
        self.assertGreaterEqual(len(v.choice), 5)
        self.assertFalse(v.figure.pixmap().isNull())
        v.reset()
        self.assertEqual(len(v.choice), 0)

    def test_cash_packages_use_real_visual_avatars_not_clones(self):
        w = self.w
        w.switch_page(3)
        v = w.fitting
        v.job.setCurrentIndex(v.job.findData('[swordman]'))
        import json
        # 50022810 (Hot Summer 2017 A) & 50023030 (Arthurian Knights A) must equip real look avatars, not clones
        for pkg_id, expected_sample in ((50022810, 501550233), (50023030, 501550253)):
            pack = v.source.visual('stackable', pkg_id)
            self.assertIsNotNone(pack)
            mids = json.loads(pack['visual'] or '[]')
            self.assertIn(expected_sample, mids, f'Package {pkg_id} should resolve real visual avatars, got {mids}')
            self.assertNotIn(501550010, mids, f'Package {pkg_id} must not use blank clone avatar 501550010')
            choice = {r['slot']: r for mid in mids if (r := v.source.visual('equipment', mid))}
            im, errs = v.stage.render('[swordman]', choice)
            self.assertFalse(errs, (pkg_id, errs))
            self.assertTrue(im.getbbox())

    def test_lv100_wallpaper_and_hero_artwork_loaded(self):
        import theme_art
        w = self.w
        self.assertIsNotNone(w.root_widget._raw_bg)
        self.assertGreaterEqual(w.root_widget._raw_bg.width, 1000)
        self.assertIsNotNone(w.hero_banner._hero_raw)
        # Switch wallpaper live and confirm it updates cleanly
        for key, *_ in theme_art.WALLPAPERS[:3]:
            im = theme_art.load_wallpaper(w.npk_dir, key)
            self.assertIsNotNone(im, f'Missing wallpaper {key}')
            self.assertGreaterEqual(im.width, 1000)


if __name__ == '__main__':
    unittest.main()
