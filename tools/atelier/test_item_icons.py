import os
from pathlib import Path
import struct
import tempfile
import unittest
import zlib

from item_icons import ItemIcons, reference
from sprites import KEY


class IndexedFrames(unittest.TestCase):
    def archive(self,p,bgra,version=2,last_link=1):
        name='sprite/item/test/test.img'
        raw=zlib.compress(bgra)
        red=struct.pack('<H',0xfc00)
        first=struct.pack('<9I',14,5,1,1,len(red),0,0,1,1)
        second=struct.pack('<9I',16,6,1,1,len(raw),0,0,1,1)
        meta=first+second+struct.pack('<II',17,last_link)
        total=32+(12 if version==5 else 0)+len(meta)+len(red)+len(raw)
        img=(b'Neople Img File\0'+struct.pack('<4I',len(meta),0,version,3)+
             (struct.pack('<3I',0,total,0) if version==5 else b'')+meta+red+raw)
        coded=bytes(a^b for a,b in zip(name.encode().ljust(256,b'\0'),KEY))
        p.write_bytes(b'NeoplePack_Bill\0'+struct.pack('<3I',1,284,len(img))+coded+img)

    def test_nonzero_frame_link_bounds_and_replaced_pack(self):
        for ver in (2,5):
            with self.subTest(ver=ver),tempfile.TemporaryDirectory() as tmp:
                root=Path(tmp);p=root/'sprite_item_test.NPK';self.archive(p,bytes([3,2,1,255]),ver)
                assets=ItemIcons(root)
                def pixel(n):return assets.read(reference('Item/Test/Test.img',n)).getpixel((0,0))
                self.assertEqual(pixel(0),(255,0,0,255))
                self.assertEqual(pixel(1),(1,2,3,255))
                self.assertEqual(pixel(2),pixel(1))
                with self.assertRaises(ValueError):pixel(3)
                stamp=p.stat().st_mtime_ns+2000000000
                self.archive(p,bytes([6,5,4,255]),ver,last_link=2);os.utime(p,ns=(stamp,stamp))
                self.assertEqual(pixel(1),(4,5,6,255))
                with self.assertRaises(ValueError):pixel(2)

    def test_rejects_invalid_references(self):
        for name,n in [('../wrong.img',1),('item/missing.txt',0),('C:/bad.img',0),('item/test.img',-1)]:
            self.assertIsNone(reference(name,n))


if __name__=='__main__':unittest.main()
