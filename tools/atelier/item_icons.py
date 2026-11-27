"""Resolve the exact inventory IMG path and zero-based frame supplied by the PVF."""
from dataclasses import dataclass
import re

from sprites import index, inventory_frame


@dataclass(frozen=True)
class IconRef:
    img: str
    frame: int


def reference(img, number):
    if not isinstance(img,str) or type(number) is not int or number<0:return None
    img=img.replace('\\','/').lower()
    if not img.startswith('sprite/'):img='sprite/'+img
    parts=img.split('/')
    if not img.endswith('.img') or not all(re.fullmatch(r'[\w ().-]+',v) and v not in ('.','..') for v in parts):
        return None
    return IconRef(img,number)


def from_cells(pair):
    if len(pair)!=2 or pair[0].get('type')!=6 or pair[1].get('type')!=0:return None
    return reference(pair[0].get('text'),pair[1].get('value'))


class ItemIcons:
    def __init__(self,packs,extra_packs=()):
        self.packs=packs
        self.extra_packs=tuple(extra_packs)

    def set_packs(self,packs,extra_packs=()):
        self.packs=packs
        self.extra_packs=tuple(extra_packs)

    def _roots(self):
        return (*self.extra_packs,self.packs)

    def locate(self,ref):
        parts=ref.img.split('/')[:-1]
        for root in self._roots():
            if not root or not root.is_dir():
                continue
            for end in range(len(parts),0,-1):
                pack=root/('_'.join(parts[:end])+'.NPK')
                if pack.is_file() and ref.img in index(pack):
                    return pack
        raise FileNotFoundError('未在客户端资源包中找到 '+ref.img)

    def read(self,ref):
        pack=self.locate(ref)
        stat=pack.stat()
        try:
            return inventory_frame(pack,stat.st_size,stat.st_mtime_ns,ref.img,ref.frame)
        except ValueError as exc:
            if '未支持的物品图集' not in str(exc) and '带 DDS' not in str(exc):raise
        from appearance import picture
        im,_,_=picture(pack,stat.st_mtime_ns,stat.st_size,ref.img,ref.frame)
        if max(im.size)>512:raise ValueError('无效物品图标尺寸')
        return im
