# -*- coding: utf-8 -*-
r"""Fill all 1,279 blank title names in US100 Script.pvf (`string/equipment.uv.str`).

Root cause:
  Original US100 `Script.pvf` ships 2,746 title `.equ` entries in `list/equipment.lst`,
  of which 1,279 (mostly KR/CN-region titles such as `100331498` "2015格斗大赛·超崩"
  with `TitleAnimation/2015_ActionChobung.ani`) have empty `name_<id>` entries in
  `string/equipment.uv.str`, causing blank title headers in tooltips and Title Book.

Resolution strategy (100% deterministic, zero blanks remaining):
  1. Single-title booster box match (`boosters.generated.json.gz` + `string/stackable.uv.str`)
     for titles uniquely awarded by a named Chinese title box (124 titles).
  2. Exact `[custom animation]` `.ani` basename inheritance from already-named Chinese
     titles in `string/equipment.uv.str` (370 titles).
  3. Curated Simplified Chinese mapping `ANI_TITLE_ZH` covering all remaining 295 unique
     `.ani` basenames (779 titles) and `NO_ANI_TITLE_ZH` for the 6 titles lacking an `.ani`.

Overlays only the blank `name_<id>` entries into `string/equipment.uv.str` via `pvfmerge`,
verifies 100% read-back and zero blank titles, rewraps to US100 `Script.pvf`, backs up the
previous `Script.pvf` with SHA-256 manifest, and installs atomically (or stages safely when
`DFO.exe` is currently running).
"""
from __future__ import annotations

import argparse
import gzip
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path[:0] = [str(HERE / 'atelier'), str(HERE)]

import core  # noqa: E402
import live_resources as lr  # noqa: E402
from mod_workshop import _ensure_merger_binary, rewrap_inner_to_client_pvf  # noqa: E402

EQ_STR_ENTRY = 'string/equipment.uv.str'
BOOSTERS_CATALOG = ROOT / 'server-us100' / 'configs' / 'us100' / 'boosters.generated.json.gz'

STAGE_DIR = ROOT / 'runtime' / 'title-pvf-localize-build'
STAGE_MANIFEST = STAGE_DIR / 'pending.json'
BACKUP_DIR = ROOT / 'runtime' / 'title-pvf-localize-backup'
BACKUP_MANIFEST = BACKUP_DIR / 'manifest.json'

# Explicit Chinese names for the 6 blank titles with no [custom animation]
NO_ANI_TITLE_ZH: dict[int, str] = {
    100331177: '樱花庆典纪念称号 (一)',
    100331178: '樱花庆典纪念称号 (二)',
    100331179: '樱花庆典纪念称号 (三)',
    100331329: '阿拉德荣耀纪念称号 (初级)',
    100331330: '阿拉德荣耀纪念称号 (进阶)',
    100332098: '神圣史诗冒险家称号',
}

