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

    def test_composed_sets_dedup_and_rarity_filter(self):
        w=self.w;w.switch_page(3);v=w.fitting
        v.job.setCurrentIndex(v.job.findData('[swordman]'));v.reset()
        # 1. Rarity filter contains real DNF rarities (no '未标注')
        rar_items=[v.rarity.itemText(i) for i in range(v.rarity.count())]
        self.assertEqual(rar_items[0],'全部稀有度')
        self.assertNotIn('未标注',rar_items)
        self.assertTrue({'普通','高级','稀有'}<=set(rar_items))

        # 2. mode='set' contains both stackable packages and composed single-piece sets
        groups={r[0] for r in v.source.db.execute(
            "select distinct g from wardrobe where job='[swordman]' and mode='set'"
        )}
        self.assertEqual(groups,{'stackable','composed_set'})

        # 3. Zero duplicate body-slot fingerprints within the same job
        body={'cap','hair','face','neck','coat','belt','pants','shoes'}
        slot_map={r[0]:r[1] for r in v.source.db.execute(
            "select id, slot from item where g='equipment' and slot!=''"
        )}
        seen=set()
        for vis, in v.source.db.execute(
            "select i.visual from wardrobe w join item i on i.g=w.g and i.id=w.id "
            "where w.job='[swordman]' and w.mode='set'"
        ):
            aids=json.loads(vis)
            b=tuple(sorted((slot_map.get(a,''),a) for a in aids if slot_map.get(a,'') in body))
            fp=b if len(b)>=2 else tuple(sorted((slot_map.get(a,''),a) for a in aids))
            self.assertNotIn(fp,seen,'Duplicate outfit fingerprint found in swordman sets')
            seen.add(fp)

        # 4. Filtering by rarity='稀有' updates counts and lists only 稀有 sets/pieces
        v._on_pill_clicked('set:')
        all_counts=v.source.slot_counts('[swordman]','全部稀有度')
        rare_counts=v.source.slot_counts('[swordman]','稀有')
        self.assertGreater(rare_counts.get('set',0),0)
        self.assertLess(rare_counts.get('set',0),all_counts.get('set',0))
        v.rarity.setCurrentIndex(v.rarity.findText('稀有'))
        self.assertGreater(v.rows.count(),0)
        # All returned rows on page 1 must be '稀有'
        rows,_,total=v.source.costume_cards('[swordman]','set','','',0,24,rarity='稀有')
        self.assertEqual(total,rare_counts['set'])
        self.assertTrue(all(r['rar']=='稀有' for r in rows))
        self.assertTrue(any(r['g']=='composed_set' for r in rows))

        # 5. Try on & grant a composed_set (Rare Sky Set)
        comp_idx=next(i for i in range(v.rows.count()) if v.rows.item(i).data(Qt.UserRole)[2]=='composed_set')
        v._wear_index(comp_idx)
        self.assertGreaterEqual(len(v.choice),4)
        self.assertTrue(v.figure.pixmap() and not v.figure.pixmap().isNull())
        with tempfile.TemporaryDirectory() as tmp:
            data=Path(tmp)
            orig_grant=core.grant_avatars
            with patch.object(core,'DATA',data), \
                 patch.object(core,'gateway',return_value={'address':'127.0.0.2:64604','avatar_gm':True}), \
                 patch.object(core,'roles',return_value=[(71,'鬼剑一号')]), \
                 patch.object(core,'grant_avatars',side_effect=lambda r,ids,reason='':orig_grant(r,ids,reason=reason,directory=data)), \
                 patch.object(QMessageBox,'question',return_value=QMessageBox.Yes):
                v.refresh_roles()
                v.rows.setCurrentRow(comp_idx)
                v.grant_selected_item()
            jobs=[core.read(p) for p in (data/'pending').glob('*.json')]
            self.assertEqual(len(jobs),1)
            self.assertEqual(jobs[0]['action'],'avatar')
            self.assertGreaterEqual(len(jobs[0]['avatar_ids']),4)
        # Reset rarity filter back to all
        v.rarity.setCurrentIndex(0)


if __name__=='__main__':unittest.main()
