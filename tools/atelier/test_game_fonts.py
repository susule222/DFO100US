from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from test_epic import epic,QApplication
from game_fonts import BITMAP_TABLES,GameFonts,ORIGINAL,faces
import core


class GameFontChecks(unittest.TestCase):
    def setUp(self):
        temp=tempfile.TemporaryDirectory();self.addCleanup(temp.cleanup)
        sandbox=Path(temp.name);self.folder=sandbox/'Fonts';self.folder.mkdir()
        self.fonts=GameFonts(self.folder,sandbox/'manager')
        guard=patch.object(core,'client_up',return_value=False);guard.start();self.addCleanup(guard.stop)

    def test_apply_real_simsun_and_restore(self):
        from fontTools.ttLib import TTFont
        src=core.pre.client_exe().parent/'Fonts'/'simsun.ttc'
        self.assertEqual(faces(src)[:2],['SimSun','NSimSun'])
        self.assertIn('SimSun',self.fonts.apply(src,0))
        self.assertTrue(self.fonts.state()['outline_only'])
        with TTFont(src,fontNumber=0) as donor:
            self.assertTrue(any(tag in donor for tag in BITMAP_TABLES))
            for n in ORIGINAL:
                with TTFont(self.folder/n) as built:
                    self.assertEqual(built['name'].getBestFamilyName().casefold(),Path(n).stem)
                    self.assertFalse(any(tag in built for tag in BITMAP_TABLES))
                    self.assertEqual(built['gasp'].gaspRange,{65535:15})
                    for c in '少女的手镯ABC1':
                        def shape(f):return f['glyf'][f.getBestCmap()[ord(c)]].getCoordinates(f['glyf'])
                        self.assertEqual(shape(built),shape(donor))
        before=self.fonts.hashes();self.fonts.apply(src,0);self.assertEqual(before,self.fonts.hashes())
        self.fonts.restore();self.assertEqual(self.fonts.hashes(),ORIGINAL)
        self.assertFalse((self.fonts.root/'pending.json').exists())

    def test_refuses_live_client_and_untracked_changes(self):
        with patch.object(core,'client_up',return_value=True),self.assertRaisesRegex(ValueError,'退出'):
            self.fonts.restore()
        self.assertEqual(list(self.folder.iterdir()),[])
        self.fonts.restore()
        (self.folder/'calibri.ttf').write_bytes(b'custom')
        with self.assertRaisesRegex(ValueError,'外部修改'):self.fonts.restore()
        self.assertEqual((self.folder/'calibri.ttf').read_bytes(),b'custom')

    def test_rollback_partial_install(self):
        with self.fonts.locked():
            baseline=self.fonts.originals_dir();copy=core.file_copy
            def fail_second(src,dst):
                if dst==self.folder/'tahoma.ttf':raise OSError('busy')
                return copy(src,dst)
            with patch.object(core,'file_copy',side_effect=fail_second),self.assertRaises(OSError):
                self.fonts.commit(baseline,{},self.fonts.hashes())
            self.assertEqual(list(self.folder.iterdir()),[]);self.assertEqual(self.fonts.state(),{})

    def test_missing_fonts_report_before_start(self):
        with patch.object(core.pre,'client_exe',return_value=self.folder.parent/'DFO.exe'),patch.object(core.subprocess,'Popen') as spawn:
            with self.assertRaisesRegex(ValueError,'calibri.ttf.*tahoma.ttf.*Mod 工坊'):core.login()
            spawn.assert_not_called()

    def test_mod_page_font_button_routes_to_selected_face(self):
        app=QApplication.instance() or QApplication([]);w=epic.EpicWindow()
        try:
            w.switch_page(4);ui=w.font_panel
            ui.source.setCurrentIndex(ui.source.findText('simsun.ttc'));ui.face.setCurrentIndex(1)
            with patch.object(w,'start_work') as start,patch.object(ui.service,'apply',return_value='ok') as apply:
                ui.apply_btn.click();self.assertEqual(start.call_args.args[1](),'ok')
                self.assertEqual(Path(apply.call_args.args[0]).name.lower(),'simsun.ttc')
                self.assertEqual(apply.call_args.args[1],1)
        finally:w.close()