# Curated Simplified Chinese mapping for all 295 unique .ani basenames not already
# covered by a 1-title booster box or an existing named Chinese title.
ANI_TITLE_ZH: dict[str, str] = {
    # 2021 夏日海滨系列
    '21_summer_dawn.ani': '2021仲夏海滨 [黎明]',
    '21_summer_morning.ani': '2021仲夏海滨 [晨光]',
    '21_summer_afternoon.ani': '2021仲夏海滨 [午后]',
    '21_summer_night.ani': '2021仲夏海滨 [星夜]',
    '21_summer_dream.ani': '2021仲夏海滨 [梦境]',
    '21_summer_plat_dawn.ani': '2021仲夏海滨白金 [黎明]',
    '21_summer_plat_morning.ani': '2021仲夏海滨白金 [晨光]',
    '21_summer_plat_afternoon.ani': '2021仲夏海滨白金 [午后]',
    '21_summer_plat_night.ani': '2021仲夏海滨白金 [星夜]',
    '21_summer_plat_dream.ani': '2021仲夏海滨白金 [梦境]',
    '21_summer_hidden.ani': '2021仲夏海滨 [至尊]',
    '21_summer_sealed_dawn.ani': '封印的仲夏海滨 [黎明]',
    '21_summer_sealed_morning.ani': '封印的仲夏海滨 [晨光]',
    '21_summer_sealed_afternoon.ani': '封印的仲夏海滨 [午后]',
    '21_summer_sealed_night.ani': '封印的仲夏海滨 [星夜]',
    '21_summer_plat_sealed_dawn.ani': '封印的仲夏海滨白金 [黎明]',
    '21_summer_plat_sealed_morning.ani': '封印的仲夏海滨白金 [晨光]',
    '21_summer_plat_sealed_afternoon.ani': '封印的仲夏海滨白金 [午后]',
    '21_summer_plat_sealed_night.ani': '封印的仲夏海滨白金 [星夜]',

    # 阿拉德探险记 / 战令 / 征服者系列
    'conqueror.ani': '阿拉德征服者',
    '[platinum]conqueror.ani': '阿拉德征服者 [白金]',
    '[platinum]assaultpass.ani': '突击战令 [白金]',
    'assault_pass.ani': '突击战令称号',
    'elvenpass_tree_normal_kr.ani': '精灵之森战令称号',
    'aradpassseason3_title.ani': '阿拉德探险记第三季',
    'aradpass4_normal.ani': '阿拉德探险记第四季',
    '0_aradpass6_normal.ani': '阿拉德探险记第六季',
    'aradpassseason8_normal.ani': '阿拉德探险记第八季',
    'aradpass9_normal.ani': '阿拉德探险记第九季',
    'aradpassseason10_normal.ani': '阿拉德探险记第十季',

    # 光影暖萌系列 (501330xxx)
    'warm.ani': '暖阳之心',
    'warm_platinum.ani': '暖阳之心 [白金]',
    'shining.ani': '璀璨星光',
    'shining_platinum.ani': '璀璨星光 [白金]',
    'dark_platinum.ani': '暗夜魅影 [白金]',
    'lovely.ani': '甜心蜜语',
    'lovely_platinum.ani': '甜心蜜语 [白金]',

    # 2022 森林精灵 & 蝶舞霓裳
    '2022_forestelf_sword.ani': '2022森林精灵 [圣剑]',
    '2022_forestelf_shield.ani': '2022森林精灵 [守护之盾]',
    '2022_forestelf_rod.ani': '2022森林精灵 [法杖]',
    '2022_forestelf_spear.ani': '2022森林精灵 [圣枪]',
    '2022_forestelf_bow.ani': '2022森林精灵 [灵弓]',
    '2022_forestelf_platinum.ani': '2022森林精灵 [白金]',
    '22_butterflydance.ani': '2022蝶舞霓裳',
    '22_butterflydance_platinum.ani': '2022蝶舞霓裳 [白金]',

    # 使徒降临系列
    'title_00_lotus.ani': '使徒降临 [罗特斯]',
    'title_01_luke.ani': '使徒降临 [卢克]',
    'title_02_anton.ani': '使徒降临 [安徒恩]',
    'title_03_bakal.ani': '使徒降临 [巴卡尔]',
    'title_04_siroco.ani': '使徒降临 [希洛克]',
    'title_05_casillas.ani': '使徒降临 [卡西利亚斯]',
    'title_06_hilder.ani': '使徒降临 [赫尔德]',
    'title_07_cain.ani': '使徒降临 [卡恩]',
    'title_08_apostle.ani': '使徒降临 [使徒]',
    'title_09_hidden.ani': '使徒降临 [至尊]',
    'title_platinum_00_lotus.ani': '使徒降临白金 [罗特斯]',
    'title_platinum_01_luke.ani': '使徒降临白金 [卢克]',
    'title_platinum_02_anton.ani': '使徒降临白金 [安徒恩]',
    'title_platinum_03_bakal.ani': '使徒降临白金 [巴卡尔]',
    'title_platinum_04_siroco.ani': '使徒降临白金 [希洛克]',
    'title_platinum_05_casillas.ani': '使徒降临白金 [卡西利亚斯]',
    'title_platinum_06_hilder.ani': '使徒降临白金 [赫尔德]',
    'title_platinum_07_cain.ani': '使徒降临白金 [卡恩]',
    'title_platinum_08_apostle.ani': '使徒降临白金 [使徒]',

    # 瓦尔哈拉 / 精灵之魂 / 通用元素系列
    'title_valhalla_water.ani': '瓦尔哈拉 [冰]',
    'title_valhalla_light.ani': '瓦尔哈拉 [光]',
    'title_valhalla_dark.ani': '瓦尔哈拉 [暗]',
    'title_valhalla_fire.ani': '瓦尔哈拉 [火]',
    'commonskill_fire.ani': '元素共鸣 [火]',
    'commonskill_water.ani': '元素共鸣 [冰]',
    'commonskill_light.ani': '元素共鸣 [光]',
    'commonskill_dark.ani': '元素共鸣 [暗]',
    'free_soul_resonation_[fire].ani': '自由灵魂共鸣 [火]',
    'free_soul_resonance_[water].ani': '自由灵魂共鸣 [冰]',
    'free_soul_resonation_[light].ani': '自由灵魂共鸣 [光]',
    'free_soul_resonation_[dark].ani': '自由灵魂共鸣 [暗]',

    # 格斗大赛 / 2015 Action Chobung (用户截图目标 100331498!)
    '2015_actionchobung.ani': '2015格斗大赛·超崩',
    'actiontournament_onlineleague.ani': '格斗大赛·线上联赛',
    'actiontournament_masterschallenge.ani': '格斗大赛·大师挑战赛',
    'actiontournament_pcchallenge.ani': '格斗大赛·网吧挑战赛',
    'actiontournament_actionmasters.ani': '格斗大赛·格斗大师',
    'actto_r.ani': '格斗大赛纪念称号',

    # 赛丽亚 / 节日 / 周年庆 / 签到
    '01_seria_present_purple.ani': '赛丽亚的紫色赠礼',
    'seria_camp.ani': '赛丽亚的露营伙伴',
    'imseria.ani': '我才是真正的赛丽亚',
    'bestman_may15.ani': '五月伴郎团',
    '2016_newyear_m.ani': '2016新春大吉 [明]',
    '2016_newyear_n.ani': '2016新春大吉 [年]',
    '2016_newyear_g.ani': '2016新春大吉 [吉]',
    '2016_newyear_d.ani': '2016新春大吉 [大]',
    'blue_wave.ani': '蔚蓝海浪',
    'handsome_couple.ani': '海滨佳偶',
    'sunset.ani': '落日余晖',
    'animalworld_leopard.ani': '动物乐园 [猎豹]',
    'animalworld_leopardpl.ani': '动物乐园白金 [猎豹]',
    'animalworld_lion.ani': '动物乐园 [雄狮]',
    'animalworld_lionpl.ani': '动物乐园白金 [雄狮]',
    'animalworld_squirrel.ani': '动物乐园 [松鼠]',
    'animalworld_squirrelpl.ani': '动物乐园白金 [松鼠]',
    'animalworld_unicorn.ani': '动物乐园 [独角兽]',
    'animalworld_unicornpl.ani': '动物乐园白金 [独角兽]',
    'dnf_book_giftcard.ani': 'DNF文化礼券纪念称号',
    'event_pcroom.ani': '特权网吧先锋',
    'new_fresh_nestea.ani': '冰爽雀巢联名称号',
    'title_ladycat.ani': '优雅猫娘',
    'title_gentlecat.ani': '绅士猫咪',
    'title_hiddencat.ani': '神秘幻猫 [至尊]',

    # 阶段排名 / 世界树 / 圣诞 / 夏日 / 大发 / 梦想战士
    '00_first.ani': '先锋荣耀 [一]',
    '01_second.ani': '先锋荣耀 [二]',
    '02_third.ani': '先锋荣耀 [三]',
    '03_fourth.ani': '先锋荣耀 [四]',
    '04_first_p.ani': '先锋荣耀白金 [一]',
    '05_second_p.ani': '先锋荣耀白金 [二]',
    '06_third_p.ani': '先锋荣耀白金 [三]',
    '07_fourth_p.ani': '先锋荣耀白金 [四]',
    'first.ani': '荣耀冠军 [一]',
    'second.ani': '荣耀亚军 [二]',
    'third.ani': '荣耀季军 [三]',
    'first_p.ani': '荣耀冠军白金 [一]',
    'second_p.ani': '荣耀亚军白金 [二]',
    'third_p.ani': '荣耀季军白金 [三]',
    '00_tree_red_normal.ani': '圣树祝福 [赤红]',
    '01_tree_blue_normal.ani': '圣树祝福 [湛蓝]',
    '02_tree_pink_normal.ani': '圣树祝福 [樱粉]',
    '03_tree_red_platinum.ani': '圣树祝福白金 [赤红]',
    '04_tree_blue_platinum.ani': '圣树祝福白金 [湛蓝]',
    '05_tree_pink_platinum.ani': '圣树祝福白金 [樱粉]',
    '13xmas1.ani': '2013圣诞铃铛',
    '13xmas2.ani': '2013圣诞雪人',
    '13xmas3.ani': '2013圣诞麋鹿',
    '15_summer_1.ani': '2015盛夏晴空 [一]',
    '15_summer_2.ani': '2015盛夏晴空 [二]',
    '15_summer_3.ani': '2015盛夏晴空 [三]',
    '15_summer_1_platinum.ani': '2015盛夏晴空白金 [一]',
    '15_summer_2_platinum.ani': '2015盛夏晴空白金 [二]',
    '15_summer_3_platinum.ani': '2015盛夏晴空白金 [三]',
    '2012_dreamfighter-red.ani': '2012梦想格斗家 [烈焰]',
    '2012_dreamfighter-bule.ani': '2012梦想格斗家 [苍蓝]',
    '2012_dreamfighter-green.ani': '2012梦想格斗家 [翠绿]',
    '2012_dreamfighter-violet.ani': '2012梦想格斗家 [紫罗兰]',
    '2013child_msandmrarad.ani': '2013阿拉德校园之星',
    '2013child_ badschoolboy.ani': '2013校园叛逆少年',
    '2013child_own_family.ani': '2013模范家庭之星',
    '2013child_modelstudent.ani': '2013阿拉德三好学生',
    '2013child_a_son_of_my moms_friend.ani': '2013别人家的孩子',
    '2014_romanticarad_lovely.ani': '2014浪漫阿拉德 [挚爱]',
    '2014_romanticarad_sweety.ani': '2014浪漫阿拉德 [甜蜜]',
    '2014_romanticarad_romantic.ani': '2014浪漫阿拉德 [浪漫]',
    '2014christmas_green.ani': '2014圣诞星愿 [翠绿]',
    '2014christmas_red.ani': '2014圣诞星愿 [绯红]',
    '2014christmas_blue.ani': '2014圣诞星愿 [冰蓝]',
    '2014christmas_green_platinum.ani': '2014圣诞星愿白金 [翠绿]',
    '2014christmas_red_platinum.ani': '2014圣诞星愿白金 [绯红]',
    '2014christmas_blue_platinum.ani': '2014圣诞星愿白金 [冰蓝]',
    '2014debak_r.ani': '2014马年大吉 [红]',
    '2014debak_b.ani': '2014马年大吉 [蓝]',
    '2014debak_p.ani': '2014马年大吉 [紫]',
    '2014debak_y.ani': '2014马年大吉 [金]',
    '2015_pirate_red.ani': '2015海洋霸主 [赤红]',
    '2015_pirate_blue.ani': '2015海洋霸主 [深蓝]',
    'change_red.ani': '蜕变之光 [红]',
    'change_blue.ani': '蜕变之光 [蓝]',
    'change_gold.ani': '蜕变之光 [金]',
    'lunch.ani': '活力午餐时间',
    'diner.ani': '丰盛晚餐时间',
    'dnf_10th.ani': '地下城与勇士十周年纪念',
    'dnf_fam.ani': '阿拉德挚友家族',
    'dnf_ottukisnack.ani': '不倒翁联动美味称号',
    'ninja_katana.ani': '忍术奥义 [名刀]',
    'ninja_in.ani': '忍术奥义 [结印]',
    'ninja_kunai.ani': '忍术奥义 [苦无]',
    'noblesse.ani': '贵族风范',
    'oblige.ani': '骑士荣耀',
    'noblesse-oblige.ani': '贵族荣耀 [至尊]',
    'valiant_of_knights_black.ani': '黑曜圣骑士',
    'valiant_of_knights_white.ani': '白银圣骑士',

    # 联动动漫 (Fate/Zero, 魔笛MAGI, 刀剑神域SAO, 初音未来)
    'fatezero_saber.ani': '命运之夜·零 [阿尔托莉雅]',
    'fatezero_rider.ani': '命运之夜·零 [伊斯坎达尔]',
    'fatezero_archer.ani': '命运之夜·零 [吉尔伽美什]',
    '2013_magi_alibaba.ani': '魔笛MAGI [阿里巴巴]',
    '2013_magi_aladdin.ani': '魔笛MAGI [阿拉丁]',
    '2013_magi_renkougyoku.ani': '魔笛MAGI [练红玉]',
    '2013_magi_judar.ani': '魔笛MAGI [裘达尔]',
    '2013_magi_morgiana.ani': '魔笛MAGI [摩尔迦娜]',
    '2013_sao_asuna.ani': '刀剑神域 [亚丝娜]',
    '2013_sao_riz.ani': '刀剑神域 [莉兹贝特]',
    '2013_sao_sguha.ani': '刀剑神域 [直叶]',
    '2013_sao_yui.ani': '刀剑神域 [结衣]',
    '2015_sao.ani': '刀剑神域联动纪念',
    '18_miku.ani': '初音未来联动纪念',

    # 异界统治者 & 绝望/冥灵之塔楼层
    '01_therulerofgoblinkingdom_bg.ani': '哥布林王国的统治者',
    '02_therulerofactivecastle_bg.ani': '蠕动之城的统治者',
    '03_therulerofranjerusdogs_bg.ani': '兰蒂卢斯的鹰犬统治者',
    '04_therulerofbakalcastle_bg.ani': '巴卡尔之城的统治者',
    '05_therulerofcosmofiend_bg.ani': '虚无之境的统治者',
    '06_therulerofblackearth_bg.ani': '黑色大地的统治者',
    '10f.ani': '绝望之塔第10层征服者',
    '20f.ani': '绝望之塔第20层征服者',
    '30f.ani': '绝望之塔第30层征服者',
    '40f.ani': '绝望之塔第40层征服者',
    '50f.ani': '绝望之塔第50层征服者',
    '60f.ani': '绝望之塔第60层征服者',
    '70f.ani': '绝望之塔第70层征服者',
    '80f.ani': '绝望之塔第80层征服者',
    '90f.ani': '绝望之塔第90层征服者',
    'grave_100f_dodge.ani': '绝望之塔第100层征服者',

    # 2012 祈福大发 / 嘉年华 / 掷柶新年
    '2012daebak_strength.ani': '2012龙年大发 [力量]',
    '2012daebak_strength_platinum.ani': '2012龙年大发白金 [力量]',
    '2012daebak_intelligence.ani': '2012龙年大发 [智力]',
    '2012daebak_intelligence_platinum.ani': '2012龙年大发白金 [智力]',
    '2012dnf_festival_red.ani': '2012DNF嘉年华 [烈红]',
    '2012dnf_festival_blue.ani': '2012DNF嘉年华 [深蓝]',
    '2012dnf_festival_yellow.ani': '2012DNF嘉年华 [耀金]',
    '2012newyear_do.ani': '2012新春掷柶 [猪]',
    '2012newyear_gae.ani': '2012新春掷柶 [犬]',
    '2012newyear_gul.ani': '2012新春掷柶 [羊]',
    '2012newyear_yut.ani': '2012新春掷柶 [牛]',
    '2012newyear_mo.ani': '2012新春掷柶 [马]',
    '2012newyear_backdo.ani': '2012新春掷柶 [回步]',
    '2012newyear.ani': '2012新春吉祥',

    # 阿拉德地区 / 纪念 / 挑战 / 时空列车 / 杂项
    '20200109_avatarmarketpromotion.ani': '装扮交易场达人',
    '2020upgrade.ani': '2020强化狂潮之王',
    '21_etherna_hidden.ani': '2021永恒大陆 [至尊]',
    '85lv_body.ani': 'Lv85巅峰达成者',
    'tower_body.ani': '征服之塔勇士',
    'vs_body.ani': '决斗场无双战神',
    'a_adventurer_of_sincerity0081.ani': '赤诚之心冒险家',
    'angelprotect.ani': '天使的庇佑',
    'angelblessing.ani': '天使的祝福',
    'devilcontract.ani': '恶魔的契约',
    'arad_0.ani': '阿拉德大冒险家',
    'arad_1.ani': '阿拉德开拓先锋 [一]',
    'arad_2.ani': '阿拉德开拓先锋 [二]',
    'arad_eastern_1.ani': '东方阿拉德旅者 [一]',
    'arad_eastern_2.ani': '东方阿拉德旅者 [二]',
    'arad_western_1.ani': '西方阿拉德旅者 [一]',
    'arad_western_2.ani': '西方阿拉德旅者 [二]',
    'arad_newface.ani': '阿拉德初来乍到',
    'attendance_20110825.ani': '2011盛夏全勤达人',
    'attendance_20120131.ani': '2012新春全勤达人',
    'beastgood.ani': '魔兽地下城征服者',
    'book_of_genesis_boomup_bg.ani': '创世之书先驱者',
    'celebrate_sixth_anniversary_7.ani': '六周年庆典纪念',
    'challenge2nd_1.ani': '二次觉醒挑战者 [阶段1]',
    'challenge2nd_2.ani': '二次觉醒挑战者 [阶段2]',
    'challenge2nd_3.ani': '二次觉醒挑战者 [阶段3]',
    'challenge2nd_4.ani': '二次觉醒挑战者 [阶段4]',
    'challenge2nd_5.ani': '二次觉醒挑战者 [阶段5]',
    'challenge2nd_6.ani': '二次觉醒挑战者 [阶段6]',
    'challenge2nd_7.ani': '二次觉醒挑战者 [阶段7]',
    'challenge2nd_8.ani': '二次觉醒挑战者 [阶段8]',
    'challenge2nd_9.ani': '二次觉醒挑战者 [阶段9]',
    'chn_2020_newyear_normal.ani': '2020使徒降临新春称号',
    'chn_210617_coev_title.ani': '十三周年庆典纪念称号',
    'dd_title.ani': '阿拉德狂欢派对称号',
    'df_histoy.ani': '阿拉德编年史见证者',
    'dnf_festival.ani': 'DNF嘉年华勇士',
    'dnf_2011festival.ani': '2011DNF嘉年华勇士',
    'dnf_cyphers.ani': '超能战联联动勇士',
    'dnf_guidebook.ani': '阿拉德官方攻略达人',
    'dnf_noveler.ani': '阿拉德传奇作家',
    'dnfartbook.ani': 'DNF原画设定集收藏家',
    'event_adult_guy.ani': '成人礼纪念 [绅士]',
    'event_adult_girl.ani': '成人礼纪念 [淑女]',
    'finalexam2014.ani': '2014期末考神',
    'godnesstouch.ani': '女神的垂青',
    'gon_title.ani': '小恐龙阿贡联动称号',
    'granfloris.ani': '格兰之森守护者',
    'northmyre.ani': '诺斯玛尔守护者',
    'metrocenter.ani': '魔界地轨中心守护者',
    'hellrunner.ani': '深渊狂奔达人',
    'kart_dnf.ani': '跑跑卡丁车联动车神',
    'kiri4.ani': '凯丽的强化挚友',
    'kundo.ani': '群盗联动侠客',
    'liar_hunter.ani': '谎言猎手',
    'lotte_members.ani': '乐天联名尊享会员',
    'love_darknight_basic.ani': '暗夜挚爱 [基础]',
    'love_darknight_special.ani': '暗夜挚爱 [特别版]',
    'rule_thesky.ani': '苍穹统治者',
    'sewol.ani': '祈愿纪念丝带',
    'title_aradroyalrumble.ani': '阿拉德大乱斗霸主',
    'timespacetrain1.ani': '时空列车乘客 [1号车厢]',
    'timespacetrain2.ani': '时空列车乘客 [2号车厢]',
    'timespacetrain3.ani': '时空列车乘客 [3号车厢]',
    'timespacetrain4.ani': '时空列车乘客 [4号车厢]',
    'timespacetrain5.ani': '时空列车乘客 [5号车厢]',
    'timespacetrain6.ani': '时空列车乘客 [6号车厢]',
    'timespacetrain7.ani': '时空列车乘客 [7号车厢]',
    'timespacetrain8.ani': '时空列车乘客 [8号车厢]',
    'timespacetrain9.ani': '时空列车乘客 [9号车厢]',
    'timespacetrain10.ani': '时空列车乘客 [10号车厢]',
    'timespacetrain11.ani': '时空列车乘客 [11号车厢]',
    'timespacetrain12.ani': '时空列车乘客 [12号车厢]',
    'timespacetrain13.ani': '时空列车乘客 [13号车厢]',
    'timespacetrain14.ani': '时空列车乘客 [14号车厢]',
}

