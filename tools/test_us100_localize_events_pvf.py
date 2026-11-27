# -*- coding: utf-8 -*-
from __future__ import annotations

import re
import sys
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path[:0] = [str(HERE), str(HERE / 'atelier')]

import us100_localize_events_pvf as mod  # noqa: E402


class TestEventPvfLocalization(unittest.TestCase):
    def test_all_patches_are_nonempty_chinese_without_korean(self):
        ko_re = re.compile(r'[\uac00-\ud7a3]')
        zh_re = re.compile(r'[\u4e00-\u9fff]')
        total = 0
        for table, kv in mod.ALL_PATCHES.items():
            self.assertGreater(len(kv), 0, f'empty patch map for {table}')
            for k, v in kv.items():
                total += 1
                self.assertTrue(k and '>' not in k, f'bad key {k!r}')
                self.assertTrue(v.strip(), f'empty value for {table}:{k}')
                self.assertFalse(ko_re.search(v), f'Korean remained in {table}:{k}={v!r}')
                self.assertTrue(zh_re.search(v), f'missing Chinese in {table}:{k}={v!r}')
                self.assertNotIn('\n', v)
                self.assertNotIn('\r', v)
        self.assertEqual(total, 65)

    def test_required_event_items_and_becky_dialogs_covered(self):
        # Event 520 reward boxes + recursive children/grandchildren
        for iid in (10333092, 10333113, 10333090, 10333117, 10333114, 10333115, 10333116, 10333089, 10333091):
            self.assertIn(f'name_{iid}', mod.STACKABLE_STRING_PATCHES)
            self.assertIn(f'explain_{iid}', mod.STACKABLE_STRING_PATCHES)
        # Event 619 reward items (including child material 10326708)
        for iid in range(10326700, 10326710):
            self.assertIn(f'name_{iid}', mod.STACKABLE_STRING_PATCHES)
            self.assertIn(f'explain_{iid}', mod.STACKABLE_STRING_PATCHES)
        # Event 520 Becky speech bubble keys
        self.assertIn('event_520_becky_dialog_start', mod.EVENT_STRING_PATCHES)
        for i in range(10):
            self.assertIn(f'event_520_becky_dialog_{i:02d}', mod.EVENT_STRING_PATCHES)

    def test_overlay_render_and_utf16_roundtrip(self):
        text = mod.render_uv_str_overlay(mod.UI_STRING_PATCHES)
        self.assertTrue(text.endswith('\r\n'))
        raw_utf16 = ('\ufeff' + text).encode('utf-16-le')
        parsed = mod.parse_uv_str_utf16(raw_utf16)
        self.assertEqual(parsed, mod.UI_STRING_PATCHES)

    def test_compute_missing_patches_idempotent(self):
        self.assertEqual(mod.compute_missing_patches(mod.ALL_PATCHES), {})
        partial = {
            mod.STK_STR_ENTRY: dict(mod.STACKABLE_STRING_PATCHES),
            mod.EV_STR_ENTRY: {},
            mod.UI_STR_ENTRY: dict(mod.UI_STRING_PATCHES),
        }
        diff = mod.compute_missing_patches(partial)
        self.assertEqual(set(diff.keys()), {mod.EV_STR_ENTRY})
        self.assertEqual(diff[mod.EV_STR_ENTRY], mod.EVENT_STRING_PATCHES)


if __name__ == '__main__':
    unittest.main()
