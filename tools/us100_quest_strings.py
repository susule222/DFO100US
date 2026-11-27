"""Prepare a key-scoped US100 quest UI table diagnostic without touching the client."""
import argparse
import codecs
import hashlib
import json
from pathlib import Path
import re


KEYS = {
    "21770": ("Quest Book", "任务手册"),
    "21771": ("Act Completed", "已完成主线"),
    "40003": ("Close", "关闭"),
    "40005": ("Abandon", "放弃"),
    "40011": ("Quest Book (%s)", "任务手册 (%s)"),
    "65009": ("<Complete condition>", "<完成条件>"),
    "65010": ("Click to accept", "点击接受"),
    "100007208": ("Lv. %d Quest", "%d 级任务"),
    "100008116": (" Act", " 主线"),
    "100008121": (" In progress", " 进行中"),
    "100008123": ("View Optimal Level quests only", "仅显示推荐等级任务"),
    "100008129": ("[Main Quests]", "[主线任务]"),
    "100008152": ("[Other Quests]", "[其他任务]"),
    "100085040": ("Guide", "指南"),
}
ROW = re.compile(r"^(\d+)>([^\r\n]*)", re.MULTILINE)
FORMAT = re.compile(r"%(?:\d+(?:\.\d+)?)?[dsf%]")


def decode(blob):
    if blob.startswith(codecs.BOM_UTF16_BE):
        raise ValueError("require a little-endian UTF-16 table")
    return blob.removeprefix(codecs.BOM_UTF16_LE).decode("utf-16-le")


def table_rows(text):
    rows = {}
    for match in ROW.finditer(text):
        key = match[1]
        if key in KEYS:
            if key in rows:
                raise ValueError(f"ambiguous key {key}")
            rows[key] = match[2]
    if rows.keys() != KEYS.keys():
        raise ValueError(f"missing keys: {sorted(KEYS.keys() - rows.keys())}")
    return rows


def overlay(original, prior):
    rows, old = table_rows(decode(original)), table_rows(decode(prior))
    report = []
    for key, (english, local) in KEYS.items():
        if rows[key] != english or re.findall(FORMAT, local) != re.findall(FORMAT, english):
            raise ValueError(f"unexpected baseline row or format for {key}")
        report.append(dict(key=key, english=english, before=old[key], after=local, changed=old[key] != local))
    output = ROW.sub(lambda m: f"{m[1]}>{KEYS[m[1]][1]}" if m[1] in KEYS else m[0], decode(prior))
    if table_rows(output) != {k:v[1] for k,v in KEYS.items()}:
        raise AssertionError("target strings did not round trip")
    bom = codecs.BOM_UTF16_LE if prior.startswith(codecs.BOM_UTF16_LE) else b""
    return bom + output.encode("utf-16-le"), report


def main():
    p = argparse.ArgumentParser(description=__doc__)
    for key in ("original", "prior", "out"):
        p.add_argument("--"+key, type=Path, required=True)
    a = p.parse_args()
    a.out = a.out.resolve()
    if a.out.is_relative_to(Path("D:/DNF/client/100US/DFO")):
        raise ValueError("diagnostic output cannot be placed in the live client")
    output, report = overlay(a.original.read_bytes(), a.prior.read_bytes())
    target = a.out / "replacement-tables" / "string" / "dnf.uv.str"
    target.parent.mkdir(parents=True)
    target.write_bytes(output)
    info = {"status":"offline_only", "game_table_verified":False,
            "table_sha256":hashlib.sha256(output).hexdigest(), "rows":report,
            "unbound_ui_labels":["All", "Rewards", "Cancel"]}
    (a.out/"quest-strings.json").write_text(json.dumps(info, ensure_ascii=False, indent=2)+"\n", encoding="utf-8")
    print(f"{len(report)} rows verified, {sum(r['changed'] for r in report)} actual edits")


if __name__ == "__main__":
    main()
