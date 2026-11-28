"""End-to-end tests for 90JP-style PVF + NPK Mod Workshop: scanning, priority, dependencies, real pvfmerge.exe, and rollback."""
import json
import os
from pathlib import Path
import struct
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile
import zlib

HERE = Path(__file__).resolve().parent
sys.path[:0] = [str(HERE.parents[1] / 'runtime' / 'atelier-qt'), str(HERE), str(HERE.parent)]
os.environ.setdefault('QT_QPA_PLATFORM', 'offscreen')

import core
import live_resources as lr
from mod_workshop import ModWorkshop, inspect_plugin_dll_bytes
from sprites import KEY

PLUGIN_BUILD_DIR = HERE.parents[1] / 'sdk' / 'plugin' / 'examples' / 'build'
CLIENT_HELLO_DLL = PLUGIN_BUILD_DIR / 'client_hello_plugin.dll'
SERVER_HELLO_DLL = PLUGIN_BUILD_DIR / 'server_hello_plugin.dll'
STUB_DLL = HERE.parents[1] / 'ngs_stub' / 'NGClient64.aes'


def _dummy_npk_bytes(body: bytes) -> bytes:
    name = b'sprite/mod_fixture.img'.ljust(256, b'\0')
    return (
        b'NeoplePack_Bill\0'
        + struct.pack('<III', 1, 284, len(body))
        + bytes(a ^ b for a, b in zip(name, KEY))
        + body
    )


def _dec_prot(key: str, data: bytes, magic: int = 0x269EC3) -> bytes:
    words = [ord(c) for c in key]
    seed = (0x339E9711 * words[0] + 0x393 * (words[3] + 0x393 * (words[2] + 0x393 * words[1]))) & 0xFFFFFFFF
    buf = bytearray(data)
    n = len(buf)
    q = n >> 2
    for i in range(q):
        t1 = (0x343FD * seed + magic) & 0xFFFFFFFF
        seed = (0x343FD * t1 + magic) & 0xFFFFFFFF
        xk = ((t1 & 0xFFFF0000) + (seed >> 16)) & 0xFFFFFFFF
        off = i << 2
        struct.pack_into('<I', buf, off, struct.unpack_from('<I', buf, off)[0] ^ xk)
    tail = n - (q << 2)
    if tail:
        t1 = (0x343FD * seed + magic) & 0xFFFFFFFF
        t2 = (0x343FD * t1 + magic) & 0xFFFFFFFF
        kb = struct.pack('<I', ((t1 & 0xFFFF0000) + (t2 >> 16)) & 0xFFFFFFFF)
        for i in range(tail):
            buf[n - tail + i] ^= kb[i]
    return bytes(buf)


def _make_minimal_inner_pvf(path: Path) -> None:
    """Build a valid minimal FormatDFO20260901 inner PVF with list/equipment.lst and string/equipment.uv.str."""
    str_a = bytearray(b'\0')

    def add_a(s: str) -> int:
        off = len(str_a) << 1
        str_a.extend(s.encode('ascii') + b'\0')
        return off

    tag_idx = add_a('[index]')
    p_a = add_a('equipment/a.equ')
    p_b = add_a('equipment/b.equ')
    n_lst = add_a('equipment.lst')
    d_lst = add_a('list')
    n_str = add_a('equipment.uv.str')
    d_str = add_a('string')

    lst_raw = (
        struct.pack('<Bi', 3, tag_idx)
        + struct.pack('<Bi', 0, 1) + struct.pack('<Bi', 6, p_a)
        + struct.pack('<Bi', 0, 2) + struct.pack('<Bi', 6, p_b)
    )
    str_raw = 'name_1>Base Sword\r\nname_2>Old Shield\r\n'.encode('utf-16-le')
    chunk_plain = lst_raw + str_raw
    comp_chunk = _dec_prot('mAIn', zlib.compress(chunk_plain, 9))

    f1 = (n_lst, d_lst, 0, 0, len(lst_raw), 1)
    f2 = (n_str, d_str, 0, len(lst_raw), len(str_raw), 3)
    ft = b''.join(struct.pack('<6I', *it) for it in (f1, f2))

    hsrm = _dec_prot('HSrm', struct.pack('<IIIIIII', 2, n_lst, d_lst, n_str, d_str, 2, n_lst) + struct.pack('<I', n_str))
    enc_a = _dec_prot('stAs', zlib.compress(bytes(str_a), 9), 0x269EC9)
    str_w = b'\0\0'
    enc_w = _dec_prot('stWs', zlib.compress(str_w, 9), 0x269EC9)
    names = (
        bytes(8)
        + struct.pack('<II', len(enc_a) ^ 0xE7ADF7EA, len(str_a) ^ len(enc_a)) + enc_a
        + struct.pack('<II', len(enc_w) ^ 0xB8DEA7AC, len(str_w) ^ len(enc_w)) + enc_w
    )
    groups = _dec_prot('Gidx', struct.pack('<II', len(comp_chunk), len(chunk_plain)))
    hdr = bytearray(48)
    hdr[0:4] = b'nkpi'
    struct.pack_into('<I', hdr, 24, 2)
    struct.pack_into('<I', hdr, 32, len(comp_chunk))
    struct.pack_into('<I', hdr, 36, 1)
    struct.pack_into('<I', hdr, 40, len(hsrm))
    struct.pack_into('<I', hdr, 44, len(names))
    hdr = _dec_prot('iNfO', bytes(hdr))
    path.write_bytes(hdr + ft + hsrm + names + groups + comp_chunk)


