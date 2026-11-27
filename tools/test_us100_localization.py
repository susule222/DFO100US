# -*- coding: utf-8 -*-
from pathlib import Path
import unittest

from extend_us100_localization import dictionary, reference_lookup
from localize_us100_tables import approved, exact_mechanical
from us100_avatar_names import AvatarNames
from us100_grouped_llm import candidate, prepare, quality, units
from us100_xui_text import display_pattern, extract_slots, legit, rewrite_reason, xml_skeleton
from us100_filter_model_overlay import problem, repair, restore_number_style
from us100_formula_text import translate_formula
from us100_local_llm import mask, unmask
from us100_calibrate_reference_numbers import adapt
from us100_rescue_llm import protect_numbers, restore_numeric_percents, revision_prompt, sentences, separate_controls, stitch_plan
from us100_contextual_terms import contextual_translation
from us100_preserve_text import preservation_reason
from us100_symbolic_fallback import recover
from us100_clause_tails import tails
from us100_numeric_roles import equivalent_numeric_roles


class LocalizationTests(unittest.TestCase):
    def test_reference_bridge_rejects_unmatched_ui_meaning(self):
        path = "string/ui.uv.str"
        key = "common_03"
        korean = {key: "취소"}
        chinese = {key: "取消"}
        ref = korean, chinese, dictionary(korean, chinese)
        self.assertEqual(reference_lookup(path, "Delete", key, korean, ref, ref, (korean, {key: "Cancel"})), [])

    def test_reference_bridge_deliberately_allows_exact_source_match(self):
        key = "name_001"
        korean = {key: "고대 망토"}
        chinese = {key: "古代披风"}
        ref = korean, chinese, dictionary(korean, chinese)
        matches = reference_lookup(
            "string/equipment.uv.str", "Ancient Cloak", key, {}, ({}, {}, {}), ref,
            (korean, {key: "Ancient Cloak"}),
        )
        self.assertIn(("古代披风", "cn115_us115_bilingual_bridge"), matches)

    def test_preserves_color_tokens_and_values(self):
        self.assertTrue(approved("@1b@Atk. +%d%%", "@1b@攻击力 +%d%%"))
        self.assertFalse(approved("@1b@Atk. +%d%%", "@1d@攻击力 +%d%%"))
        self.assertTrue(approved("Chases #target_name", "追击#target_name"))
        self.assertFalse(approved("Chases #target_name", "追击目标"))
        self.assertTrue(approved("%dD", "%d天"))
        self.assertFalse(approved("Test", "距离领奖还差%dLv。"))
        self.assertFalse(approved("Removed on %dy %dm %dt by %s.", "%s在%d年%d月%d日移除。"))
        self.assertTrue(approved("Removed on %dy %dm %dt by %s.", "于%d年%d月%d日由%s移除。"))
        self.assertFalse(exact_mechanical(
            "string/newrandomoption.uv.str", "P_1_basic_explain", "Atk. +12%%", "攻击力 +15%%",
        ))

    def test_avatar_name_requires_all_parts(self):
        names = AvatarNames(Path(__file__).with_name("us100_avatar_terms.txt"))
        self.assertEqual(names.translate("Hot Summer 2019 Surfing Board [Type C]"), "2019热辣夏日·冲浪板 [C型]")
        self.assertEqual(names.translate("Wrecking Ball's Short Ponytail [Type A]"), "狂暴者·短款马尾 [A型]")
        self.assertIsNone(names.translate("Hot Summer 2019 Unrecognized Part [Type C]"))

    def test_grouped_numeric_and_color_values_round_trip(self):
        source = r"@1b@Skill Atk. +12%%\nCooldown: 3 seconds"
        one, two = prepare("@1b@Skill Atk. +12%%"), prepare("Cooldown: 3 seconds")
        self.assertEqual(one[1], ("@1b@", "+12", "%%"))
        lookup = {one[0]: "<pv0>技能攻击力 <pv1><pv2>", two[0]: "冷却时间：<pv0>秒"}
        self.assertEqual(candidate(source, units(source, "equipment"), lookup),
                         r"@1b@技能攻击力 +12%%\n冷却时间：3秒")
        self.assertEqual(quality("Skill Atk. +12%%", "技能 +12%%"), "game_term_missing")
        self.assertEqual(quality("Weekly Entry Count: %d/%d", "每周登录次数：%d/%d"), "game_term_missing")

    def test_xui_text_attributes_exclude_control_and_reference(self):
        xml = ('<?xml version="1.0" encoding="UTF-8"?>'
               '<ROOT><CNButton ID="STR:CloseButton" ButtonName="STR:Close" Pos="VECTOR:1, 2" />'
               '<CNUIControlText string="STR:&lt;20::example&gt;" />'
               '<!-- <CNButton ButtonName="STR:Ignored" /> --></ROOT>')
        slots = list(extract_slots(xml))
        self.assertEqual([(s.control, s.value) for s in slots], [("STR:CloseButton", "Close"), ("", "<20::example>")])
        self.assertEqual(rewrite_reason(slots[1].value), "string_reference")
        self.assertEqual(rewrite_reason("<20:native_key>"), "string_reference")
        patched = xml[:slots[0].start] + "关闭" + xml[slots[0].stop:]
        self.assertEqual(xml_skeleton(xml)[0].attrib["ID"], xml_skeleton(patched)[0].attrib["ID"])
        self.assertEqual(rewrite_reason("NNNN / NNNN"), "editor_placeholder")
        date = "19 DEC ~ 23 JAN, until maintenance(UTC)"
        self.assertEqual(display_pattern(date), "十二月19日 ~ 一月23日，至维护前（UTC）")
        self.assertTrue(legit(date, display_pattern(date)))

    def test_semantic_cooldown_guard_and_targeted_repair(self):
        source = r"All skill cooldown -20%% for 10 min.\nShares cooldown with Mind Stimulant Potions."
        machine = r"所有技能冷却时间 -20%% 分钟，持续10分钟。\n使用精神刺激药剂可缩短冷却。"
        self.assertNotEqual(problem(source, machine), "")
        repaired = repair(source, machine)
        self.assertEqual(repaired, r"所有技能冷却时间 -20%%，持续10分钟。\n与精神刺激灵药共用冷却时间。")
        self.assertEqual(problem(source, repaired), "")
        self.assertEqual(repair("Skill Atk. +17%%", "技能攻击力提高17%%"), "技能攻击力 +17%%")
        self.assertEqual(repair("Skill Atk. -17%% after casting", "施放后技能攻击力提高17%%"),
                         "施放后技能攻击力提高17%%")
        self.assertEqual(restore_number_style("Consumes 1,000 Gold for 1.0 sec.", "消耗1000金币，持续1秒。"),
                         "消耗1,000金币，持续1.0秒。")
        self.assertEqual(restore_number_style("Consumes 1,000 Gold", "消耗100金币"), "消耗100金币")
        self.assertEqual(
            problem("Increases Dungeon Clear EXP by 3 times for 30 minutes.",
                    "在3分钟内增加30点地下城通关经验值。"),
            "duration_attached_to_wrong_value",
        )

    def test_formula_preserves_status_tolerance_and_radius(self):
        src = "Abnormal Status Tolerances -10 for every Reinforcement/Amplification level for all enemies in 300 px."
        target = translate_formula(src)
        self.assertIn("异常状态抗性 -10", target)
        self.assertIn("300像素", target)
        self.assertEqual(problem(src, target), "")
        src = r"@1b@Skill Atk. +1%% if your combined option levels are 240 or higher (#%d%%#)"
        self.assertEqual(problem(src, translate_formula(src)), "")

    def test_numbered_talisman_bullet_is_not_a_negative_modifier(self):
        source = r"Bonus Damage +17%%\n- 1st slot Talisman skill cooldown -30%% and Atk. +55%%"
        target = candidate(source, units(source, "equipment"), {})
        self.assertEqual(target, r"附加伤害 +17%%\n- 第1护石栏技能冷却时间 -30%%，技能攻击力 +55%%")
        self.assertEqual(problem(source, target), "")
        self.assertNotEqual(problem(source, target.replace("-30%%", "+30%%")), "")

    def test_residual_numeric_anchors_and_sentence_boundaries(self):
        source = "Lv. 15 - 70 Skill Atk. +25%%. Cooldown 5 sec."
        masked, values = protect_numbers(source)
        self.assertEqual(values, ["15 - 70", "+25%%", "5"])
        self.assertEqual(unmask(masked, values), source)
        self.assertIsNone(unmask("<pv1><pv0><pv2>", values))
        self.assertEqual(list(sentences(source)), ["Lv. 15 - 70 Skill Atk. +25%%.", " ", "Cooldown 5 sec."])
        plain, markers = mask(source, calendar=True, retain_numeric_percents=True)
        self.assertEqual((plain, markers), (source, []))
        self.assertEqual(restore_numeric_percents("Damage +25%%", "伤害 +25％"), "伤害 +25%%")
        self.assertEqual(list(separate_controls(r"Gain @1b@12%% damage for %d seconds")),
                         [("Gain ", False), ("@1b@", True), ("12%% damage for ", False),
                          ("%d", True), (" seconds", False)])
        title = list(stitch_plan("<Arcane Forest>", "dnf", {}, isolate=True))
        self.assertIn(("Arcane Forest", True), title)
        source = "Increases Damage +10%% for 30 seconds at the cost of 1 Clear Cube Fragment."
        clauses = list(tails(source))
        self.assertEqual("".join(piece[0] for piece in clauses), source)
        self.assertIn(("for 30 seconds", "，持续30秒"), clauses)
        self.assertIn(("at the cost of 1 Clear Cube Fragment", "，消耗1个无色小晶块"), clauses)

    def test_expiration_formula_checks_deletion_order_without_changing_date(self):
        source = "This item will be deleted at 6:00 a.m. on June 18, 2019."
        self.assertEqual(translate_formula(source), "此道具将于上午6:00（六月18日，2019年）删除。")
        self.assertEqual(problem(source, translate_formula(source)), "")

    def test_only_fully_bound_numeric_order_changes_are_accepted(self):
        source = "Automatically consumes 1 Cube Fragment every 30 seconds."
        result = "每30秒自动消耗1个小晶块。"
        self.assertTrue(equivalent_numeric_roles(source, result))
        self.assertFalse(equivalent_numeric_roles(source, "每1秒自动消耗30个小晶块。"))
        self.assertFalse(equivalent_numeric_roles(source, "每30秒自动消耗1个无色小晶块。"))
        self.assertTrue(exact_mechanical("string/equipment.uv.str", "basic_explain_05", source, result))
        self.assertFalse(equivalent_numeric_roles("Consumes %d Cube Fragments every 30 seconds.",
                                                  "每30秒消耗%d个小晶块。"))
        self.assertTrue(equivalent_numeric_roles(source + r"\nBonus Damage +12%%",
                                                 result + r"\n附加伤害 +12%%"))
        self.assertFalse(equivalent_numeric_roles(source + r"\nBonus Damage +12%%",
                                                  result + r"\n附加伤害 +15%%"))

    def test_revision_feedback_lists_original_number_and_sign_slots(self):
        original = "Attack Speed +12%% for 30 seconds."
        prompt = revision_prompt(original, original, "30%%的攻速", "number_or_sign_changed")
        self.assertIn("12、30", prompt)
        self.assertIn("+12", prompt)
        self.assertIsNone(revision_prompt(original, original, "ignored", "unchanged_text"))

    def test_contextual_terms_and_runtime_values(self):
        self.assertEqual(contextual_translation("string/dnf.uv.str", "340", "Spirit"), "精神")
        self.assertEqual(contextual_translation("string/dnf.uv.str", "300001843", "Spirit"), "精灵")
        self.assertIsNone(contextual_translation("string/dnf.uv.str", "3184", "Spirit"))
        self.assertEqual(preservation_reason("string/dnf.uv.str", "291", "/trade"), "literal_chat_command")
        self.assertEqual(preservation_reason("string/dnf.uv.str", "5420", "Dotum"), "installed_font_name")
        self.assertFalse(preservation_reason("string/dnf.uv.str", "1920", "NA"))

    def test_symbolic_item_description_uses_only_same_build_display_slot(self):
        key = "basic_explain_12345"
        known = {key: "공격하면 2%% 확률로 2초동안 적을 기절상태로 만듭니다."}
        self.assertEqual(recover("string/equipment.uv.str", key, key, known),
                         "攻击时，有2%%的概率使敌人进入持续2秒的眩晕状态。")
        self.assertIsNone(recover("string/equipment.uv.str", key, "Always Stuns enemies", known))
        self.assertIsNone(recover("string/stackable.uv.str", key, key, known))

    def test_localized_calendar_keeps_day_year_and_time(self):
        src = "Can trade until December 2, 2021 at 6:00 a.m."
        masked, tokens = mask(src, calendar=True)
        self.assertIn("<pv0>", masked)
        target = unmask("可交易至<pv0>上午6:00。", tokens)
        self.assertEqual(target, "可交易至十二月2日（2021年）上午6:00。")
        self.assertEqual(problem(src, target), "")
        src = r"<nar>The story began.\brOne day..."
        self.assertEqual(problem(src, r"<nar>故事开始了。\br有一天……"), "")
        self.assertNotEqual(problem(src, r"故事开始了。\n有一天……"), "")

    def test_numeric_calibration_requires_identical_korean_sentence_shape(self):
        src = "Increases Skill Atk. by 12%%"
        self.assertEqual(adapt(src, "스킬 공격력 12%% 증가", "스킬 공격력 10%% 증가", "技能攻击力提高10%%"),
                         "技能攻击力提高12%%")
        self.assertIsNone(adapt(src, "스킬 공격력 12%% 증가", "스킬 공격력 10%% 감소", "技能攻击力降低10%%"))
        src = "Restores 30 Fatigue. (Can be used once a day. This item does not share cooldown with other Fatigue Recovery Potions.)"
        self.assertIn("不与其他疲劳恢复药剂共用", translate_formula(src))
        self.assertEqual(problem(src, translate_formula(src)), "")


if __name__ == "__main__":
    unittest.main()
