"""Create, verify, and deploy the incremental 100US character status localization."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from typing import NamedTuple

from us100_dstr_ui import CLIENT, EXE, GameFonts, core, key_pair, pack, unpack
from us100_quest_act import OUTLINE_FONTS, REL
from us100_quest_strings import FORMAT, ROW
from us100_quest_text_trial import ACT, EXPECTED_TRIAL
from us100_quest_ui_install import PVF
from us100_hud_priority_labels import ROWS as HUD_PRIORITY_ROWS
from us100_dungeon_clear_labels import BASE as CLEAR_BASE, PVF as CLEAR_PVF, ROWS as CLEAR_ROWS
from us100_quest_recommend_labels import BASE as REC_BASE, PVF as REC_PVF, ROWS as REC_ROWS
from us100_dungeon_level_labels import BASE as LEVEL_BASE, PVF as LEVEL_PVF, QUEST as LEVEL_QUEST, ROWS as LEVEL_ROWS
from us100_skill_reward_ui_labels import ROWS as SKILL_REWARD_UI_ROWS
from us100_system_reward_notice_labels import ROWS as SYSTEM_REWARD_NOTICE_ROWS
from us100_weapon_tooltip_labels import ROWS as WEAPON_TOOLTIP_ROWS


BASE = "4ccf24f93922a31a2ace1b08d750a40fd5897ab91efd96aa22abe9486f21799e"
ROWS = {
    "42000": ("My Status (%s)", "角色信息 (%s)"),
    "42001": ("Other's Information", "角色信息"),
    "42006": ("HP", "生命值"),
    "42007": ("MP", "魔法值"),
    "42008": ("Strength", "力量"),
    "42009": ("Intelligence", "智力"),
    "42010": ("Vitality", "体力"),
    "42011": ("Spirit", "精神"),
    "42012": ("Physical Atk.", "物理攻击"),
    "42013": ("Physical Def.", "物理防御"),
    "42014": ("Magical Atk.", "魔法攻击"),
    "42015": ("Magical Def.", "魔法防御"),
    "42016": ("Fire", "火"),
    "42017": ("Water", "冰"),
    "42018": ("Light", "光"),
    "42019": ("Shadow", "暗"),
    "21421": ("Physical Crit.", "物理暴击"),
    "21422": ("Magical Crit.", "魔法暴击"),
    "100087435": ("Def. Info", "防御信息"),
    "100087436": ("Def. Rate (P)", "物理减伤率"),
    "100087437": ("Def. Rate (M)", "魔法减伤率"),
    "52040": ("Ind. Atk.", "独立攻击"),
    "4087": ("Movement", "移动速度"),
    "4088": ("Attack Spd.", "攻击速度"),
    "4089": ("Casting Spd.", "施放速度"),
    "371": ("Move Spd.", "移动速度"),
    "372": ("Attack Spd.", "攻击速度"),
    "373": ("Casting Spd.", "施放速度"),
    "40253": ("Adventurer Fame", "冒险家名望"),
    "61015": ("Adventurer Fame %d", "冒险家名望 %d"),
    "48036": ("Lv. %d %s", "%d级 %s"),
    "69188": ("Lv. %d %s", "%d级 %s"),
    "100002322": ("Lv. %d %s", "%d级 %s"),
}
SENTINELS = {"100008129": "[主线任务]", "49009": "开始游戏"}
ELEMENT_ROWS = {
    "52021": ("Fire", "火"),
    "52022": ("Water", "冰"),
    "52023": ("Light", "光"),
    "52024": ("Shadow", "暗"),
    "52061": ("Attack attribute (Elemental Damage)\\nIncreases your damage of the corresponding element.",
              "攻击属性（属性强化）\\n提高对应属性的攻击伤害。"),
}
SHOP_ROWS = {
    "1242": ("Loading ...", "正在加载…"),
    "46703": ("Loading...", "正在加载…"),
    "46113": ("Tip: Items that you want to safely store can be Alt + Left Clicked to lock them from being traded or discarded.",
              "提示：按住 Alt 并左键点击物品即可锁定，防止误交易或丢弃。"),
    "21090": ("Material", "材料"),
    "23028": ("Top", "上衣"), "23029": ("Bottom", "下装"),
    "23030": ("Hair", "头发"), "23031": ("Hat", "帽子"),
    "23032": ("Face", "脸部"), "23033": ("Torso", "胸部"),
    "23034": ("Waist", "腰部"), "23035": ("Shoes", "鞋子"),
    "23036": ("Skin", "皮肤"), "23037": ("Clone", "克隆"),
    "23050": ("Newest", "最新"), "23051": ("Oldest", "最早"),
    "23052": ("Level: Highest", "等级从高到低"), "23053": ("Level: Lowest", "等级从低到高"),
    "23054": ("Name: Asc.", "名称升序"), "23055": ("Name: Des.", "名称降序"),
    "23056": ("Creature", "宠物"), "23057": ("Consumable", "消耗品"),
    "23059": ("Item", "道具"), "23060": ("Buy", "购买"),
    "23061": ("Gift", "赠送"), "23062": ("Buy", "购买"),
    "23064": ("Charge CERA", "充值点券"), "23069": ("Gold", "金币"),
    "27115": ("Cart", "购物车"), "27116": ("Use", "消耗品"),
    "27117": ("Avatar", "装扮"), "27118": ("Creature", "宠物"),
    "33014": ("Package", "礼包"), "33015": ("Slayer/Dark Knight", "鬼剑士/黑暗武士"),
    "33016": ("Slayer (F)", "鬼剑士（女）"), "33017": ("Fighter (M)", "格斗家（男）"),
    "33018": ("Fighter (F)", "格斗家（女）"), "33019": ("Gunner (M)", "神枪手（男）"),
    "33020": ("Gunner (F)", "神枪手（女）"), "33021": ("Mage (M)", "魔法师（男）"),
    "33022": ("Mage (F)/Creator", "魔法师（女）/缔造者"), "33023": ("Priest", "圣职者（男）"),
    "33024": ("Priest (F)", "圣职者（女）"), "33025": ("Thief (M)", "暗夜使者（男）"),
    "33026": ("Thief", "暗夜使者"), "33031": ("Weapon", "武器"),
    "35190": ("Dye", "染色"), "35226": ("Slayer/Dark Knight", "鬼剑士/黑暗武士"),
    "38397": ("Scroll to change", "滚动鼠标滚轮切换"),
    "38398": (" Turn off item tooltip", " 关闭物品提示"),
    "39040": ("Gold", "金币"), "39041": ("Mileage", "积分"),
    "100009005": ("Dye", "染色"), "100009008": ("Search", "搜索"),
    "100009013": ("Gift All", "全部赠送"), "100009014": ("Buy All", "全部购买"),
    "100009015": ("Charge CERA", "充值点券"), "100009019": ("Gold", "金币"),
}
CLEAR_FONTS = {
    "calibri.ttf":"479b76623ddf9e4e49a31180f84860d3b4c62764018f002a89642a77733e3471",
    "tahoma.ttf":"2e8a4bcd698e61ba3e426ec2dfae9fe834ede70a2a6fbaf3fe51db642725fb81",
}


class Profile(NamedTuple):
    baseline: str
    rows: dict
    sentinels: dict


PROFILES = {
    "status": Profile(BASE,ROWS,SENTINELS),
    "dungeon-level-notices": Profile(LEVEL_BASE,LEVEL_ROWS,SENTINELS | {
        "100087473": "升级指南", "100087474": "升到 %d 级该做什么", "49288": "技能剩余冷却：%.*f秒",
    }),
    "quest-recommend": Profile(REC_BASE,REC_ROWS,SENTINELS | {
        "31095": "与%s交谈：%d/%d", "879": "%s 已进入虚弱状态。",
        "193": "疲劳值：%d/%d", "100088588": "游戏点数：%d/%d",
    }),
    "dungeon-clear-ui": Profile(CLEAR_BASE,CLEAR_ROWS,SENTINELS | {
        "193": "疲劳值：%d/%d", "100088588": "游戏点数：%d/%d",
        "35190": "染色", "60938": "详情（%s）", "60935": "普通属性（%s）",
    }),
    "elements": Profile("198b1170701e2564cdb480e7ffc491cb920f19bb51c92e66d8db8d66c74bf5bc",
                        ELEMENT_ROWS,SENTINELS | {"42000":"角色信息 (%s)","52040":"独立攻击"}),
    "shop": Profile("14349737f51c17227298bbab6de86d06eb66ad3d2456940aace8a47069b42e53",
                    SHOP_ROWS,SENTINELS | {"42000":"角色信息 (%s)","52021":"火"}),
}
PROFILES["shop-v2"] = PROFILES["shop"]._replace(rows=SHOP_ROWS | {
    "21096": ("CERA", "点券"), "23068": (" CERA", " 点券"),
    "27114": ("New", "新"), "39038": ("CERA", "点券"),
})

EQUIP_FILTER_ROWS = {
    "100086047": ("Up to 4 tags can be added.", "最多可添加 4 个标签。"),
    "100086048": ("Add/Remove Goal Equipment", "添加/移除目标装备"),
    "100086049": ("Select Fame.", "请选择冒险家名望。"),
    "100086050": ("The added equipment will be changed to the Recommended Equipment and deleted.\\nAdd Recommended Equipment?",
                  "已添加的目标装备将被推荐装备替换。\\n确定添加推荐装备吗？"),
    "100087494": ("All Categories", "全部分类"),
    "100087496": ("Set", "套装"), "100087495": ("Dungeon", "地下城"),
    "100087493": ("Event", "活动"), "100087492": ("Event Card", "活动卡片"),
    "100087491": ("Event Bead", "活动宝珠"),
    "100087497": ("  Auction Hall purchase available (Go)", "  可在拍卖行购买（前往）"),
    "100087540": ("Event Cards and Beads can be found in the separate Event category.", "活动卡片和宝珠可在「活动」分类中查看。"),
    "100087642": ("Set Info", "套装信息"), "100087643": ("Source", "获得途径"),
    "100087644": ("My Equipment Info", "我的装备"), "100087645": ("Card Collection", "卡片收藏"),
    "100087646": ("No available set info for the selected equipment.", "所选装备暂无套装信息。"),
    "100087647": ("No available source info for the selected item.", "所选道具暂无获得途径。"),
    "100087648": ("No available equipment to compare.", "没有可对比的装备。"),
    "100087649": ("Start navigating.", "开始导航。"),
    "100087681": ("Physical Atk.", "物理攻击"), "100087682": ("Magical Atk.", "魔法攻击"),
    "100087773": ("Too many items.", "搜索结果过多。"),
    "100087788": ("Card", "卡片"), "100087789": ("Bead", "宝珠"),
    "100087805": ("Atk.", "攻击"), "100087806": ("Def.", "防御"),
    "100087807": ("Synergy", "协同"), "100087808": ("Others", "其他"),
    "100087809": ("Growth/Transfer Option", "属性成长/传送"),
    "100087810": ("Cards and Beads", "卡片和宝珠"), "100087843": ("Dungeon", "地下城"),
    "100087851": ("White Land", "白色大地"), "100087852": ("Vericollis", "圣殿贝里科蒂斯"),
    "100087853": ("Nasau Forest", "纳瑟乌森林"), "100087854": ("Eternal Flame Lab", "永恒之光研究所"),
    "100087855": ("Canyon Hills", "柯涅恩山"), "100087856": ("Queen Fallt", "昆法特"),
    "100087857": ("King's Cradle", "王之摇篮"), "100087858": ("Heblon's Altar", "海伯伦的预言所"),
    "100087859": ("Destroyed Castle of the Dead", "毁坏的寂静城"), "100087860": ("Noblesse Code", "贵族机要"),
    "100088341": ("The Meister's Laboratory", "机械七战神实验室"),
    "100087861": (" (View)", " （查看）"), "100087877": ("Navigation failed.", "导航失败。"),
    "100087911": ("Common Option", "通用属性"), "100087912": ("Original Option", "原有属性"),
    "100087913": ("Weapon Option", "武器属性"), "100087914": ("Armor Option", "防具属性"),
    "100087915": ("Accessory Option", "首饰属性"), "100087916": ("Special Equipment Option", "特殊装备属性"),
    "100087917": ("Exclusive Equipment Option", "专属装备属性"),
    "100087926": ("Enter a Card/Bead name.", "请输入卡片或宝珠名称。"),
    "100087927": ("Enter in-game terminology.", "请输入游戏术语。"),
    "100087928": ("Enter a Transfer option.", "请输入传送属性。"),
    "100087929": ("Enter equipment name.", "请输入装备名称。"), "100087930": ("Lv.", "等级"),
    "100088265": ("Up to 30 favorites can be registered.", "最多可收藏 30 项。"),
    "100088273": ("Add tag (#)", "添加标签（#）"),
    "100088274": ("Up to 4 tags can be added.", "最多可添加 4 个标签。"),
    "100088275": ("No available tags (#)", "没有可用标签（#）"),
    "100088280": ("Only up to 4 tags can be added.", "最多只能添加 4 个标签。"),
    "100088424": ("Find Combination", "查找搭配"), "100088425": ("Add to Favorites", "添加到收藏"),
    "100088426": ("All Characters", "全部角色"), "100088427": ("All Classes", "全部职业"),
    "100088428": ("View Tags", "查看标签"), "100088429": ("Fold Tags", "收起标签"),
    "100088430": ("Remove Favorite", "取消收藏"), "100088431": ("Add/Remove Favorites", "添加/取消收藏"),
    "60773": ("All Parts", "全部部位"), "60774": ("All Rarities", "全部稀有度"),
    "39475": ("Weapon", "武器"), "39476": ("Armor", "防具"),
    "39477": ("Accessory", "首饰"), "39478": ("Special Equipment", "特殊装备"),
    "19220": ("Sub Equipment", "辅助装备"), "19221": ("Magic Stone", "魔法石"),
    "100002306": ("Sub Equipment", "辅助装备"), "100007110": ("Sub Equipment", "辅助装备"),
    "400001827": ("Sub Equipment", "辅助装备"),
    "13090": ("DFOpedia", "装备词典"), "37017": ("DFOpedia", "装备词典"),
    "49067": ("DFOpedia (%s)", "装备词典 (%s)"),
}
PROFILES["equip-filter"] = Profile(
    "aa0ec1b107755a10716064d00134fff4e1d8ae45242b7c7c1a6a1897904f38df", EQUIP_FILTER_ROWS,
    SENTINELS | {"42000":"角色信息 (%s)","52021":"火","38397":"滚动鼠标滚轮切换"},
)

EQUIP_PVF = "c540bd70f084d93284c365e133b6909d9f9903cd611c02c3609206decd26950e"
EXPIRY_PVF = "a3cf04a87f295de24ec9753267865cbc8c70482a6e1da9519a1952be6ac4865a"
PROFILES["inventory-timed-items"] = Profile(
    "87b7d539b7f4263d3837ced8ea0e7c44e6b20523a51ae78108a059071549cfa2", {
        "21080": ("Notice", "提示"), "1850": ("Notice", "提示"),
        "27064": ("Notice", "提示"), "31528": ("Notice", "提示"),
        "44220": ("Notice", "提示"), "46515": ("Notice", "提示"),
        "21400": ("OK", "确定"), "21403": ("OK", "确定"),
        "30435": ("OK", "确定"), "30445": ("OK", "确定"),
        "60885": ("OK", "确定"), "100009006": ("OK", "确定"),
        "52092": ("OK", "确定"), "90973": ("OK", "确定"),
        "746": ("Your Inventory is full.", "物品栏已满。"),
        "748": ("Your Inventory is full.", "物品栏已满。"),
        "1330": ("Your Inventory is full.", "物品栏已满。"),
        "2057": ("Your Inventory is full.", "物品栏已满。"),
        "753": ("This character's Inventory is full.", "该角色的物品栏已满。"),
        "791": ("Your Inventory is full; you cannot receive the reward item.", "物品栏已满，无法领取奖励。"),
        "793": ("Your Inventory is full; you cannot receive this item.", "物品栏已满，无法领取该道具。"),
        "21085": ("Inventory", "物品栏"), "21086": ("Avatar", "装扮"),
        "21087": ("Creature", "宠物"), "21088": ("Equip", "装备"),
        "21089": ("Use", "消耗品"), "21091": ("Quest", "任务"), "21092": ("Sort", "整理"),
        "21101": ("Item Inventory (%s)", "物品栏（%s）"),
        "21102": ("Avatar Inventory (%s)", "装扮栏（%s）"),
        "21103": ("Creature Inventory (%s)", "宠物栏（%s）"),
        "35170": ("Inventory (%s)", "物品栏（%s）"),
        "31074": ("Premium and Item Duration", "限时道具信息栏"),
        "31076": ("%s item that is currently applied: %d", "生效中的%s：%d"),
        "31077": ("%d day(s) %d hr(s)", "%d天%d小时"),
        "31078": ("%d day(s)", "%d天"), "31079": ("%d hr(s)", "%d小时"),
        "31080": ("%d min(s)", "%d分钟"), "33004": ("Extend", "续期"),
        "35321": ("Select Cube", "选择晶块"),
    }, SENTINELS | {"1099":"命中率 %+.*f%%%%", "23065":"商城结算", "100087929":"请输入装备名称。"},
)
NOTICE_ROWS={
    "44213": ("System　", "系统　"),
    "46104": ("Allow you to use Skill Style 2.", "已解锁技能类型2。"),
    "46101": ("Change Skill Build\\n(Available after purchasing a Dual Skill Build License from the Item Shop.)",
              "切换技能加点方案\\n（在商城购买双技能加点许可后可用。）"),
    "46102": ("Change Skill Build", "切换技能加点"),
    "2126": ("Conqueror's Contract", "霸王之契约"),
    "3132": ("Tactician's Contract", "达人之契约"),
    "36117": ("Growth Contract", "成长之契约"), "49143": ("Growth Contract", "成长之契约"),
    "35254": ("Cube Contract", "晶之契约"), "100022079": ("Cube Contract", "晶之契约"),
    "39344": ("%s expires in %I64u days.", "%s还有%I64u天到期。"),
    "39345": ("%s expires in %I64u hours and %I64u minutes.", "%s还有%I64u小时%I64u分钟到期。"),
    "39346": ("%s expires in %I64u minutes.", "%s还有%I64u分钟到期。"),
    "39347": ("%s expires in %I64u hours.", "%s还有%I64u小时到期。"),
    "243": ("%s has %d day(s) left until expiration.", "%s还有%d天到期。"),
    "244": ("%s has %d hr(s) left until expiration.", "%s还有%d小时到期。"),
    "395": ("%s expires in %d hr.", "%s还有%d小时到期。"),
    "42098": ("Lethe's Contract expires in %s.", "遗忘河之契约还有%s到期。"),
}
PROFILES["inventory-system-notices"] = PROFILES["inventory-timed-items"]._replace(
    rows=PROFILES["inventory-timed-items"].rows|NOTICE_ROWS,
)
# All four IDs are passed to the same text lookup in the naming dialog
# initializer at RVA 0xD3A050. Do not replace the unrelated Confirm/Check rows.
PROFILES["character-name"] = Profile(
    "82d668a551614e5acf61c9aa48246710d02c60f69cc13778156f35a7beed327c", {
        "36152": ("Enter the character name.", "请输入角色名。"),
        "100008058": ("Up to 12 English characters can be entered.", "最多可输入 12 个英文字符。"),
        "100009002": ("Check", "查重"),
        "13032": ("Confirm", "确定"),
    }, SENTINELS | {"13033":"取消", "501031061":"创建角色", "21101":"物品栏（%s）"},
)
PROFILES["weapon-tooltip"] = Profile(
    "3bd88f77c0dfb33cf35e123c4534733bbf3b4d22004a3ebee99df0d11c03d702", WEAPON_TOOLTIP_ROWS,
    SENTINELS | {"36152":"请输入角色名。", "13032":"确定", "60860":"神话",
                 "60917":"合成", "60918":"分解", "60919":"[X]", "60928":"[O]"},
)
PROFILES["skill-reward-ui"] = Profile(
    "5d428f17c324a2e667400259ee3178867d945f2f3ba399141cad21153d264ea8", SKILL_REWARD_UI_ROWS,
    SENTINELS | {"36152":"请输入角色名。", "13032":"确定", "21401":"取消",
                 "30231":"物理攻击力", "38174":"[成长追加属性]", "100009006":"确定"},
)
PROFILES["system-reward-notices"] = Profile(
    "add8d6e60a6a578edc4c57febe6e20651b4f41e939a0a3674d0d02be4d60d42e", SYSTEM_REWARD_NOTICE_ROWS,
    SENTINELS | {"46104":"已解锁技能类型2。", "39344":"%s还有%I64u天到期。",
                 "39345":"%s还有%I64u小时%I64u分钟到期。", "2126":"霸王之契约",
                 "100022079":"晶之契约", "36152":"请输入角色名。", "38174":"[成长追加属性]",
                 "1147":"瞬发", "39212":"领取"},
)
PROFILES["hud-priority"] = Profile(
    "5ec1cb71d8c2c0573b29e21ae047b694e853dd24bed3aa4869d902155715655f", HUD_PRIORITY_ROWS,
    SENTINELS | {"90176":"新尊享特权契约（增强版）", "1516":"获得 %d 点经验。",
                 "1179":"获得 %d 个%s。", "1147":"瞬发", "39212":"领取", "38174":"[成长追加属性]"},
)


def register_avatar_ui_profile():
    rows = core.read(Path(__file__).with_name("us100_avatar_ui_rows.json"))
    PROFILES["avatar-ui"] = Profile(
        "48a8b7ebc3b53f193121c14f01eaf8d3396cdd887c81e45383d3482c99a26836",
        {k: tuple(v) for k, v in rows.items()},
        SENTINELS | {"193": "疲劳值：%d/%d", "100088588": "游戏点数：%d/%d",
                     "35190": "染色", "60938": "详情（%s）", "60935": "普通属性（%s）",
                     "90176": "新尊享特权契约（增强版）"},
    )
# In addition to standard tooltip formats, contract notices use the native
# Windows unsigned 64-bit specifier. It must survive byte-for-byte.
WIN_FORMAT = re.compile(r"%I(?:32|64)[diouxX]")
PROFILES["shop-small-tabs"] = Profile(
    "d17f913b8415e4b639664ff83d637cf292264df32ab035ce5b3243d628760f76", {
        "39022": ("Cargo/ETC", "消耗品/其他"),
        "39023": ("Equipment", "装备"),
        "39024": ("Dungeon", "地下城"),
        "39025": ("Creature", "宠物"),
        "39026": ("Limited Edition", "限定商品"),
        "39027": ("Premium", "高级商品"),
        "39021": ("Item Shop Inventory", "商城仓库"),
    }, SENTINELS | {"501031101":"鬼剑士", "501031117":"极诣·剑魂", "100087494":"全部分类",
                    "38397":"滚动鼠标滚轮切换"})


def register_equipment_tooltip_profile():
    from us100_equipment_tooltip_labels import LABELS
    base = "f7474705da1f3e5a0d0c44144807ecfb8123bda746da98d68bad313f5f83a7e9"
    source = Path(__file__).resolve().parents[1]/"runtime"/"us100-town-shop-r3"/"dstr.dat"
    if core.digest(source) != base:
        raise ValueError("equipment tooltip needs the verified preceding DSTR artifact")
    zh={str(k):v for k,v in LABELS.items()}
    english=extract(unpack(source.read_bytes(),key_pair(CLIENT/"DFO.exe")),zh.keys())
    PROFILES["equipment-tooltip"] = Profile(base,{k:(english[k],v) for k,v in zh.items()},SENTINELS | {
        "501031101":"鬼剑士", "100087494":"全部分类", "100087929":"请输入装备名称。",
        "49067":"装备词典 (%s)", "39023":"装备", "39024":"地下城", "100087930":"等级",
    })


def register_create_profile():
    from us100_create_labels import translations
    base = "effce5632ed73f74073177c4590f5a6d04977f1ce9784a2fd78cf19c76fec747"
    previous = Path(__file__).resolve().parents[1]/"runtime"/"us100-equipfilter-dstr-r1"/"dstr.dat"
    if core.digest(previous) != base:
        raise ValueError("creation profile needs the verified preceding DSTR artifact")
    zh = translations()
    raw = unpack(previous.read_bytes(), key_pair(CLIENT/"DFO.exe"))
    english = extract(raw, zh.keys())
    PROFILES["character-create"] = Profile(base, {k:(english[k],v) for k,v in zh.items()}, SENTINELS | {
        "100087494":"全部分类", "100087929":"请输入装备名称。", "42000":"角色信息 (%s)",
        "52021":"火", "38397":"滚动鼠标滚轮切换",
    })


def extract(raw, keys):
    rows = {}
    for m in ROW.finditer(raw.decode("utf-8")):
        if m[1] in keys:
            if m[1] in rows:
                raise ValueError(f"duplicate DSTR ID {m[1]}")
            rows[m[1]] = m[2]
    if rows.keys()!=keys:
        raise ValueError(f"missing DSTR rows {keys-rows.keys()}")
    return rows


def replace(raw, changes):
    before=extract(raw,changes.keys())
    for key,(old,new) in changes.items():
        if (before[key]!=old or FORMAT.findall(old)!=FORMAT.findall(new)
                or WIN_FORMAT.findall(old)!=WIN_FORMAT.findall(new)
                or old.count("\\n")!=new.count("\\n") or any(c in new for c in "\r\n\0")):
            raise ValueError(f"unexpected source or format controls: {key}")
    return ROW.sub(lambda m: m[1]+">"+changes[m[1]][1] if m[1] in changes else m[0],
                   raw.decode("utf-8")).encode("utf-8")


def apply(baseline, diff, exe, output, profile="status"):
    base, rows, _=PROFILES[profile]
    manifest=core.read(diff)
    if (manifest.get("profile","status")!=profile or manifest["before"]!=base
            or core.digest(baseline)!=base or output.resolve().is_relative_to(CLIENT.resolve())):
        raise ValueError("wrong offline patch baseline or live output")
    changes={row["id"]:(row["before"],row["after"]) for row in manifest["rows"]}
    if changes!=rows or len(manifest["rows"])!=len(rows):
        raise ValueError("unexpected status patch footprint")
    keys=key_pair(exe)
    raw=unpack(baseline.read_bytes(),keys)
    new=replace(raw,changes)
    result=pack(new,keys)
    if unpack(result,keys)!=new or replace(new,{k:(b,a) for k,(a,b) in changes.items()})!=raw:
        raise ValueError("DSTR diff is not exactly reversible")
    if hashlib.sha256(result).hexdigest()!=manifest["after"]:
        raise ValueError("DSTR patch output fingerprint differs")
    with output.open("xb") as f:
        f.write(result)


def inspect(path,exe,profile="status"):
    _, rows, sentinels=PROFILES[profile]
    keys=key_pair(exe)
    blob=path.read_bytes()
    raw=unpack(blob,keys)
    if pack(raw,keys)!=blob:
        raise ValueError("native DSTR round trip differs")
    values=extract(raw,rows.keys()|sentinels.keys())
    if any(values[k]!=v for k,v in sentinels.items()):
        raise ValueError("previous Chinese localization missing")
    print(json.dumps({"native_roundtrip":True,"labels":{k:values[k] for k in rows},
                      "preserved":sentinels},ensure_ascii=False,sort_keys=True))


def build(source,exe,out,profile="status"):
    base, rows, _=PROFILES[profile]
    out=out.resolve()
    if core.digest(source)!=base or out.is_relative_to(CLIENT.resolve()):
        raise ValueError("require the existing localized DSTR and separate output")
    out.mkdir(parents=True)
    backup, changed=out/"before-dstr.dat",out/"dstr.dat"
    core.file_copy(source,backup)
    key=key_pair(exe)
    raw=unpack(backup.read_bytes(),key)
    new=pack(replace(raw,rows),key)
    diff=out/"DSTR.diff.json"
    core.atomic(diff,{"schema":"100US SC01 DSTR keyed patch v1","before":base,"profile":profile,
                     "after":hashlib.sha256(new).hexdigest(),
                     "rows":[dict(id=k,before=a,after=b) for k,(a,b) in rows.items()]})
    apply(backup,diff,exe,changed,profile)
    rollback=out/"ROLLBACK.sh"
    core.file_copy(Path(__file__).with_name("us100_status_rollback.sh"),rollback)
    rollback.chmod(0o755)
    copy=out/"rollback-test.dat"
    core.file_copy(changed,copy)
    exe=exe.resolve()
    ledger=[f"UTC: {datetime.now(timezone.utc).isoformat()}","Scope: DSTR display rows only",
            f"Profile: {profile}",f"Baseline SHA-256: {base}",f"Modified SHA-256: {core.digest(changed)}",
            "Changed DSTR IDs: "+", ".join(rows),""]
    def run(action,*argv):
        cmd=[str(v) for v in argv]
        p=subprocess.run(cmd,capture_output=True,encoding="utf-8",env={**os.environ,"PYTHONIOENCODING":"utf-8"})
        ledger.extend([action,"argv="+json.dumps(cmd,ensure_ascii=False),f"exit={p.returncode}",
                       "stdout:",p.stdout,"stderr:",p.stderr])
        if p.returncode:
            raise RuntimeError(f"{action} failed: {p.stderr}")
        return p.stdout
    def probe(action,path):
        return run(action,sys.executable,Path(__file__).resolve(),"inspect","--file",path,"--exe",exe,
                   "--profile",profile)
    try:
        original=probe("BASELINE",backup)
        local=probe("MODIFIED",changed)
        run("ROLLBACK.sh",sys.executable,rollback,copy)
        restored=probe("ROLLBACK",copy)
        if (original!=restored or core.digest(copy)!=base
                or json.loads(original)["labels"]!={k:v[0] for k,v in rows.items()}
                or json.loads(local)["labels"]!={k:v[1] for k,v in rows.items()}):
            raise ValueError("baseline/modified/rollback behavior mismatch")
        verification=out/"reapplied-dstr.dat"
        run("REAPPLY",sys.executable,Path(__file__).resolve(),"apply","--baseline",copy,
            "--diff",diff,"--exe",exe,"--out",verification,"--profile",profile)
        if core.digest(verification)!=core.digest(changed):
            raise ValueError("reapplied archive differs")
        ledger.append("Restored SHA-256: "+core.digest(copy))
        ledger.append("Reapplied SHA-256: "+core.digest(verification))
        ledger.append("No game display validation was performed by this transaction.")
    finally:
        (out/"VERIFICATION.txt").write_text("\n".join(ledger)+"\n",encoding="utf-8")
    for role in (changed,diff,out/"VERIFICATION.txt",rollback):
        print(f"{role}: {core.digest(role)}")


def deploy(out, rollback=False,profile="status"):
    base,_,_=PROFILES[profile]
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()):
        raise ValueError("require an offline bundle")
    backup,patch,live=out/"before-dstr.dat",out/"dstr.dat",CLIENT/"dstr.dat"
    m=core.read(out/"DSTR.diff.json")
    service=GameFonts(CLIENT/"Fonts")
    with service.locked():
        current,wanted=(m["after"],base) if rollback else (base,m["after"])
        if (m.get("profile","status")!=profile or m["before"]!=base
                or core.digest(backup)!=base or core.digest(live)!=current):
            raise ValueError("live file or backup identity changed")
        if not rollback and (core.digest(patch)!=wanted
                or (profile in ("character-name","weapon-tooltip","skill-reward-ui","system-reward-notices","hud-priority","dungeon-clear-ui","quest-recommend","dungeon-level-notices")
                    and core.digest(out/"reapplied-dstr.dat")!=wanted)
                or service.hashes() not in (OUTLINE_FONTS,CLEAR_FONTS)
                or core.digest(CLIENT/REL)!=(LEVEL_QUEST if profile=="dungeon-level-notices" else ACT)
                or core.digest(CLIENT/"DFO.exe")!=EXE
                or core.digest(CLIENT/"Script.pvf") not in (
                    {LEVEL_PVF} if profile=="dungeon-level-notices" else
                    {REC_PVF} if profile=="quest-recommend" else
                    {CLEAR_PVF} if profile=="dungeon-clear-ui" else
                    {EXPIRY_PVF} if profile in ("inventory-timed-items","inventory-system-notices","character-name",
                                                "weapon-tooltip","skill-reward-ui","system-reward-notices","hud-priority") else
                    {EQUIP_PVF} if profile in ("character-create","shop-small-tabs","equipment-tooltip") else {PVF,EXPECTED_TRIAL})):
            raise ValueError("installed client resources or candidate changed")
        if core.client_up() or core.digest(live)!=current:
            raise ValueError("live file changed or DFO reopened")
        core.file_copy(backup if rollback else patch,live)
        if core.digest(live)!=wanted:
            raise ValueError("deployed DSTR hash mismatch")
        core.atomic(out/"deployment.json", {"utc":datetime.now(timezone.utc).isoformat(),
                    "status":"rolled_back" if rollback else "installed_pending_game_check",
                    "file":str(live),"before":current,"after":wanted,"changed_only":"dstr.dat"})
        print(f"Verified live {live}: {wanted}")


if __name__=="__main__":
    if "avatar-ui" in sys.argv[1:]:
        register_avatar_ui_profile()
    if "equipment-tooltip" in sys.argv[1:]:
        register_equipment_tooltip_profile()
    if "character-create" in sys.argv[1:]:
        register_create_profile()
    p=argparse.ArgumentParser(description=__doc__)
    commands=p.add_subparsers(dest="action",required=True)
    for action,fields in (("build",("source","exe","out")),("inspect",("file","exe")),
                          ("apply",("baseline","diff","exe","out")),("install",("out",)),
                          ("rollback",("out",))):
        q=commands.add_parser(action)
        for field in fields:
            q.add_argument("--"+field,type=Path,required=True)
        q.add_argument("--profile",choices=PROFILES,default="status")
    a=p.parse_args()
    if a.action=="build":build(a.source,a.exe,a.out,a.profile)
    elif a.action=="inspect":inspect(a.file,a.exe,a.profile)
    elif a.action=="apply":apply(a.baseline,a.diff,a.exe,a.out,a.profile)
    else:deploy(a.out,a.action=="rollback",a.profile)
