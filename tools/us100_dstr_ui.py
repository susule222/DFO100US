"""Read, verify, and replace only selected text rows in the exact 100US dstr.dat."""
import argparse
from datetime import datetime, timezone
import hashlib
from pathlib import Path
import re
import struct

from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

from us100_quest_act import OUTLINE_FONTS, REL
from us100_quest_strings import FORMAT, KEYS, ROW
from us100_quest_text_trial import ACT, EXPECTED_TRIAL
from us100_quest_ui_install import CLIENT, DSTR, PVF, GameFonts, core


EXE = "5d276d08faca85a19846ea7e595e999316440bec97e4476239b117740f662931"
IMAGE_BASE = 0x140000000
KEY_POINTERS = (0x947A280, 0x947A278)
SEED = 0x5819AF17
UI_ROWS = KEYS | {
    "21285": ("Create", "创建"),
    "21287": ("Delete", "删除"),
    "21288": ("Exit Game", "退出游戏"),
    "21342": ("Server", "服务器"),
    "21567": ("Guild Hideout", "公会基地"),
    "30051": ("Grand Flores", "格兰之森"),
    "44140": ("Elvenguard", "艾尔文防线"),
    "44141": ("Hendon Myre", "赫顿玛尔"),
    "48447": ("Rewards", "奖励"),
    "49009": ("Start Game", "开始游戏"),
    "67126": ("  The following characters can be\\nmoved to your Favorite Character area.",
              "  以下角色可移至\\n常用角色栏。"),
    "100003144": ("(Spacebar) ", "(空格键) "),
}
COMMON = {"All": "全部", "Cancel": "取消"}


def key_pair(exe):
    data = exe.read_bytes()
    if hashlib.sha256(data).hexdigest() != EXE:
        raise ValueError("require the exact 100US client executable")
    out = []
    for pos in KEY_POINTERS:
        off = struct.unpack_from("<Q", data, pos)[0] - IMAGE_BASE
        entry = data[off:off+65]
        if not re.fullmatch(rb"[a-fA-F0-9]{64}\0", entry):
            raise ValueError("invalid DSTR key reference")
        out.append(bytes.fromhex(entry[:-1].decode("ascii")))
    return out


def crypt_prefix(blob, keys, decrypt):
    n = len(blob) & ~255
    prefix = blob[:n]
    for key in keys if decrypt else reversed(keys):
        a = Cipher(algorithms.AES(key), modes.CBC(bytes(16)))
        c = a.decryptor() if decrypt else a.encryptor()
        prefix = c.update(prefix) + c.finalize()
    return prefix + blob[n:]


def xor_words(data, decrypt):
    out = bytearray(len(data))
    step = SEED
    for i, (word,) in enumerate(struct.iter_unpack("<I", data)):
        encoded = word ^ step
        struct.pack_into("<I", out, i*4, encoded)
        step = word if decrypt else encoded
    return bytes(out)


def unpack(blob, keys):
    inner = crypt_prefix(blob, keys, True)
    if len(inner) < 8 or inner[:4] != b"SC01" or len(inner) % 4:
        raise ValueError("invalid 100US DSTR container")
    size, = struct.unpack_from("<I", inner, 4)
    if not 0 < size <= len(inner)-8 or (len(inner)-8)-size > 3:
        raise ValueError("invalid SC01 payload span")
    return xor_words(inner[8:], True)[:size]


def pack(text, keys):
    data = text + bytes((-len(text)) % 4)
    inner = b"SC01" + struct.pack("<I", len(text)) + xor_words(data, False)
    return crypt_prefix(inner, keys, False)


def translate(raw):
    counts, changed = {}, []
    text = raw.decode("utf-8")

    def row(m):
        key, old = m[1], m[2]
        if key in UI_ROWS:
            expected, new = UI_ROWS[key]
            if old != expected:
                raise ValueError(f"unexpected source for UI row {key}")
        elif old.strip() in COMMON:
            word = old.strip()
            new = old.replace(word, COMMON[word], 1)
        else:
            return m[0]
        counts[key] = counts.get(key, 0) + 1
        if counts[key] > 1 or FORMAT.findall(old) != FORMAT.findall(new) or old.count("\\n") != new.count("\\n"):
            raise ValueError(f"duplicate key or changed format for {key}")
        changed.append({"key":key,"before":old,"after":new})
        return key + ">" + new

    out = ROW.sub(row, text)
    if UI_ROWS.keys() - counts.keys() or len(changed) != 86:
        raise ValueError(f"unexpected UI row coverage: {len(changed)}")
    if out.count("\r\n") != text.count("\r\n"):
        raise ValueError("changed physical line breaks")
    return out.encode("utf-8"), changed


