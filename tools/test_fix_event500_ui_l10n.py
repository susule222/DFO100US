# -*- coding: utf-8 -*-
import unittest
from pathlib import Path
import sys

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import fix_event500_ui_l10n as mod


class TestEvent500UIL10n(unittest.TestCase):
    def test_patch_upgrade_and_spec_xui_replaces_static_text(self):
        sample = (
            '<?xml version="1.0" encoding="UTF-16"?>'
            '<ROOT>'
            '<CNUIControlText ID="STR:base_item_name" Pos="VECTOR:45, 74" Size="VECTOR:300, 15" '
            'string="STR:Static Text" stringLenth="NUM:300" />'
            '</ROOT>'
        )
        patched, changed = mod.patch_upgrade_and_spec_xui(sample)
        self.assertTrue(changed)
        self.assertNotIn('Static Text', patched)
        self.assertIn('STR:&lt;20::branch_of_vandrassil_05&gt;', patched)
        # Idempotent on second run
        patched2, changed2 = mod.patch_upgrade_and_spec_xui(patched)
        self.assertFalse(changed2)
        self.assertEqual(patched, patched2)

    def test_patch_dstr_plain_updates_all_8_rows_and_preserves_crlf(self):
        rows = [
            '1>unrelated\r\n',
            '66333>无法与世界树铁砧同时使用。\r\n',
            '66336> %s已成功将世界树分支提升至 +%d。\r\n',
            '66337> %s未能将世界树分支提升至 +%d。\r\n',
            '100088591>分支增幅成功率是 ???\\n如果你失败，其增幅等级将会降低。\r\n',
            '100088592>分支增幅成功率为 %.0f%%。\\n如果你失败，其增幅等级将会降低。\r\n',
            '100088593>分支增幅成功率为 %.0f%%。\r\n',
            '100088822>+1只能提取出至少一个阶段的树枝。\r\n',
            '100088870>无法使用已提取能量的树枝。\r\n',
        ]
        patched, count = mod.patch_dstr_plain(''.join(rows))
        self.assertEqual(count, 8)
        self.assertIn('1>unrelated\r\n', patched)
        self.assertIn('100088593>灵魂之石强化成功率为 %.0f%%。\r\n', patched)
        self.assertNotIn('分支增幅', patched)
        self.assertNotIn('树枝', patched)


if __name__ == '__main__':
    unittest.main()
