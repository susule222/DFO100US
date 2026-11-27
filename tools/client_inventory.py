# -*- coding: utf-8 -*-
"""Full inventory + triage of D:\\DNF\\client\\US100.

Collects, in one pass:
  * recursive file census per subdirectory (count, bytes, extensions);
  * PE version info + SHA256 for the identity-bearing binaries;
  * readable heads of every small text/binary config file (<=64KB);
  * magic-byte classification of the large data files.
Writes runtime/client_inventory.json + prints a readable digest.
Nothing in the client directory is modified.

Usage: python tools/client_inventory.py
"""
import collections
import hashlib
import json
import pathlib
import struct
import sys

CLIENT = pathlib.Path(r'D:\DNF\client\US100')
ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / 'runtime' / 'client_inventory.json'

MAGICS = {
    b'MZ': 'PE executable/DLL',
    b'\x7fELF': 'ELF',
    b'PK\x03\x04': 'ZIP container',
    b'7z\xbc\xaf': '7z container',
    b'\x1aE\xdf\xa3': 'Matroska/WebM',
    b'RIFF': 'RIFF (wav/avi)',
    b'OggS': 'Ogg',
    b'FSB5': 'FMOD sound bank',
    b'\x30\x26\xb2\x75': 'ASF/WMA/WMV',
    b'\x00\x00\x01\xba': 'MPEG-PS',
    b'\x89PNG': 'PNG',
    b'DDS ': 'DDS texture',
    b'GIF8': 'GIF',
    b'\xff\xd8\xff': 'JPEG',
}


def classify(path):
    try:
        with open(path, 'rb') as f:
            head = f.read(16)
    except OSError as exc:
        return 'unreadable: %s' % exc, head_hex(path)
    for magic, label in MAGICS.items():
        if head.startswith(magic):
            return label, head.hex()
    printable = sum(1 for b in head[:8] if 32 <= b < 127)
    if printable >= 6:
        return 'text-ish', head.hex()
    return 'unknown binary', head.hex()


def head_hex(path, n=64):
    try:
        with open(path, 'rb') as f:
            return f.read(n).hex()
    except OSError:
        return ''


def sha256(path, limit=200 * 1024 * 1024):
    h = hashlib.sha256()
    try:
        with open(path, 'rb') as f:
            while True:
                b = f.read(4 * 1024 * 1024)
                if not b:
                    break
                h.update(b)
                if h and f.tell() > limit:
                    return 'truncated@%dMB' % (limit // 1048576) + ':' + h.hexdigest()[:16]
    except OSError:
        return 'unreadable'
    return h.hexdigest()


def main():
    dirs = collections.OrderedDict()
    files = []
    for p in sorted(CLIENT.rglob('*')):
        if p.is_file():
            rel = p.relative_to(CLIENT)
            d = str(rel.parent) if str(rel.parent) != '.' else '(root)'
            e = dirs.setdefault(d, {'count': 0, 'bytes': 0, 'exts': collections.Counter()})
            try:
                sz = p.stat().st_size
            except OSError:
                sz = -1
            e['count'] += 1
            e['bytes'] += max(sz, 0)
            e['exts'][p.suffix.lower()] += 1
            files.append((rel, sz))
    print('== directories ==')
    for d, e in dirs.items():
        exts = ', '.join('%s×%d' % kv for kv in e['exts'].most_common(6))
        print('  %-28s %5d files %12.1f MB  [%s]' % (d, e['count'], e['bytes'] / 1048576, exts))

    # identity table for executables/DLLs and big data files
    identities = {}
    key_names = {'DFO.exe', 'CEF.exe', 'NeopleLauncher.exe', 'LauncherUpdator.exe',
                 'NGClient64.aes', 'NGClient64.aes.real', 'awesomium_process.exe',
                 '7z.exe', 'libcef.dll', 'steam_api64.dll', 'SecureEngineSDK64.dll',
                 'MachineIdLib.dll', 'NPS.dll', 'Script.pvf', 'sk.dat', 'dstr.dat',
                 'package.lst', 'localpackage.lst', 'version.ini', 'Patch.trc'}
    print('\n== identity (sha256 first 16 / size / mtime) ==')
    for rel, sz in files:
        if rel.name in key_names and str(rel.parent) == '.':
            p = CLIENT / rel
            digest = sha256(p)
            identities[str(rel)] = {'size': sz, 'sha256': digest,
                                    'mtime': p.stat().st_mtime}
            print('  %-24s %10d  %s' % (rel, sz, digest[:32]))

    # small files: full readable dump (<=64KB), text or hex
    print('\n== small file heads ==')
    small = {}
    for rel, sz in files:
        if 0 < sz <= 65536 and rel.suffix.lower() in ('.ini', '.txt', '.bat', '.cra', '.cfg', '.lst', '.dat', '.trc'):
            p = CLIENT / rel
            kind, hx = classify(p)
            try:
                raw = p.read_bytes()
            except OSError:
                continue
            text = None
            for enc in ('utf-8', 'utf-16-le', 'cp949', 'latin1'):
                try:
                    text = raw.decode(enc)
                    if sum(1 for c in text[:400] if c.isprintable() or c in '\r\n\t') > 300:
                        break
                except UnicodeDecodeError:
                    text = None
            entry = {'size': sz, 'kind': kind, 'hex_head': raw[:64].hex()}
            if text:
                entry['text_head'] = text[:1200]
            small[str(rel)] = entry
            print('--- %s (%d bytes, %s)' % (rel, sz, kind))
            if text:
                head = text[:400].replace('\r\n', ' | ').replace('\n', ' | ')
                print('    %s' % head[:380])

    out = {
        'directories': {d: {'count': e['count'], 'bytes': e['bytes'],
                            'exts': dict(e['exts'])} for d, e in dirs.items()},
        'identities': identities,
        'small_files': small,
    }
    OUT.write_text(json.dumps(out, indent=2, ensure_ascii=False), encoding='utf-8')
    print('\nwrote %s' % OUT)
    return 0


if __name__ == '__main__':
    sys.exit(main())
