"""Classify values that are intentionally identical in all client languages."""
import re


COMMAND = re.compile(r"/[A-Za-z][A-Za-z0-9_]*")
PASSIVE_SYMBOL = re.compile(r"(?:[A-Za-z\d]+_)+[A-Za-z\d]+")
SELF_KEY_CODE = re.compile(r"(?:[A-Za-z0-9]+_)+[A-Za-z0-9_.]+")
LOG_FIELD = re.compile(r"(?:Avatar_[A-Za-z]+_(?:Level|Socket[12])|CraftingTraining_[A-Za-z]+_Level|Equipitem_[A-Za-z]+_Enhancedlevel)")
LEGACY_CODE = re.compile(r"Legacy_(?:Sun|Moon)_[0-5]|basic_explain_500310172")
LEGACY_SLOT = re.compile(r"(?:name|basic_explain)_500310(?:17[2-9]|18[01]|19[01])")
NA_GROWTH_KEYS = {"growtype_name_238", "growtype_name_239", "growtype_name_319"}
NA_DNF_KEYS = {"7612", "19583", "19584", "19600", "20991", "20993",
               "31294", "42608", "48412", "48598", "48599"}
FONT_KEYS = {"5420"}
MODEL_CODES = {
    ("string/event.uv.str", "gunner_275_name", "MSC-7"),
    ("string/event.uv.str", "at_gunner_273_name", "FSC-7"),
    ("string/event.uv.str", "challenge_gunner_skill_2_1", "MSC-7"),
    ("string/monster.uv.str", "name_9225", "GT-9600"),
    ("string/monster.uv.str", "at_0", "GT-700"),
    ("string/monster.uv.str", "AT_5T_Name", "AT_5T"),
}
DIAGNOSTIC_CODES = {
    ("string/stackable.uv.str", "name_50004205", "LTapr_dummy"),
    ("string/passiveobject.uv.str", "PD_success_wanning_0", "PD_Success_Wanning"),
    ("string/monster.uv.str", "name_500000181", "Phase_Change_01"),
}
EXACT_SYMBOLS = {
    ("string/dnf.uv.str", "1521", "/\u3161"),
    ("string/dnf.uv.str", "13283", "/\u3161"),
    ("string/dnf.uv.str", "150030", "/\u3163"),
    ("string/dnf.uv.str", "29126", "\u318d"),
    ("string/dnf.uv.str", "13144", "ABCDEFGHIJKLMNOPQRSTUVWXYZ"),
    ("string/dnf.uv.str", "31607", "A|B|C|D|E|F|G|H|I|J|K|L|M|N|O|P|Q|R|S|T|U|V|W|XYZ"),
    ("string/dnf.uv.str", "21460", "Mjpeg"),
    ("string/dnf.uv.str", "22078", "DFOWebImg"),
    ("string/dnf.uv.str", "27258", "\\DFO"),
    ("string/dnf.uv.str", "44375", "JPEG (.jpg)"),
    ("string/dnf.uv.str", "44376", "TGA (.tga)"),
    ("string/dnf.uv.str", "44377", "PNG (.png)"),
    ("string/dnf.uv.str", "100005005", "[QA]"),
    ("string/equipment.uv.str", "name_105600229", "OR-DX"),
    ("string/ui.uv.str", "LiveStream_WebLink_01", "www.youtube.com/@dfoneopleglobal"),
    ("string/etc.uv.str", "premium_service_buff_02", "String" * 9),
    ("string/monster.uv.str", "monster_war_die_check_00", "monster_war_die_check"),
    ("string/monster.uv.str", "name_500000185", "Item_Drop_Monster"),
    ("string/passiveobject.uv.str", "Luke_light_dummy_Beckey_0", "Dummy_Becky"),
    ("string/passiveobject.uv.str", "Luke_light_dummy_Rosaura_0", "Dummy_Rosaura"),
    ("string/passiveobject.uv.str", "Moth_TOP_0", "Deschamps_Moth_Tower"),
    ("string/dnf.uv.str", "36027", "SKT"),
    ("string/dnf.uv.str", "36028", "KT"),
    ("string/dnf.uv.str", "36029", "LG U+"),
    ("string/ui.uv.str", "gm_uidebug_tool66", "XUI"),
    ("string/ui.uv.str", "minimap_npc", "NPC"),
    ("string/ui.uv.str", "event_143_target_channel_name", "CHANNEL_MT_RANGE_OF_EXILE"),
    ("string/ui.uv.str", "event_312_channel_name", "CHANNEL_NIGHT_OF_REVELATION"),
    ("string/stackable.uv.str", "name_2600051", "GSD"),
    ("string/passiveobject.uv.str", "Do_Chul_Body_L", "Taotie_Left_Right"),
    ("string/passiveobject.uv.str", "Do_Chul_Body_R", "Taotie_Right_Left"),
    ("string/passiveobject.uv.str", "Goong_Gi_Warning_L_0", "Qiongqi_Warning_Left"),
    ("string/passiveobject.uv.str", "Goong_Gi_Warning_R_0", "Qiongqi_Warning_Right"),
}
MAIL_HOSTS = {
    "38334": "naver.com", "38335": "hanmail.net", "38337": "nate.com", "38338": "lycos.co.kr",
    "38339": "yahoo.co.kr", "38340": "hotmail.com",
}
PROTO_LABELS = {
    "70156": "GAMBLE_RESULT_COMMON", "70157": "GAMBLE_RESULT_GOOD", "70164": "RAMEN_ITEM_OUT",
    "70165": "COKE_ITEM_OUT", "70166": "NAVATA_EMBLEM_OK", "70171": "FAVOR_LV_UP",
    "70172": "FAVOR_LV_DOWN", "70189": "R_ALL_KILL",
}
UI_SAMPLES = {
    "chatwindow_20": "ABCDEFABCDEF", "hc_threedays_event_77": "nn/nn x",
    "hc_FavoritePlayerListLine_5": "ABCDE", "hc_FavoritePlayerListLine_6": "ABCDEF",
    "hc_FavoritePlayerListLine_7": "ABCDEFG", "hc_PopupMenuGiftWindow_7": "ABCDEFG - ABCDEFG",
}
MJ_KEYS = {"Aradonald_Total_Title", "Aradonald_Daily_Title", "1st_DeveloperEvent_specialname_06",
           "name_500000068", "name_500000119", "field_name_500000119"}


