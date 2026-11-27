"""Read-only US100 NPK index, avatar standing frames and indexed inventory sprites."""
import re
import struct
import zlib
from functools import lru_cache
from pathlib import Path

from PIL import Image

KEY = (b'puchikon@neople dungeon and fighter ' + b'DNF' * 74)[:255] + b'\0'
SLOTS = ('skin', 'shoes', 'pants', 'coat', 'belt', 'neck', 'face', 'hair', 'cap')


@lru_cache(maxsize=28)
def _index(path, size, stamp):
    with path.open('rb') as f:
        if f.read(16) != b'NeoplePack_Bill\0':
            raise ValueError('不是 Neople NPK 资源包')
        count, = struct.unpack('<I', f.read(4))
        end = 20 + 264 * count
        if not 0 < count <= 70000 or end > size:
            raise ValueError('无效的 NPK 索引')
        rows = {}
        for _ in range(count):
            data = f.read(264)
            offset, n = struct.unpack_from('<II', data)
            if offset < end or offset + n > size:
                raise ValueError('NPK 数据越界')
            name = bytes(a ^ b for a, b in zip(data[8:], KEY)).split(b'\0')[0].decode('cp949', 'replace')
            rows[name.lower()] = (offset, n)
        return rows


def index(path):
    stat = path.stat()
    return _index(path, stat.st_size, stat.st_mtime_ns)


def frame(path, name, palette=0):
    offset, size = index(path)[name]
    with path.open('rb') as f:
        f.seek(offset)
        def read(n):
            if n < 0 or f.tell() + n > offset+size:
                raise ValueError('IMG 越界')
            data = f.read(n)
            if len(data) != n:
                raise ValueError('IMG 已截断')
            return data
        def u32():
            return struct.unpack('<I', read(4))[0]
        if read(16) != b'Neople Img File\0':
            raise ValueError('不是 IMG 图层')
        length, _, version, count = struct.unpack('<4I', read(16))
        if version not in (2, 4, 6) or count == 0 or length < 8:
            raise ValueError(f'暂不支持 IMG v{version} 或空图层')
        colors = None
        palettes = u32() if version == 6 else int(version == 4)
        if palettes > 512:
            raise ValueError('调色板数量越界')
        for p in range(palettes):
            n = u32()
            if n > 256:
                raise ValueError('调色板无效')
            table = read(4*n)
            if p == palette % palettes:
                colors = table
        table_start = f.tell()
        kind, comp = struct.unpack('<2I', read(8))
        if kind == 17:
            raise ValueError('首帧为链接，暂不支持')
        w, h, n, x, y, _, _ = struct.unpack('<IIIiiII', read(28))
        if kind not in (14, 15, 16) or not (0 < w <= 4096 and 0 < h <= 4096 and w*h <= 4194304) or n > 20*1024*1024:
            raise ValueError('未支持的帧格式')
        f.seek(table_start + length)
        raw = read(n)
        return _pixels(kind,comp,w,h,raw,colors), (x,y)