def _write_90jp_collection_zip(zip_path: Path):
    with zipfile.ZipFile(zip_path, 'w', compression=zipfile.ZIP_DEFLATED) as zf:
        zf.writestr('version.json', json.dumps({
            'schemaVersion': 1,
            'kind': 'collection',
            'id': 'demo.col',
            'name': '90JP风格示例合集',
            'version': '1.0.0',
            'moduleOrder': ['demo.col.core', 'demo.col.title'],
        }, ensure_ascii=False))
        # Sub-module 1: core (PVF + NPK)
        zf.writestr('core/version.json', json.dumps({
            'schemaVersion': 1,
            'kind': 'mod',
            'id': 'demo.col.core',
            'name': '基础模组',
            'description': '提供基础词条与资源',
            'version': '1.0.0',
            'dependencies': [],
        }, ensure_ascii=False))
        zf.writestr('core/PVF/list/equipment.lst', '[index]\n2 `equipment/b_from_core.equ`\n')
        zf.writestr('core/PVF/string/equipment.uv.str', 'name_2>核心护盾\r\n')
        zf.writestr('core/NPK/ImagePacks2/demo_custom.NPK', _dummy_npk_bytes(b'mod-npk-v1'))
        zf.writestr('core/NPK/ImagePacks2/existing_pack.NPK', _dummy_npk_bytes(b'replaced-existing'))
        # Sub-module 2: title (depends on core, overrides ID 2 and adds ID 3 + new .equ)
        zf.writestr('title/version.json', json.dumps({
            'schemaVersion': 1,
            'kind': 'mod',
            'id': 'demo.col.title',
            'name': '称号拓展',
            'description': '新增称号脚本',
            'version': '1.0.0',
            'dependencies': ['demo.col.core'],
        }, ensure_ascii=False))
        zf.writestr('title/PVF/list/equipment.lst', '[index]\n2 `equipment/b_from_title.equ`\n3 `equipment/new_title.equ`\n')
        zf.writestr('title/PVF/equipment/new_title.equ', '#PVF_File\n[name]\n`百级神话称号`\n[rarity]\n7\n')
        zf.writestr('title/PVF/string/equipment.uv.str', 'name_3>百级神话称号\r\n')


