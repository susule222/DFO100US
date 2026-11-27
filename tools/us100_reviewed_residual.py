# -*- coding: utf-8 -*-
"""Exact human-written sentences and narrowly matched US100 residual templates."""
import re


LINES = {
    "DFO": "地下城与勇士",
    "EXP": "经验值",
    "Fp": "疲劳值",
    "STR": "力量",
    "INT": "智力",
    "VIT": "体力",
    "SPR": "精神",
    "SPI": "精神",
    "DFOpedia": "地下城与勇士百科",
    "DPS/Synergy": "输出/协同",
    "Cargo/ETC": "货物/其他",
    "[DIO]": "[迪奥]",
    "Rrrrrrrrrrrr!": "噜噜噜噜！",
    "DJ Puppy": "DJ小狗",
    "Casting Time %.*fSec(s)": "施放时间 %.*f秒",
    "TP:": "特性技能点（TP）：",
    "SP:": "技能点（SP）：",
    "ID:": "ID：",
    "ETC": "其他",
    "DPL : E": "DNF职业联赛：E",
    "A keyword must be entered to search the \"All\" category.": "搜索「全部」分类时必须输入关键词。",
    "you can't have a second chance.": "你不会再有第二次机会。",
    "You can't have a second chance.": "你不会再有第二次机会。",
    "Dungeon can be entered after accepting \"The Merchant's Test\" quest from NPC Merchant Triss.": (
        "从NPC商人特丽丝处接受「商人的考验」任务后，即可进入地下城。"
    ),
    "Here are the Avatars that you've selected in the Showroom!": "这是你在展柜中选择的装扮！",
    "Here are the rewards obtained from the board game.": "这是你在棋盘游戏中获得的奖励。",
    "Alpha Go": "阿尔法围棋",
    "DFOthello": "地下城与勇士黑白棋",
    "Ra~": "啦~",
    "ZZZ...": "呼噜……",
    "*hic*": "*嗝*",
    "Enables you a one-time name change for your creature; entering \"Nameless\" resets the creature's name to its default.": (
        "可为宠物改名一次；输入 \"Nameless\" 可将宠物的名字恢复为默认名称。"
    ),
    "<DFO x G-Star>": "<地下城与勇士 × G-Star>",
    "< DD x DFO >": "< DD × 地下城与勇士 >",
    "VIT/SPR": "体力/精神",
    "If you haven't entered \"Start Adventure\" yet,": "如果尚未进入 \"Start Adventure\"（开始冒险），",
    "To readjust the current ward, enter \"             .\"": "要重新调整当前结界，请输入 \"             .\"",
    "STR/VIT/INT/SPR": "力量/体力/智力/精神",
    "bytes/Total": "字节/总计",
    "SD Ram": "迷你拉姆",
    "SD Rem": "迷你蕾姆",
    "SD Puck": "迷你帕克",
    "SD Miku": "迷你初音未来",
    "SD Hailey": "迷你海莉",
    "Harp Seal": "竖琴海豹",
    "XOXO": "亲亲抱抱",
    "Rrrr...": "呜噜噜……",
    "<Art DFO>": "<地下城与勇士艺术展>",
    "<CheerUP!!>": "<加油！！>",
    "<BINGO 2020>": "<宾果 2020>",
    "<DFO 15th>": "<地下城与勇士15周年>",
    "<FANTASTIC☆JUMPING>": "<梦幻☆跳级>",
    "<Chicken Maru x DFO>": "<Chicken Maru × 地下城与勇士>",
    "<OverpowerBalance>": "<力量平衡>",
    "<Pit-a-pat Hero's Dreams>": "<怦然心动的英雄之梦>",
    "<CERA & GIFT>": "<点券和礼物>",
    "< Re:ZERO >": "< Re:从零开始的异世界生活 >",
    "<happY. turkeY. daY!>": "<感恩节快乐！>",
    "Pinky": "粉扑扑",
    "Roundy": "圆滚滚",
    "GRRRRRRR...": "嗷呜呜呜……",
    "Grrrrrrrr.....": "呜噜噜噜……",
    "<Re:ZERO>": "<Re:从零开始的异世界生活>",
    "< Gazua 2019 >": "< 奔向2019 >",
    "Contains 8 pieces of untradable Re:ZERO Avatars.": "内含8件无法交易的《Re:从零开始的异世界生活》联动装扮。",
    "Contains MIKUMIKU Collaboration Avatars (6 pieces).": "内含初音未来联动装扮（6件）。",
    "Contains a set of special Christmas Avatars.": "内含一套特别圣诞装扮。",
    "Contains a set of Contender Avatars": "内含一套挑战者装扮。",
    "Kikki Mart": "琪琪商店",
    "Bugi": "布吉",
    "Chain Drive Atk. +6%%, Cooldown -6%%": "暗街夺命锁攻击力 +6%%，冷却时间 -6%%",
    "Cave-in Small Shard Atk. +10%%Big Shard Atk. +30%%": "飞沙走石小碎块攻击力 +10%%，大碎块攻击力 +30%%",
    "Elemental Field Atk. +6%%, Cooldown -6%%Convergence Cannon Upgrade": "元素禁域攻击力 +6%%，冷却时间 -6%%；强化聚能魔炮",
    "Elemental Field Atk. +6%%, Cooldown -6%%Convergence Cannon Atk. +10": "元素禁域攻击力 +6%%，冷却时间 -6%%；聚能魔炮攻击力 +10",
    ", Range +20%%": "，范围 +20%%",
    "Physical Atk. Increased Slightly": "略微提高物理攻击力",
    "Magic Atk. Increased Slightly": "略微提高魔法攻击力",
    "Base Atk. Increased Slightly": "略微提高基础攻击力",
    "Stat Increased Slightly": "略微提高属性",
    "+30 MAX HP": "HP上限 +30",
    "+30 MAX MP": "MP上限 +30",
    "Attack Speed +1%%": "攻击速度 +1%%",
    "Movement Speed +1%%": "移动速度 +1%%",
    "Casting Speed +1%%Convergence Cannon Atk. +2%%, Cooldown -2": "施放速度 +1%%；聚能魔炮攻击力 +2%%，冷却时间 -2",
    "Casting Speed +1%%Shard Magnum Atk. +2%%, Cooldown -2": "施放速度 +1%%；碎冰破攻击力 +2%%，冷却时间 -2",
    "Attack/Movement Speeds +3%% for every 15 Shadow Resistance (up to +12%%)": (
        "攻击/移动速度 +3%%（每15点暗属性抗性叠加一次，最高+12%%）。"
    ),
    "Casting Speed +4.5%% for every 15 Shadow Resistance (up to +18%%)": (
        "施放速度 +4.5%%（每15点暗属性抗性叠加一次，最高+18%%）。"
    ),
    "Skills create a 15%% chance of generating Star Energy and increasing Additional Attack Damage +10%% for 60 sec. (Max Stacks: 1. Up to 3 Star Energies can be created.)": (
        "使用技能时，有15%%的概率生成星辰能量，并使攻击伤害额外增加 +10%%，持续60秒。"
        "（最多叠加1层，最多生成3点星辰能量。）"
    ),
    "Wespeace": "韦斯皮斯",
    "[Griggly B]": "[格里格利 B]",
    "*LOL*": "*哈哈*",
    "*Yum Yum*": "*真好吃*",
    "Here are some exclusive gifts to help make your return more enjoyable!": "为你准备了一些回归专属礼物，助你轻松重返阿拉德！",
    "Please note, all items in the box are account bound EXCEPT for the Demon Invitation Generator.": (
        "请注意：除深渊邀请函生成器外，礼盒内所有道具均为账号绑定。"
    ),
    "Contained Avatars has 2 emblem sockets, and cannot be traded/sundered/combined.": "所含装扮有2个徽章镶嵌栏，无法交易、分解或合成。",
    "Creates a chance at reinforcing equipment.": "有一定概率强化装备。",
    "Does not consume any Clear Cube Fragments or Gold.": "不消耗无色小晶块或金币。",
    "Using skills has a 30% chance of granting +4 to all Lv. 1 - 100 skill levels for 60 seconds.": (
        "使用技能时，有30%的概率使技能等级 +4（适用于Lv. 1 - 100的所有技能），持续60秒。"
    ),
    "Critical Chance +10%": "暴击率 +10%",
    "Boss Unique drop rate increased according to a number of party members using this contract.": (
        "队伍中使用该契约的成员越多，领主掉落神器装备的概率越高。"
    ),
    "Casts Dismantle Junk when equipped. (Option canceled when unequipped.)": (
        "穿戴时触发「拆解废品」。（卸下时解除效果。）"
    ),
    "Press the Equipment Option Control key to drop a Big Junk Pillar in front of you. (Cooldown: 20 sec.)": (
        "按下装备属性指令键，在前方落下一根巨型废铁柱。（冷却时间：20秒。）"
    ),
    "AP is used for registering Avatars and Creatures on the Auction Hall and for prices that exceed the Auction Hall's upper limit.": (
        "AP用于在拍卖行登记装扮及宠物，也可用于登记价格超过拍卖行上限的商品。"
    ),
    "Attacking and casting create a 5%% chance of Hit Rate +3%%, Attack/Casting Speeds +3%%, and Movement Speed +5%%": (
        "攻击或施放时，有5%%的概率使命中率 +3%%、攻击/施放速度 +3%%、移动速度 +5%%。"
    ),
    "Enables you to select one of the following two effects upon entering dungeons. (The For Party effect is selected by default. Use the Auction Hall (B) key to switch to the For You effect. Switching cooldown: 5 sec.)": (
        "进入地下城时，可选择以下两种效果之一。（默认选择队伍效果。按拍卖行键（B）可切换为自身效果，切换冷却时间为5秒。）"
    ),
    "Selectable Effects": "可选效果",
    "- For party: Incoming damage -10%% for party members in 900 px (Max Stacks: 1)": (
        "- 队伍：所受伤害 -10%%（适用于900像素内的队员，最多叠加1层）。"
    ),
    "- For you: Incoming damage -15%%": "- 自身：所受伤害 -15%%。",
    "All Elemental Resistances -25 for all enemies within a 300 px range while equipped": (
        "穿戴时，所有属性抗性 -25（适用于300像素范围内的所有敌人）。"
    ),
    "Casts an aura that decreases all Abnormal Tolerances -50 for all enemies within a 500 px range.": (
        "释放光环，使所有异常状态抗性 -50（适用于500像素范围内的所有敌人）。"
    ),
    "Additional 5%% damage on enemies in Abnormal statuses": "附加5%%伤害（攻击异常状态的敌人时）。",
    "Curses/Burns/Shocks all enemies within 300 px for 10 sec. every 2 sec.": (
        "使300像素内的所有敌人陷入诅咒、灼伤及感电状态，持续10秒，每2秒触发一次。"
    ),
    "- Burns enemies, inflicting damage at 100 Atk. every 0.5 seconds for 10 sec.": (
        "- 使敌人灼伤，以100点攻击力每0.5秒造成一次伤害，持续10秒。"
    ),
    "- Shocks enemies, inflicting additional damage at 100 Atk. with every attack for 10 sec.": (
        "- 使敌人感电，之后每次攻击以100点攻击力造成额外伤害，持续10秒。"
    ),
    "- Curses enemies, decreasing STR/VIT/INT/SPR -10 for 10 sec.": (
        "- 使敌人陷入诅咒，力量/体力/智力/精神 -10，持续10秒。"
    ),
    "(This equipment's Curse chance and effect decrease for targets at Lv. 91 or above and increase at Lv. 90 or below.)": (
        "（对Lv. 91及以上目标，诅咒概率和效果降低；对Lv. 90及以下目标，诅咒概率和效果提高。）"
    ),
    "Attack Damage +17%%": "攻击伤害 +17%%",
    "Curses the nearest enemy within a 500 px range, decreasing Strength/Vitality/Intelligence/Spirit -50 for 10 seconds every 10 seconds, while equipped.": (
        "穿戴时，对500像素范围内最近的敌人施加诅咒，使其力量/体力/智力/精神 -50，持续10秒，每10秒触发一次。"
    ),
    "Attacks Curse their targets, decreasing Strength/Vitality/Intelligence/Spirit -50 for 5 seconds.": (
        "攻击时诅咒目标，使其力量/体力/智力/精神 -50，持续5秒。"
    ),
    "(The chance and effect decrease if the targets are Lv. 91 or above, and increase if Lv. 90 or below. Cooldown: 1 sec.)": (
        "（对Lv. 91及以上目标，触发概率和效果降低；对Lv. 90及以下目标，触发概率和效果提高。冷却时间：1秒。）"
    ),
    "Endows your weapon with Light Attribute for 15 seconds at 30-second intervals.": (
        "为武器附加光属性，持续15秒，每隔30秒触发一次。"
    ),
    "Dragon Road Event Cape": "龙之路活动披风",
    "Set effects when used with Rare Avatars.": "与稀有装扮一起穿戴时适用套装效果。",
    "Set effects when used with Advanced Avatars.": "与高级装扮一起穿戴时适用套装效果。",
    "Contains DFO Arena Avatars given as a reward.": "内含作为奖励发放的地下城与勇士决斗场装扮。",
    "Contains Icecream(Hat, Top and Shoes) Avatars.": "内含冰淇淋装扮（帽子、上衣及鞋）。",
    "Adds 2 Multicolored Emblem socks to Skin Avatars.": "为皮肤装扮添加2个彩色徽章镶嵌栏。",
    "Can be upgraded upon combining Avatars.": "合成装扮时可以进行升级。",
    "Generates 8 different kinds of Untradable Rare Clone Avatars.": "可获得8件无法交易的稀有克隆装扮。",
    "Generates 8 different Clone Rare Avatars (untradeable).": "可获得8件无法交易的稀有克隆装扮。",
    "Increases character's damage by 10%%%% and Attack/Movement/Casting Speed by 5%%%% for 20 seconds.": (
        "角色伤害增加10%%%%，攻击/移动/施放速度增加5%%%%，持续20秒。"
    ),
    "Clear any dungeon with using 20 or more Clear Cube Fragments.": "消耗至少20个无色小晶块并通关任意地下城。",
    "If HP falls below 50%%, Strength/Intelligence +15.": "HP低于50%%时，力量/智力 +15。",
    "Every 10%% loss of HP from 50%% increases the stats +15.": "HP每减少10%%（以50%%为基准），属性再 +15。",
    "Up to 75 Strength/Intelligence bonuses": "力量/智力最多增加75点。",
    "If 100%% MP, MP -15%%": "MP达到100%%时，MP -15%%。",
    "Every 1%% loss of MP from 100%% MP increases Physical/Magic Critical Chances +1%%.": (
        "MP每减少1%%（以100%%为基准），物理/魔法暴击率 +1%%。"
    ),
    "Every 1%% loss of MP from 100%% MP increases Physical/Magic Critical Chances +1%% (up to 15%%).": (
        "MP每减少1%%（以100%%为基准），物理/魔法暴击率 +1%%（最高15%%）。"
    ),
    "Casts Devil's Temptation when equipped.": "穿戴时发动恶魔诱惑。",
    "During Devil's Temptation,": "恶魔诱惑持续期间：",
    "- HP -0.5%% every sec. (HP can't fall below 666 by this option.)": "- HP每秒 -0.5%%（该词条不会使HP低于666）。",
    "- HP +0.6%% for every 6 hits": "- HP +0.6%%（每命中6次触发一次）。",
    "- HP +6%% with each kill": "- 每击杀一名敌人，HP +6%%。",
    "If 50%% or less HP, activates the following effects.": "HP不高于50%%时，触发以下效果。",
    "Explodes your spirit, which has been sold to the demon, when you die.": "阵亡时，已出卖给恶魔的灵魂将发生爆炸。",
    "[Dimensional Force Shield Effect]": "[次元力盾效果]",
    "Casts Dimensional Force Shield at the cost of 1 Clear Cube Fragment every 20 sec.": (
        "发动次元力盾，消耗1个无色小晶块，每20秒发动一次。"
    ),
    "Attacks cast Deadly Poison on enemies for 10 sec.": "攻击时使敌人陷入剧毒状态，持续10秒。",
    "Attacks cast Deadly Poison on enemies for 10 sec. (Max Stacks: 1)": "攻击时使敌人陷入剧毒状态，持续10秒。（最多叠加1层。）",
    "HP +0.5%% for every 10 attacks on Deadly Poisoned enemies": "HP +0.5%%（每攻击剧毒状态的敌人10次触发一次）。",
    "Generates 1 Star Energy when equipped. (Once only inside dungeons, and reset upon resurrection.)": (
        "穿戴时生成1点星辰能量。（在地下城内仅触发一次，复活后重置。）"
    ),
    "Skills create a 20%% chance of generating Star Energy.": "使用技能时，有20%%的概率生成星辰能量。",
    "If 1 or more Star Energies, press the Equipment Option Control key to create 5 Shooting Stars and increase all Atk. +18%% for 60 sec. at the cost of 1 Star Energy. (Max Stacks: 1)": (
        "星辰能量达到1点时，按装备属性指令键生成5颗流星，并使所有攻击力 +18%%，持续60秒；消耗1点星辰能量。（最多叠加1层。）"
    ),
    "(Up to 3 Star Energies can be generated at a time. All options canceled when unequipped.)": (
        "（同时最多生成3点星辰能量，卸下装备时解除所有词条效果。）"
    ),
    "This Spearhead Flag casts an aura, increasing Attack Speed +15%% for all party members within 500 px around you.": (
        "先锋旗释放光环，使攻击速度 +15%%（适用于你周围500像素内的所有队员）。"
    ),
    "Puppeteer INT +127": "人偶操纵者智力 +127",
    "Lv. 30 Buff skill application range +20%%": "Lv. 30增益技能作用范围 +20%%",
    "Skills create a 25%% of creating a Snowy White Flower Garden with the following effects for 60 sec. (Max Stacks: 1)": (
        "施放技能时，有25%%的概率生成带有以下效果的白雪花园，持续60秒。（最多叠加1层。）"
    ),
    "- Hot Affection buff duration +10%%": "- 火热的爱意增益持续时间 +10%%",
    "- Curse of Terror Bear ally buff duration +30%%": "- 咆哮吧！疯疯熊对队友的增益持续时间 +30%%",
    "- Eternal Possession casts a barrier around the resurrected party member, decreasing incoming damage -50%% for 5 sec.": (
        "- 永恒的占据为复活的队友施加护盾，使所受伤害 -50%%，持续5秒。"
    ),
    "- Party members' HP +0.5%% every sec. within Snowy White Flower Garden": "- 白雪花园内的队员HP +0.5%%（每秒触发一次）。",
    "Quick Rebound creates a 30%% chance of creating a shockwave.": "使用受身蹲伏时，有30%%的概率产生冲击波。",
    "Quick Rebound creates a 30%% chance of creating a shock wave.": "使用受身蹲伏时，有30%%的概率产生冲击波。",
    "Damage from enemy attacks creates a 10%% chance of granting Strength/Intelligence/Vitality/Spirit +2%% for 20 seconds.": (
        "受到敌人攻击时，有10%%的概率使力量/智力/体力/精神 +2%%，持续20秒。"
    ),
    "Incoming damage creates a 10%% chance of STR/INT/VIT/SPI +2%% for 20 sec. (Max Stacks: 2. Cooldown: 10 sec. Option canceled when unequipped.)": (
        "受到伤害时，有10%%的概率使力量/智力/体力/精神 +2%%，持续20秒。（最多叠加2层，冷却时间10秒；卸下时解除。）"
    ),
    "Bestows your weapon with Light Attribute for 15 seconds at 30-second intervals. (Activates 10 seconds after equipped.)": (
        "为武器附加光属性，持续15秒，每隔30秒触发一次。（穿戴10秒后开始生效。）"
    ),
    "Equipping casts Seria's blessing which increases Strength and Intelligence by 45 and Vitality and Spirit by 30 for 20 seconds at 40 second intervals. (Activates 40 seconds after equipping.)": (
        "穿戴时触发赛丽亚的祝福，使力量/智力提高45点，体力/精神提高30点，持续20秒，每隔40秒触发一次。"
        "（穿戴40秒后开始生效。）"
    ),
    "Generates a Valhalla Bead for the class you want.": "可选择获得指定职业的英灵殿宝珠。",
    "* Contents Info": "* 内容说明",
    "These Beads, when used on Valhalla titles, create a chance of adding a Level 15 - 70 skill for the corresponding character class at Level 1 - 2.": (
        "用于英灵殿称号时，有一定概率使对应职业的15 - 70级技能提升1 - 2级。"
    ),
    "Beads are issued in tradeable form, and can only be used on Valhalla titles.": "宝珠可交易，仅可用于英灵殿称号。",
    "%dD": "%d天",
    "%dH": "%d小时",
    "%dM": "%d分钟",
    "Lv %d": "等级 %d",
    "%dea %s": "%d个 %s",
    "Resealed(%dTimes)": "再封装（%d次）",
    "%dSec(s)": "%d秒",
    "%dM %dSecs(s)": "%d分钟 %d秒",
    "%s (%dEA)": "%s（%d个）",
    "%ds": "%d秒",
    "%dh": "%d小时",
    "%dm": "%d分钟",
    "%s %dF": "%s 第%d层",
    "Next Refresh : %dMonth %dDay %dHour %dMinute": "下次刷新：%d月%d日%d时%d分",
    "Cooldown: %dH %dM": "冷却时间：%d小时%d分钟",
    "Cooldown: %dH": "冷却时间：%d小时",
    "%dH %dM": "%d小时%d分钟",
    "%dD %dH": "%d天%d小时",
    "%dline": "%d行",
    "%dLv": "%d级",
    "ToD(%dF): %d/%d": "死亡之塔（第%d层）：%d/%d",
    "ToA(%dF): %d/%d": "悲叹之塔（第%d层）：%d/%d",
    "%dx": "%d次",
    "%s has defeated %s and conquered the %dth floor of the Tower of Anguish.": "%s击败了%s，征服了悲叹之塔第%d层。",
    "Craft Time: %dH %dM": "制作时间：%d小时%d分钟",
    "%dH %dM %dS": "%d小时%d分钟%d秒",
    "%dM %dS": "%d分钟%d秒",
    "%dS": "%d秒",
    "Crafting Time: %dH %dM %dS": "制作时间：%d小时%d分钟%d秒",
    "AP: %dp": "AP：%d点",
    "%dG": "%d金币",
    "%dh %dm %ds": "%d小时%d分钟%d秒",
    "Advanced Hell Party Ticket (Remaining Duration: %dmin(s).)": "高级深渊派对通行证（剩余%d分钟）。",
    "Advanced Hell Party Ticket is activated.": "已激活高级深渊派对通行证。",
    "(Remaining Duration: %dmin(s).)": "（剩余%d分钟。）",
    "The Guild Hideout has been removed on %dy %dm %dt by %s.": "公会藏身处于%d年%d月%d日由%s移除。",
    "1st Awakening Clear Ticket": "第1次觉醒任务完成券",
    "2nd Awakening Quest Clear Ticket": "第2次觉醒任务完成券",
    "불로초": "不老草",
    "오오아아아아아~": "哦哦啊啊啊~",
    "컵라면이 설익었어..ㅜㅜ": "杯面还没泡熟……呜呜。",
    "단거먹쟈~♡": "吃点甜的吧~♡",
    "냐금냐금": "啊呜啊呜",
    "깍두기": "萝卜泡菜",
    "날갯죽지": "翅根",
    "<한글날 기념>": "<韩文日纪念>",
    "[백성 민]우리 한글을 널리 알려주세요": "[百姓之民]请和我们一起推广韩文",
    "<열파참 : 열번째 파티에 참여하세요!>": "<裂波斩：参加第十场派对！>",
    "<던파 X G마켓>": "<地下城与勇士 × G市场>",
    "던스타그램 참가보상": "DNF晒图活动参与奖励",
    "개발자 축전 이벤트": "开发者贺卡活动",
    "던스타그램 이벤트": "DNF晒图活动",
    "<소모 재료>": "<消耗材料>",
    "- 반물질 입자 96개": "- 反物质粒子96个",
    "강화/증폭/마법부여 옵션이 유지됩니다.": "保留强化、增幅与附魔属性。",
    "보주는 상의/하의/무기에 부여가 가능합니다.": "宝珠可附魔于上衣、下装或武器。",
    "보주는 계정귀속 아이템 입니다.": "宝珠为账号绑定道具。",
    "낮은 확률로 증폭 보호권 아이템이 등장합니다.": "有较低概率获得增幅保护券。",
    "Burp.": "嗝。",
    "RAWR!": "嗷呜！",
    "<Fall Bingo!>": "<秋日宾果！>",
    "<Farming! 5959>": "<刷图！5959>",
    "<DPL: E>": "<DNF职业联赛：E>",
    "Hatsune Miku's Boots": "初音未来的靴子",
    "Laurel Wreath": "月桂花冠",
    "Poult Hat": "雏鸟帽",
    "Changes a selected Avatar stat.": "变更一项选定的装扮属性。",
    "Changes a selected Rare Avatar stat.": "变更一项选定的稀有装扮属性。",
    "Can't be used on Rare/(Old) Normal Avatars and those that have no optional stats.": (
        "不可用于稀有装扮、（旧版）普通装扮及没有可选属性的装扮。"
    ),
    "Can't be used on Danjin's Cape and Legionnaire's Avatars.": "不可用于土罐的披风及军团装扮。",
    "Fast Leveling characters can't use this item.": "快速升级角色无法使用该道具。",
    "Can't be used in conjunction with the regular Growth Potions.": "不能与普通成长药剂同时使用。",
    "When used, this item is moved to your Skin Storage.": "使用后，该道具将移入皮肤仓库。",
    "You can change your 1st Awakening Skill cutscene to this item's in the Skin Storage.": (
        "可在皮肤仓库中使用该道具更换第1次觉醒技能的插画演出。"
    ),
    "Not for the Side Story characters.": "外传职业不适用。",
    "Shares cooldown with Warlord's Cry Potions.": "与斗神之吼秘药共用冷却时间。",
    "Shares cooldown with Remy's Sparkling Touch.": "与闪亮的雷米援助共用冷却时间。",
    "(Certain skills excluded. Level 1 - 70 skills for the Side Story characters.)": (
        "（部分技能不适用；外传职业适用1 - 70级技能。）"
    ),
    "This Special Title Bead can only be used on the [Special] Welcome to Hell titles.": (
        "此特别称号宝珠仅可用于[特别]欢迎来到深渊称号。"
    ),
    "This Bead can only be used on Untradable or Untradable(Sealed) Titles.": (
        "此宝珠仅可用于无法交易或无法交易（封装）状态的称号。"
    ),
    "This Bead can only be used on Untradable or Untradable (Sealed) titles.": (
        "此宝珠仅可用于无法交易或无法交易（封装）状态的称号。"
    ),
    "Will of the Virtuous [Fire]": "贤者的意志 [火]",
    "Will of the Virtuous [Water]": "贤者的意志 [冰]",
    "Will of the Virtuous [Light]": "贤者的意志 [光]",
    "Will of the Virtuous [Shadow]": "贤者的意志 [暗]",
    "A gauge is accumulated for every use of a Bead, and you can select and imbue +2 to an option of your choice "
    "when using a Bead at full gauge. Gauge is reset if the target Title is moved between accounts through trading, "
    "the Auction Hall, mail, and etc.": (
        "每次使用宝珠都会增加进度。进度满后，可再次使用宝珠为所选词条附加+2级。"
        "通过交易、拍卖行或邮件等方式将该称号转移至其他账号时，进度将重置。"
    ),
    "When you purchase the Arabia Package, you can receive a Magic Lamp. (Provided only at the first purchase per character.)": (
        "购买阿拉伯礼包可获得魔法神灯。（每个角色仅首次购买时获得。）"
    ),
    "Improves equipped Talisman skills. (Talisman Book skill standards applied. Buffer characters excluded.)": (
        "强化已装备护石对应的技能。（以护石图鉴技能为准；辅助职业不适用。）"
    ),
    "Cannot be traded, Crafted, or Disassembled.": "无法交易、制作或分解。",
    "Cannot be reinforced, amplified, refined, disassembled, gifted, or stored in the Storage.": (
        "无法强化、增幅、锻造、分解、赠送或存入仓库。"
    ),
    "The Skin Avatar has no Emblem sockets. These Avatars are issued in tradeable form.": (
        "皮肤装扮没有徽章镶嵌栏。获得的装扮可交易。"
    ),
    "The Skin Avatars do not have Emblem Sockets. All the Avatars are issued in tradeable form.": (
        "皮肤装扮没有徽章镶嵌栏。获得的所有装扮均可交易。"
    ),
    "All the chests are issued in tradable form.": "获得的所有礼盒均可交易。",
    "All the Chests are issued in tradeable form.": "获得的所有礼盒均可交易。",
    "It's a new year! Whoever the culprit is, please identify yourself!": "新的一年到了！肇事者是谁？请自己站出来！",
    '"Changes your character image to the Culprit\'s. (Overwrites all the other Avatars\' images, no matter how many you have equipped.)"': (
        '"将角色外观变为肇事者的样子。（无论穿戴了多少其他装扮，都会覆盖其外观。）"'
    ),
    "Attacking increases Maddening Energy +1.": "攻击时，疯狂能量 +1。",
    "Attacking increases Maddening Energy +1. (Cooldown: 0.5 sec. Max: 100)": (
        "攻击时，疯狂能量 +1。（冷却时间：0.5秒；上限：100。）"
    ),
    "If 10 or higher Maddening Energy, Physical/Magical/Independent Atk. +10%%": (
        "疯狂能量不低于10时，物理/魔法/独立攻击力 +10%%。"
    ),
    "If 1 or less combo while enemies exist in the map, Maddening Energy -2 every 3 sec.": (
        "地图内存在敌人且连击数不高于1时，疯狂能量 -2（每3秒触发一次）。"
    ),
    "If you lose 5%% of Max HP at once, Maddening Energy -3": (
        "单次损失相当于最大HP 5%%的生命值时，疯狂能量 -3。"
    ),
    "If you lose 5%% of Max HP at once, Maddening Energy -3 (Cooldown: 1 sec.)": (
        "单次损失相当于最大HP 5%%的生命值时，疯狂能量 -3。（冷却时间：1秒。）"
    ),
    "[Arad Grand Clash Avatars]": "[阿拉德大乱斗装扮]",
    "- Weapon Avatars excluded": "- 不含武器装扮",
    "- Transparent/Clone Avatars excluded": "- 不含透明/克隆装扮",
    "- Event and other special Avatars excluded": "- 不含活动及其他特殊装扮",
    "- Avatars registered in the DFO Showroom (Last updated: August 22, 2019)": (
        "- 地下城与勇士展柜中登记的装扮（最近更新：八月22日，2019年）"
    ),
    "All the Avatars except the Skin have Emblem sockets.": "除皮肤装扮外，所有装扮均有徽章镶嵌栏。",
    "Weapon Avatars are issued in untradeable and undisassemblable form.": "获得的武器装扮无法交易或分解。",
    "All the generated Avatars except the Skin last permanently, and can be traded, combined, converted, dyed, and disassembled.": (
        "除皮肤装扮外，获得的装扮均为永久，可交易、合成、转换、染色及分解。"
    ),
    "Hyperdimension Shoes": "超次元鞋子",
    "<HiddenMisson>": "<隐藏任务>",
    "Head/Shoulder": "头肩",
    "STR/INT": "力量/智力",
    "STR/INT/VIT/SPR": "力量/智力/体力/精神",
    "STR: +<quorum>": "力量：+<quorum>",
    "INT: +<quorum>": "智力：+<quorum>",
    "VIT: +<quorum>": "体力：+<quorum>",
    "SPR: +<quorum>": "精神：+<quorum>",
    "HP/MP +15%%": "生命值/魔法值 +15%%",
    "Use on [Special] Welcome to Hell titles to create a chance of adding a Level 15 - 70 skill for the "
    "corresponding Advanced class at 1 - 2 higher levels.": (
        "用于[特别]欢迎来到深渊称号时，有一定概率使对应转职的15 - 70级技能提升1 - 2级。"
    ),
    "Use on Seria's Gift titles to add 1 level to a Level 15 - 70 skill of the corresponding Advanced class. "
    "(Certain skills excluded. Level 1 - 70 skills for the Side Story characters.)": (
        "用于赛丽亚的礼物称号，使技能提升1级；适用于对应转职的15 - 70级技能。"
        "（部分技能不适用；外传职业适用1 - 70级技能。）"
    ),
    "Use on Divine Revelation titles to create a chance of adding a Level 15 - 70 skill for the corresponding Advanced class at Level 2.": (
        "用于神圣启示称号时，有一定概率使对应转职的15 - 70级技能提升2级。"
    ),
    "Attacks create a 5%% chance of STR/VIT/INT/SPR +25 and Attack/Casting/Movement Speeds +2%% for 30 sec.": (
        "攻击时有5%%的概率使力量/体力/智力/精神 +25，攻击/施放/移动速度 +2%%，持续30秒。"
    ),
    "(Option canceled when unequipped. Option disabled during casting. Cooldown: 30 sec.)": (
        "（卸下时解除效果；施放期间不触发。冷却时间：30秒。）"
    ),
    "Can only be Upgraded up to +10 before Level 50.": "最高只能强化至+10（角色未达到50级时）。",
    "Strength +15, Intelligence +15 with each additional party member. (If you are a party of 1, you will earn Strength +15, Intelligence +15)": (
        "队伍每增加一名成员，力量 +15、智力 +15。（队伍只有1人时，力量 +15、智力 +15。）"
    ),
    "When a party member dies, your weapon turns red for 10 seconds, and the Atk. increases by 30%%. (When unequipped, the option is canceled.)": (
        "队友阵亡时，武器变红，持续10秒，期间攻击力增加30%%。（卸下时解除效果。）"
    ),
    "Recovers 10%% HP by consuming 5 Clear Cube Fragment when HP falls below 10%%.": (
        "可恢复10%%的HP，并消耗5个无色小晶块（HP低于10%%时触发）。"
    ),
    "Strength/Intelligence +15 every additional party member (1-person party: Default Strength/Intelligence +15)": (
        "队伍每增加一名成员，力量/智力 +15（1人队伍：默认力量/智力 +15）。"
    ),
    "When a party member dies, your weapon turns red and inflicts +30%% damage for 10 seconds.": (
        "队友阵亡时，武器变红，造成的伤害 +30%%，持续10秒。"
    ),
    "*Multiple +%% damage bonus does not stack. Only the highest value of bonus applies. (Not applicable to Critical Damage) Canceled when the item is unequipped.": (
        "*多项 +%% 的伤害加成不叠加，仅最高值生效。（不适用于暴击伤害。）卸下装备时解除。"
    ),
    "If 10%% or lower HP, HP +10%% at the cost of 5 Clear Cube Fragments. (Cooldown: 60 seconds)": (
        "HP不高于10%%时，HP +10%%，消耗5个无色小晶块。（冷却时间：60秒。）"
    ),
    "Jumping Event unrestricted weapon: Grows up to Level 67 depending on your Character Level.": (
        "快速成长活动专用武器：随角色等级成长，最高达到67级。"
    ),
    "Attack Damage +4%%": "攻击伤害 +4%%",
    "Cast an Invulnerability buff on yourself that lasts for 2s every 10s": "自身进入无敌状态2秒，每10秒触发一次。",
    "Physical/Magical/Independent Atk. +20%%": "物理/魔法/独立攻击力 +20%%",
    "For every 10 combos, obtain an All Atk. +12%% buff for 60s": "每达成10次连击，获得所有攻击力 +12%%的增益，持续60秒。",
    "(Buff Max 4 stacks, combo max 40 combos)": "（增益最多叠加4层，计入的连击数上限为40。）",
    "Upon casting skills, has a 30%% to apply +4 to all Lv. 1-100 Skills for 60s": (
        "施放技能时，有30%%的概率使所有技能等级 +4，适用于Lv. 1-100技能，持续60秒。"
    ),
    "Critical Chance +10%%": "暴击率 +10%%",
    "Skill Cooldown Recovery Speed +100%% for Lv. 25, 30, 35, 40 ,45 Skills for all classes": (
        "技能冷却恢复速度 +100%%（适用于所有职业的Lv. 25、30、35、40、45技能）。"
    ),
    "Upon casting a skill, has a 50%% chance to reduce the cooldown for a random Lv. 1-45 Skill by 1s.": (
        "施放技能时，有50%%的概率随机缩短一个Lv. 1-45技能的冷却时间，缩短1秒。"
    ),
    "When hit, received damage is reduced by 10%%, HP/MP 1%% recovered every 10s, STR/INT +30%%": (
        "被击时，所受伤害减少10%%，恢复1%%的HP/MP（每10秒一次），力量/智力 +30%%。"
    ),
    "Contained Avatars has 2 emblem sockets, and cannot be traded/dyed.": "内含装扮有2个徽章镶嵌栏，无法交易或染色。",
    "Contains 8 pieces of untradable Clone Avatars.": "内含8件无法交易的克隆装扮。",
    "Contains 5 pieces of untradable Arad Restaurant Avatars.": "内含5件无法交易的阿拉德餐厅装扮。",
    "!Contains Avatars cannot be sundered.": "！内含装扮无法分解。",
    "< Happy DFO 3rd Anniversary Cake >": "< 地下城与勇士3周年纪念蛋糕 >",
    "< Happy DFO 3rd Anniversary >": "< 地下城与勇士3周年快乐 >",
    "<Honey Time>": "<在线有礼>",
    "<EPIC BINGO!>": "<史诗宾果！>",
    "Elemetal Harmony": "元素调和",
    "DFO Mega Drill": "地下城与勇士巨型钻机",
    "Automatically clears 2nd Awakening quest when used.": "使用后自动完成第2次觉醒任务。",
    "Cannot be used for Side Story characters.": "外传职业无法使用。",
    "Can be transferred to your Account Vault or another character on the same account via mail.": (
        "可放入账号金库，或通过邮件转移给同一账号下的其他角色。"
    ),
    "2nd Awakening Quest Clearance Ticket": "第2次觉醒任务完成券",
    "Generates Divine Revelation Avatar Full Set Boxes and Divine Revelation Weapon Avatar Boxes.": (
        "可获得神圣启示装扮套装礼盒和神圣启示武器装扮礼盒。"
    ),
    "All the boxes can be traded and will be deleted on December 2, 2021, at 6:00 a.m.": (
        "所有礼盒均可交易，将于十二月2日（2021年）上午6:00删除。"
    ),
    "Generates a Hell Conqueror Avatar Full Set Chest (tradeable), a Hell Conqueror Title Chest (account-binding), and a Hell Conqueror Item Shop Chest (account-binding).": (
        "可获得深渊征服者装扮套装礼盒（可交易）、深渊征服者称号礼盒（账号绑定）及深渊征服者商城道具礼盒（账号绑定）。"
    ),
    "Contains Fantasy Cosplay Avatar Full Set Chests, Title Chests, Aura Chests, Creature Chests, and Item Shop Chests.": (
        "内含奇幻角色扮演装扮套装礼盒、称号礼盒、光环礼盒、宠物礼盒及商城道具礼盒。"
    ),
    "Partially draws out your latent power upon Advancement and increases your stats.": "转职时激发一部分潜能，提升属性。",
    "Additionally increases your stats upon 1st and 2nd Awakening.": "在第1次和第2次觉醒时进一步提升属性。",
    "Clear the True Awakening 1 (4/4) quest to improve your realized power and decrease SP cost for 1st/2nd Awakening Passive and Active skills.": (
        "完成「真正的觉醒 1（4/4）」任务后，进一步提升已激发的潜能，并减少第1次/第2次觉醒被动和主动技能的SP消耗。"
    ),
    "Disabled inside the Arena.": "在决斗场中不生效。",
    "Select and receive 2nd or 3rd Chronicle Set Sub Equipment or Magic Stone of your class.": (
        "可选择一件适用于本职业的第2期或第3期勇者套装辅助装备或魔法石。"
    ),
    "Damage reflected(Damage scales with character's Physical/Magical Defense)": "反弹伤害（伤害随角色的物理/魔法防御力变化）。",
    "Physical/Magical Defense +100000": "物理/魔法防御力 +100000",
    "Physical/Magical Defense +10000 for each monster within 500px of the character": (
        "物理/魔法防御力 +10000（角色500像素范围内每存在一只怪物叠加一次）。"
    ),
    "Movement Speed/Attack Speed/Casting Speed -50%%": "移动速度/攻击速度/施放速度 -50%%",
    "Increases character's damage by 10%%%% for 20 seconds.": "角色伤害增加10%%%%，持续20秒。",
    "Generates full set (8 pieces) of untradable Rare Clone Avatars.": "可获得一套稀有克隆装扮（8件），无法交易。",
    "Enables you to select 9 different Advancement Book Avatars (tradable) for the Awakening name and type you want.": (
        "可按照觉醒名称和款式自选9件转职之书装扮（可交易）。"
    ),
    "[Dimensional Force Field Effect]": "[次元力场效果]",
    "Casts Dimensional Force Field at the cost of 1 Clear Cube Fragment every 20 sec.": (
        "发动次元力场，消耗1个无色小晶块，每20秒发动一次。"
    ),
    "Physical/Magical/Independent Atk. +10%%": "物理/魔法/独立攻击力 +10%%",
    "Increase Attack Speed, Casting Speed, and Movement Speed by +2%% when HP is less than 50%% of total; gain an additional +2%% for each subsequent 10%% loss of HP": (
        "攻击速度、施放速度和移动速度 +2%%（HP低于50%%时）；此后额外 +2%%（HP每再降低10%%叠加一次）。"
    ),
    "Max Attack Speed, Casting Speed, Movement Speed +10%%": "攻击速度、施放速度及移动速度最高 +10%%。",
    "Contains Danjin's Pot Head and Cape Avatars. These avatars could bring you good luck in 2016 just like Danjin has! Avatars will expire in 15 days as this box opened.": (
        "内含土罐的罐子头和披风装扮。希望这些装扮能为你在2016年带来和土罐一样的好运！开启礼盒后，装扮将在15天后到期。"
    ),
    "Can be purchased only ONCE per server and class.": "每个服务器的每个职业只可购买一次。",
}

