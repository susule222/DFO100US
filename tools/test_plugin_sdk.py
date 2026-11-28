# -*- coding: utf-8 -*-
"""End-to-end verification tests for 100US client external DLL plugin loader."""
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
STUB_DLL = ROOT / 'ngs_stub' / 'NGClient64.aes'
CLIENT_PLUGIN_DLL = ROOT / 'sdk' / 'plugin' / 'examples' / 'build' / 'client_hello_plugin.dll'


class TestClientPluginHost(unittest.TestCase):
    def test_ngs_stub_loads_external_client_plugin_and_dispatches_tick(self):
        self.assertTrue(STUB_DLL.is_file(), f'missing built stub: {STUB_DLL}')
        self.assertTrue(CLIENT_PLUGIN_DLL.is_file(), f'missing built client plugin: {CLIENT_PLUGIN_DLL}')

        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            plugins_dir = root / 'plugins'
            plugins_dir.mkdir()
            shutil.copy2(CLIENT_PLUGIN_DLL, plugins_dir / '01_client_hello.dll')
            # Underscore-prefixed DLL must be skipped
            shutil.copy2(CLIENT_PLUGIN_DLL, plugins_dir / '_ignored.dll')
            # Per-plugin isolated INI file (loaded via "client_hello_plugin:scoped_greeting")
            (plugins_dir / 'client_hello_plugin.ini').write_text(
                'scoped_greeting=isolated_ini_ok\n',
                encoding='utf-8',
            )

            ini_path = root / 'ngs_stub.ini'
            ini_path.write_text(
                '1=0\n2=0\n3=0\n4=0\n5=0\n6=0\n'
                'hooks=0\nfast_channel_select=0\n'
                'plugins_enabled=1\nplugins_dir=plugins\n'
                'hello_greeting=us100_ok\n'
                '[other_mod]\n'
                'hello_greeting=should_not_override_global\n',
                encoding='utf-8',
            )
            log_path = root / 'stub_test.log'

            # Run inside an isolated child Python process so DllMain hooks/state
            # never affect the unittest runner process.
            harness = (
                "import ctypes, os, sys\n"
                "dll = ctypes.WinDLL(sys.argv[1])\n"
                "fn1 = dll[1]\n"
                "fn1.argtypes = [ctypes.c_uint64]*4\n"
                "fn1.restype = ctypes.c_uint64\n"
                "assert fn1(0, 0, 0, 0) == 0\n"
                "fn6 = dll[6]\n"
                "fn6.argtypes = [ctypes.c_uint64]*4\n"
                "fn6.restype = ctypes.c_uint64\n"
                "assert fn6(0, 0, 0, 0) == 0\n"
                "assert fn6(0, 0, 0, 0) == 0\n"
            )
            env = os.environ.copy()
            env['US100_LOG_PATH'] = str(log_path)
            env['US100_STUB_INI_PATH'] = str(ini_path)
            env['US100_CLIENT_PLUGINS_DIR'] = str(plugins_dir)

            res = subprocess.run(
                [sys.executable, '-c', harness, str(STUB_DLL)],
                env=env, capture_output=True, text=True, timeout=30,
            )
            self.assertEqual(res.returncode, 0, f'child failed: stdout={res.stdout}\nstderr={res.stderr}')
            self.assertTrue(log_path.is_file(), 'stub log file was not created')
            log_text = log_path.read_text(encoding='utf-8', errors='replace')

            self.assertIn('loaded client plugin client_hello_plugin v1.0.0 (01_client_hello.dll)', log_text)
            self.assertNotIn('_ignored.dll', log_text)
            self.assertIn('mz=4D5A(rc=0)', log_text)
            self.assertIn('greeting=us100_ok', log_text)
            self.assertIn('scoped=isolated_ini_ok', log_text)
            self.assertIn('first frame tick observed: seq=1', log_text)
            self.assertIn('shutdown complete', log_text)

    def test_ngs_stub_scoped_section_fallback_in_ngs_stub_ini(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            plugins_dir = root / 'plugins'
            plugins_dir.mkdir()
            shutil.copy2(CLIENT_PLUGIN_DLL, plugins_dir / '01_client_hello.dll')

            ini_path = root / 'ngs_stub.ini'
            ini_path.write_text(
                '1=0\n2=0\n3=0\n4=0\n5=0\n6=0\n'
                'hooks=0\nfast_channel_select=0\n'
                'plugins_enabled=1\nplugins_dir=plugins\n'
                'hello_greeting=top_level\n'
                '[client_hello_plugin]\n'
                'scoped_greeting=section_fallback_ok\n',
                encoding='utf-8',
            )
            log_path = root / 'stub_section.log'
            harness = (
                "import ctypes, sys\n"
                "dll = ctypes.WinDLL(sys.argv[1])\n"
                "fn1 = dll[1]\n"
                "fn1.argtypes = [ctypes.c_uint64]*4\n"
                "fn1.restype = ctypes.c_uint64\n"
                "assert fn1(0, 0, 0, 0) == 0\n"
            )
            env = os.environ.copy()
            env['US100_LOG_PATH'] = str(log_path)
            env['US100_STUB_INI_PATH'] = str(ini_path)
            env['US100_CLIENT_PLUGINS_DIR'] = str(plugins_dir)
            res = subprocess.run(
                [sys.executable, '-c', harness, str(STUB_DLL)],
                env=env, capture_output=True, text=True, timeout=30,
            )
            self.assertEqual(res.returncode, 0, f'child failed: {res.stderr}')
            log_text = log_path.read_text(encoding='utf-8', errors='replace')
            self.assertIn('greeting=top_level', log_text)
            self.assertIn('scoped=section_fallback_ok', log_text)


if __name__ == '__main__':
    unittest.main()
