# -*- coding: utf-8 -*-
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import fix_everyday_connect_ui as fec


SAMPLE_MAIN = '''<?xml version="1.0" encoding="UTF-8" ?>
<ROOT ScriptVer="1.0">
    <POPUPWINDOW IsShowTitleBar="BOOL:true" Size="VECTOR:540, 440" />
    <CONTROL>
        <CNUIControlImage ID="STR:bg" ImagePack="STR:Interface2/event/EveryDayConnect/EveryDayConnect.img" imageIndex="NUM:0">
            <CNUIControlImage ImagePack="STR:Interface2/event/EveryDayConnect/MainBanner.img" Size="VECTOR:519, 90" imageIndex="NUM:7" />
            <CNButton ButtonName="STR:&lt;20::event_515_window_4&gt;" ID="STR:allRecvBtn" IsEnable="BOOL:false" IsVisible="BOOL:false" Pos="VECTOR:451, 369" Size="VECTOR:60, 24" />
            <CNLabelHyperLink ID="STR:hyper" Pos="VECTOR:447, 407" Size="VECTOR:77, 13">
                <CNUIControlImage ID="STR:hyper_img" imageIndex="NUM:2" />
            </CNLabelHyperLink>
        </CNUIControlImage>
    </CONTROL>
</ROOT>'''

SAMPLE_CALENDAR = '''<?xml version="1.0" encoding="UTF-8" ?>
<ROOT ScriptVer="1.0">
    <POPUPWINDOW IsShowTitleBar="BOOL:true" Size="VECTOR:274, 400" />
    <CONTROL>
        <CNUIControlImage ImagePack="STR:Interface2/event/EveryDayConnect/CalendarBanner.img" Size="VECTOR:261, 78" imageIndex="NUM:7" />
        <CNLabelHyperLink ID="STR:hyper" Pos="VECTOR:191, 369" Size="VECTOR:77, 13">
            <CNUIControlImage ID="STR:hyper_img" imageIndex="NUM:2" />
        </CNLabelHyperLink>
    </CONTROL>
</ROOT>'''

SAMPLE_SLOT = '''<?xml version="1.0" encoding="UTF-8" ?>
<ROOT ScriptVer="1.0">
    <CONTROL>
        <CNUIControlText ID="STR:dailyText" Pos="VECTOR:12, 5" Size="VECTOR:32, 12" sortType="NUM:1" string="STR:-" stringLenth="NUM:32" />
    </CONTROL>
</ROOT>'''


class FixEveryDayConnectUITest(unittest.TestCase):
    def test_patch_main_xui_idempotent(self):
        out, st = fec.patch_main_xui_text(SAMPLE_MAIN)
        self.assertTrue(st['changed'])
        self.assertIn(fec.TITLE_ATTR, out)
        self.assertIn('imageIndex="NUM:0"', out)
        self.assertIn('Size="VECTOR:519, 58"', out)
        self.assertIn('ID="STR:allRecvBtn" IsEnable="BOOL:false" IsVisible="BOOL:true"', out)
        self.assertIn('IsVisible="BOOL:false"', out)
        out2, st2 = fec.patch_main_xui_text(out)
        self.assertFalse(st2['changed'])
        self.assertEqual(out, out2)

    def test_patch_calendar_xui_idempotent(self):
        out, st = fec.patch_calendar_xui_text(SAMPLE_CALENDAR)
        self.assertTrue(st['changed'])
        self.assertIn(fec.TITLE_ATTR, out)
        self.assertIn('imageIndex="NUM:0"', out)
        out2, st2 = fec.patch_calendar_xui_text(out)
        self.assertFalse(st2['changed'])
        self.assertEqual(out, out2)

    def test_patch_slot_daily_text_idempotent(self):
        out, st = fec.patch_slot_daily_text(SAMPLE_SLOT, pos='0, 5', width=52)
        self.assertTrue(st['changed'])
        self.assertIn('Pos="VECTOR:0, 5"', out)
        self.assertIn('Size="VECTOR:52, 12"', out)
        self.assertIn('stringLenth="NUM:52"', out)
        out2, st2 = fec.patch_slot_daily_text(out, pos='0, 5', width=52)
        self.assertFalse(st2['changed'])
        self.assertEqual(out, out2)


if __name__ == '__main__':
    unittest.main()
