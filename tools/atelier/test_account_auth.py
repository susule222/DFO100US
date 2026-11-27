import hashlib
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import MagicMock, patch

HERE = Path(__file__).resolve().parent
sys.path[:0] = [str(HERE.parents[1] / 'runtime' / 'atelier-qt'), str(HERE), str(HERE.parent)]
os.environ.setdefault('QT_QPA_PLATFORM', 'offscreen')

import core
import epic
from PySide6.QtWidgets import QApplication, QMessageBox


class AccountAuthAndDbGateTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = QApplication.instance() or QApplication([])
        epic.apply_theme(cls.app)

    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.tmp = Path(tmp.name)
        self.db_file = self.tmp / 'us100.db'
        self.data = self.tmp / 'launcher-gm'
        core.logout_account()
        self.addCleanup(core.logout_account)
        for p in (
            patch.object(core, 'db_path', return_value=self.db_file),
            patch.object(core, 'DATA', self.data),
            patch.object(epic, 'PRESET', self.data / 'preset.json'),
            patch.object(core, 'client_up', return_value=False),
            patch.object(core, 'gateway', return_value=None),
        ):
            p.start()
            self.addCleanup(p.stop)

    def test_no_db_before_account_creation_and_login_blocked(self):
        self.assertFalse(self.db_file.exists())
        self.assertFalse(core.db_exists())
        self.assertEqual(core.list_accounts(), [])
        self.assertEqual(core.roles(), [])
        self.assertIsNone(core.active_account())

        # Attempting to start server or game before creating an account MUST fail
        # and MUST NOT create us100.db!
        with self.assertRaisesRegex(ValueError, '创建账号'):
            core.login(server_only=True)
        with self.assertRaisesRegex(ValueError, '创建账号'):
            core.login(server_only=False)
        with self.assertRaisesRegex(ValueError, '创建账号'):
            core.authenticate_account('player01', 'pass1234')
        self.assertFalse(self.db_file.exists())

    def test_create_account_initializes_db_hashes_password_and_authenticates(self):
        # Validation checks
        with self.assertRaisesRegex(ValueError, '账号长度'):
            core.create_account('ab', '1234', '1234')
        with self.assertRaisesRegex(ValueError, '密码长度'):
            core.create_account('player01', '123', '123')
        with self.assertRaisesRegex(ValueError, '不一致'):
            core.create_account('player01', 'pass1234', 'pass9999')
        self.assertFalse(self.db_file.exists())

        # Creating the first account creates us100.db atomically
        res = core.create_account('AradHero_01', 'MyPass2026', 'MyPass2026')
        self.assertTrue(res['db_created'])
        self.assertEqual(res['id'], 1)
        self.assertEqual(res['username'], 'AradHero_01')
        self.assertEqual(core.active_account(), 'AradHero_01')
        self.assertTrue(self.db_file.is_file())

        doc = json.loads(self.db_file.read_text(encoding='utf-8'))
        self.assertEqual(doc['magic'], 'US100DB1')
        self.assertEqual(doc['version'], 1)
        self.assertEqual(doc['revision'], 1)
        self.assertEqual(doc['tables']['meta']['seq:accounts'], 1)
        acct_row = doc['tables']['accounts']['1']
        self.assertEqual(acct_row['username'], 'AradHero_01')
        self.assertFalse(acct_row['development_only'])
        self.assertTrue(acct_row['password_hash'].startswith('pbkdf2_sha256$100000$'))
        self.assertNotIn('MyPass2026', acct_row['password_hash'])

        # Duplicate username (case-insensitive) is rejected
        with self.assertRaisesRegex(ValueError, '已存在'):
            core.create_account('aradhero_01', 'OtherPass', 'OtherPass')

        # Second account appends cleanly and bumps sequence
        res2 = core.create_account('SecondPlayer', 'Pass5678', 'Pass5678')
        self.assertFalse(res2['db_created'])
        self.assertEqual(res2['id'], 2)
        self.assertEqual(core.active_account(), 'SecondPlayer')
        self.assertEqual([a['username'] for a in core.list_accounts()], ['AradHero_01', 'SecondPlayer'])

        # Logout clears active session and re-locks login()
        core.logout_account()
        self.assertIsNone(core.active_account())
        with self.assertRaisesRegex(ValueError, '创建账号'):
            core.login(server_only=True)

        # Wrong password rejected; right password (case-insensitive username) logs in
        with self.assertRaisesRegex(ValueError, '密码错误'):
            core.authenticate_account('aradhero_01', 'WrongPass')
        auth = core.authenticate_account('aradhero_01', 'MyPass2026')
        self.assertEqual(auth['username'], 'AradHero_01')
        self.assertEqual(core.active_account(), 'AradHero_01')

    def test_roles_and_currency_scoped_to_active_account(self):
        core.create_account('AcctA', 'pass1111', 'pass1111')
        core.create_account('AcctB', 'pass2222', 'pass2222')
        doc = json.loads(self.db_file.read_text(encoding='utf-8'))
        doc['tables']['characters'] = {
            '10': {'id': 10, 'account_id': 1, 'name': 'A的角色'},
            '20': {'id': 20, 'account_id': 2, 'name': 'B的角色'},
        }
        doc['tables']['account_currency'] = {
            '1': {'account_id': 1, 'cera': 8800, 'mileage': 120},
            '2': {'account_id': 2, 'cera': 3300, 'mileage': 45},
        }
        self.db_file.write_text(json.dumps(doc), encoding='utf-8')

        core.authenticate_account('AcctA', 'pass1111')
        self.assertEqual(core.roles(), [(10, 'A的角色')])
        self.assertEqual(core.account_cera(), 8800)
        self.assertEqual(core.account_mileage(), 120)

        core.authenticate_account('AcctB', 'pass2222')
        self.assertEqual(core.roles(), [(20, 'B的角色')])
        self.assertEqual(core.account_cera(), 3300)
        self.assertEqual(core.account_mileage(), 45)

    def test_login_spawns_gateway_with_account_and_require_account(self):
        core.create_account('ChannelHero', 'pass4321', 'pass4321')
        spawned = []
        fake_proc = MagicMock()
        fake_proc.poll.return_value = None

        def fake_popen(cmd, **kwargs):
            spawned.append(cmd)
            return fake_proc

        gw_states = [None, {'address': '127.0.0.2:40001', 'account': 'ChannelHero', 'online_item_grants': True, 'launcher_gm': True}]
        with patch.object(core, 'gateway', side_effect=gw_states), \
             patch.object(core.subprocess, 'Popen', side_effect=fake_popen):
            msg = core.login(server_only=True)
        self.assertIn('ChannelHero', msg)
        self.assertEqual(len(spawned), 1)
        cmd = spawned[0]
        self.assertIn('--account', cmd)
        self.assertIn('ChannelHero', cmd)
        self.assertIn('--require-account', cmd)

    def test_homepage_ui_create_login_logout_flow(self):
        w = epic.EpicWindow()
        self.addCleanup(w.close)
        # Before creating any account: DB is NOT created and status clearly warns user
        self.assertFalse(self.db_file.exists())
        self.assertIn('数据库尚未生成', w.acc_status_lbl.text())
        self.assertFalse(w.logout_acc_btn.isEnabled())

        # Create account via Homepage UI
        w.acc_input.setText('UiPlayer_01')
        w.pwd_input.setText('UiPass2026')
        w.pwd_confirm_input.setText('UiPass2026')
        with patch.object(QMessageBox, 'information', return_value=QMessageBox.Ok):
            w.create_acc_btn.click()

        self.assertTrue(self.db_file.is_file())
        self.assertEqual(core.active_account(), 'UiPlayer_01')
        self.assertIn('已登录账号：UiPlayer_01', w.acc_status_lbl.text())
        self.assertTrue(w.logout_acc_btn.isEnabled())
        self.assertEqual(w.pwd_input.text(), '')
        self.assertEqual(w.pwd_confirm_input.text(), '')

        # Logout via Homepage UI
        w.logout_acc_btn.click()
        self.assertIsNone(core.active_account())
        self.assertFalse(w.logout_acc_btn.isEnabled())
        self.assertIn('数据库已就绪', w.acc_status_lbl.text())

        # Login with wrong password shows warning and stays logged out
        w.acc_input.setText('UiPlayer_01')
        w.pwd_input.setText('WrongPwd')
        with patch.object(QMessageBox, 'warning', return_value=QMessageBox.Ok) as warn:
            w.login_acc_btn.click()
            self.assertTrue(warn.called)
        self.assertIsNone(core.active_account())

        # Login with correct password unlocks session
        w.pwd_input.setText('UiPass2026')
        w.login_acc_btn.click()
        self.assertEqual(core.active_account(), 'UiPlayer_01')
        self.assertIn('已登录账号：UiPlayer_01', w.acc_status_lbl.text())

    def test_pbkdf2_vector_matches_go_implementation(self):
        dk = hashlib.pbkdf2_hmac('sha256', b'password', bytes.fromhex('0102030405060708'), 1000, dklen=32).hex()
        encoded = f'pbkdf2_sha256$1000$0102030405060708${dk}'
        self.assertTrue(core.verify_password(encoded, 'password'))
        self.assertFalse(core.verify_password(encoded, 'wrong'))


if __name__ == '__main__':
    unittest.main()