def _pixels(kind,comp,w,h,raw,colors=None):
    expected=w*h*(4 if kind==16 else 2)
    if comp==6:
        decoder=zlib.decompressobj()
        raw=decoder.decompress(raw,expected+1)
        if not decoder.eof:
            raise ValueError('IMG 压缩数据无效')
    elif comp!=5:
        raise ValueError('未知压缩格式')
    if colors and len(raw)==w*h:
        if max(raw)>=len(colors)//4:
            raise ValueError('像素调色索引越界')
        im=Image.frombytes('P',(w,h),raw)
        im.putpalette(colors.ljust(1024,b'\0'),rawmode='RGBA')
        return im.convert('RGBA')
    if len(raw)!=expected:
        raise ValueError('像素尺寸不匹配')
    if kind==16:
        return Image.frombytes('RGBA',(w,h),raw,'raw','BGRA')
    pixels=[]
    for value, in struct.iter_unpack('<H',raw):
        if kind==15:
            pixels.append(tuple(((value>>bit)&15)*17 for bit in (8,4,0,12)))
        else:
            pixels.append(tuple(((value>>bit)&31)*255//31 for bit in (10,5,0))+(255*(value>>15),))
    im=Image.new('RGBA',(w,h))
    im.putdata(pixels)
    return im


@lru_cache(maxsize=48)
def _inventory_table(path, size, stamp, name):
    start,span=_index(path,size,stamp)[name]
    with path.open('rb') as f:
        f.seek(start)
        if span<32 or f.read(16)!=b'Neople Img File\0':
            raise ValueError('无效 IMG 图集')
        length,_,version,count=struct.unpack('<4I',f.read(16))
        if version==5:
            if span<44 or struct.unpack('<3I',f.read(12))!=(0,span,0):
                raise ValueError('不支持带 DDS 纹理的 v5 图集')
        elif version!=2:
            raise ValueError(f'未支持的物品图集 v{version}')
        first=f.tell()+length
        if not (0<count<=65536 and count*8<=length<=count*36 and first<=start+span):
            raise ValueError('图帧目录越界')
        table=f.read(length)
    rows=[]; at=0; pos=first
    for _ in range(count):
        if at+8>len(table):raise ValueError('帧目录截断')
        kind,comp=struct.unpack_from('<2I',table,at)
        if kind==17:
            rows.append(comp);at+=8
            continue
        if kind not in (14,15,16) or at+36>len(table):raise ValueError('未知图帧类型')
        w,h,n=struct.unpack_from('<3I',table,at+8)
        if pos+n>start+span:raise ValueError('图帧数据越界')
        rows.append((kind,comp,w,h,pos,n));at+=36;pos+=n
    if at!=length:raise ValueError('帧目录长度不匹配')
    return rows


@lru_cache(maxsize=768)
def inventory_frame(path,size,stamp,name,number):
    rows=_inventory_table(path,size,stamp,name)
    seen=set()
    while 0<=number<len(rows) and number not in seen:
        row=rows[number]
        if isinstance(row,int):
            seen.add(number);number=row
            continue
        kind,comp,w,h,pos,n=row
        if not (0<w<=512 and 0<h<=512 and n<=2*1024*1024):
            raise ValueError('无效物品图标尺寸')
        with path.open('rb') as f:
            f.seek(pos);data=f.read(n)
        return _pixels(kind,comp,w,h,data)
    raise ValueError('图帧编号越界或存在循环引用')


class Wardrobe:
    def __init__(self, packs, extra_packs=()):
        self.extra_packs = tuple(extra_packs)
        self.set_packs(packs, extra_packs)

    def set_packs(self, packs, extra_packs=()):
        self.packs = packs
        self.extra_packs = tuple(extra_packs)
        self.jobs = {}
        if packs and packs.is_dir():
            for p in sorted(packs.glob('sprite_character_*_avatar_skin.NPK')):
                m = re.fullmatch(r'sprite_character_(.+?)_(atequipment|equipment)_avatar_skin', p.stem, re.I)
                if m:
                    self.jobs[m[1] + (' / AT' if m[2].lower() == 'atequipment' else '')] = p.name[:-8]

    def choices(self, job, slot):
        path = self.packs / (self.jobs[job]+slot+'.NPK')
        if not path.is_file():
            return path, []
        names = [s for s in index(path) if re.fullmatch(r'[a-z]+_(?:body|'+slot+r')\d+[a-z]*\.img',Path(s).name)]
        return path, sorted(names)

    def compose(self, job, selected):
        canvas = Image.new('RGBA',(600,600))
        used = []
        for slot in SLOTS:
            path, names = self.choices(job,slot)
            if not names or selected.get(slot) == '-':
                continue
            chosen = selected.get(slot, names[0])
            # Report an unsupported selected layer; never substitute different art.
            im, (x,y) = frame(path,chosen)
            canvas.alpha_composite(im,(x,y))
            used.append(Path(chosen).stem)
        bounds=canvas.getbbox()
        if bounds is None:
            raise ValueError('当前搭配没有可见像素')
        return canvas.crop(bounds), used
