# -*- coding: utf-8 -*-
from __future__ import annotations

import re
import sys
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path[:0] = [str(HERE), str(HERE / 'atelier')]

import us100_localize_titles_pvf as mod  # noqa: E402


class TestTitlePvfLocalization(unittest.TestCase):
    def test_all_curated_maps_are_valid_chinese(self):
        ko_re = re.compile(r'[\uac00-\ud7a3]')
        zh_re = re.compile(r'[\u4e00-\u9fff]')
        self.assertEqual(len(mod.NO_ANI_TITLE_ZH), 6)
        self.assertGreaterEqual(len(mod.ANI_TITLE_ZH), 290)
        for k, v in list(mod.ANI_TITLE_ZH.items()) + [(str(i), n) for i, n in mod.NO_ANI_TITLE_ZH.items()]:
            self.assertTrue(v.strip(), f'empty value for {k}')
            self.assertFalse(ko_re.search(v), f'Korean in {k}={v!r}')
            self.assertTrue(zh_re.search(v), f'missing Chinese in {k}={v!r}')
            self.assertNotIn('\n', v)
            self.assertNotIn('\r', v)

    def test_screenshot_target_100331498_resolved(self):
        blanks = [
            {'id': 100331498, 'path': 'equipment/character/common/title/100331498.equ', 'ani': 'TitleAnimation/2015_ActionChobung.ani'},
            {'id': 100330003, 'path': 'equipment/character/common/title/100330003.equ', 'ani': 'TitleAnimation/unknown_box.ani'},
            {'id': 100331177, 'path': 'equipment/character/common/title/100331177.equ', 'ani': ''},
        ]
        patches, unresolved = mod.resolve_blank_title_names(
            blanks,
            ani_to_named={},
            tid_to_box={100330003: '草原之幻想 (棕熊)礼盒'},
        )
        self.assertEqual(unresolved, [])
        self.assertEqual(patches['name_100331498'], '2015格斗大赛·超崩')
        self.assertEqual(patches['name_100330003'], '草原之幻想 (棕熊)')
        self.assertEqual(patches['name_100331177'], mod.NO_ANI_TITLE_ZH[100331177])

    def test_overlay_utf16_roundtrip(self):
        sample = {'name_100331498': '2015格斗大赛·超崩', 'name_100331177': '樱花庆典纪念称号 (一)'}
        text = mod.render_uv_str_overlay(sample)
        parsed = mod.parse_uv_str_utf16(('\ufeff' + text).encode('utf-16-le'))
        self.assertEqual(parsed, sample)


if __name__ == '__main__':
    unittest.main()