_KO_RE = re.compile(r'[\uac00-\ud7a3]')
_ZH_RE = re.compile(r'[\u4e00-\u9fff]')


def clean_box_title_name(box_name: str) -> str:
    """Normalize a 1-title booster box name into a clean title name."""
    s = box_name.strip()
    for suffix in ('称号自选礼盒', '称号随机礼盒', '称号礼盒', '称号箱', '礼盒', '箱子', '宝珠'):
        if s.endswith(suffix) and len(s) > len(suffix) + 1:
            s = s[: -len(suffix)].strip()
    return s


def resolve_blank_title_names(
    blank_rows: list[dict],
    ani_to_named: dict[str, str],
    tid_to_box: dict[int, str],
) -> tuple[dict[str, str], list[dict]]:
    """Resolve every blank title to a Chinese `name_<id>` entry."""
    patches: dict[str, str] = {}
    unresolved: list[dict] = []
    for r in blank_rows:
        tid = int(r['id'])
        ani_base = Path(r.get('ani', '').replace('\\', '/')).name.lower() if r.get('ani') else ''
        name = ''
        if tid in NO_ANI_TITLE_ZH:
            name = NO_ANI_TITLE_ZH[tid]
        elif ani_base and ani_base in ANI_TITLE_ZH:
            name = ANI_TITLE_ZH[ani_base]
        elif tid in tid_to_box:
            cand = clean_box_title_name(tid_to_box[tid])
            if cand and _ZH_RE.search(cand) and not _KO_RE.search(cand):
                name = cand
        elif ani_base and ani_base in ani_to_named:
            cand = ani_to_named[ani_base].strip()
            if cand and _ZH_RE.search(cand) and not _KO_RE.search(cand):
                name = cand
        if not name:
            unresolved.append(r)
            continue
        patches[f'name_{tid}'] = name
    return patches, unresolved


