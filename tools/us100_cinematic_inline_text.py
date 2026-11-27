# -*- coding: utf-8 -*-
"""Join proven cinematic dialog with the existing inline UI localization plan."""
import argparse
from collections import Counter
import json
from pathlib import Path

from localize_us100_tables import NUMBER, approved
from us100_verify_inline import exports


ENGLISH = {
    "Zhang Fei, Great General of Shu": "蜀汉大将·张飞",
    "Cao Cao, Founder of Wei": "曹魏奠基者·曹操",
    "Lu Bu, God of War": "战神·吕布",
    "Lancelot": "兰斯洛特",
    "Wa ha ha ha!! I am Zhang Fei, courtesy name Yide. Who dares to challenge me?": (
        "哈哈哈哈！我乃张飞，字翼德。谁敢与我一战？"
    ),
    "Amazing... But this is as far as you go. What is it that makes you want to run towards your death?": (
        "了不起……可你也就到此为止了。到底是什么，让你非要来送死？"
    ),
    "You've chased me this far... impressive. But you can never defeat me. This place will be your grave!!": (
        "竟能一路追到这里……有点本事。但你绝不可能战胜我，这里就是你的葬身之地！"
    ),
    "Argh... It's like I've been hypnotized by something... Let's relay this information to big brother Liu Bei.": (
        "呃……我好像中了什么催眠术……快把这消息告诉刘备大哥。"
    ),
    "Thank you, adventurer. I am Zhao Yun, courtesy name Zilong. I will be escorting Liu Shan myself. "
    "Could you go in advance and secure the escape route? I will follow shortly.": (
        "多谢你，冒险家。我乃赵云，字子龙。我会亲自护送刘禅。"
        "劳烦你先行一步，确保撤离路线安全，我随后就到。"
    ),
    "You live up to your name King Arthur. But this is as far as you go. This is your last chance to stop.": (
        "不愧是亚瑟王。不过，你只能走到这里了。这是你收手的最后机会。"
    ),
    "I'll destroy Camelot and your world as well!!": "我要毁灭卡美洛，连同你的世界一起！",
    "shut up!": "闭嘴！",
    "stop": "住手！",
    "die": "去死！",
    "Watch out!": "小心！",
}
RECOVERED = {
    ("cp949", "나만 바라봐"): "只看着我。",
    ("cp949", "?斯洛特"): "兰斯洛特",
    ("gb18030", "吾乃燕人张翼德是也！ 谁敢与我决一死战？"): "吾乃燕人张翼德是也！谁敢与我决一死战？",
    ("gb18030", "尔等竟敢阻我统一大业， 该当何罪！"): "尔等竟敢阻我统一大业，该当何罪！",
}


def identify(source):
    if source in ENGLISH:
        return ENGLISH[source], "self_translated_english", source
    for (encoding, witness), zh in RECOVERED.items():
        if witness.encode(encoding).decode("cp437") == source:
            return zh, "recovered_" + encoding, witness
    return None, "", ""


def build(args):
    plan = json.loads(args.base_plan.read_text(encoding="utf-8"))
    baseline = json.loads(args.base_report.read_text(encoding="utf-8"))
    methods = Counter(baseline["methods"])
    cinema_cells = 0
    cinema_sources = exports(args.input_dir)
    with args.evidence.open("x", encoding="utf-8") as evidence:
        for path, filename in cinema_sources.items():
            tokens = json.loads(filename.read_text(encoding="utf-8"))
            counts = Counter((a["text"], b["text"]) for a, b in zip(tokens, tokens[1:])
                             if a["type"] == 3 and b["type"] == 6 and a.get("text") in ("[notice]", "[speech]"))
            if not counts or path in plan:
                raise ValueError(f"missing or duplicate cinematic text: {path}")
            rows = []
            for (field, source), count in sorted(counts.items()):
                target, how, witness = identify(source)
                if not approved(source, target) or NUMBER.findall(source) != NUMBER.findall(target):
                    raise ValueError(f"unverified cinematic text: {path}, {source!r}")
                rows.append({"field": field, "source": source, "target": target, "count": count})
                methods[how] += count
                cinema_cells += count
                evidence.write(json.dumps([path, field, count, how, source, witness, target], ensure_ascii=True) + "\n")
            plan[path] = rows
    runs = sum(row["count"] for rows in plan.values() for row in rows)
    summary = {"scripts": len(plan), "cells": runs, "translated": runs, "untranslated": 0,
               "wisdom_simulator_values": baseline["translated"], "cinematic_values": cinema_cells, "methods": methods}
    with args.output.open("x", encoding="utf-8") as out:
        json.dump(plan, out, ensure_ascii=True, indent=2)
    with args.report.open("x", encoding="utf-8") as out:
        json.dump(summary, out, indent=2)
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for key in ("input-dir", "base-plan", "base-report", "output", "report", "evidence"):
        parser.add_argument("--" + key, required=True, type=Path)
    build(parser.parse_args())