PACKAGES = {
    "Divine Revelation": "神圣启示",
    "Fairy Hero": "妖精英雄",
    "Hell Conqueror": "深渊征服者",
    "Desert Guardian": "沙漠守护者",
    "Fantasy Cosplay": "奇幻角色扮演",
    "[Vintage] Behemoth Pirate": "[复古]天帷巨兽海盗",
    "Descending Apostle": "使徒降临",
    "Royal Arad High School Returns": "皇家阿拉德高校归来",
    "Valhalla": "英灵殿",
    "Monster Cosplay": "怪物装扮",
}
EXP = re.compile(
    r"Additionally increases Dungeon Clear EXP by (\d+%%) for (\d+) minutes\. "
    r"This effect persists even if you die in dungeons, move to the town, or re-?log\s?in, and (does not stack|stacks) with those of EXP events\."
)
SPEED = re.compile(
    r"Attacks have a (\d+%%) chance of increasing Movement/Attack/Casting Speeds (\+\d+%%) for (\d+) seconds"
    r"(?: \((\d+) seconds in the Arena\))? at the cost of (\d+%%) Equipment Defense\."
    r"(?: \(Cooldown: (\d+) seconds\))?"
)
AVATAR = re.compile(
    r"Generates (\d+) (?:different )?(.+?) Avatars(?: \((tradable|tradeable)\))? "
    r"(?:of your choice|of the type you want)\.( \(Skin excluded\))?"
)
DAMAGE = re.compile(r"Damage (\+\d+%%) for (\d+) sec\.")
MAX_STAT = re.compile(r"(HP|MP) MAX (\d+\+|[+-]?\d+%%?)", re.I)
GRACIA = re.compile(
    r"Attacks create a (\d+%%) chance of increasing (?:STR|Strength) (\+\d+), (?:INT|Intelligence) (\+\d+), "
    r"and Light Damage (\+\d+) for (\d+) seconds\. \(Cooldown: (\d+) sec\. Max Stacks: (\d+)\. "
    r"This option is shared among Gracia Family Heirloom: Shoulder, Belt, and Shoes\. Canceled when the items are unequipped\.\)"
)
BOX = re.compile(
    r"Contains (\d+) pieces? of (?:Halloween )?(?:tradable )?look Avatars?(?:\s*\(Tradable\))?"
    r"(?: and SD Character Transformation\(Account Bound\)| and (\d+) days of Neo Premium Contract Plus|"
    r" and Neo Premium Contract \((\d+) days\)| and (\d+) Single-use Amplifiers)?\.", re.I,
)
HIGHLIGHT_STATS = re.compile(r"STR/INT (\+#\d+%%#) \((\d+ - \d+%%)\)")
SKILL_LEVEL = re.compile(
    r"All classes' Lv\. (\d+(?:/\d+)+) skills (\+\d+) levels \(Special skills excluded\)"
)
CHRONICLE = re.compile(r"Enables you to choose a 2nd/3rd Chronicle Set item for ([A-Za-z ]+)\.")
CHRONICLE_BOX = re.compile(r"Contains the 2nd or 3rd Tier Chronicle ([A-Za-z ]+) Set items\.")
CLASSES = {
    "Asuras": "阿修罗", "Avengers": "复仇者", "Berserkers": "狂战士", "Brawlers": "街霸",
    "Chaoses": "混沌魔灵", "Crusaders": "圣骑士", "Dark Templars": "暗殿骑士",
    "Demon Slayers": "契魔者", "Dragon Knights": "龙骑士", "Elemental Bombers": "元素爆破师",
    "Elementalists": "元素师", "Elven Knights": "精灵骑士", "Exorcists": "驱魔师",
    "Glacial Masters": "冰结师", "Grapplers": "柔道家", "Kunoichis": "忍者",
    "Launchers": "枪炮师", "Mechanics": "机械师", "Monks": "蓝拳圣使", "Necromancers": "死灵术士",
    "Paladins": "帕拉丁", "Rangers": "漫游枪手", "Rogues": "刺客", "Soul Benders": "鬼泣",
    "Spitfires": "弹药专家", "Strikers": "散打", "Summoners": "召唤师",
    "Sword Masters": "驭剑士", "Vagabonds": "流浪武士", "Weapon Masters": "剑魂",
    "Witches": "魔道学者",
}
SINGULAR_CLASSES = {
    **{job[:-1]: name for job, name in CLASSES.items() if job.endswith("s") and not job.endswith(("es", "ers"))},
    "Avenger": "复仇者", "Asura": "阿修罗", "Battle Mage": "战斗法师", "Berserker": "狂战士",
    "Brawler": "街霸", "Creator": "缔造者", "Dark Knight": "黑暗武士", "Elementalist": "元素师",
    "Elemental Bomber": "元素爆破师", "Grappler": "柔道家", "Launcher": "枪炮师",
    "Nen Master": "气功师", "Ranger": "漫游枪手", "Striker": "散打", "Summoner": "召唤师",
    "Witch": "魔道学者",
}
EXP_SIMPLE = re.compile(
    r"(?:Increases|Additionally increases) Dungeon Clear EXP by (\d+)( times|%%) for (\d+) minutes\."
)
DAMAGE_DURATION = re.compile(r"Increases damage by (\d+%%) for (\d+) seconds\.")
BONUS_HP_RANGE = re.compile(r"Bonus Damage ([+-]?\d+%%?) on enemies with (\d+ - \d+(?:\.\d+)?%%) HP")
COOLDOWN_LABEL = re.compile(r"(\()?Cooldown:? (\d+(?:\.\d+)?) sec\.(\))?")
KOREAN_SKILL_LEVEL = re.compile(r"(마운트|축염포) \((\d+)Lv\)")
STARS = re.compile(r"Generates (\d+) Star Energy (when equipped|every (\d+) sec\.)\.?")
FATIGUE_SHORT = re.compile(r"Restores (\d+) Fatigue\.")
ARENA_AVATAR = re.compile(r"Contains DFO Arena Avatars given as a reward\. \((\d+) pieces\)")
DANJI_MONTH = re.compile(r"< DANJI \((JAN|FEB|DEC)\) >")
KOR_TRIGGER = re.compile(r"(?:Intelligence|INT) (\+\d+) at (\d+%%) chance for (\d+) seconds upon casting\.")
AURA_PARTY = re.compile(r"Casts an aura that increases (Fire|Water|Light|Shadow) Damage (\+\d+) for party members in a (\d+) px range\.")
MAGIC_DOWN = re.compile(
    r"Attacks create a (\d+%%) chance of decreasing Magical Def\. (-\d+%%) for all enemies (?:within|in) "
    r"(\d+) px around their targets for (\d+) seconds\.(?: \(Cooldown: (\d+) sec\.\))?"
)
AURA_DOWN = re.compile(
    r"(?:Casts|Creates) an aura that decreases (Movement Speed|Attack Speed|Water Resistance|Fire Resistance|Light Resistance|Shadow Resistance) "
    r"(-?\d+(?:%%)?) for enemies within a (\d+) px range\."
)
AURA_STATS = {
    "Movement Speed": "移动速度", "Attack Speed": "攻击速度", "Water Resistance": "冰属性抗性",
    "Fire Resistance": "火属性抗性", "Light Resistance": "光属性抗性", "Shadow Resistance": "暗属性抗性",
}
BOOST = re.compile(
    r"Lv\.\s*(\d+) (Buff|Active) [Ss]kills?(?:')? (Physical/Magical/Independent Atk\.|STR/INT) bonus (\+\d+(?:%%)?)"
)
RES_DOWN = re.compile(
    r"Attacks (?:have|create) a (\d+%%) chance of decreasing all elemental resistances (?:by )?(-\d+) for all enemies "
    r"within (?:a )?(\d+) px(?: range)? for (\d+) (?:seconds|sec\.)\.?(?: \(Cooldown: (\d+) sec\.\))?"
)
ILLUSION = re.compile(
    r"Increases the Basic Slash Count of the Illusion Sword Dance by (\d+) and increases the Slash Count by (\d+) "
    r"while decreasing the Atk\. by (\d+%%) when Level (\d+) or higher on the Katana and Lightsabre Mastery\."
)
GROWTH = re.compile(
    r"(?:Sign a Growth Contract\.|Contains (\d+)-day supply of Growth Contract\.) "
    r"Once you sign a contract, Quest EXP, Quest Item drop rate, Hunting EXP, Dungeon clear EXP will be increased by (\d+%%)\. "
    r"Also inflicts (\d+) additional damage when attacking with a (\d+%%) chance\. "
    r"Boss Unique drop rate increased according to a number of party members using this contract\."
    r"( \(This item will be directly applied on purchase and will be applied to all of your characters in this server\.\))?"
)
EXPERIENCE_EFFECT = re.compile(
    r"When used, EXP obtained by clearing dungeons will be increased by (\d+%%) for (\d+) minutes\. "
    r"The effect remains even if you die in the dungeon, move back to town, re-connect\."
)
BOX_DELETE = re.compile(
    r"All the Chests except for the Avatar Full Set Chest will be deleted from your inventory on "
    r"(September|November) (\d+), (20\d\d) at (\d+:\d\d) a\.m\.", re.I,
)
SPEED_PROC = re.compile(
    r"(?:Attacks have a|Attacks create a) (\d+%%) chance of increasing Attack/Casting/Movement Speeds (?:by )?"
    r"(\+\d+%%) for (\d+) seconds\."
)
SHADOW_SCALE = re.compile(
    r"(Additional Damage|Additional Critical Damage|STR/INT|All Atk\.|All Elemental Damage|Skill Atk\.) "
    r"(\+\d+(?:\.\d+)?(?:%%)?) for every (\d+) Shadow Resistance \(Up to (\+\d+(?:\.\d+)?(?:%%)?)\)"
)
SHADOW_STAT = {
    "Additional Damage": "伤害额外增加", "Additional Critical Damage": "暴击伤害额外增加",
    "STR/INT": "力量/智力", "All Atk.": "所有攻击力", "All Elemental Damage": "所有属性强化",
    "Skill Atk.": "技能攻击力",
}


