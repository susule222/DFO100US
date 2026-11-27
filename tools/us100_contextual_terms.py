# -*- coding: utf-8 -*-
"""Reviewed translations whose meaning depends on the exact STR slot."""

SPIRIT_STAT_KEYS = {"340", "1282", "4056", "42011", "46770", "46774", "100008"}
CERA_LABELS = {"888", "889", "894", "901", "5484", "13149", "21096", "23068", "35048", "39038"}
DAYS = {
    "42592": ("SUN", "周日"), "42593": ("MON", "周一"), "42597": ("FRI", "周五"), "42598": ("SAT", "周六"),
    "46796": ("MON", "周一"), "46800": ("FRI", "周五"), "46801": ("SAT", "周六"), "46802": ("SUN", "周日"),
}
MONTH_LABELS = {
    str(91193 + i): (abbr, month + "月")
    for i, (abbr, month) in enumerate(zip(
        ("JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"),
        ("一", "二", "三", "四", "五", "六", "七", "八", "九", "十", "十一", "十二"),
    ))
}


def contextual_translation(table, key, value):
    if table == "string/dnf.uv.str":
        if value == "Spirit":
            if key in SPIRIT_STAT_KEYS:
                return "精神"
            if key == "300001843":
                return "精灵"
        if value == "CERA" and key in CERA_LABELS:
            return "CERA点券"
        if value == "Raid" and key in {"31143", "31529", "46926", "100002034", "51049"}:
            return "攻坚战"
        if value == "Char." and key in {"33203", "47737"}:
            return "角色"
        if value == "Fuse" and key in {"15106", "60907"}:
            return "融合"
        if key in DAYS and value == DAYS[key][0]:
            return DAYS[key][1]
        if key in MONTH_LABELS and value == MONTH_LABELS[key][0]:
            return MONTH_LABELS[key][1]
    if table == "string/etc.uv.str" and key == "log_id_8" and value == "Spirit":
        return "精神"
    if table == "string/etc.uv.str" and key in VALUE_GUIDES and VALUE_GUIDES[key][0] == value:
        return VALUE_GUIDES[key][1]
    if table == "string/ui.uv.str":
        if value == "Lv" and key in {"item_dictionary_03", "jumping_character_text_02", "spec_info_03"}:
            return "等级"
        if key == "surpriseshop_cera" and value == "CERA":
            return "CERA点券"
        if key == "univ_dfothello_mode_0" and value == "PVE":
            return "挑战模式"
        if key == "univ_dfothello_mode_1" and value == "PVP":
            return "对战模式"
        if key == "bookofgenesis_UI_07" and value == "PVP":
            return "玩家对战"
    return None


VALUE_GUIDES = {"guide_title_3": ("PVP", "玩家对战"), "guide_title_39": ("OTP", "动态密码认证"),
                "guide_book_00": ("UI", "界面")}