def build(dstr, exe, reference, out):
    out = out.resolve()
    if out.is_relative_to(CLIENT.resolve()) or core.digest(dstr) != DSTR:
        raise ValueError("expected the original DSTR and an offline output directory")
    keys = key_pair(exe)
    source = dstr.read_bytes()
    raw = unpack(source, keys)
    expected = reference.read_bytes().decode("utf-16-le").removeprefix("\ufeff").rstrip("\0").encode("utf-8")
    if raw != expected or pack(raw, keys) != source:
        raise ValueError("original DSTR failed exact text or native byte round trip")
    translated, rows = translate(raw)
    candidate = pack(translated, keys)
    if unpack(candidate, keys) != translated or pack(unpack(candidate, keys), keys) != candidate:
        raise ValueError("localized DSTR failed exact round trip")
    out.mkdir(parents=True)
    (out/"dstr.dat").write_bytes(candidate)
    core.atomic(out/"manifest.json", {"before":DSTR,"after":hashlib.sha256(candidate).hexdigest(),
                "original_text_sha256":hashlib.sha256(raw).hexdigest(),"native_round_trip":True,
                "original_matches_pvf_source":True,"candidate_bytes":len(candidate),"rows":rows})
    print(f"Verified original and localized DSTR; translated {len(rows)} rows")


def deploy(bundle, rollback):
    bundle = bundle.resolve()
    if bundle.is_relative_to(CLIENT.resolve()):
        raise ValueError("bundle must not overlap the live client")
    m = core.read(bundle/"manifest.json")
    live, candidate, backup = CLIENT/"dstr.dat", bundle/"dstr.dat", bundle/"before-dstr.dat"
    expected = DSTR if rollback else m["after"]
    fonts = GameFonts(CLIENT/"Fonts")
    with fonts.locked():
        current = core.digest(live)
        if m["before"] != DSTR or not m["native_round_trip"] or current not in {DSTR, m["after"]}:
            raise ValueError("unknown DSTR baseline")
        if not rollback:
            if (fonts.hashes() != OUTLINE_FONTS or core.digest(CLIENT/REL) != ACT
                    or core.digest(CLIENT/"DFO.exe") != EXE
                    or core.digest(CLIENT/"Script.pvf") not in {PVF, EXPECTED_TRIAL}
                    or core.digest(candidate) != expected):
                raise ValueError("unexpected installed identity or changed candidate")
            if current == DSTR and not backup.exists():
                core.file_copy(live, backup)
        if not backup.is_file() or core.digest(backup) != DSTR:
            raise ValueError("no verified original DSTR backup")
        if core.client_up() or core.digest(live) != current:
            raise ValueError("DFO reopened or DSTR changed")
        if current != expected:
            core.file_copy(backup if rollback else candidate, live)
        if core.digest(live) != expected:
            raise ValueError("live DSTR hash mismatch")
        core.atomic(bundle/"deployment.json", {"utc":datetime.now(timezone.utc).isoformat(),
                    "status":"rolled_back" if rollback else "installed_pending_game_check",
                    "file":str(live),"sha256":expected,"changed_only":"dstr.dat"})
        print(f"Verified live dstr.dat: {expected}")


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    actions = p.add_subparsers(dest="action", required=True)
    b = actions.add_parser("build")
    for name in ("dstr", "exe", "reference", "out"):
        b.add_argument("--"+name, type=Path, required=True)
    for action in ("install", "rollback"):
        actions.add_parser(action).add_argument("--bundle", type=Path, required=True)
    a = p.parse_args()
    if a.action == "build":
        build(a.dstr, a.exe, a.reference, a.out)
    else:
        deploy(a.bundle, a.action == "rollback")
