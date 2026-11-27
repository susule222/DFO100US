"""Build a separate US100 client-format PVF and verify its entire round trip."""
import argparse
import hashlib
import json
from pathlib import Path
import sys

from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "tools"))
from pvf_unwrap import (  # noqa: E402
    HEX64, SEGMENT_HEAD, SEGMENT_SIZE, cbc_decrypt, derive_segment_keys,
    first_segment_head, identify_inner, rsa_candidates, stream_xor,
)


def segments(f, keys):
    for i, chunk in enumerate(iter(lambda: f.read(SEGMENT_SIZE), b"")):
        if i >= len(keys) or len(chunk) < SEGMENT_HEAD:
            raise ValueError("unsupported segment count or short encrypted head")
        yield keys[i], chunk


def resolve_keys(original):
    exe = (original.parent / "DFO.exe").read_bytes()
    sk = (original.parent / "sk.dat").read_bytes()
    head = first_segment_head(original)
    hexes = {m.group() for m in HEX64.finditer(exe)}
    for _, _, _, blocks in rsa_candidates(exe, sk):
        if isinstance(blocks, Exception):
            continue
        for key in hexes:
            keys, tail = derive_segment_keys(b"".join(blocks), bytes.fromhex(key.decode()))
            stream, sig, _ = identify_inner(keys, head)
            if not tail and stream == "iNfO" and sig == b"nkpi":
                return keys
    raise ValueError("original US100 wrapper could not be verified")


def verify_original(original, baseline, keys):
    outer, inner = hashlib.sha256(), hashlib.sha256()
    with original.open("rb") as src, baseline.open("rb") as expected:
        for key, chunk in segments(src, keys):
            unwrapped = cbc_decrypt(key, chunk[:SEGMENT_HEAD]) + chunk[SEGMENT_HEAD:]
            if expected.read(len(chunk)) != unwrapped:
                raise ValueError("baseline is not an exact unwrap of the installed original")
            outer.update(chunk)
            inner.update(unwrapped)
        if expected.read(1):
            raise ValueError("baseline contains additional bytes")
    return outer.hexdigest(), inner.hexdigest()


def wrap(original, baseline, candidate, output):
    paths = tuple(p.resolve() for p in (original, baseline, candidate, output))
    original, baseline, candidate, output = paths
    if len(set(paths)) != 4 or output.parent == original.parent:
        raise ValueError("write only to a separate, new candidate location")
    manifest = output.with_suffix(".manifest.json")
    if output.exists() or manifest.exists():
        raise FileExistsError(output)
    keys = resolve_keys(original)
    source_hash, baseline_hash = verify_original(original, baseline, keys)
    size = candidate.stat().st_size
    count = (size + SEGMENT_SIZE - 1) // SEGMENT_SIZE
    if not 0 < count <= len(keys) or size - (count - 1) * SEGMENT_SIZE < SEGMENT_HEAD:
        raise ValueError("candidate does not fit the installed key schedule")
    inner, outer = hashlib.sha256(), hashlib.sha256()
    tmp = output.with_suffix(".pending")
    with candidate.open("rb") as src, tmp.open("xb") as dst:
        for i, (key, chunk) in enumerate(segments(src, keys)):
            if i == 0 and not stream_xor("iNfO", chunk[:48]).startswith(b"nkpi"):
                raise ValueError("candidate does not have the expected inner header")
            enc = Cipher(algorithms.AES(key), modes.CBC(bytes(16))).encryptor()
            data = enc.update(chunk[:SEGMENT_HEAD]) + enc.finalize() + chunk[SEGMENT_HEAD:]
            dst.write(data)
            inner.update(chunk)
            outer.update(data)
    reopened, wrapped = hashlib.sha256(), hashlib.sha256()
    with tmp.open("rb") as src, candidate.open("rb") as expected:
        for key, data in segments(src, keys):
            decoded = cbc_decrypt(key, data[:SEGMENT_HEAD]) + data[SEGMENT_HEAD:]
            if decoded != expected.read(len(data)):
                raise ValueError("candidate failed whole-file unwrap verification")
            reopened.update(decoded)
            wrapped.update(data)
        if expected.read(1) or (reopened.digest(), wrapped.digest()) != (inner.digest(), outer.digest()):
            raise ValueError("candidate round-trip digest mismatch")
    tmp.rename(output)
    receipt = {
        "source": str(original),
        "source_sha256": source_hash,
        "baseline_inner_sha256": baseline_hash,
        "candidate_inner_sha256": inner.hexdigest(),
        "candidate": str(output),
        "candidate_sha256": outer.hexdigest(),
        "bytes": size, "segments": count,
        "verified_rewrap_round_trip": True,
        "installed": False, "game_validated": False,
    }
    with manifest.open("x", encoding="utf-8") as handle:
        json.dump(receipt, handle, indent=2)
    print(json.dumps(receipt, indent=2))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in ("original", "baseline", "candidate", "output"):
        parser.add_argument("--" + flag, required=True, type=Path)
    wrap(**vars(parser.parse_args()))
