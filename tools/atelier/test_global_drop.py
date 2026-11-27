import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

HERE = Path(__file__).resolve().parent
sys.path[:0] = [str(HERE.parents[1] / 'runtime' / 'atelier-qt'), str(HERE), str(HERE.parent)]
os.environ.setdefault('QT_QPA_PLATFORM', 'offscreen')
os.environ['ATELIER_NO_AUTO_SYNC'] = '1'

import core
import epic
from PySide6.QtWidgets import QApplication, QMessageBox


class GlobalDropTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = QApplication.instance() or QApplication([])
        epic.apply_theme(cls.app)

    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.data = Path(tmp.name)
        for p in (
            patch.object(core, 'DATA', self.data),
            patch.object(epic, 'PRESET', self.data / 'preset.json'),
            patch.object(core, 'roles', return_value=[(71, '角色甲')]),
            patch.object(core, 'gateway', return_value=None),
        ):
            p.start()
            self.addCleanup(p.stop)
        self.w = epic.EpicWindow()
        self.addCleanup(self.w.close)

    def test_nav_renamed_and_no_startup_writes(self):
        self.assertIn('全局掉落设置', self.w.nav_btns[2].text())
        self.assertFalse(any(self.data.iterdir()))

    def test_difficulty_and_abyss_tiers_save_and_reload(self):
        dp = self.w.drop_page
        self.assertEqual(len(dp.diff_cards), 5)

        # Edit 噩梦难度 (index 4)
        dp._select_difficulty(4)
        dp.diff_chance_spin.setValue(68)
        dp.diff_mult_spin.setValue(2.5)
        dp.diff_count_spin.setValue(3)
        dp.diff_rw_spins['4'].setValue(50)
        dp.diff_rw_spins['7'].setValue(15)
        dp.diff_pool_widget._fill_sample()
        self.assertGreater(len(dp.diff_pool_widget.item_ids()), 0)

        # Edit 普通深渊掉落
        dp.na_enable_chk.setChecked(True)
        dp.na_chance_spin.setValue(42)
        dp.na_count_spin.setValue(2)
        dp.na_rw_spins['4'].setValue(70)
        dp.na_pool_widget._fill_sample()
        self.assertGreater(len(dp.na_pool_widget.item_ids()), 0)

        # Edit 100级深渊掉落 (智慧的引导)
        dp.la_enable_chk.setChecked(True)
        dp.la_chance_spin.setValue(85)
        dp.la_count_spin.setValue(3)
        dp.la_epic_spin.setValue(60)
        dp.la_mythic_spin.setValue(40)
        dp.la_pool_widget._fill_sample()
        self.assertGreater(len(dp.la_pool_widget.item_ids()), 0)

        # Save to gateway JSON
        with patch.object(QMessageBox, 'information', return_value=QMessageBox.Ok):
            dp.save_global_policy()

        saved_path = self.data / 'global_drop.json'
        self.assertTrue(saved_path.is_file())
        loaded, from_disk = core.load_global_drop(self.data)
        self.assertTrue(from_disk)
        diff4 = loaded['global']['difficulties'][4]
        self.assertEqual(diff4['equip_drop_chance'], 68)
        self.assertEqual(diff4['drop_count'], 3)
        self.assertEqual(diff4['rarity_weights']['7'], 15)
        self.assertEqual(loaded['normal_abyss']['drop_chance'], 42)
        self.assertEqual(loaded['normal_abyss']['drop_count'], 2)
        self.assertEqual(loaded['lv100_abyss']['drop_chance'], 85)
        self.assertEqual(loaded['lv100_abyss']['mythic_weight'], 40)
        self.assertGreater(len(loaded['lv100_abyss']['custom_item_ids']), 0)

    def test_quick_presets_and_validation(self):
        dp = self.w.drop_page
        dp.quick_preset_combo.setCurrentIndex(2)  # 神话狂欢
        dp._apply_quick_preset()
        self.assertEqual(dp.policy['lv100_abyss']['drop_chance'], 100)
        self.assertEqual(dp.policy['lv100_abyss']['mythic_weight'], 45)

        # Custom pool mode without items should reject on save
        bad = core.default_global_drop()
        bad['lv100_abyss']['pool_mode'] = 'custom'
        bad['lv100_abyss']['custom_item_ids'] = []
        with self.assertRaises(ValueError):
            core.save_global_drop(bad, self.data)


if __name__ == '__main__':
    unittest.main()
