# -*- coding: utf-8 -*-
from __future__ import annotations

import sys
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path[:0] = [str(HERE), str(HERE / 'atelier')]

import us100_localize_avatars_pvf as mod  # noqa: E402


class TestAvatarPvfLocalization(unittest.TestCase):
    def test_resolution_hierarchy_and_zero_var_guard(self):
        blanks = [
            # Exact (job, part, va, vb) match
            {'id': 101551928, 'key': 'name_101551928', 'job': 'swordman', 'part': 'cap', 'va': 161, 'vb': 0, 'rarity': 0},
            # Exact (job, part, va) match with alternate color vb=2 -> [C型]
            {'id': 101551930, 'key': 'name_101551930', 'job': 'swordman', 'part': 'cap', 'va': 161, 'vb': 2, 'rarity': 0},
            # Booster box theme fallback
            {'id': 101600999, 'key': 'name_101600999', 'job': 'swordman', 'part': 'coat', 'va': 999, 'vb': 1, 'rarity': 2},
            # Aura with va=0, vb=0 MUST NOT inherit random (0, 0) aura name
            {'id': 101590229, 'key': 'name_101590229', 'job': 'swordman', 'part': 'aura', 'va': 0, 'vb': 0, 'rarity': 2},
        ]
        patches, stats = mod.resolve_blank_avatar_names(
            blanks,
            var_full_named={
                'swordman|cap|161|0': '哥特风格耳机 [A型]',
                'swordman|aura|0|0': '错误的默认光环',
            },
            var_a_named={'swordman|cap|161': '哥特风格耳机 [A型]'},
            tid_to_box={101600999: '愚人节纪念装扮套装礼盒'},
        )
        self.assertEqual(patches['name_101551928'], '哥特风格耳机 [A型]')
        self.assertEqual(patches['name_101551930'], '哥特风格耳机 [C型]')
        self.assertEqual(patches['name_101600999'], '愚人节纪念上衣 [B型]')
        self.assertNotEqual(patches['name_101590229'], '错误的默认光环')
        self.assertEqual(patches['name_101590229'], '纪念光环装扮 (鬼剑士)')
        self.assertEqual(stats, {'var_full': 1, 'var_a': 1, 'box': 1, 'structured': 1})

    def test_overlay_utf16_roundtrip(self):
        sample = {'name_101551928': '哥特风格耳机 [A型]', 'name_101590229': '纪念光环装扮 (鬼剑士)'}
        text = mod.render_uv_str_overlay(sample)
        parsed = mod.parse_uv_str_utf16(('\ufeff' + text).encode('utf-16-le'))
        self.assertEqual(parsed, sample)


if __name__ == '__main__':
    unittest.main()
