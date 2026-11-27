import sys
from pathlib import Path
import unittest

HERE = Path(__file__).resolve().parent
sys.path[:0] = [str(HERE / 'atelier'), str(HERE)]

import fix_channel_select_ui as fcu


SAMPLE_XUI = '''<?xml version="1.0" encoding="UTF-8" ?>
<ROOT ScriptVer="1.0">
    <CONTROL>
        <CNUIControlImage ID="STR:1212" ImagePack="STR:Interface2/Channel/channelselect/channelbase/ChannelBase.img" Pos="VECTOR:344, 33" Size="VECTOR:113, 32" imageAutoResize="BOOL:false" imageIndex="NUM:0" />
        <CNUIControlImage ID="STR:1210" ImagePack="STR:Interface2/Channel/channelselect/channelbase/ChannelBase.img" Pos="VECTOR:592, 17" Size="VECTOR:196, 47" imageAutoResize="BOOL:false" imageIndex="NUM:9" />
        <CNUIControlImage ID="STR:1213" ImagePack="STR:Interface2/Channel/channelselect/channelbase/ChannelBase.img" Pos="VECTOR:273, 73" Size="VECTOR:256, 36" imageAutoResize="BOOL:false" imageIndex="NUM:1" />
        <CNUIControlImage ID="STR:1221" ImagePack="STR:Interface2/Channel/channelselect/channelbase/ChannelBase.img" Pos="VECTOR:119, 151" Size="VECTOR:560, 37" imageAutoResize="BOOL:false" imageIndex="NUM:21" />
        <CNCheckBox CaptionType="STR:Right" CheckEffectImgAlpha="NUM:255" CheckEffectImgIndex="NUM:24" CheckEffectImgPackName="STR:Interface2/Channel/channelselect/channelbase/ChannelBase.img" CheckImgIndex="NUM:0" DisableImgIndex="NUM:0" ID="STR:0" ImagePack="STR:Interface2/Channel/channelselect/ChannelSlot/ChannelSlot3.img" IsCheck="BOOL:false" OverEffectImgAlpha="NUM:128" OverEffectImgIndex="NUM:25" OverEffectImgPackName="STR:Interface2/Channel/channelselect/channelbase/ChannelBase.img" OverImgIndex="NUM:0" Pos="VECTOR:146, 196" Size="VECTOR:172, 40" UncheckImgIndex="NUM:0">
        </CNCheckBox>
        <CNButton ButtonDrawType="STR:NORMALIMAGE" ClickSound="STR:CLICK_BUTTON1" DisableImg="NUM:6" DownImg="NUM:5" ID="STR:2002" ImagePack="STR:Interface2/Channel/channelselect/channelbase/channelbase.img" OverImg="NUM:4" Pos="VECTOR:619, 466" Size="VECTOR:64, 20" UpImg="NUM:3" addImageScale="VECTOR2F:0.000000, 0.000000" />
        <CNUIControlImage ID="STR:-1" ImagePack="STR:Interface2/Channel/channelselect/channelbase/ChannelBase.img" Pos="VECTOR:146, 417" Size="VECTOR:565, 1" imageAutoResize="BOOL:false" imageIndex="NUM:8" />
    </CONTROL>
</ROOT>
'''


class ChannelSelectUIFixTests(unittest.TestCase):
    def test_patch_eliminates_all_oob_and_misaligned_channelbase_refs(self):
        raw = SAMPLE_XUI.encode('utf-16-le')
        fixed_raw, stats = fcu.patch_channel_xui_bytes(raw)
        fixed = fixed_raw.decode('utf-16-le')

        self.assertEqual(stats['before_oob21'], 1)
        self.assertEqual(stats['before_ck24'], 1)
        self.assertEqual(stats['before_ov25'], 1)
        self.assertEqual(stats['after_oob'], 0)
        self.assertEqual(stats['after_channelbase_refs'], 0)

        # Control IDs preserved for C++ lookups
        for cid in ('STR:1210', 'STR:1212', 'STR:1213', 'STR:1221', 'STR:0', 'STR:2002', 'STR:-1'):
            self.assertIn(cid, fixed)

        # Broken overlay images moved off-screen and hidden
        self.assertEqual(fixed.count('Pos="VECTOR:-2000, -2000"'), 5)
        self.assertEqual(fixed.count('IsShow="BOOL:false"'), 5)

        # Channel checkbox uses valid 166x40 glow in ChannelSlot2.img[25]
        self.assertIn('CheckEffectImgPackName="STR:Interface2/Channel/channelselect/ChannelSlot/ChannelSlot2.img"', fixed)
        self.assertIn('OverEffectImgPackName="STR:Interface2/Channel/channelselect/ChannelSlot/ChannelSlot2.img"', fixed)
        self.assertIn('CheckEffectImgIndex="NUM:25"', fixed)
        self.assertIn('OverEffectImgIndex="NUM:25"', fixed)

        # Refresh button switched to native WindowCommon PATTERN
        self.assertIn('ButtonDrawType="STR:PATTERN" ButtonName="STR:刷新"', fixed)
        self.assertIn('patternImgPack="STR:Interface/WindowCommon.img"', fixed)

        # Idempotent on second pass
        fixed_raw2, stats2 = fcu.patch_channel_xui_bytes(fixed_raw)
        self.assertEqual(fixed_raw2, fixed_raw)
        self.assertEqual(stats2['after_oob'], 0)


if __name__ == '__main__':
    unittest.main()
