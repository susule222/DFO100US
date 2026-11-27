"""Install a selected local typeface under the two family names used by US100."""
from contextlib import contextmanager
from copy import deepcopy
import ctypes
from pathlib import Path
import msvcrt
import tempfile
import uuid

import core


ORIGINAL={
    'calibri.ttf':'dfcc969b54dca341656a935d8a8711d920bf2b6fadbffbba7b6b75ae0c7044c3',
    'tahoma.ttf':'b890858df485549d20fcb0a2c519dffe9c5c155d16b55e0b9252d6e66db555e5',
}
IDENTITY={1,2,3,4,6,16,17,21,22}
BITMAP_TABLES=('EBDT','EBLC','EBSC','CBDT','CBLC','bdat','bloc','sbix')


def faces(path):
    from fontTools.ttLib import TTFont,TTCollection
    path=Path(path)
    if path.suffix.lower() not in ('.ttf','.ttc') or not 0<path.stat().st_size<=96*1024**2:
        raise ValueError('请选择不超过 96 MiB 的 TTF 或 TTC 字体。')
    with path.open('rb') as f:
        if f.read(4)==b'ttcf':
            f.seek(0)
            with TTCollection(f,lazy=True) as collection:
                return [font['name'].getBestFamilyName() or f'字面 {i}' for i,font in enumerate(collection.fonts)]
    with TTFont(path,lazy=True) as font:return [font['name'].getBestFamilyName() or path.stem]


def windows_font_check(path):
    # This process-private GDI registration is removed immediately, not installed in Windows.
    dll=ctypes.WinDLL('gdi32',use_last_error=True)
    add=dll.AddFontMemResourceEx;add.restype=ctypes.c_void_p
    add.argtypes=[ctypes.c_void_p,ctypes.c_uint32,ctypes.c_void_p,ctypes.POINTER(ctypes.c_uint32)]
    remove=dll.RemoveFontMemResourceEx;remove.argtypes=[ctypes.c_void_p];remove.restype=ctypes.c_int
    data=path.read_bytes();count=ctypes.c_uint32()
    handle=add(data,len(data),None,ctypes.byref(count))
    if not handle:raise ValueError(f'Windows 无法加载 {path.name}，未应用。')
    try:
        if not count.value:raise ValueError(f'Windows 没有识别 {path.name} 的字面。')
    finally:remove(handle)


def build_face(src,face,original,dest,outline_only=False):
    from fontTools.ttLib import TTFont
    from fontTools.pens.recordingPen import DecomposingRecordingPen
    from fontTools.pens.transformPen import TransformPen
    from fontTools.pens.ttGlyphPen import TTGlyphPen
    with TTFont(src,fontNumber=face,recalcTimestamp=False) as font, TTFont(original) as base:
        cmap=font.getBestCmap() or {}
        if 'glyf' not in font or 'fvar' in font or not all(ord(c) in cmap for c in ' Aa09'):
            raise ValueError('请选择带 Unicode 映射的静态 TrueType 字体；不能使用此字面。')
        expected=base['name'].getBestFamilyName()
        font['name'].names=[n for n in font['name'].names if n.nameID not in IDENTITY]
        font['name'].names.extend(deepcopy(n) for n in base['name'].names if n.nameID in IDENTITY)
        if outline_only:
            for tag in BITMAP_TABLES:
                if tag in font:del font[tag]
            # Request grayscale at the small sizes otherwise reserved for bitmap strikes.
            if 'gasp' in font:
                font['gasp'].gaspRange={65535:15}
        # Fill missing US glyphs with outlines from its original font.
        original_cmap=base.getBestCmap();order=list(font.getGlyphOrder());fallback={};mapping={}
        glyphs=base.getGlyphSet();scale=font['head'].unitsPerEm/base['head'].unitsPerEm
        for cp in sorted(original_cmap.keys()-cmap.keys()):
            name=original_cmap[cp]
            if name not in fallback:
                label=f'atelierFallback{len(order)}'
                if label in font['glyf']:raise ValueError('后备字形名称冲突')
                rec=DecomposingRecordingPen(glyphs);glyphs[name].draw(rec);pen=TTGlyphPen(None)
                rec.replay(TransformPen(pen,(scale,0,0,scale,0,0)));g=pen.glyph();g.recalcBounds({})
                font['glyf'][label]=g;order.append(label);fallback[name]=label
                font['hmtx'].metrics[label]=(round(glyphs[name].width*scale),getattr(g,'xMin',0))
                if 'vmtx' in font:font['vmtx'].metrics[label]=(font['head'].unitsPerEm,0)
            mapping[cp]=fallback[name]
        font.setGlyphOrder(order)
        for t in font['cmap'].tables:
            if t.isUnicode() and t.format in (4,12):
                t.cmap.update({c:n for c,n in mapping.items() if t.format==12 or c<0xffff})
        if 'DSIG' in font:del font['DSIG']
        font.save(dest)
        required=set(original_cmap)|set(cmap)
    with TTFont(dest) as f:
        if f['name'].getBestFamilyName()!=expected or not required<=(f.getBestCmap() or {}).keys():
            raise ValueError('生成后的字体名称或字符覆盖校验失败，未应用。')
    windows_font_check(dest)