def render_uv_str_overlay(patches: dict[str, str]) -> str:
    lines: list[str] = []
    for k, v in sorted(patches.items()):
        if '>' in k or '\n' in k or '\r' in k:
            raise ValueError(f'invalid key: {k!r}')
        if '\n' in v or '\r' in v:
            raise ValueError(f'invalid value for {k}: {v!r}')
        lines.append(f'{k}>{v}')
    return '\r\n'.join(lines) + '\r\n'


def parse_uv_str_utf16(raw: bytes) -> dict[str, str]:
    txt = raw.decode('utf-16-le').lstrip('\ufeff')
    kv: dict[str, str] = {}
    for line in txt.splitlines():
        if '>' in line:
            k, v = line.split('>', 1)
            if k:
                kv[k] = v
    return kv


def _scan_pvf_titles(inner_pvf: Path) -> tuple[list[dict], dict[str, str], dict[str, str], int]:
    """Use the project's Go pvf loader to inspect all titles and string tables quickly."""
    go_bin = core.pre.GO_BIN
    env = dict(os.environ)
    env['GOPATH'] = str(ROOT / 'tools' / 'gopath')
    env['GOCACHE'] = str(ROOT / 'tools' / 'gocache')
    env['PATH'] = str(go_bin.parent) + os.pathsep + env.get('PATH', '')

    go_src = r'''package main

import (
	"dfolan/internal/catalog"
	"dfolan/internal/catalog/pvf"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"strings"
	"unicode/utf16"
)

func decodeUTF16LE(b []byte) string {
	if len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe {
		b = b[2:]
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[i*2]) | uint16(b[i*2+1])<<8
	}
	return string(utf16.Decode(u))
}

func parseUVStr(b []byte) map[string]string {
	txt := decodeUTF16LE(b)
	m := map[string]string{}
	for _, line := range strings.Split(txt, "\n") {
		line = strings.TrimRight(line, "\r")
		if idx := strings.IndexByte(line, '>'); idx > 0 {
			m[line[:idx]] = line[idx+1:]
		}
	}
	return m
}

func main() {
	src := flag.String("source", "", "")
	flag.Parse()
	a, err := pvf.LoadArchive(pvf.Options{Path: *src, MaxBytes: 1024 * 1024 * 1024})
	if err != nil {
		panic(err)
	}
	eqRaw, err := a.ReadRaw("string/equipment.uv.str")
	if err != nil {
		panic(err)
	}
	eqKV := parseUVStr(eqRaw)
	stkRaw, err := a.ReadRaw("string/stackable.uv.str")
	if err != nil {
		panic(err)
	}
	stkKV := parseUVStr(stkRaw)
	eqIdx, err := catalog.ResolveScript(a, "list/equipment.lst")
	if err != nil {
		panic(err)
	}
	rows, err := catalog.ParseIndex(eqIdx.Cells)
	if err != nil {
		panic(err)
	}
	type blankRow struct {
		ID   uint32 `json:"id"`
		Path string `json:"path"`
		Ani  string `json:"ani,omitempty"`
	}
	var blanks []blankRow
	aniNamed := map[string]string{}
	for _, r := range rows {
		if !strings.Contains(strings.ToLower(r.Path), "title") {
			continue
		}
		nm := strings.TrimSpace(eqKV[fmt.Sprintf("name_%d", r.ID)])
		p := r.Path
		if !strings.HasPrefix(strings.ToLower(p), "equipment/") {
			p = path.Join("equipment", p)
		}
		ani := ""
		if s, err := catalog.ResolveScript(a, p); err == nil {
			for i := 0; i < len(s.Cells); i++ {
				if s.Cells[i].Type == 3 && s.Cells[i].Text == "[custom animation]" && i+1 < len(s.Cells) && s.Cells[i+1].Type == 6 {
					ani = s.Cells[i+1].Text
					break
				}
			}
		}
		if nm == "" {
			blanks = append(blanks, blankRow{ID: r.ID, Path: r.Path, Ani: ani})
		} else if ani != "" {
			base := strings.ToLower(path.Base(strings.ReplaceAll(ani, "\\", "/")))
			if _, exists := aniNamed[base]; !exists {
				aniNamed[base] = nm
			}
		}
	}
	out := map[string]any{
		"blanks":       blanks,
		"ani_named":    aniNamed,
		"stk_names":    stkKV,
		"eq_key_count": len(eqKV),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out)
}
'''
    tmp_go = core.SERVER / 'cmd' / 'titlescan_tmp.go'
    tmp_go.write_text(go_src, encoding='utf-8')
    try:
        res = subprocess.run(
            [str(go_bin), 'run', './cmd/titlescan_tmp.go', '-source', str(inner_pvf)],
            cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
            creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=180,
        )
        if res.returncode != 0:
            raise RuntimeError('扫描称号失败：' + (res.stderr or res.stdout or '')[-600:])
        data = json.loads(res.stdout)
        return (
            data.get('blanks') or [],
            data.get('ani_named') or {},
            data.get('stk_names') or {},
            int(data.get('eq_key_count') or 0),
        )
    finally:
        tmp_go.unlink(missing_ok=True)


