# -*- coding: utf-8 -*-
"""Read-only unwrap of the US100 (2.24) Script.pvf outer wrapper.

Recovered from the client image (all addresses verified statically):

  * RSA private key   : PEM at RVA 0x7DCBE30, referenced by the global at
                        0x9481190 (the first PEM at 0x7B9F1B0 is the wrong
                        key: it shares the modulus with the embedded public
                        key but does not decrypt sk.dat).
  * AES-256 key       : 64 hex chars at RVA 0x7DCC1D0, referenced by the global
                        at 0x9481198.
  * sk.dat            : 11 PKCS#1 v1.5 blocks -> 1216 bytes; the first
                        len//256*256 bytes are AES-CBC decrypted with zero IV.
                        That yields 38 segment keys, matching the 38 segments
                        the 390,977,429-byte Script.pvf splits into.
  * Segment transform : the first 0x2800 bytes of segment N are AES-CBC
                        decrypted with segment key N, then the 48-byte inner
                        header is XORed with the `iNfO` stream. The rest of the
                        segment is copied unchanged.

The original client directory is never modified; the key material stays in
memory and is never written out. Usage:

    python unwrap_us100_pvf.py [--out PATH] [--dry-run]
"""
from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import struct
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import PE

from cryptography.hazmat.primitives.asymmetric.padding import PKCS1v15
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
from cryptography.hazmat.primitives.serialization import load_pem_private_key

CLIENT = pathlib.Path(r'D:\DNF\client\US100')
EXE = CLIENT / 'DFO.exe'
SK = CLIENT / 'sk.dat'
PVF = CLIENT / 'Script.pvf'

PEM_PRIVATE_KEY_RVA = 0x7DCBE30
AES_KEY_RVA = 0x7DCC1D0
PEM_GLOBAL_VA = 0x9481190
AES_GLOBAL_VA = 0x9481198

SEGMENT_SIZE = 0xA00000
SEGMENT_HEAD = 0x2800


def cbc_decrypt(key: bytes, raw: bytes) -> bytes:
    d = Cipher(algorithms.AES(key), modes.CBC(bytes(16))).decryptor()
    return d.update(raw) + d.finalize()


def stream_xor(key: str, raw: bytes) -> bytes:
    k = [ord(x) for x in key]
    seed = (0x339E9711 * k[0] + 0x393 * (k[3] + 0x393 * (k[2] + 0x393 * k[1]))) & 0xFFFFFFFF
    out = bytearray(raw)
    for off in range(0, len(out), 4):
        t = (0x343FD * seed + 0x269EC3) & 0xFFFFFFFF
        seed = (0x343FD * t + 0x269EC3) & 0xFFFFFFFF
        q = struct.pack('<I', (t & 0xFFFF0000) + (seed >> 16))
        for i in range(min(4, len(out) - off)):
            out[off + i] ^= q[i]
    return bytes(out)


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument('--out', default='')
    ap.add_argument('--dry-run', action='store_true')
    args = ap.parse_args()
    out = pathlib.Path(args.out) if args.out else (
        pathlib.Path(r'D:\DNF project\service\美服100服务端\runtime\pvf_source') / 'Script.inner.pvf')

    pe = PE(str(EXE))
    raw_pem = pe.read(PEM_PRIVATE_KEY_RVA, 0x600)
    end = raw_pem.find(b'-----END PRIVATE KEY-----')
    if end < 0:
        raise SystemExit('private key PEM not found')
    private = load_pem_private_key(raw_pem[:end + len(b'-----END PRIVATE KEY-----')], password=None)
    aes_hex = pe.read(AES_KEY_RVA, 64).decode('ascii')
    if len(aes_hex) != 64:
        raise SystemExit('AES key is not 64 hex characters')

    sk_data = SK.read_bytes()
    size = private.key_size // 8
    if len(sk_data) % size:
        raise SystemExit('sk.dat is not a whole number of RSA blocks')
    unwrapped = b''.join(
        private.decrypt(sk_data[i:i + size], PKCS1v15()) for i in range(0, len(sk_data), size))
    prefix = len(unwrapped) // 256 * 256
    unwrapped = cbc_decrypt(bytes.fromhex(aes_hex), unwrapped[:prefix]) + unwrapped[prefix:]
    if len(unwrapped) % 32:
        raise SystemExit('unwrapped sk.dat is not 32-byte aligned')
    keys = [unwrapped[i:i + 32] for i in range(0, len(unwrapped), 32)]

    total = PVF.stat().st_size
    segments = (total + SEGMENT_SIZE - 1) // SEGMENT_SIZE
    print('sk.dat %d bytes -> %d segment keys; Script.pvf %d bytes -> %d segments' % (
        len(sk_data), len(keys), total, segments))
    if len(keys) < segments:
        raise SystemExit('fewer segment keys than segments')

    with PVF.open('rb') as f:
        first = f.read(SEGMENT_HEAD)
    head = stream_xor('iNfO', cbc_decrypt(keys[0], first)[:48])
    if head[:4] != b'nkpi':
        raise SystemExit('inner header signature did not match after decryption')
    words = list(struct.unpack('<12I', head))
    print('inner header ok: file_count=%d group_count=%d' % (words[1], words[2]))
    if args.dry_run:
        print('dry run: nothing written')
        pe.close()
        return

    out.parent.mkdir(parents=True, exist_ok=True)
    if out.exists():
        raise SystemExit('output already exists, refusing to overwrite: %s' % out)

    source_hash = hashlib.sha256()
    inner_hash = hashlib.sha256()
    index = 0
    with PVF.open('rb') as source, out.open('xb') as dest:
        while True:
            chunk = source.read(SEGMENT_SIZE)
            if not chunk:
                break
            source_hash.update(chunk)
            if index < len(keys):
                if len(chunk) < SEGMENT_HEAD:
                    raise SystemExit('segment %d shorter than its encrypted head' % index)
                chunk = cbc_decrypt(keys[index], chunk[:SEGMENT_HEAD]) + chunk[SEGMENT_HEAD:]
            dest.write(chunk)
            inner_hash.update(chunk)
            index += 1
    manifest = {
        'source': str(PVF),
        'source_sha256': source_hash.hexdigest(),
        'output': str(out),
        'inner_sha256': inner_hash.hexdigest(),
        'segments': index,
        'key_count': len(keys),
        'header_words': words,
        'file_count': words[1],
        'group_count': words[2],
        'private_key_rva': hex(PEM_PRIVATE_KEY_RVA),
        'aes_key_rva': hex(AES_KEY_RVA),
        'key_globals': {'pem': hex(PEM_GLOBAL_VA), 'aes': hex(AES_GLOBAL_VA)},
        'status': 'inner NKPI header verified',
    }
    (out.parent / 'unwrap.json').write_text(json.dumps(manifest, indent=2), encoding='utf-8')
    print(json.dumps({k: manifest[k] for k in
                      ('source_sha256', 'inner_sha256', 'segments', 'file_count', 'group_count')}, indent=2))
    print('written:', out)
    pe.close()


if __name__ == '__main__':
    if sys.stdout is not None:
        sys.stdout.reconfigure(encoding='utf-8', errors='replace')
    main()
