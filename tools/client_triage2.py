# -*- coding: utf-8 -*-
"""Companion triage: packed-vs-unpacked DFO.exe, container magics, anticheat logs.

Usage: python tools/client_triage2.py
"""
import math
import pathlib
import struct

CLIENT = pathlib.Path(r'D:\DNF\client\US100')
DUMP = pathlib.Path(__file__).resolve().parent.parent / 'runtime' / 'dump'


def entropy(b):
    if not b:
        return 0.0
    freq = [0] * 256
    for x in b:
        freq[x] += 1
    n = len(b)
    return -sum((c / n) * math.log2(c / n) for c in freq if c)


def sections(path):
    data = pathlib.Path(path).read_bytes() if isinstance(path, (str, pathlib.Path)) else None
    with open(path, 'rb') as f:
        head = f.read(4096)
    e_lfanew = struct.unpack_from('<I', head, 0x3c)[0]
    nsec = struct.unpack_from('<H', head, e_lfanew + 6)[0]
    opt_size = struct.unpack_from('<H', head, e_lfanew + 20)[0]
    tab = e_lfanew + 24 + opt_size
    out = []
    for i in range(nsec):
        at = tab + i * 40
        name = head[at:at + 8].rstrip(b'\x00').decode('latin1')
        vsize, va, rawsize, rawptr = struct.unpack_from('<IIII', head, at + 8)
        out.append((name, va, vsize, rawptr, rawsize))
    return out


print('== at-rest DFO.exe (packed) ==')
for name, va, vs, rp, rs in sections(CLIENT / 'DFO.exe'):
    with open(CLIENT / 'DFO.exe', 'rb') as f:
        f.seek(rp)
        sample = f.read(min(65536, rs))
    print('  %-8s VA=%#09x VSize=%#09x Raw=%#08x@%#08x entropy=%.2f' %
          (name, va, vs, rs, rp, entropy(sample)))

print('== unpacked DFO_unpacked.exe ==')
up = DUMP / 'DFO_unpacked.exe'
for name, va, vs, rp, rs in sections(up):
    with open(up, 'rb') as f:
        f.seek(rp)
        sample = f.read(min(65536, rs))
    print('  %-8s VA=%#09x VSize=%#09x Raw=%#08x@%#08x entropy=%.2f' %
          (name, va, vs, rs, rp, entropy(sample)))

print('\n== container magics ==')
samples = [
    CLIENT / 'Script.pvf',
    CLIENT / 'sk.dat',
    CLIENT / 'dstr.dat',
]
img = sorted((CLIENT / 'ImagePacks2').glob('*.npk'))[:2]
snd = sorted((CLIENT / 'SoundPacks').glob('*.npk'))[:1]
rep = sorted((CLIENT / 'Replay' / 'Intro').glob('*.rep'))[:1]
mus = sorted((CLIENT / 'Music').glob('*.ogg'))[:1]
vid = sorted((CLIENT / 'Video').glob('*.avi'))[:1]
for p in samples + img + snd + rep + mus + vid:
    if p.is_file():
        with open(p, 'rb') as f:
            head = f.read(32)
        print('  %-52s %s' % (str(p.relative_to(CLIENT))[:52], head[:16].hex() + '  ' +
                               ''.join(chr(c) if 32 <= c < 127 else '.' for c in head[:16])))

print('\n== anticheat logs ==')
for name in ('BlackCipher/NGClient64.log', 'BlackCipher/BlackCall64.log', 'BlackCipher/BlackCipher64.log', 'BlackCipher/BlackXchg.log'):
    p = CLIENT / name
    if p.is_file():
        raw = p.read_bytes()
        for enc in ('utf-8', 'utf-16-le', 'cp949', 'latin1'):
            try:
                text = raw.decode(enc)
                break
            except UnicodeDecodeError:
                continue
        print('--- %s (%d bytes, %s)' % (name, len(raw), enc))
        print('    ' + text[:500].replace('\r\n', ' | ').replace('\n', ' | '))
cfg = CLIENT / 'BlackCipher' / 'config.bc'
if cfg.is_file():
    raw = cfg.read_bytes()
    print('--- config.bc (%d bytes) head: %s' % (len(raw), raw[:48].hex()))