class GameFonts:
    def __init__(self,folder,root=None,originals=None):
        self.folder=Path(folder)
        self.root=root or core.ROOT/'runtime'/'atelier-fonts'
        self.originals=originals or core.ROOT/'runtime'/'us100-font-r23-local-trial'/'before'
    def hashes(self):
        return {n:core.digest(self.folder/n) if (self.folder/n).is_file() else None for n in ORIGINAL}
    def state(self):
        path=self.root/'state.json'
        return core.read(path) if path.exists() else {}
    def originals_dir(self):
        cache=self.root/'originals'
        for n,h in ORIGINAL.items():
            if (cache/n).is_file() and core.digest(cache/n)==h:continue
            p=next((p for p in (self.originals/n,self.folder/n) if p.is_file() and core.digest(p)==h),None)
            if not p:raise ValueError('缺少与 100US 清单一致的原版字体：'+n)
            core.file_copy(p,cache/n)
        return cache
    @contextmanager
    def locked(self):
        self.root.mkdir(parents=True,exist_ok=True)
        with (self.root/'font.lock').open('a+b') as lock:
            try:msvcrt.locking(lock.fileno(),msvcrt.LK_NBLCK,1)
            except OSError as e:raise ValueError('另一个字体操作正在进行。') from e
            if core.client_up():raise ValueError('请先正常退出 DFO.exe，再切换游戏字体。')
            self.recover()
            yield
    def recover(self):
        path=self.root/'pending.json'
        if not path.exists():return
        p=core.read(path);current=self.hashes()
        if any(current[n] not in (p['before'][n],p['after'][n]) for n in ORIGINAL):
            raise ValueError('字体在操作中被其他程序改动，已保留现场和备份。')
        if all(current[n]==p['after'][n] for n in ORIGINAL) and self.state()==p['state']:
            path.unlink();return
        for n in ORIGINAL:
            if current[n]==p['before'][n]:continue
            if p['before'][n] is None:(self.folder/n).unlink(missing_ok=True)
            else:
                bak=self.root/'backups'/p['id']/n
                if core.digest(bak)!=p['before'][n]:raise ValueError('字体备份校验失败')
                core.file_copy(bak,self.folder/n)
        path.unlink()
    def commit(self,staged,state,before):
        if core.client_up() or self.hashes()!=before:raise ValueError('游戏已启动或字体已变化，未应用。')
        previous=self.state()
        if previous and previous['hashes']!=before:raise ValueError('游戏字体已被外部修改，不覆盖；请先核对备份。')
        after={n:core.digest(staged/n) for n in ORIGINAL}
        key=uuid.uuid4().hex;backup=self.root/'backups'/key
        for n in ORIGINAL:
            if before[n]:
                core.file_copy(self.folder/n,backup/n)
                if core.digest(backup/n)!=before[n]:raise ValueError('字体备份不一致，未应用。')
        state=dict(state,hashes=after)
        if self.hashes()!=before or core.client_up():raise ValueError('字体状态已变化，未应用。')
        journal=self.root/'pending.json'
        core.atomic(journal,dict(id=key,before=before,after=after,state=state))
        try:
            for n in ORIGINAL:core.file_copy(staged/n,self.folder/n)
            if self.hashes()!=after:raise ValueError('写入校验失败')
            core.atomic(self.root/'state.json',state)
        except Exception:
            self.recover();raise
        journal.unlink()
    def apply(self,src,face=0,outline_only=None):
        src=Path(src).resolve()
        with self.locked():
            before=self.hashes();baseline=self.originals_dir();names=faces(src)
            if not 0<=face<len(names):raise ValueError('无效字体编号')
            if outline_only is None:outline_only=names[face].casefold()=='simsun'
            if src in {(self.folder/n).resolve() for n in ORIGINAL}:raise ValueError('请选择源字体，不要选择游戏生成的入口文件。')
            with tempfile.TemporaryDirectory(dir=self.root) as tmp:
                stage=Path(tmp);donor=stage/('source'+src.suffix)
                core.file_copy(src,donor);sha=core.digest(donor)
                if core.digest(src)!=sha:raise ValueError('源字体已变化')
                state=self.state()
                if (state.get('sha256')==sha and state.get('face')==face
                        and state.get('outline_only',False)==outline_only and before==state.get('hashes')):
                    return '已应用该字体：'+names[face]
                for n in ORIGINAL:build_face(donor,face,baseline/n,stage/n,outline_only=outline_only)
                self.commit(stage,dict(family=names[face],file=src.name,face=face,sha256=sha,outline_only=outline_only),before)
        return '游戏字体已应用：'+names[face]+'。下次启动游戏生效。'
    def restore(self):
        with self.locked():
            before=self.hashes();baseline=self.originals_dir()
            if before==ORIGINAL:
                core.atomic(self.root/'state.json',dict(family='100US 原版',hashes=before))
            else:self.commit(baseline,dict(family='100US 原版'),before)
        return '已还原 100US 原版字体。'
