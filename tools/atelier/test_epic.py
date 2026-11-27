import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

HERE=Path(__file__).resolve().parent
sys.path[:0]=[str(HERE.parents[1]/'runtime'/'atelier-qt'),str(HERE),str(HERE.parent)]
os.environ.setdefault('QT_QPA_PLATFORM','offscreen')
import core
import epic
from epic_data import JOBS
from PySide6.QtWidgets import QApplication,QLabel,QMessageBox,QPushButton


class EpicChecks(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app=QApplication.instance() or QApplication([])
        epic.apply_theme(cls.app)

    def setUp(self):
        tmp=tempfile.TemporaryDirectory();self.addCleanup(tmp.cleanup)
        self.data=Path(tmp.name)
        for p in (patch.object(core,'DATA',self.data),patch.object(epic,'PRESET',self.data/'preset.json'),
                  patch.object(core,'roles',return_value=[(71,'角色甲'),(82,'角色乙')]),
                  patch.object(core,'gateway',return_value=None)):
            p.start();self.addCleanup(p.stop)
        self.w=epic.EpicWindow()
        self.addCleanup(self.w.close)

    def test_original_cards_pagination_and_real_filter(self):
        w=self.w
        self.assertEqual(w.stack.count(),6)
        self.assertEqual(w.grid_layout.count(),12)
        w.next_btn.click();self.assertEqual(w.current_page,2)
        item=next(i for i in w.items if i.category=='武器' and '鬼剑士(男)' in i.jobs)
        w.search_input.setText(str(item.item_id))
        w.category_combo.setCurrentText('武器');w.rarity_combo.setCurrentText(item.rarity)
        w.job_combo.setCurrentText('鬼剑士(男)')
        self.assertIn(item,w.filtered_items)
        wrong=next(j for j in JOBS[1:] if j not in item.jobs)
        w.job_combo.setCurrentText(wrong);self.assertNotIn(item,w.filtered_items)

    def test_item_rarity_clean_and_group_classification(self):
        w=self.w
        # Rarity dropdown must only contain real DNF rarities in tier order (never 未标注)
        rars=[w.rarity_combo.itemText(i) for i in range(w.rarity_combo.count())]
        self.assertEqual(rars[0],'全部稀有度')
        self.assertNotIn('未标注',rars)
        self.assertEqual(rars[1:],['普通','高级','稀有','神器','勇者','传说','史诗','神话'])
        # Item classification group narrows category dropdown and filters results
        w._select_item_group('防具')
        self.assertEqual(w.group_combo.currentText(),'防具')
        sub_cats=[w.category_combo.itemText(i) for i in range(1,w.category_combo.count())]
        self.assertTrue(sub_cats and all(c.startswith('防具-') for c in sub_cats))
        self.assertTrue(len(w.filtered_items)>0)
        self.assertTrue(w.filtered_items[0].category.startswith('防具-'))
        # Grantable-only toggle filters strictly to grantmode > 0
        w._select_item_group('礼盒与宝珠')
        w.only_grantable_chk.setChecked(True)
        self.assertTrue(len(w.filtered_items)>0)
        self.assertTrue(all(it.grantmode>0 for it in w.filtered_items[:12]))

    def test_cards_submit_to_locked_recipients(self):
        w=self.w
        submit=core.submit
        w.qty_spin.setValue(2)
        first=w.grid_layout.itemAt(0).widget()
        first.findChild(QPushButton).click()
        w.recipient.setCurrentIndex(1)
        first.findChild(QPushButton).click()
        with patch.object(QMessageBox,'question',return_value=QMessageBox.Yes), \
             patch.object(core,'submit',side_effect=lambda *args:submit(*args,directory=self.data)):
            w._confirm_add_items()
        jobs=[core.read(p) for p in (self.data/'pending').glob('*.json')]
        self.assertEqual(sorted(d['character_id'] for d in jobs),[71,82])
        self.assertTrue(all(d['items']==[{'amount':2,'template':first.item.item_id}] for d in jobs))
        self.assertEqual(w.queue_list.count(),0)

    def test_item_icon_widget_uses_source_frame(self):
        w=self.w
        refs={w.by_id[n].icon for n in range(10300000,10300012)}
        self.assertEqual(len(refs),1)
        ref=refs.pop()
        self.assertEqual((ref.img,ref.frame),('sprite/item/stackable/material.img',574))
        w.search_input.setText('10300000')
        icon=w.grid_layout.itemAt(0).widget().findChild(QLabel,'ItemIcon')
        actual=icon.pixmap()
        self.assertFalse(actual.isNull())
        expected=epic.pix(w.item_icons.read(ref)).scaled(actual.size(),epic.Qt.KeepAspectRatio,epic.Qt.FastTransformation)
        self.assertEqual(actual.toImage().constBits().tobytes(),expected.toImage().constBits().tobytes())
        self.assertNotEqual(w.item_icons.read(ref).tobytes(),w.item_icons.read(w.by_id[27000].icon).tobytes())

    def test_failure_keeps_only_unsent_rows(self):
        w=self.w
        for item in w.items[:3]:w._on_add_item_to_queue(item)
        with patch.object(QMessageBox,'question',return_value=QMessageBox.Yes), \
             patch.object(core,'submit',side_effect=['job-1',OSError('disk unavailable')]):
            with self.assertRaises(OSError):w._confirm_add_items()
        self.assertEqual([r[1] for r in w.added_queue],[i.item_id for i in w.items[1:3]])
        self.assertEqual(w.queue_list.count(),2)

    def test_delivery_waits_for_receipt_and_displays_quest_slot(self):
        w=self.w;ident='d'*32;name=ident+'.json';w.last_grant=ident
        state={'launcher_gm':True}
        core.atomic(self.data/'pending'/name,{'character_id':39,'items':[{'template':10165055,'amount':1}]})
        w.refresh_delivery(state)
        self.assertIn('尚未入包',w.delivery.text());self.assertIn('正常退出',w.delivery.text())
        w.refresh_delivery({'launcher_gm':True,'online_item_grants':True})
        self.assertIn('无需退出',w.delivery.text());self.assertIn('尚未入包',w.delivery.text())
        core.atomic(self.data/'receipts'/name,{'receipt':{'items':[{'Template':10165055,'Amount':1,'Slots':[177]}]}})
        w.refresh_delivery(state)
        self.assertIn('已入包：少女的手镯（任务道具） ×1',w.delivery.text())
        self.assertIn('177',w.delivery.text());self.assertNotIn('尚未入包',w.delivery.text())
        core.atomic(self.data/'receipts'/name,{'error':'bag full'})
        w.refresh_delivery(state)
        self.assertIn('发放失败：bag full',w.delivery.text())

    def test_wardrobe_and_preset_isolation(self):
        w=self.w
        self.assertFalse(any(self.data.iterdir()))
        w.switch_page(3)
        self.assertFalse(w.figure.pixmap().isNull())
        w.drop_rate_spin.setValue(4)
        with patch.object(QMessageBox,'information',return_value=QMessageBox.Ok):w.save_preset()
        self.assertEqual(core.read(epic.PRESET)['drop_rate_multiplier'],4)
        self.assertFalse((self.data/'abyss.json').exists())
        w.rooms.item(0).setSelected(True);w.gear.setCurrentIndex(0);w.chance.setValue(25)
        save=core.save_rule
        with patch.object(QMessageBox,'question',return_value=QMessageBox.Yes), \
             patch.object(core,'save_rule',side_effect=lambda *args:save(*args,directory=self.data)):w.save_live()
        self.assertEqual(core.read(self.data/'abyss.json')['percent'],25)

    def test_launch_button_uses_real_backend(self):
        w=self.w
        with patch.object(w,'start_work') as start:
            b=next(b for b in w.findChildren(QPushButton) if '立即进入' in b.text());b.click()
            self.assertIs(start.call_args.args[1],core.login)
        self.assertEqual(Path(w.exe_path_input.text()).name,'DFO.exe')

    def test_home_server_start_stop_and_restart_controls(self):
        w=self.w
        # Initially gateway() is mocked to None -> start enabled, stop/restart disabled
        self.assertTrue(w.srv_start_btn.isEnabled())
        self.assertFalse(w.srv_stop_btn.isEnabled())
        self.assertFalse(w.srv_restart_btn.isEnabled())
        with patch.object(w,'start_work') as start:
            w.srv_start_btn.click()
            self.assertTrue(start.called)
        # Simulate online gateway -> start disabled, stop/restart enabled
        with patch.object(core,'gateway',return_value={'address':'127.0.0.2:55555','pid':4321,'launcher_gm':True,'online_item_grants':True,'character_gm':True}):
            w.refresh_status()
        self.assertFalse(w.srv_start_btn.isEnabled())
        self.assertTrue(w.srv_stop_btn.isEnabled())
        self.assertTrue(w.srv_restart_btn.isEnabled())
        self.assertIn('运行中',w.home_srv_badge.text())
        self.assertIn('55555',w.home_srv_detail.text())
        with patch.object(core,'client_up',return_value=False), patch.object(w,'start_work') as start:
            w.srv_stop_btn.click()
            self.assertIs(start.call_args.args[1],core.stop_server)
            w.srv_restart_btn.click()
            self.assertIs(start.call_args.args[1],core.restart_server)

    def test_character_page_all_modules(self):
        w=self.w
        snap={
            'id':71,'account_id':41,'name':'角色甲','profession':0,'level':5,'experience':4000,
            'advancement':0,'skill_points':[90,90],'technique_points':[0,0],
            'attributes':{'[hp max]':640,'[mp max]':630,'[physical attack]':117.3},
            'cera':12000,'gold':50000,
            'items':[{'slot':177,'Template':10165055,'Amount':3}],
            'equipment':[{'slot':9,'template':10018,'durability':20}],
            'worn':[{'slot':12,'template':10018,'durability':35}],
            'quests':[
                {'id':3145,'status':'completed','progress':0,'progress_model':'single-clear-map-remaining-v1','accepted_at':'','completed_at':'2026-11-24'},
                {'id':3146,'status':'accepted','progress':1,'progress_model':'completed-quest-v1','accepted_at':'2026-11-24','completed_at':''},
            ],
        }
        orig_submit=core.submit_op
        with patch.object(core,'character_snapshot',return_value=snap), \
             patch.object(core,'submit_op',side_effect=lambda a,p,reason='':orig_submit(a,p,reason=reason,directory=self.data)), \
             patch.object(QMessageBox,'question',return_value=QMessageBox.Yes):
            w.switch_page(5)
            cp=w.character_page
            self.assertIn('鬼剑士(男)',cp.badge_job.text())
            self.assertIn('Lv.5',cp.badge_level.text())
            self.assertEqual(cp.badge_cera.text(),'12,000')
            self.assertEqual(cp.badge_gold.text(),'50,000')
            self.assertEqual(cp.bag_table.rowCount(),3)
            # 1. Level + SP/TP
            cp.level_spin.setValue(100);cp._preset_sp(20000);cp._preset_tp(40);cp._apply_level()
            # 2. Advancement -> 3 狂战士
            cp.adv_combo.setCurrentIndex(cp.adv_combo.findData(3));cp._apply_advancement()
            # 3. Currency (cera + gold)
            cp.cera_input.setText('50000');cp._apply_cera()
            cp.gold_input.setText('1000000');cp._apply_gold()
            # 4. Bag (set amount + sort)
            item_row=next(i for i,r in enumerate(cp._bag_rows) if r['list_kind']=='items')
            cp.bag_table.selectRow(item_row);cp.bag_qty_spin.setValue(9);cp._bag_set_amount()
            cp._bag_sort()
            # 5. Quests (view + complete objective + skip epic)
            cp.tabs.setCurrentIndex(3)
            self.assertEqual(cp.quest_table.rowCount(),1)
            cp.quest_table.selectRow(0);cp._quest_complete_obj(False)
            cp.epic_level_spin.setValue(50);cp._quest_skip_epic()

        jobs=[core.read(p) for p in (self.data/'pending').glob('*.json')]
        actions=sorted(d['action'] for d in jobs)
        self.assertEqual(actions,['advancement','bag','bag','currency','currency','level','quest','quest'])
        lv_job=next(d for d in jobs if d['action']=='level')
        self.assertEqual((lv_job['level'],lv_job['sp'],lv_job['tp']),(100,20000,40))
        adv_job=next(d for d in jobs if d['action']=='advancement')
        self.assertEqual((adv_job['advancement'],adv_job['reset_skills']),(3,True))
        # Verify receipt formatting for op_receipt
        self.assertIn('角色属性已生效：Lv.100',w.grant_text({'op_receipt':{'action':'level','summary':'Lv.100'}}))


if __name__=='__main__':unittest.main()
