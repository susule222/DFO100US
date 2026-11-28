# -*- coding: utf-8 -*-
import unittest
from pathlib import Path
import sys

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import fix_event_detail_ui as fed  # noqa: E402


SAMPLE_XUI = (
    '\ufeff<?xml version="1.0" encoding="UTF-8" ?>\r\n'
    '<ROOT ScriptVer="1.0">\r\n'
    '    <CONTROL>\r\n'
    '        <CNBoardImage ID="STR:0" Size="VECTOR:243, 230">\r\n'
    '            <CNUIControlText ID="STR:1" Pos="VECTOR:6, 6" Size="VECTOR:106, 11" />\r\n'
    '            <CNUIControlImage ID="STR:bg" ImagePack="STR:Interface2/PVP/PVP_Fair/PVP_Fair.img" '
    'Pos="VECTOR:57, 19" Size="VECTOR:316, 22" imageAutoResize="BOOL:false" imageIndex="NUM:22" />\r\n'
    '            <CNLabelHyperLink ID="STR:4" Pos="VECTOR:4, 40" Size="VECTOR:200, 20">\r\n'
    '                <CNUIControlImage ID="STR:5" ImagePack="STR:Interface2/UI/EventList/Event_window.img" '
    'Pos="VECTOR:-15, -1" Size="VECTOR:14, 13" imageIndex="NUM:2" />\r\n'
    '            </CNLabelHyperLink>\r\n'
    '        </CNBoardImage>\r\n'
    '    </CONTROL>\r\n'
    '</ROOT>\r\n\x00'
)


class FixEventDetailUITest(unittest.TestCase):
    def test_all_13_events_parse_cleanly_in_parse_markup_simulator(self):
        kv = fed.build_event_str_map()
        self.assertEqual(len(fed.US100_EVENT_DETAILS), 13)
        self.assertEqual(len(kv), 39)
        for ev_id, spec in fed.US100_EVENT_DETAILS.items():
            detail = kv[f'us100_ev_detail_{ev_id}']
            controls, remaining = fed.simulate_parse_markup(detail)
            self.assertEqual(len(controls), len(spec['lines']), f'event {ev_id}')
            for idx, (text, x, y, color) in enumerate(controls):
                self.assertEqual(text, spec['lines'][idx][0], f'event {ev_id} line {idx}')
                self.assertEqual(x, 14)
                self.assertEqual(y, 14 + idx * 24)
                self.assertEqual(color, spec['lines'][idx][1])
            self.assertGreaterEqual(remaining.count('\n'), 12)

    def test_patch_xui_hides_bg_face_and_preserves_id(self):
        patched, stats = fed.patch_eventdesignwindow_xui_text(SAMPLE_XUI)
        self.assertEqual(stats['bg_patched'], 1)
        self.assertNotIn('PVP_Fair.img', patched)
        self.assertIn('ID="STR:bg"', patched)
        self.assertIn('IsShow="BOOL:false"', patched)
        self.assertIn('IsVisible="BOOL:false"', patched)
        self.assertIn('Pos="VECTOR:-2000, -2000"', patched)
        # Idempotent on second run
        patched2, stats2 = fed.patch_eventdesignwindow_xui_text(patched)
        self.assertEqual(stats2['bg_patched'], 0)
        self.assertEqual(patched, patched2)

    def test_patch_eventlistwindow_tokens_idempotent_and_preserves_originals(self):
        orig = [
            {'type': 3, 'text': '[entry]'},
            {'type': 3, 'text': '[event index]'},
            {'type': 0, 'value': 174},
            {'type': 3, 'text': '[/entry]'},
        ]
        patched, stats = fed.patch_eventlistwindow_tokens(orig)
        self.assertTrue(stats['changed'])
        self.assertEqual(stats['preserved_entries'], 1)
        self.assertEqual(len(stats['upserted_events']), 13)
        script = fed.render_evt_script(patched)
        self.assertIn('[popupwindow design]', script)
        self.assertIn('<23::us100_ev_detail_520>', script)
        # Second run is a no-op even when pvfinspect attaches extra raw/offset metadata
        with_meta = [dict(t, raw=123, offset=idx * 5) for idx, t in enumerate(patched)]
        patched2, stats2 = fed.patch_eventlistwindow_tokens(with_meta)
        self.assertFalse(stats2['changed'])
        self.assertEqual([fed._token_key(t) for t in patched], [fed._token_key(t) for t in patched2])

    def test_patch_informationbuttoninfo_repairs_event_306_and_is_idempotent(self):
        orig = [
            {'type': 3, 'text': '[info]'},
            {'type': 3, 'text': '[id]'}, {'type': 0, 'value': 110},
            {'type': 3, 'text': '[event]'}, {'type': 0, 'value': 306},
            {'type': 3, 'text': '[link popupwindow type]'}, {'type': 6, 'text': 'free cash pcroom'}, {'type': 0, 'value': 0}, {'type': 0, 'value': 0},
            {'type': 3, 'text': '[tooltip]'}, {'type': 6, 'text': 'enable'}, {'type': 8, 'reference': '<20::us100_act_tooltip_306>'},
            {'type': 3, 'text': '[/info]'},
            {'type': 3, 'text': '[info]'},
            {'type': 3, 'text': '[id]'}, {'type': 0, 'value': 204},
            {'type': 3, 'text': '[event]'}, {'type': 0, 'value': 392},
            {'type': 3, 'text': '[link popupwindow type]'}, {'type': 6, 'text': 'daily select mission window'}, {'type': 0, 'value': 0}, {'type': 0, 'value': 0},
            {'type': 3, 'text': '[/info]'},
        ]
        patched, stats = fed.patch_informationbuttoninfo_tokens(orig)
        self.assertTrue(stats['changed'])
        self.assertEqual(stats['blocks'], 2)
        self.assertEqual(stats['patched_buttons'], [{'id': 110, 'event': 306, 'popup': 'design popup window'}])
        script = fed.render_evt_script(patched)
        self.assertIn('`design popup window`', script)
        self.assertNotIn('free cash pcroom', script)
        self.assertIn('`daily select mission window`', script)
        patched2, stats2 = fed.patch_informationbuttoninfo_tokens(patched)
        self.assertFalse(stats2['changed'])
        self.assertEqual([fed._token_key(t) for t in patched], [fed._token_key(t) for t in patched2])


if __name__ == '__main__':
    unittest.main()