def _build_tid_to_box_map(blank_ids: set[int], stk_names: dict[str, str]) -> dict[int, str]:
    if not BOOSTERS_CATALOG.is_file():
        return {}
    with gzip.open(BOOSTERS_CATALOG, 'rt', encoding='utf-8') as f:
        boxes = (json.load(f).get('boxes') or {})
    tid_to_box: dict[int, str] = {}
    for bid, box in boxes.items():
        bn = stk_names.get(f'name_{bid}', '').strip()
        if not bn or bn == '测试' or 'test' in bn.lower():
            continue
        all_items = [it for lst in (box.get('c') or {}).values() for it in lst] + [
            it for grp in (box.get('g') or []) for it in grp
        ]
        tids = {int(it.get('t', 0)) for it in all_items if int(it.get('t', 0)) in blank_ids}
        if len(tids) == 1 and len({int(it.get('t', 0)) for it in all_items}) <= 2:
            tid_to_box.setdefault(next(iter(tids)), bn)
    return tid_to_box


def _extract_eq_str(inner_pvf: Path, out_dir: Path) -> dict[str, str]:
    go_bin = core.pre.GO_BIN
    env = dict(os.environ)
    env['GOPATH'] = str(ROOT / 'tools' / 'gopath')
    env['GOCACHE'] = str(ROOT / 'tools' / 'gocache')
    env['PATH'] = str(go_bin.parent) + os.pathsep + env.get('PATH', '')
    res = subprocess.run(
        [
            str(go_bin), 'run', './cmd/pvfinspect',
            '-source', str(inner_pvf),
            '-files', EQ_STR_ENTRY,
            '-output', str(out_dir),
        ],
        cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
        creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=180,
    )
    if res.returncode != 0:
        raise RuntimeError('pvfinspect 提取失败：' + (res.stderr or res.stdout or '')[-600:])
    bf = out_dir / '00-equipment.uv.str.bin'
    if not bf.is_file():
        raise FileNotFoundError(f'PVF 缺少 {EQ_STR_ENTRY}')
    return parse_uv_str_utf16(bf.read_bytes())


