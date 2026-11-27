import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from test_epic import core, epic, QApplication
from PySide6.QtCore import Qt
from PySide6.QtWidgets import QMessageBox


class FittingChecks(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app=QApplication.instance() or QApplication([])
        epic.apply_theme(cls.app)
        cls.w=epic.EpicWindow()

    @classmethod
    def tearDownClass(cls):cls.w.close()

    def test_all_professions_use_their_stand(self):
        w=self.w;w.switch_page(3);v=w.fitting
        self.assertEqual(v.job.count(),16)
        for i in range(v.job.count()):
            job=v.job.itemData(i)
            pose=v.source.poses[job]
            self.assertGreater(pose['Frame'],0)
            img,err=v.stage.render(job,{})
            self.assertFalse(err,(job,err));self.assertTrue(img.getbbox())

    def test_search_set_weapon_aura_and_job_reset(self):
        w=self.w;w.switch_page(3);v=w.fitting
        v.job.setCurrentIndex(v.job.findData('[swordman]'));v.reset()
        def select(mode,slot,ident):
            v.mode.setCurrentIndex(v.mode.findData(mode));v.slot.setCurrentIndex(v.slot.findData(slot))
            v.search.setText(str(ident));v.delay.stop();v.reload()
            for i in range(v.rows.count()):
                if v.rows.item(i).data(Qt.UserRole)[0]==ident:
                    v.rows.setCurrentRow(i);v.try_btn.click();break
            else:self.fail(f'item not found: {ident}')
            self.assertTrue(v.figure.pixmap() and not v.figure.pixmap().isNull())
            self.assertEqual(v.figure.toolTip(),'')

        select('set','',74)
        pack=v.source.visual('stackable',74)
        self.assertEqual({r['id'] for r in v.choice.values()},set(json.loads(pack['visual'])))
        set_pixels=v.figure.pixmap().toImage()
        select('single','weapon',501600816)
        sword_pixels=v.figure.pixmap().toImage();self.assertNotEqual(set_pixels,sword_pixels)
        select('single','aura',501590377)
        self.assertNotEqual(sword_pixels,v.figure.pixmap().toImage())
        self.assertTrue({'weapon','aura'}<=set(v.choice))
        v.remove();self.assertNotIn('aura',v.choice);self.assertIn('weapon',v.choice)
        v.job.setCurrentIndex(v.job.findData('[at mage]'))
        self.assertFalse(v.choice)
        v.search.setText('天外无此装扮xyz');v.reload()
        self.assertFalse(v.rows.count());self.assertFalse(v.try_btn.isEnabled())

    def test_full_catalog_and_earrings(self):
        source=self.w.source
        total=source.db.execute('select count(*) from item').fetchone()[0]
        self.assertGreater(total,390000)
        rings=source.match('','特殊装备-耳环','全部稀有度','全部职业')
        self.assertGreater(len(rings),1000)
        self.assertEqual(source.grantmode(100390336),2)
        row=source.match('军神的心之所念','特殊装备-耳环','全部稀有度','全部职业')[0]
        self.assertIsNotNone(self.w.item_icons.read(row.icon).getbbox())

    def test_realtime_avatar_grant_outfit_and_single(self):
        w=self.w;w.switch_page(3);v=w.fitting
        with tempfile.TemporaryDirectory() as tmp:
            data=Path(tmp)
            orig_grant=core.grant_avatars
            with patch.object(core,'DATA',data), \
                 patch.object(core,'gateway',return_value={'address':'127.0.0.2:64604','avatar_gm':True}), \
                 patch.object(core,'roles',return_value=[(71,'鬼剑一号')]), \
                 patch.object(core,'grant_avatars',side_effect=lambda r,ids,reason='':orig_grant(r,ids,reason=reason,directory=data)), \
                 patch.object(QMessageBox,'question',return_value=QMessageBox.Yes):
                v.refresh_roles()
                v.job.setCurrentIndex(v.job.findData('[swordman]'));v.reset()
                v._on_pill_clicked('set:')
                v.search.setText('74');v.delay.stop();v.reload()
                v._wear_index(0)
                self.assertGreater(len(v.choice),0)
                # 1. Grant full currently worn outfit
                v.grant_current_outfit()
                # 2. Grant selected set directly
                v.grant_selected_item()
            jobs=[core.read(p) for p in (data/'pending').glob('*.json')]
            self.assertEqual(len(jobs),2)
            for j in jobs:
                self.assertEqual(j['action'],'avatar')
                self.assertEqual(j['character_id'],71)
                self.assertGreater(len(j['avatar_ids']),0)


if __name__=='__main__':unittest.main()