def translate_reviewed(text):
    if text in LINES:
        return LINES[text]
    if m := EXP.fullmatch(text):
        stacking = "不可与" if m[3] == "does not stack" else "可与"
        return (f"通关地下城时额外获得{m[1]}经验值，效果持续{m[2]}分钟。"
                f"在地下城阵亡、返回城镇或重新登录后仍有效，且{stacking}经验值活动叠加。")
    if m := SPEED.fullmatch(text):
        arena = f"（决斗场持续{m[4]}秒）" if m[4] else ""
        cooldown = f"（冷却时间：{m[6]}秒。）" if m[6] else ""
        return (f"攻击时有{m[1]}的概率使移动速度、攻击速度和施放速度 {m[2]}，"
                f"持续{m[3]}秒{arena}，同时损失{m[5]}的装备防御力。{cooldown}")
    if (m := AVATAR.fullmatch(text)) and (name := PACKAGES.get(m[2])):
        options = ("（可交易）" if m[3] else "") + ("（不含皮肤装扮）" if m[4] else "")
        return f"可选择获得{m[1]}件{name}装扮。{options}"
    if m := DAMAGE.fullmatch(text):
        return f"伤害 {m[1]}，持续{m[2]}秒。"
    if m := MAX_STAT.fullmatch(text):
        return f"{m[1]}上限 {m[2]}"
    if m := GRACIA.fullmatch(text):
        return (f"攻击时有{m[1]}的概率使力量 {m[2]}、智力 {m[3]}、光属性强化 {m[4]}，持续{m[5]}秒。"
                f"（冷却时间：{m[6]}秒；最多叠加{m[7]}层。格拉西亚家族遗物的头肩、腰带和鞋共用该词条。"
                "卸下对应装备时解除效果。）")
    if m := BOX.fullmatch(text):
        name = "万圣节" if "halloween" in text.casefold() else ""
        trade = "（可交易）" if "tradable" in text.casefold() else ""
        extra = ("，另含迷你角色变身道具（账号绑定）" if "Transformation" in text
                 else f"，另含{m[2]}天Neo高级会员契约加强版" if m[2]
                 else f"，另含Neo高级会员契约（{m[3]}天）" if m[3]
                 else f"，另含{m[4]}个一次性增幅器" if m[4] else "")
        return f"内含{m[1]}件{name}外观装扮{trade}{extra}。"
    if m := HIGHLIGHT_STATS.fullmatch(text):
        return f"力量/智力 {m[1]}（{m[2]}）"
    if m := SKILL_LEVEL.fullmatch(text):
        return f"所有职业Lv. {m[1]}技能等级 {m[2]}（特殊技能除外）"
    if (m := CHRONICLE.fullmatch(text)) and (name := CLASSES.get(m[1])):
        return f"可自选一件第2期或第3期勇者套装装备（{name}）。"
    if (m := CHRONICLE_BOX.fullmatch(text)) and (name := SINGULAR_CLASSES.get(m[1])):
        return f"内含第2期或第3期勇者套装装备（{name}）。"
    if m := EXP_SIMPLE.fullmatch(text):
        extra = "额外" if text.startswith("Additionally") else ""
        amount = f"增加至{m[1]}倍" if m[2] == " times" else f"{extra}增加{m[1]}%%"
        return f"地下城通关经验值{amount}，持续{m[3]}分钟。"
    if m := DAMAGE_DURATION.fullmatch(text):
        return f"伤害增加{m[1]}，持续{m[2]}秒。"
    if m := BONUS_HP_RANGE.fullmatch(text):
        return f"附加伤害 {m[1]}（对HP处于{m[2]}的敌人生效）"
    if m := COOLDOWN_LABEL.fullmatch(text):
        return ("（" if m[1] else "") + f"冷却时间：{m[2]}秒" + ("）" if m[3] else "。")
    if m := KOREAN_SKILL_LEVEL.fullmatch(text):
        name = {"마운트": "伏虎霸王拳", "축염포": "蓄念炮"}[m[1]]
        return f"{name}（{m[2]}级）"
    if m := STARS.fullmatch(text):
        if m[3]:
            return f"生成{m[1]}点星辰能量，每{m[3]}秒触发一次。"
        return f"穿戴时生成{m[1]}点星辰能量。"
    if m := FATIGUE_SHORT.fullmatch(text):
        return f"恢复{m[1]}点疲劳值。"
    if m := ARENA_AVATAR.fullmatch(text):
        return f"内含作为奖励发放的地下城与勇士决斗场装扮（{m[1]}件）。"
    if m := DANJI_MONTH.fullmatch(text):
        month = {"JAN": "一", "FEB": "二", "DEC": "十二"}[m[1]]
        return f"< 土罐（{month}月） >"
    if m := KOR_TRIGGER.fullmatch(text):
        return f"智力 {m[1]}（施放时有{m[2]}的概率触发，持续{m[3]}秒）。"
    if m := AURA_PARTY.fullmatch(text):
        attr = {"Fire": "火", "Water": "冰", "Light": "光", "Shadow": "暗"}[m[1]]
        return f"释放光环，使{attr}属性强化 {m[2]}（适用于{m[3]}像素范围内的队员）。"
    if m := MAGIC_DOWN.fullmatch(text):
        cd = f"（冷却时间：{m[5]}秒。）" if m[5] else ""
        return f"攻击时有{m[1]}的概率使魔法防御力 {m[2]}（适用于目标周围{m[3]}像素内的敌人），持续{m[4]}秒。{cd}"
    if m := AURA_DOWN.fullmatch(text):
        change = f" {m[2]}" if m[2].startswith("-") else f"降低{m[2]}"
        return f"释放光环，使{AURA_STATS[m[1]]}{change}（适用于{m[3]}像素范围内的敌人）。"
    if m := BOOST.fullmatch(text):
        kind = "增益" if m[2] == "Buff" else "主动"
        stat = "力量/智力" if m[3] == "STR/INT" else "物理/魔法/独立攻击力"
        return f"Lv. {m[1]}{kind}技能提供的{stat}加成 {m[4]}"
    if m := RES_DOWN.fullmatch(text):
        cd = f"（冷却时间：{m[5]}秒。）" if m[5] else ""
        return f"攻击时有{m[1]}的概率使所有属性抗性 {m[2]}（适用于{m[3]}像素内的所有敌人），持续{m[4]}秒。{cd}"
    if m := ILLUSION.fullmatch(text):
        return (f"幻影剑舞的基础斩击次数增加{m[1]}次、斩击次数增加{m[2]}次，攻击力降低{m[3]}。"
                f"（太刀精通和光剑精通达到{m[4]}级及以上时生效。）")
    if m := GROWTH.fullmatch(text):
        prefix = f"内含{m[1]}天的成长契约。" if m[1] else "签订成长契约。"
        scope = "（购买后立即生效，适用于本服务器内账号下的所有角色。）" if m[5] else ""
        return (prefix + f"签订后，任务经验值、任务道具掉落率、狩猎经验值与地下城通关经验值增加{m[2]}。"
                f"额外造成{m[3]}点伤害（攻击时有{m[4]}的概率触发）。"
                f"队伍中使用该契约的成员越多，领主掉落神器装备的概率越高。{scope}")
    if m := EXPERIENCE_EFFECT.fullmatch(text):
        return f"使用后，地下城通关经验值增加{m[1]}，持续{m[2]}分钟。在地下城阵亡、返回城镇或重新登录后仍有效。"
    if m := BOX_DELETE.fullmatch(text):
        month = {"september": "九", "november": "十一"}[m[1].casefold()]
        return f"除装扮套装礼盒外，其余礼盒将于{month}月{m[2]}日（{m[3]}年）上午{m[4]}从物品栏删除。"
    if m := SPEED_PROC.fullmatch(text):
        return f"攻击时，有{m[1]}的概率使攻击/施放/移动速度 {m[2]}，持续{m[3]}秒。"
    if m := SHADOW_SCALE.fullmatch(text):
        return f"{SHADOW_STAT[m[1]]} {m[2]}（每{m[3]}点暗属性抗性叠加一次，最高{m[4]}）。"
    return None