def _backup_live_pvf(live_pvf: Path, before_sha: str) -> Path:
    BACKUP_DIR.mkdir(parents=True, exist_ok=True)
    backup_pvf = BACKUP_DIR / 'Script.pvf.bak'
    if not backup_pvf.is_file():
        core.file_copy(live_pvf, backup_pvf)
        core.atomic(BACKUP_MANIFEST, {
            'live_pvf': str(live_pvf),
            'backup_pvf': str(backup_pvf),
            'original_sha256': before_sha,
            'original_size': live_pvf.stat().st_size,
        })
    return backup_pvf


def apply_staged() -> dict:
    if core.client_up():
        raise RuntimeError('检测到 DFO.exe 正在运行，请先关闭游戏客户端再应用称号汉化补丁')
    if not STAGE_MANIFEST.is_file():
        raise FileNotFoundError(f'未找到待安装清单：{STAGE_MANIFEST}')
    meta = core.read(STAGE_MANIFEST)
    staged = Path(meta['staged_pvf'])
    live = Path(meta['live_pvf'])
    expected_before = meta['before_sha256']
    expected_after = meta['after_sha256']
    if not staged.is_file():
        raise FileNotFoundError(f'未找到暂存文件：{staged}')
    cur_sha = core.digest(live)
    if cur_sha == expected_after:
        staged.unlink(missing_ok=True)
        STAGE_MANIFEST.unlink(missing_ok=True)
        return {'status': 'already_installed', 'sha256': cur_sha}
    if cur_sha != expected_before:
        raise RuntimeError('当前 Script.pvf 已变更，请重新运行本工具构建最新补丁')
    if core.digest(staged) != expected_after:
        raise RuntimeError('暂存 Script.pvf 校验和不匹配')
    backup_pvf = _backup_live_pvf(live, cur_sha)
    core.file_copy(staged, live)
    if core.digest(live) != expected_after:
        raise RuntimeError('安装写入后校验和不匹配')
    staged.unlink(missing_ok=True)
    STAGE_MANIFEST.unlink(missing_ok=True)
    return {
        'status': 'installed',
        'backup_pvf': str(backup_pvf),
        'before_sha256': cur_sha,
        'after_sha256': expected_after,
    }