def preservation_reason(table, key, value, korean_rows=None):
    if (table, key, value) in MODEL_CODES:
        return "language_neutral_model_designation"
    if (table, key, value) in DIAGNOSTIC_CODES:
        return "diagnostic_object_identifier"
    if (table, key, value) in EXACT_SYMBOLS:
        return "language_neutral_or_runtime_identifier"
    if table == "string/dnf.uv.str" and (MAIL_HOSTS.get(key) == value or PROTO_LABELS.get(key) == value):
        return "language_neutral_or_runtime_identifier"
    if table == "string/ui.uv.str" and UI_SAMPLES.get(key) == value:
        return "editor_placeholder"
    if (table == "string/event.uv.str" and key.startswith("1st_DeveloperEvent_name_")
            and re.fullmatch(r"[A-Za-z][A-Za-z0-9_ ]{1,20}", value)):
        return "original_contributor_nickname"
    if key in MJ_KEYS and value == "MJ":
        return "original_nickname_initials"
    if (table == "string/dnf.uv.str" and key.isascii() and key.isdigit() and 10016 <= int(key) <= 10030
            and value == chr(65 + int(key) - 10016) * 2):
        return "literal_alphabet_index"
    if (table == "string/ui.uv.str" and key.startswith("abb_dictionary_") and len(value) == 1
            and "\u3131" <= value <= "\u314e" and korean_rows is not None and korean_rows.get(key) == value):
        return "korean_dictionary_lookup_index"
    if table in ("string/dnf.uv.str", "string/chatemoticon.uv.str") and COMMAND.fullmatch(value):
        return "literal_chat_command"
    if table == "string/event.uv.str" and key.startswith("event_chatting_tab_shortcut_") and COMMAND.fullmatch(value):
        return "literal_chat_command"
    if table == "string/dnf.uv.str" and key in FONT_KEYS and value == "Dotum":
        return "installed_font_name"
    if table == "string/clientonly.uv.str" and key == "MainMenu_global_1" and value == "STEAM":
        return "platform_trademark"
    if table == "string/character.uv.str" and key in NA_GROWTH_KEYS and value == "NA":
        return "unused_growth_slot"
    if table == "string/passiveobject.uv.str" and key == value + "_0" and PASSIVE_SYMBOL.fullmatch(value):
        return "passiveobject_internal_name"
    if table == "string/monster.uv.str" and key == "Dummy_square_0" and value == "square_0\u786e\u8ba4\u8bc1\u636e":
        return "diagnostic_object_identifier"
    if table == "string/aicharacter.uv.str" and key == "grief_infighter2_9" and value == "<emphasis>#target_name!!":
        return "runtime_placeholder_only"
    if table == "string/etc.uv.str" and key.startswith("log_id_") and LOG_FIELD.fullmatch(value):
        return "log_schema_field_preserved"
    if (table == "string/equipment.uv.str" and LEGACY_SLOT.fullmatch(key) and LEGACY_CODE.fullmatch(value)
            and korean_rows is not None and korean_rows.get(key) == value):
        return "source_identifier_without_display_text"
    if (korean_rows is not None and table.startswith("string/") and (not korean_rows.get(key) or korean_rows[key] == value)):
        if value == key and SELF_KEY_CODE.fullmatch(value):
            return "source_key_placeholder_without_fallback"
        if table == "string/dnf.uv.str" and key in NA_DNF_KEYS and value == "NA":
            return "source_unavailable_placeholder"
    return ""
