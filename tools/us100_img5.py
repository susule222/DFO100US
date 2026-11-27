"""Read the v5 IMG frame and surface layout used by the US100 shop atlas."""
from dataclasses import dataclass
from io import BytesIO
import struct
import zlib

from PIL import Image

from us100_ui_atlas import _pixels


@dataclass(frozen=True)
class Pixels:
    kind: int
    compression: int
    width: int
    height: int
    start: int
    raw: bytes

    def image(self):
        if self.kind in (18,19,20):
            data=zlib.decompress(self.raw) if self.compression==6 else self.raw
            with Image.open(BytesIO(data)) as f:
                return f.convert("RGBA")
        return _pixels(self.kind,self.compression,self.width,self.height,self.raw)


@dataclass(frozen=True)
class View:
    pixels: Pixels
    crop: tuple
    rotated: bool=False

    def image(self):
        im=self.pixels.image().crop(self.crop)
        return im.transpose(Image.Transpose.ROTATE_90) if self.rotated else im


def views(raw):
    if raw[:16]!=b"Neople Img File\0" or len(raw)<44:
        raise ValueError("not an IMG atlas")
    table,_,version,count,n,total,palette=struct.unpack_from("<7I",raw,16)
    if version!=5 or total!=len(raw) or palette or not 0<count<=4096 or n>4096:
        raise ValueError("not a supported v5 IMG atlas")
    meta=44+n*28
    pos=meta+table
    if pos>len(raw):
        raise ValueError("v5 frame table outside the member")
    surfaces=[]
    result=[]

    def payload(kind,comp,w,h,sz):
        nonlocal pos
        if not (0<w<=8192 and 0<h<=8192 and pos<=pos+sz<=len(raw)):
            raise ValueError("v5 pixel payload outside bounds")
        p=Pixels(kind,comp,w,h,pos,raw[pos:pos+sz])
        pos+=sz
        return p

    for i in range(n):
        flags,kind,ident,sz,expected,w,h=struct.unpack_from("<7I",raw,44+28*i)
        if flags!=1 or ident!=i or expected<1:
            raise ValueError("unexpected v5 surface descriptor")
        surfaces.append(payload(kind,6,w,h,sz))
    a=meta
    for i in range(count):
        if a+8>meta+table:
            raise ValueError("truncated v5 frame header")
        kind,c=struct.unpack_from("<2I",raw,a)
        if kind==17:
            result.append(c)
            a+=8
            continue
        if a+36>meta+table:
            raise ValueError("truncated v5 frame")
        w,h,sz=struct.unpack_from("<3I",raw,a+8)
        if c==7:
            if a+64>meta+table:
                raise ValueError("truncated v5 atlas reference")
            _,sid,x1,y1,x2,y2,rot=struct.unpack_from("<7I",raw,a+36)
            s=surfaces[sid]
            if (rot not in (0,1) or (x2-x1,y2-y1)!=((h,w) if rot else (w,h))
                    or x2>s.width or y2>s.height or kind!=s.kind):
                raise ValueError(f"unexpected v5 frame crop {i}")
            result.append(View(s,(x1,y1,x2,y2),bool(rot)))
            a+=64
        elif c in (5,6):
            result.append(View(payload(kind,c,w,h,sz),(0,0,w,h)))
            a+=36
        else:
            raise ValueError(f"unsupported v5 frame compression {c}")
    if a!=meta+table or pos!=len(raw):
        raise ValueError("unexpected v5 member tail")
    return result