def rollback_live_pvf() -> dict:
    if core.client_up():
        raise RuntimeError('检测到 DFO.exe 正在运行，请先关闭游戏客户端再回滚 Script.pvf')
    if not BACKUP_MANIFEST.is_file():
        raise FileNotFoundError(f'未找到备份清单：{BACKUP_MANIFEST}')
    meta = core.read(BACKUP_MANIFEST)
    backup_pvf = Path(meta['backup_pvf'])
    live_pvf = Path(meta['live_pvf'])
    expected = meta['original_sha256']
    if core.digest(backup_pvf) != expected:
        raise RuntimeError('备份 Script.pvf 校验和不匹配，中止回滚')
    core.file_copy(backup_pvf, live_pvf)
    if core.digest(live_pvf) != expected:
        raise RuntimeError('回滚写入后校验和不匹配')
    return {'status': 'rolled_back', 'live_pvf': str(live_pvf), 'sha256': expected}


def build_and_apply(stage_only: bool = False, inner_override: Path | None = None) -> dict:
    client_dir = core.pre.client_exe().parent
    live_pvf = client_dir / 'Script.pvf'
    if not live_pvf.is_file():
        raise FileNotFoundError(f'未找到客户端 Script.pvf：{live_pvf}')

    before_sha = core.digest(live_pvf)
    fp = lr.pvf_fingerprint(live_pvf)
    is_inner = bool(fp and fp.get('inner'))
    inner_source = (
        Path(inner_override).resolve()
        if inner_override is not None
        else (live_pvf if is_inner else lr.ensure_inner_pvf(live_pvf, fp))
    )

    blanks, ani_named, stk_names, orig_eq_count = _scan_pvf_titles(inner_source)
    if not blanks and inner_override is None:
        return {'status': 'already_localized', 'sha256': before_sha, 'blank_titles': 0}

    blank_ids = {int(r['id']) for r in blanks}
    tid_to_box = _build_tid_to_box_map(blank_ids, stk_names)
    patches, unresolved = resolve_blank_title_names(blanks, ani_named, tid_to_box)
    if unresolved:
        sample = [(r['id'], r.get('ani', ''), r.get('path', '')) for r in unresolved[:10]]
        raise RuntimeError(f'仍有 {len(unresolved)} 个空称号未映射到中文名：{sample}')

    merger = _ensure_merger_binary()
    STAGE_DIR.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=STAGE_DIR, prefix='.work-') as tmpdir:
        tmp = Path(tmpdir)
        ov = tmp / 'equipment.uv.str'
        ov.write_text(render_uv_str_overlay(patches), encoding='utf-8')
        plan_file = tmp / 'plan.json'
        plan_file.write_text(
            json.dumps({
                'layers': [{
                    'path': EQ_STR_ENTRY,
                    'source': str(ov),
                    'mod_id': 'title-names-zh',
                    'priority': 0,
                }]
            }, ensure_ascii=False),
            encoding='utf-8',
        )
        merged_inner = tmp / 'merged.inner.pvf'
        env = dict(os.environ)
        env['GOGC'] = '10'
        res = subprocess.run(
            [str(merger), '-source', str(inner_source), '-plan', str(plan_file), '-output', str(merged_inner)],
            cwd=str(core.SERVER), env=env, capture_output=True, text=True, errors='replace',
            creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0), timeout=600,
        )
        if res.returncode != 0:
            raise RuntimeError('pvfmerge 失败：' + (res.stderr or res.stdout or '')[-800:])
        merge_report = json.loads((res.stdout or '{}').strip().splitlines()[-1])

        # Strict post-merge read-back verification
        verify_dir = tmp / 'verify'
        verify_dir.mkdir()
        v_eq = _extract_eq_str(merged_inner, verify_dir)
        if len(v_eq) < orig_eq_count:
            raise RuntimeError(f'合并后 equipment.uv.str 词条数减少：{len(v_eq)} < {orig_eq_count}')
        for k, want in patches.items():
            if v_eq.get(k) != want:
                raise RuntimeError(f'回读校验失败 {k}: got={v_eq.get(k)!r}, want={want!r}')

        staged_pvf = STAGE_DIR / 'Script.pvf'
        if is_inner:
            core.file_copy(merged_inner, staged_pvf)
            after_sha = core.digest(staged_pvf)
        else:
            after_sha = rewrap_inner_to_client_pvf(merged_inner, staged_pvf, client_dir)

    stats = {
        'patched_titles': len(patches),
        'orig_eq_keys': orig_eq_count,
        'verified_eq_keys': len(v_eq),
        'sample_100331498': patches.get('name_100331498'),
    }
    if not stage_only and not core.client_up():
        backup_pvf = _backup_live_pvf(live_pvf, before_sha)
        core.file_copy(staged_pvf, live_pvf)
        if core.digest(live_pvf) != after_sha:
            raise RuntimeError('写入 Script.pvf 校验失败')
        staged_pvf.unlink(missing_ok=True)
        STAGE_MANIFEST.unlink(missing_ok=True)
        return {
            'status': 'installed',
            'backup_pvf': str(backup_pvf),
            'before_sha256': before_sha,
            'after_sha256': after_sha,
            'merge_report': merge_report,
            'stats': stats,
        }

    core.atomic(STAGE_MANIFEST, {
        'live_pvf': str(live_pvf),
        'staged_pvf': str(staged_pvf),
        'before_sha256': before_sha,
        'after_sha256': after_sha,
        'stats': stats,
    })
    return {
        'status': 'staged_pending_client_close',
        'staged_pvf': str(staged_pvf),
        'before_sha256': before_sha,
        'after_sha256': after_sha,
        'merge_report': merge_report,
        'stats': stats,
    }


def main():
    p = argparse.ArgumentParser(description='Fill all 1,279 blank title names in US100 Script.pvf')
    p.add_argument('--stage-only', action='store_true', help='Build and stage without touching live Script.pvf')
    p.add_argument('--apply-staged', action='store_true', help='Install staged Script.pvf after closing DFO.exe')
    p.add_argument('--rollback', action='store_true', help='Restore original Script.pvf from backup')
    p.add_argument('--inner-source', type=Path, default=None, help='Optional baseline inner PVF path')
    args = p.parse_args()
    if args.rollback:
        result = rollback_live_pvf()
    elif args.apply_staged:
        result = apply_staged()
    else:
        result = build_and_apply(stage_only=args.stage_only, inner_override=args.inner_source)
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
