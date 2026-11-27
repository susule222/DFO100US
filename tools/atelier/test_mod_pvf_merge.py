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
from mod_workshop import ModWorkshop
from sprites import KEY


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


if __name__ == '__main__':
    unittest.main()