class ModPvfMergeChecks(unittest.TestCase):
    def test_90jp_scan_deps_priority_merge_and_rollback(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(core, 'client_up', return_value=False):
            base = Path(tmp)
            client_dir = base / 'client'
            packs_dir = client_dir / 'ImagePacks2'
            packs_dir.mkdir(parents=True)
            live_pvf = client_dir / 'Script.pvf'
            _make_minimal_inner_pvf(live_pvf)
            orig_pvf_bytes = live_pvf.read_bytes()
            existing_npk = packs_dir / 'existing_pack.NPK'
            existing_npk.write_bytes(_dummy_npk_bytes(b'original-existing'))
            orig_npk_bytes = existing_npk.read_bytes()

            ws = ModWorkshop(client_dir=client_dir, mod_dir=client_dir / 'MOD', cache_root=base / 'cache')
            src_zip = base / '01-demo.zip'
            _write_90jp_collection_zip(src_zip)
            ws.import_mod_file(src_zip)

            mods = ws.scan()
            self.assertEqual([m['id'] for m in mods], ['demo.col.core', 'demo.col.title'])
            self.assertEqual(len(mods[0]['pvf_entries']), 2)
            self.assertEqual(len(mods[0]['npk_entries']), 2)
            self.assertEqual(len(mods[1]['pvf_entries']), 3)

            # Dependency rule: enabling only 'title' without 'core' excludes 'title'
            ws.save_selection(['demo.col.title', 'demo.col.core'], ['demo.col.title'])
            mods = ws.scan()
            by_id = {m['id']: m for m in mods}
            self.assertEqual(by_id['demo.col.title']['missing_deps'], ['demo.col.core'])
            self.assertFalse(by_id['demo.col.title']['effective'])

            # Enable both with 'title' higher priority than 'core' -> merge into Script.pvf + NPKs!
            ws.save_selection(['demo.col.title', 'demo.col.core'], ['demo.col.title', 'demo.col.core'])
            res = ws.apply_merge()
            rep = res['pvf_report']
            self.assertIsNotNone(rep)
            self.assertEqual(rep['replaced_files'], 2)
            self.assertEqual(rep['added_files'], 1)
            self.assertEqual(rep['total_files'], 3)
            self.assertEqual(res['npk_count'], 2)
            self.assertNotEqual(live_pvf.read_bytes(), orig_pvf_bytes)
            self.assertTrue((packs_dir / 'demo_custom.NPK').is_file())
            self.assertEqual(existing_npk.read_bytes(), _dummy_npk_bytes(b'replaced-existing'))
            self.assertTrue(lr.is_inner_pvf(live_pvf))

            # Full rollback restores exact original Script.pvf and NPKs
            msg = ws.restore_original()
            self.assertIn('还原原版', msg)
            self.assertEqual(live_pvf.read_bytes(), orig_pvf_bytes)
            self.assertEqual(existing_npk.read_bytes(), orig_npk_bytes)
            self.assertFalse((packs_dir / 'demo_custom.NPK').exists())

    def test_dll_pe64_validation_rejects_invalid_and_detects_targets(self):
        self.assertTrue(CLIENT_HELLO_DLL.is_file(), f'missing {CLIENT_HELLO_DLL}')
        self.assertTrue(SERVER_HELLO_DLL.is_file(), f'missing {SERVER_HELLO_DLL}')
        self.assertTrue(STUB_DLL.is_file(), f'missing {STUB_DLL}')

        cinfo = inspect_plugin_dll_bytes(CLIENT_HELLO_DLL.read_bytes())
        self.assertEqual(cinfo['target'], 'client')
        self.assertIn('us100_client_plugin_init', cinfo['exports'])

        sinfo = inspect_plugin_dll_bytes(SERVER_HELLO_DLL.read_bytes())
        self.assertEqual(sinfo['target'], 'server')
        self.assertIn('us100_server_plugin_init', sinfo['exports'])

        # Valid x64 DLL without ABI v1 init export must be rejected
        with self.assertRaises(ValueError) as ctx:
            inspect_plugin_dll_bytes(STUB_DLL.read_bytes())
        self.assertIn('缺少 100US ABI v1', str(ctx.exception))

        # Corrupt / non-PE bytes must be rejected
        with self.assertRaises(ValueError):
            inspect_plugin_dll_bytes(b'not a dll')

        # Synthetic 32-bit x86 DLL header must be rejected
        x86_pe = bytearray(0x100)
        x86_pe[0:2] = b'MZ'
        struct.pack_into('<I', x86_pe, 0x3C, 0x40)
        x86_pe[0x40:0x44] = b'PE\x00\x00'
        struct.pack_into('<HHIIIHH', x86_pe, 0x44, 0x014C, 0, 0, 0, 0, 0xE0, 0x2002)
        with self.assertRaises(ValueError) as ctx32:
            inspect_plugin_dll_bytes(bytes(x86_pe))
        self.assertIn('非 64 位', str(ctx32.exception))

    def test_dll_mod_workshop_priority_server_gate_and_rollback(self):
        self.assertTrue(CLIENT_HELLO_DLL.is_file())
        self.assertTrue(SERVER_HELLO_DLL.is_file())

        with tempfile.TemporaryDirectory() as tmp, \
             patch.object(core, 'client_up', return_value=False), \
             patch.object(core, 'gateway', return_value=None):
            base = Path(tmp)
            client_dir = base / 'client'
            server_pdir = base / 'server_plugins'
            client_dir.mkdir(parents=True)
            server_pdir.mkdir(parents=True)
            (client_dir / 'plugins').mkdir()

            # Pre-existing manual plugins must never be deleted by workshop
            manual_c = client_dir / 'plugins' / 'manual_keep.dll'
            manual_s = server_pdir / 'manual_srv.dll'
            manual_c.write_bytes(b'manual-client-dll')
            manual_s.write_bytes(b'manual-server-dll')

            ws = ModWorkshop(
                client_dir=client_dir,
                mod_dir=client_dir / 'MOD',
                cache_root=base / 'cache',
                server_plugins_dir=server_pdir,
            )

            # 1) Import standalone client DLL
            c_src = base / 'solo_client.dll'
            c_src.write_bytes(CLIENT_HELLO_DLL.read_bytes())
            ws.import_mod_file(c_src)

            # 2) Import 90JP collection ZIP with both client DLL (+ini) and server DLL
            col_zip = base / '02-dll-pack.zip'
            with zipfile.ZipFile(col_zip, 'w', compression=zipfile.ZIP_DEFLATED) as zf:
                zf.writestr('version.json', json.dumps({
                    'schemaVersion': 1, 'kind': 'collection', 'id': 'dll.pack',
                    'name': '外接插件合集', 'version': '1.0', 'moduleOrder': ['dll.pack.dual'],
                }, ensure_ascii=False))
                zf.writestr('dual/version.json', json.dumps({
                    'schemaVersion': 1, 'kind': 'mod', 'id': 'dll.pack.dual',
                    'name': '双端扩展', 'version': '1.0', 'dependencies': [],
                }, ensure_ascii=False))
                zf.writestr('dual/DLL/client/client_hello_plugin.dll', CLIENT_HELLO_DLL.read_bytes())
                zf.writestr('dual/DLL/client/client_hello_plugin.ini', 'scoped_greeting=from_mod\n')
                zf.writestr('dual/DLL/server/server_hello_plugin.dll', SERVER_HELLO_DLL.read_bytes())
            ws.import_mod_file(col_zip)

            # 3) ZIP with mismatched directory vs export must surface error on scan
            bad_zip = base / '03-mismatch.zip'
            with zipfile.ZipFile(bad_zip, 'w') as zf:
                zf.writestr('DLL/client/wrong_side.dll', SERVER_HELLO_DLL.read_bytes())
            ws.import_mod_file(bad_zip)

            mods = ws.scan()
            by_id = {m['id']: m for m in mods}
            self.assertIn('dll.client.solo_client', by_id)
            self.assertIn('dll.pack.dual', by_id)
            self.assertEqual(len(by_id['dll.pack.dual']['client_dll_entries']), 2)  # dll + ini
            self.assertEqual(len(by_id['dll.pack.dual']['server_dll_entries']), 1)
            self.assertIn('目录与导出符号不匹配', by_id['zip.03-mismatch']['error'])

            # Enable both valid mods in priority order: dual first, solo_client second
            ws.save_selection(
                ['dll.pack.dual', 'dll.client.solo_client'],
                ['dll.pack.dual', 'dll.client.solo_client'],
            )

            # Server running without auto_stop_server must block server DLL deployment
            with patch.object(core, 'gateway', return_value={'pid': 1234}):
                self.assertTrue(ws.needs_server_stop_for_apply())
                with self.assertRaises(ValueError) as srv_err:
                    ws.apply_merge(auto_stop_server=False)
                self.assertIn('wireprobe-us100.exe', str(srv_err.exception))

            # Apply cleanly when server stopped
            res = ws.apply_merge()
            self.assertEqual(res['client_dll_count'], 2)
            self.assertEqual(res['server_dll_count'], 1)
            c_deployed = sorted(p.name for p in (client_dir / 'plugins').glob('*.dll'))
            s_deployed = sorted(p.name for p in server_pdir.glob('*.dll'))
            self.assertEqual(c_deployed, [
                '001_dll_pack_dual__client_hello_plugin.dll',
                '002_dll_client_solo_client__solo_client.dll',
                'manual_keep.dll',
            ])
            self.assertEqual(s_deployed, [
                '001_dll_pack_dual__server_hello_plugin.dll',
                'manual_srv.dll',
            ])
            self.assertTrue((client_dir / 'plugins' / 'client_hello_plugin.ini').is_file())
            self.assertIn('plugins_enabled=1', (client_dir / 'ngs_stub.ini').read_text(encoding='utf-8'))

            # Flip priority order -> prefix numbers swap cleanly and old prefixed names are removed
            ws.save_selection(
                ['dll.client.solo_client', 'dll.pack.dual'],
                ['dll.client.solo_client', 'dll.pack.dual'],
            )
            ws.apply_merge()
            c_deployed_2 = sorted(p.name for p in (client_dir / 'plugins').glob('*.dll'))
            self.assertEqual(c_deployed_2, [
                '001_dll_client_solo_client__solo_client.dll',
                '002_dll_pack_dual__client_hello_plugin.dll',
                'manual_keep.dll',
            ])

            # Restore removes only managed DLLs & INI, preserving manual DLLs
            msg = ws.restore_original()
            self.assertIn('外接 DLL', msg)
            self.assertEqual([p.name for p in (client_dir / 'plugins').glob('*.dll')], ['manual_keep.dll'])
            self.assertEqual([p.name for p in server_pdir.glob('*.dll')], ['manual_srv.dll'])
            self.assertFalse((client_dir / 'plugins' / 'client_hello_plugin.ini').exists())


if __name__ == '__main__':
    unittest.main()
