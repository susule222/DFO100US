# -*- coding: utf-8 -*-
"""Unit tests for tools/localize_event_tooltips.py."""
from __future__ import annotations

import sys
from pathlib import Path
import unittest

HERE = Path(__file__).resolve().parent
sys.path[:0] = [str(HERE)]

import localize_event_tooltips as let  # noqa: E402


class LocalizeEventTooltipsTests(unittest.TestCase):
    def test_patch_evn_tokens_adds_enable_disable_and_is_idempotent(self):
        sample = [
            {'type': 3, 'text': '[info]'},
            {'type': 3, 'text': '[id]'},
            {'type': 0, 'value': 254},
            {'type': 3, 'text': '[event]'},
            {'type': 0, 'value': 619},
            {'type': 3, 'text': '[link popupwindow type]'},
            {'type': 6, 'text': 'time attendance random pick event window'},
            {'type': 0, 'value': 0},
            {'type': 0, 'value': 0},
            {'type': 3, 'text': '[/info]'},
            {'type': 3, 'text': '[info]'},
            {'type': 3, 'text': '[id]'},
            {'type': 0, 'value': 0},
            {'type': 3, 'text': '[tooltip]'},
            {'type': 6, 'text': 'enable'},
            {'type': 8, 'reference': '<20::info_tooltip_enable_000>'},
            {'type': 3, 'text': '[/info]'},
        ]

        patched, st1 = let.patch_evn_tokens(sample)
        self.assertEqual(st1['total_blocks'], 2)
        self.assertEqual(st1['patched_blocks'], 1)
        self.assertEqual(st1['patched_events'], [619])
        self.assertEqual(len(patched), len(sample) + 6)

        rendered = let.render_evn_script(patched)
        self.assertIn('[tooltip]\n`enable` <20::us100_act_tooltip_619>', rendered)
        self.assertIn('[tooltip]\n`disable` <20::us100_act_tooltip_619>', rendered)
        self.assertIn('<20::info_tooltip_enable_000>', rendered)

        patched2, st2 = let.patch_evn_tokens(patched)
        self.assertEqual(st2['patched_blocks'], 0)
        self.assertEqual(st2['already_matching'], 1)
        self.assertEqual(patched2, patched)

    def test_overlay_contains_all_chinese_labels(self):
        overlay = let.render_ui_str_overlay()
        for ev_id, (key, label) in let.EVENT_TOOLTIPS.items():
            self.assertIn(f'{key}>{label}', overlay, f'missing event {ev_id}')
            self.assertTrue(any('\u4e00' <= ch <= '\u9fff' for ch in label))


if __name__ == '__main__':
    unittest.main()
