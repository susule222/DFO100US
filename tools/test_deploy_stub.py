# -*- coding: utf-8 -*-
"""Verification tests for NGClient64.aes stub patches and deployment staging."""
import pathlib
import shutil
import struct
import sys
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / 'tools'))

import deploy_stub  # noqa: E402
import inspect_pe  # noqa: E402
import us100_preflight as preflight  # noqa: E402


class TestNgsStubAndDeploy(unittest.TestCase):
    def test_built_stub_pe_exports_ordinals_1_to_6(self):
        self.assertTrue(deploy_stub.BUILT.is_file(), f'missing {deploy_stub.BUILT}')
        import pefile
        pe = pefile.PE(str(deploy_stub.BUILT), fast_load=False)
        try:
            self.assertEqual(pe.FILE_HEADER.Machine, 0x8664, 'must be AMD64 DLL')
            self.assertTrue(bool(pe.FILE_HEADER.Characteristics & 0x2000), 'must be DLL')
            ordinals = sorted(e.ordinal for e in pe.DIRECTORY_ENTRY_EXPORT.symbols)
            self.assertEqual(ordinals, [1, 2, 3, 4, 5, 6])
        finally:
            pe.close()

    def test_client_exe_matches_all_stub_patch_sites(self):
        exe = preflight.client_exe()
        if not exe.is_file():
            self.skipTest(f'client exe not present: {exe}')
        pe = inspect_pe.PE(str(exe))
        try:
            expected_sites = [
                (0x38B7350, bytes([0x40, 0x53, 0x55, 0x56, 0x57, 0x41, 0x56, 0x41, 0x57, 0x48, 0x83, 0xEC, 0x68])),
                (0x4EC40B7, bytes([0x74, 0x28])),
                (0x3702001, bytes([0x83, 0xB9, 0xA0, 0x00, 0x00, 0x00, 0x06])),
                (0x4AC94BE, bytes([0x74, 0x0F])),
                (0x3702192, bytes([0xBA, 0x64, 0x03, 0x00, 0x00])),
                (0x3702258, bytes([0xBA, 0x64, 0x03, 0x00, 0x00])),
            ]
            for rva, expected in expected_sites:
                actual = pe.read(rva, len(expected))
                self.assertEqual(actual, expected, f'RVA 0x{rva:x} mismatch: got {actual.hex()}')
        finally:
            pe.close()

    def test_deploy_and_stage_roundtrip_in_tempdir(self):
        orig_stage = deploy_stub.STAGE_DIR
        orig_pending = deploy_stub.PENDING_JSON
        orig_running = deploy_stub.client_is_running
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            fake_client = root / 'client'
            fake_client.mkdir()
            target = fake_client / 'NGClient64.aes'
            target.write_bytes(b'ORIGINAL_NEXON_STUB')
            (fake_client / 'ngs_stub.ini').write_text('1=0\n5=1\n', encoding='utf-8')
            deploy_stub.STAGE_DIR = root / 'stage'
            deploy_stub.PENDING_JSON = deploy_stub.STAGE_DIR / 'pending.json'
            try:
                # Simulate running client -> stages instead of failing
                deploy_stub.client_is_running = lambda: True
                res = deploy_stub.apply_stub(fake_client, allow_stage=True)
                self.assertEqual(res['status'], 'staged')
                self.assertTrue(deploy_stub.PENDING_JSON.is_file())
                self.assertEqual(target.read_bytes(), b'ORIGINAL_NEXON_STUB')
                ini_text = (fake_client / 'ngs_stub.ini').read_text(encoding='utf-8')
                self.assertIn('5=0', ini_text)
                self.assertIn('fast_channel_select=1', ini_text)
                self.assertIn('skip_server_select=0', ini_text)
                self.assertIn('locale_hooks=0', ini_text)

                # Simulate client closed -> pending staged stub applies cleanly with backup
                deploy_stub.client_is_running = lambda: False
                msg = deploy_stub.apply_staged_if_pending()
                self.assertIn('applied staged NGClient64.aes stub', msg)
                self.assertFalse(deploy_stub.PENDING_JSON.is_file())
                self.assertEqual(deploy_stub.digest(target), deploy_stub.digest(deploy_stub.BUILT))
                backups = list(fake_client.glob('NGClient64.aes.*.bak'))
                self.assertEqual(len(backups), 1)
                self.assertEqual(backups[0].read_bytes(), b'ORIGINAL_NEXON_STUB')
            finally:
                deploy_stub.STAGE_DIR = orig_stage
                deploy_stub.PENDING_JSON = orig_pending
                deploy_stub.client_is_running = orig_running


if __name__ == '__main__':
    unittest.main()
