# -*- coding: utf-8 -*-
"""Recover reviewed equipment display text from same-build Korean fallbacks."""
import re

from localize_us100_tables import KOREAN, NUMBER, approved
from us100_korean_items import RIDE, translate_extra


NAME_ALIAS = re.compile(r"set_name_\d+")
DISPLAY_KEY = re.compile(r"(?:basic_explain|detail_explain|explain|[a-z0-9_]*fullset[a-z0-9_]*|flavor_text|set_name|name)_\d+")
ITEM_TABLES = {"string/equipment.uv.str", "string/stackable.uv.str", "string/creature.uv.str"}
CLASSES = {
    "격투가": "格斗家", "귀검사": "鬼剑士", "마법사": "魔法师", "프리스트": "圣职者",
    "도적": "暗夜使者", "거너": "神枪手", "여성거너": "女神枪手",
}
NAMES = {"제국의 용사": "帝国勇士", "해변의 몸짱": "海滩健身高手", "5월의 신랑": "5月的新郎", "5월의 신부": "5月的新娘"}
EXACT = {
    "강화, 증폭, 제련, 해체, 선물, 창고 저장 불가능": "无法强化、增幅、锻造、分解、赠送或存入仓库。",
    "강화, 증폭, 재련, 해체, 선물, 창고 저장 불가능": "无法强化、增幅、锻造、分解、赠送或存入仓库。",
    "해체 불가능": "无法分解。",
    "세리아방의 한복 루나 상점에서 판매하는 무제한 변경권을 이용하여 무제한 무기로 변경할 수 있습니다.": (
        "可在赛丽亚房间里身穿韩服的露娜处购买永久变更券，将武器变更为永久武器。"
    ),
    "무제한 변경권으로 변환 시 장비의 강화, 증폭, 재련, 차원의 능력, 마법부여 효과, 장비 등급이 유지됩니다.": (
        "使用永久变更券时，保留装备的强化、增幅、锻造、异次元属性、附魔效果和品级。"
    ),
    "무제한으로 변경 시 계승이 가능한 장비로 변경됩니다.": "变更为永久装备后，可用于继承。",
    "능력치는 유지한 상태로, 장착중인 아바타의 디자인을 복사합니다. 복사된 디자인의 아바타가 인벤토리에 존재하는동안에는 해당 아바타의 디자인이 유지되며, 아바타가 없을 경우에는 재접속시 기본 디자인의 아바타로 돌아옵니다.": (
        "在保留属性的前提下复制当前穿戴的装扮外观。只要提供外观的装扮仍在物品栏中，"
        "复制的外观就会保留；装扮不在物品栏时，重新登录后恢复默认外观。"
    ),
    "< 로얄 패스 캐릭터 >": "< 皇家通行证角色 >",
    "마지막 내리찍을때 다크 썬더볼트 시전": "最后一次下砸时施放暗黑雷电。",
}
SPELLS = {
    "애시드 클라우드": "酸雨云", "메가 드릴": "冰霜钻孔车", "반중력 기동장치": "反重力装置",
    "변이 파리채": "变异苍蝇拍", "고출력 매직 미사일": "改良魔法星弹",
}
STATES = {
    "중독": "中毒", "기절": "眩晕", "구속": "束缚", "감전": "感电", "화상": "灼伤",
    "빙결": "冰冻", "석화": "石化", "둔화": "减速", "저주": "诅咒", "혼란": "混乱", "암흑": "失明",
}
NAME = re.compile(r"(.+?)\[([^\]]+)\]")
RELIC = re.compile(r"던전 입장시 유물의 축복으로 공격속도\s*(\d+%%), 캐스트속도\s*(\d+%%) 증가 합니다\.")
CRIT = re.compile(r"피격시 (\d+%%) 확률로 (\d+)초동안 자신의 크리티컬 확률 (\d+%%) 증가")
SPELL = re.compile(r"(.+?) (대성공률|성공률) (\+\d+(?:\.\d+)?%%)(?:, 성공률 (\+\d+(?:\.\d+)?%%))?")
REFLECT = re.compile(r"(근거리 )?피격\s*시 (\d+%%) 확률로 받은 데미지의 (\d+%%)를 적에게 되돌려줍니다\.")
STUN = re.compile(r"공격\s*(?:시|하면) (\d+%%) 확률로 (?:(\d+)초동안 )?적을 기절상태로 (?:만듦|만듭니다\.)")
CONDITIONAL = re.compile(
    r"(.+?) 상태의 적을 공격시 (\d+%%) 확률로 무색큐브조각을 (\d+)개 소비하여 적의 HP (\d+%%) 감소 "
    r"\(보스 적용 안됨\. 쿨타임(\d+)초\)"
)


def translate_line(source):
    if source in EXACT:
        return EXACT[source]
    if extra := translate_extra(source):
        return extra
    if (m := NAME.fullmatch(source)) and m[1] in NAMES and m[2] in CLASSES:
        return f"{NAMES[m[1]]}[{CLASSES[m[2]]}]"
    if m := RELIC.fullmatch(source):
        return f"进入地下城时获得传承的祝福，攻击速度增加{m[1]}，施放速度增加{m[2]}。"
    if m := CRIT.fullmatch(source):
        return f"被击时有{m[1]}的概率使自身暴击率提升，持续{m[2]}秒，提升幅度为{m[3]}。"
    if (m := SPELL.fullmatch(source)) and m[1] in SPELLS:
        label = "大成功率" if m[2] == "대성공률" else "成功率"
        return f"{SPELLS[m[1]]}{label} {m[3]}" + (f"，成功率 {m[4]}" if m[4] else "")
    if m := REFLECT.fullmatch(source):
        where = "受到近距离攻击" if m[1] else "被击"
        return f"{where}时，有{m[2]}的概率将所受伤害的{m[3]}反弹给敌人。"
    if m := STUN.fullmatch(source):
        time = f"持续{m[2]}秒的" if m[2] else ""
        return f"攻击时，有{m[1]}的概率使敌人进入{time}眩晕状态。"
    if (m := CONDITIONAL.fullmatch(source)) and m[1] in STATES:
        return (f"攻击{STATES[m[1]]}状态的敌人时，有{m[2]}的概率消耗{m[3]}个无色小晶块，"
                f"使敌人的HP减少{m[4]}。（对首领不生效。冷却时间：{m[5]}秒。）")
    return None


def recover(table, key, original, korean_rows):
    if (table in ("string/event.uv.str", "string/etc.uv.str") and original == key
            and re.fullmatch(r"ridable_english_name[1-5]", key)):
        witness = korean_rows.get(key)
        target = RIDE.get(witness)
        return target if target and approved(witness, target) and NUMBER.findall(witness) == NUMBER.findall(target) else None
    if table not in ITEM_TABLES or not DISPLAY_KEY.fullmatch(key):
        return None
    if table == "string/stackable.uv.str" and not key.startswith(("name_", "explain_", "flavor_text_")):
        return None
    if table == "string/creature.uv.str" and not key.startswith("name_"):
        return None
    if original != key and not (NAME_ALIAS.fullmatch(key) and NAME_ALIAS.fullmatch(original)):
        return None
    source = korean_rows.get(key)
    if not source or not KOREAN.search(source):
        return None
    translated = [translate_line(line.strip()) for line in source.split(r"\n")]
    if any(part is None for part in translated):
        return None
    target = r"\n".join(translated)
    if not approved(source, target) or NUMBER.findall(source) != NUMBER.findall(target):
        return None
    return target
