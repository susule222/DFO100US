import shutil
import struct
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
import core
from sprites import KEY, Wardrobe


def fixture(path,body):
    name=b'sprite/fixture.img'.ljust(256,b'\0')
    path.write_bytes(b'NeoplePack_Bill\0'+struct.pack('<III',1,284,len(body))+
                     bytes(a^b for a,b in zip(name,KEY))+body)


class Checks(unittest.TestCase):
    def test_mod_roundtrip(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(core,'client_up',return_value=False):
            base=Path(tmp); packs=base/'client';packs.mkdir()
            lib=core.Library(packs,base/'mods')
            a=base/'avatar.npk';fixture(a,b'new');target=packs/a.name;fixture(target,b'original');orig=target.read_bytes()
            k=lib.import_pack(a,'时装');lib.switch(k,True)
            self.assertEqual(target.read_bytes(),a.read_bytes())
            target.write_bytes(b'external')
            with self.assertRaises(ValueError):lib.switch(k,False)
            self.assertEqual(target.read_bytes(),b'external')
            shutil.copyfile(a,target);lib.switch(k,False);self.assertEqual(target.read_bytes(),orig)
            p=base/'pet.npk';fixture(p,b'pet');pet=lib.import_pack(p,'宠物');lib.switch(pet,True)
            lib.switch(pet,False);self.assertFalse((packs/p.name).exists())

    def test_wardrobe(self):
        w=Wardrobe(core.pre.client_exe().parent/'ImagePacks2')
        for job in w.jobs:
            with self.subTest(job=job):self.assertTrue(w.compose(job,{})[0].getbbox())
        im,used=w.compose('swordman',{})
        self.assertEqual(len(used),9)
        _,choices=w.choices('swordman','coat')
        updated,_=w.compose('swordman',{'coat':choices[len(choices)//2]})
        self.assertNotEqual(im.tobytes(),updated.tobytes())

    def test_rule_and_queue(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(core,'roles',return_value=[(7,'demo')]):
            dest=Path(tmp);items={10018:['装备']}
            key=core.submit(7,10018,1,'test',items,dest)
            self.assertEqual(core.read(dest/'pending'/(key+'.json'))['character_id'],7)
            with self.assertRaises(ValueError):core.submit(8,10018,1,'bad',items,dest)
            core.save_rule([3],10018,100,items,[3,4],dest)
            with self.assertRaises(ValueError):core.save_rule([9],10018,100,items,[3,4],dest)
            self.assertEqual(core.read(dest/'abyss.json')['maps'],[3])

    def test_warm_login_and_duplicate_guard(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(core,'ROOT',Path(tmp)), \
             patch.object(core,'gateway',return_value={'launcher_gm':True}), \
             patch.object(core.subprocess,'Popen') as popen:
            (Path(tmp)/'runtime').mkdir()
            with patch.object(core,'client_up',return_value=True):
                with self.assertRaises(ValueError):core.login()
                self.assertIn('网关',core.login(True))
                popen.assert_not_called()
            with patch.object(core,'client_up',side_effect=[False,True]):
                popen.return_value.poll.return_value=None
                self.assertIn('客户端已启动',core.login())
                self.assertIn('tools/us100_client.py',popen.call_args.args[0])


if __name__=='__main__':unittest.main()
