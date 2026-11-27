"""Visual Server Activity Center (活动中心) for the Lv.100 Wisdom's Guide launcher."""
from __future__ import annotations

import datetime
from typing import Dict, List

from PySide6.QtCore import Qt, QTimer
from PySide6.QtWidgets import (
    QCheckBox,
    QComboBox,
    QDoubleSpinBox,
    QFrame,
    QGridLayout,
    QHBoxLayout,
    QHeaderView,
    QLabel,
    QMessageBox,
    QPushButton,
    QScrollArea,
    QSpinBox,
    QTableWidget,
    QTableWidgetItem,
    QTabWidget,
    QVBoxLayout,
    QWidget,
)

import core


EXTRA_ACTIVITY_CARDS = (
    {
        'key': 'daily_checkin',
        'policy_field': 'daily_checkin_enabled',
        'title': '每日签到 / 蜜蜂在线福利',
        'desc': '按日防重复领取：金币 +100,000 · 商城积分 +15 · 炉岩核 ×20',
        'per_day': True,
        'reward_gold': 100000,
        'reward_mileage': 15,
        'reward_items': [{'template': 10327281, 'amount': 20}],
        'reward_contracts': [],
        'reward_contract_days': 0,
    },
    {
        'key': 'seria_welcome',
        'policy_field': 'seria_welcome_enabled',
        'title': '赛丽亚的欢迎活动 (seriawelcome.etc)',
        'desc': '欢迎药剂 (10301306) + 欢迎装备礼盒 (10313708) + 成长/霸王/达人契约 7 天',
        'per_day': False,
        'reward_gold': 200000,
        'reward_mileage': 20,
        'reward_items': [{'template': 10301306, 'amount': 5}, {'template': 10313708, 'amount': 1}],
        'reward_contracts': [22, 27, 79],
        'reward_contract_days': 7,
    },
    {
        'key': 'returnee_care',
        'policy_field': 'returnee_care_enabled',
        'title': '回归勇士关怀活动 (returneecare.etc)',
        'desc': '金币 +500,000 · 协调结晶体 ×30 · 积分 +30 · Neo 尊享 Plus 15 天',
        'per_day': False,
        'reward_gold': 500000,
        'reward_mileage': 30,
        'reward_items': [{'template': 10327282, 'amount': 30}],
        'reward_contracts': [118],
        'reward_contract_days': 15,
    },
    {
        'key': 'otp_security',
        'policy_field': 'otp_security_enabled',
        'title': '安全认证 (OTP) 奖励 (securityreward.etc)',
        'desc': 'OTP 专属奖励道具 (50080959) + 商城积分 +20',
        'per_day': False,
        'reward_gold': 0,
        'reward_mileage': 20,
        'reward_items': [{'template': 50080959, 'amount': 1}],
        'reward_contracts': [],
        'reward_contract_days': 0,
    },
    {
        'key': 'wisdom_sprint_pack',
        'policy_field': 'daily_checkin_enabled',
        'title': '100级智慧的引导冲刺补给（按日）',
        'desc': '炉岩核 ×60 · 协调结晶体 ×40 · 金币 +300,000（每日一次）',
        'per_day': True,
        'reward_gold': 300000,
        'reward_mileage': 10,
        'reward_items': [{'template': 10327281, 'amount': 60}, {'template': 10327282, 'amount': 40}],
        'reward_contracts': [],
        'reward_contract_days': 0,
    },
)


def _scroll_container(widget: QWidget) -> QScrollArea:
    sc = QScrollArea()
    sc.setWidgetResizable(True)
    sc.setFrameShape(QFrame.NoFrame)
    sc.setStyleSheet('QScrollArea{background:transparent;border:none;}')
    widget.setStyleSheet('background:transparent;')
    sc.setWidget(widget)
    return sc


def _make_card(title: str, subtitle: str = '') -> tuple[QFrame, QVBoxLayout]:
    card = QFrame()
    card.setObjectName('PanelCard')
    lay = QVBoxLayout(card)
    lay.setContentsMargins(16, 13, 16, 13)
    lay.setSpacing(8)
    hdr = QLabel(title)
    hdr.setStyleSheet('font-size:14px;font-weight:bold;color:#ffe285;border:none;background:transparent;')
    lay.addWidget(hdr)
    if subtitle:
        sub = QLabel(subtitle)
        sub.setWordWrap(True)
        sub.setStyleSheet('font-size:11px;color:#b8ad99;border:none;background:transparent;')
        lay.addWidget(sub)
    return card, lay


def _make_badge(title: str, value: str, color: str = '#ffe285') -> tuple[QFrame, QLabel]:
    box = QFrame()
    box.setObjectName('PanelCard')
    lay = QVBoxLayout(box)
    lay.setContentsMargins(12, 8, 12, 8)
    lay.setSpacing(2)
    t = QLabel(title)
    t.setStyleSheet('font-size:11px;color:#b8ad99;border:none;background:transparent;')
    v = QLabel(value)
    v.setStyleSheet(f'font-size:15px;font-weight:bold;color:{color};border:none;background:transparent;')
    lay.addWidget(t)
    lay.addWidget(v)
    return box, v


class ActivityCenterPage(QWidget):
    """Unified launcher console for live server events, Mileage shop, contracts, and operational activities."""

    def __init__(self, host):
        super().__init__(host)
        self.host = host
        self._roles: List[tuple[int, str]] = []
        self._policy: dict = core.default_events_policy(active=False)
        self._policy_exists: bool = False
        self._last_job_id: str = ''
        self._contract_labels: Dict[int, QLabel] = {}
        self._extra_chks: Dict[str, QCheckBox] = {}
        self._claim_status_lbls: Dict[str, QLabel] = {}

        self._build_ui()
        self._load_policy_into_ui()
        self.refresh_state()

    def _build_ui(self):
        root = QVBoxLayout(self)
        root.setContentsMargins(20, 16, 20, 14)
        root.setSpacing(10)

        # Header row
        top = QHBoxLayout()
        title_box = QVBoxLayout()
        title_box.setSpacing(2)
        t = QLabel('服务器活动中心')
        t.setObjectName('TitleLabel')
        sub = QLabel('全服倍率、疲劳、原生深渊派对、每日/周常挑战、商城积分与运营福利均可随时热开关，无需重启服务端')
        sub.setObjectName('SubLabel')
        title_box.addWidget(t)
        title_box.addWidget(sub)
        top.addLayout(title_box, 1)

        role_lbl = QLabel('当前角色:')
        role_lbl.setStyleSheet('color:#d8caa8;font-size:12px;')
        self.role_combo = QComboBox()
        self.role_combo.setMinimumWidth(180)
        self.role_combo.currentIndexChanged.connect(lambda *_: self.refresh_state())
        refresh_btn = QPushButton('🔄 刷新状态')
        refresh_btn.setObjectName('DarkBtn')
        refresh_btn.clicked.connect(self.activate)
        top.addWidget(role_lbl)
        top.addWidget(self.role_combo)
        top.addWidget(refresh_btn)
        root.addLayout(top)

        # Summary KPI badges
        kpi_row = QHBoxLayout()
        kpi_row.setSpacing(10)
        b1, self.badge_events_state = _make_badge('活动配置状态', '原版默认（未开启）', '#cbd5e1')
        b2, self.badge_mileage = _make_badge('账号商城积分 (Mileage)', '0', '#60a5fa')
        b3, self.badge_daily = _make_badge('今日挑战 (击杀 / 通关)', '0 / 0', '#4ade80')
        b4, self.badge_weekly = _make_badge('本周周常 / 累计深渊', '0/6 · 0次', '#f59e0b')
        for b in (b1, b2, b3, b4):
            kpi_row.addWidget(b, 1)
        root.addLayout(kpi_row)

        # Tabs
        self.tabs = QTabWidget()
        self.tabs.addTab(self._build_rates_fatigue_tab(), '🔥 倍率与疲劳活动')
        self.tabs.addTab(self._build_protocol_events_tab(), '⚔️ 深渊 / 挑战 / 周常')
        self.tabs.addTab(self._build_mileage_tab(), '💎 积分商城 (Mileage)')
        self.tabs.addTab(self._build_extra_activities_tab(), '🎁 运营福利 & 契约特权')
        root.addWidget(self.tabs, 1)

        # Bottom action bar
        bar = QHBoxLayout()
        bar.setSpacing(8)
        self.save_btn = QPushButton('💾 保存并热生效到服务端')
        self.save_btn.setObjectName('GoldBtn')
        self.save_btn.clicked.connect(self.save_policy)

        self.preset_all_btn = QPushButton('⚡ 一键推荐全开 (2x+燃烧+深渊+周常)')
        self.preset_all_btn.setObjectName('DarkBtn')
        self.preset_all_btn.clicked.connect(self.apply_recommended_preset)

        self.reset_off_btn = QPushButton('⏹ 全部关闭恢复原版')
        self.reset_off_btn.setObjectName('DarkBtn')
        self.reset_off_btn.clicked.connect(self.reset_to_vanilla)

        self.result_lbl = QLabel('提示：修改活动开关后点击「保存并热生效」立即写入 events.json 并通知在线网关。')
        self.result_lbl.setStyleSheet('color:#d4c7ac;font-size:12px;')
        self.result_lbl.setWordWrap(True)

        bar.addWidget(self.save_btn)
        bar.addWidget(self.preset_all_btn)
        bar.addWidget(self.reset_off_btn)
        bar.addWidget(self.result_lbl, 1)
        root.addLayout(bar)

    # ---------------- Tab 1: Rates, Fatigue & Quick Global Drop ----------------
    def _build_rates_fatigue_tab(self) -> QWidget:
        inner = QWidget()
        lay = QVBoxLayout(inner)
        lay.setContentsMargins(12, 12, 12, 12)
        lay.setSpacing(12)

        # Card 1: EXP / Drop / Gold / Card multipliers
        c1, l1 = _make_card('全服经验 / 掉落 / 翻牌倍率活动', '杀怪与通关经验、怪物基础掉率、金币与通关翻牌金币实时生效')
        g1 = QGridLayout()
        g1.setHorizontalSpacing(14)
        g1.setVerticalSpacing(8)

        self.exp_chk = QCheckBox('开启全服经验倍率活动')
        self.exp_spin = QDoubleSpinBox()
        self.exp_spin.setRange(1.0, 10.0)
        self.exp_spin.setSingleStep(0.5)
        self.exp_spin.setSuffix(' 倍')

        self.drop_chk = QCheckBox('开启全服掉落与金币倍率活动')
        self.drop_spin = QDoubleSpinBox()
        self.drop_spin.setRange(1.0, 10.0)
        self.drop_spin.setSingleStep(0.5)
        self.drop_spin.setSuffix(' 倍掉率')
        self.gold_spin = QDoubleSpinBox()
        self.gold_spin.setRange(1.0, 10.0)
        self.gold_spin.setSingleStep(0.5)
        self.gold_spin.setSuffix(' 倍金币')

        self.card_chk = QCheckBox('开启通关黄金翻牌福利倍率')
        self.card_spin = QDoubleSpinBox()
        self.card_spin.setRange(1.0, 5.0)
        self.card_spin.setSingleStep(0.5)
        self.card_spin.setSuffix(' 倍翻牌金币')

        g1.addWidget(self.exp_chk, 0, 0)
        g1.addWidget(QLabel('经验倍率:'), 0, 1)
        g1.addWidget(self.exp_spin, 0, 2)
        g1.addWidget(self.drop_chk, 1, 0)
        g1.addWidget(QLabel('基础掉率 / 金币:'), 1, 1)
        dg_row = QHBoxLayout()
        dg_row.addWidget(self.drop_spin)
        dg_row.addWidget(self.gold_spin)
        g1.addLayout(dg_row, 1, 2)
        g1.addWidget(self.card_chk, 2, 0)
        g1.addWidget(QLabel('翻牌金币倍率:'), 2, 1)
        g1.addWidget(self.card_spin, 2, 2)
        l1.addLayout(g1)
        lay.addWidget(c1)

        # Card 2: Fatigue events + PC Bang
        c2, l2 = _make_card('疲劳值与燃烧活动', '周末双倍疲劳、燃烧疲劳阶段经验（30/60/100/156 → +20%/+50%/+75%/+100%）、无限疲劳及网吧特权')
        g2 = QGridLayout()
        g2.setHorizontalSpacing(14)
        g2.setVerticalSpacing(8)

        self.weekend_fp_chk = QCheckBox('周末双倍疲劳活动（周六/周日基础疲劳翻倍 156 → 312）')
        self.burning_fp_chk = QCheckBox('燃烧疲劳阶段经验加成（随当日消耗疲劳自动叠加最高 +100% EXP）')
        self.zero_fp_chk = QCheckBox('无限疲劳活动（进图零消耗，0 疲劳也可正常刷图）')
        self.pcbang_chk = QCheckBox('PC Bang 网吧特权活动（全服额外 +20% 经验 & 每日 +30 疲劳上限）')

        self.bonus_fp_spin = QSpinBox()
        self.bonus_fp_spin.setRange(0, 500)
        self.bonus_fp_spin.setSingleStep(20)
        self.bonus_fp_spin.setSuffix(' 点每日额外疲劳')

        g2.addWidget(self.weekend_fp_chk, 0, 0, 1, 2)
        g2.addWidget(self.burning_fp_chk, 1, 0, 1, 2)
        g2.addWidget(self.zero_fp_chk, 2, 0, 1, 2)
        g2.addWidget(self.pcbang_chk, 3, 0, 1, 2)
        g2.addWidget(QLabel('每日额外疲劳上限:'), 4, 0)
        g2.addWidget(self.bonus_fp_spin, 4, 1)
        l2.addLayout(g2)
        lay.addWidget(c2)

        # Card 3: Quick global_drop.json master switch
        c3, l3 = _make_card('全局装备掉落 & 100级智慧的引导（史诗/神话深渊）快捷开关', '直接联动「全局掉落设置」页的 runtime/launcher-gm/global_drop.json')
        gd_row = QHBoxLayout()
        self.gd_status_lbl = QLabel('当前状态：未启用')
        self.gd_status_lbl.setStyleSheet('color:#ffd700;font-weight:bold;border:none;background:transparent;')
        btn_gd_on = QPushButton('⚡ 一键开启默认全局掉落 + 智慧的引导')
        btn_gd_on.setObjectName('GoldBtn')
        btn_gd_on.clicked.connect(lambda: self._toggle_global_drop(True))
        btn_gd_off = QPushButton('关闭全局掉落加成')
        btn_gd_off.setObjectName('DarkBtn')
        btn_gd_off.clicked.connect(lambda: self._toggle_global_drop(False))
        btn_gd_jump = QPushButton('前往详细配置 →')
        btn_gd_jump.setObjectName('DarkBtn')
        btn_gd_jump.clicked.connect(lambda: self.host.switch_page(2))
        gd_row.addWidget(self.gd_status_lbl, 1)
        gd_row.addWidget(btn_gd_on)
        gd_row.addWidget(btn_gd_off)
        gd_row.addWidget(btn_gd_jump)
        l3.addLayout(gd_row)
        lay.addWidget(c3)
        lay.addStretch()
        return _scroll_container(inner)

    # ---------------- Tab 2: Medium Protocol Events ----------------
    def _build_protocol_events_tab(self) -> QWidget:
        inner = QWidget()
        lay = QVBoxLayout(inner)
        lay.setContentsMargins(12, 12, 12, 12)
        lay.setSpacing(12)

        # 1. Native Hell Party (NOTI 28 RandomHell)
        c1, l1 = _make_card('原生深渊派对触发（NOTI 28 随机深渊房间）', '进图时按概率在非起点/非Boss房写入 NOTI 28 随机深渊坐标，清完该房间爆发史诗/神话深渊掉落')
        g1 = QGridLayout()
        self.hell_chk = QCheckBox('开启原生深渊派对活动')
        self.hell_chance_spin = QSpinBox()
        self.hell_chance_spin.setRange(0, 100)
        self.hell_chance_spin.setSuffix(' %')
        self.hell_minlv_spin = QSpinBox()
        self.hell_minlv_spin.setRange(1, 100)
        self.hell_minlv_spin.setPrefix('Lv.')
        self.hell_count_spin = QSpinBox()
        self.hell_count_spin.setRange(1, 5)
        self.hell_count_spin.setSuffix(' 件')
        self.hell_epic_spin = QSpinBox()
        self.hell_epic_spin.setRange(0, 100)
        self.hell_epic_spin.setSuffix(' 史诗权重')
        self.hell_mythic_spin = QSpinBox()
        self.hell_mythic_spin.setRange(0, 100)
        self.hell_mythic_spin.setSuffix(' 神话权重')

        g1.addWidget(self.hell_chk, 0, 0, 1, 4)
        g1.addWidget(QLabel('触发概率:'), 1, 0)
        g1.addWidget(self.hell_chance_spin, 1, 1)
        g1.addWidget(QLabel('最低副本等级:'), 1, 2)
        g1.addWidget(self.hell_minlv_spin, 1, 3)
        g1.addWidget(QLabel('清房爆率件数:'), 2, 0)
        g1.addWidget(self.hell_count_spin, 2, 1)
        g1.addWidget(QLabel('史诗 / 神话权重:'), 2, 2)
        hw = QHBoxLayout()
        hw.addWidget(self.hell_epic_spin)
        hw.addWidget(self.hell_mythic_spin)
        g1.addLayout(hw, 2, 3)
        l1.addLayout(g1)
        lay.addWidget(c1)

        # 2. Daily Challenge (PVF etc/dailychallengetable.etc)
        c2, l2 = _make_card('每日挑战自动发奖（dailychallengetable.etc）', '当日击杀与通关达标后自动直发背包（NOTI 14）并累加商城积分（NOTI 53）')
        g2 = QGridLayout()
        self.daily_chk = QCheckBox('开启每日挑战系统')
        self.daily_kill_spin = QSpinBox()
        self.daily_kill_spin.setRange(1, 500)
        self.daily_kill_spin.setSuffix(' 只怪物')
        self.daily_clear_spin = QSpinBox()
        self.daily_clear_spin.setRange(1, 50)
        self.daily_clear_spin.setSuffix(' 次通关')
        self.daily_gold_spin = QSpinBox()
        self.daily_gold_spin.setRange(0, 10000000)
        self.daily_gold_spin.setSingleStep(50000)
        self.daily_gold_spin.setSuffix(' 金币')
        self.daily_mileage_spin = QSpinBox()
        self.daily_mileage_spin.setRange(0, 500)
        self.daily_mileage_spin.setSuffix(' 积分')

        g2.addWidget(self.daily_chk, 0, 0, 1, 4)
        g2.addWidget(QLabel('每日击杀目标:'), 1, 0)
        g2.addWidget(self.daily_kill_spin, 1, 1)
        g2.addWidget(QLabel('每日通关目标:'), 1, 2)
        g2.addWidget(self.daily_clear_spin, 1, 3)
        g2.addWidget(QLabel('达标金币奖励:'), 2, 0)
        g2.addWidget(self.daily_gold_spin, 2, 1)
        g2.addWidget(QLabel('达标积分奖励:'), 2, 2)
        g2.addWidget(self.daily_mileage_spin, 2, 3)
        l2.addLayout(g2)
        lay.addWidget(c2)

        # 3. Weekly Dungeon (PVF etc/weeklydungeon.etc)
        c3, l3 = _make_card('周常地下城奖励（weeklydungeon.etc · 每周二重置）', '每周前 N 次通关额外发放协调结晶体、金币与商城积分')
        g3 = QGridLayout()
        self.weekly_chk = QCheckBox('开启周常地下城奖励')
        self.weekly_limit_spin = QSpinBox()
        self.weekly_limit_spin.setRange(1, 30)
        self.weekly_limit_spin.setSuffix(' 次/周')
        self.weekly_gold_spin = QSpinBox()
        self.weekly_gold_spin.setRange(0, 50000000)
        self.weekly_gold_spin.setSingleStep(100000)
        self.weekly_gold_spin.setSuffix(' 金币')
        self.weekly_mileage_spin = QSpinBox()
        self.weekly_mileage_spin.setRange(0, 500)
        self.weekly_mileage_spin.setSuffix(' 积分')

        g3.addWidget(self.weekly_chk, 0, 0, 1, 4)
        g3.addWidget(QLabel('每周奖励次数:'), 1, 0)
        g3.addWidget(self.weekly_limit_spin, 1, 1)
        g3.addWidget(QLabel('单次金币 / 积分:'), 1, 2)
        wr = QHBoxLayout()
        wr.addWidget(self.weekly_gold_spin)
        wr.addWidget(self.weekly_mileage_spin)
        g3.addLayout(wr, 1, 3)
        l3.addLayout(g3)
        lay.addWidget(c3)

        # 4. Extended Contract Effects
        c4, l4 = _make_card('契约进阶效果实装开关', '为已购买或登录器激活的成长/加百利/不稳定裂缝契约启用服务端实际增益')
        self.contract_growth_chk = QCheckBox('成长之契约 (Kind 79) 生效 +20% 怪物基础掉落率加成')
        self.contract_gabriel_chk = QCheckBox('加百利之契约 (Kind 73) 通关 35% 概率触发加百利材料/金币补给')
        self.contract_rift_chk = QCheckBox('不稳定裂缝契约 (Kind 125) 深渊派对房间额外增加 +1 件掉落判定')
        l4.addWidget(self.contract_growth_chk)
        l4.addWidget(self.contract_gabriel_chk)
        l4.addWidget(self.contract_rift_chk)
        lay.addWidget(c4)
        lay.addStretch()
        return _scroll_container(inner)

    # ---------------- Tab 3: Mileage Shop & Points ----------------
    def _build_mileage_tab(self) -> QWidget:
        inner = QWidget()
        lay = QVBoxLayout(inner)
        lay.setContentsMargins(12, 12, 12, 12)
        lay.setSpacing(12)

        c1, l1 = _make_card('商城积分（Mileage）获取规则（NOTI 53 实时同步）', '通关地下城自动累计积分、商城消费 CERA 按比例返还积分')
        g1 = QGridLayout()
        self.mileage_chk = QCheckBox('开启商城积分活动')
        self.mileage_clear_spin = QSpinBox()
        self.mileage_clear_spin.setRange(0, 100)
        self.mileage_clear_spin.setSuffix(' 积分/次通关')
        self.mileage_cap_spin = QSpinBox()
        self.mileage_cap_spin.setRange(0, 9999)
        self.mileage_cap_spin.setSuffix(' 每日上限 (0=无限)')
        self.mileage_rebate_spin = QSpinBox()
        self.mileage_rebate_spin.setRange(0, 50)
        self.mileage_rebate_spin.setSuffix(' % 消费返利')

        g1.addWidget(self.mileage_chk, 0, 0, 1, 4)
        g1.addWidget(QLabel('通关赠送:'), 1, 0)
        g1.addWidget(self.mileage_clear_spin, 1, 1)
        g1.addWidget(QLabel('每日刷图上限:'), 1, 2)
        g1.addWidget(self.mileage_cap_spin, 1, 3)
        g1.addWidget(QLabel('商城消费返利:'), 2, 0)
        g1.addWidget(self.mileage_rebate_spin, 2, 1)

        adj_row = QHBoxLayout()
        self.mileage_adj_spin = QSpinBox()
        self.mileage_adj_spin.setRange(1, 100000)
        self.mileage_adj_spin.setValue(500)
        btn_add_m = QPushButton('➕ 发放积分')
        btn_add_m.setObjectName('DarkBtn')
        btn_add_m.clicked.connect(lambda: self._adjust_mileage('add'))
        btn_set_m = QPushButton('设为该值')
        btn_set_m.setObjectName('DarkBtn')
        btn_set_m.clicked.connect(lambda: self._adjust_mileage('set'))
        adj_row.addWidget(self.mileage_adj_spin)
        adj_row.addWidget(btn_add_m)
        adj_row.addWidget(btn_set_m)
        g1.addWidget(QLabel('GM 积分调整:'), 2, 2)
        g1.addLayout(adj_row, 2, 3)
        l1.addLayout(g1)
        lay.addWidget(c1)

        c2, l2 = _make_card('US100 积分商城兑换（etc/mileageshop.etc）', '扣除账号积分并直接发入当前角色背包，游戏在线时实时同步 NOTI 14 与 NOTI 53')
        self.mileage_table = QTableWidget(len(core.MILEAGE_SHOP_ITEMS), 5)
        self.mileage_table.setHorizontalHeaderLabels(['模板 ID', '商品名称', '数量', '所需积分', '来源说明'])
        self.mileage_table.setSelectionBehavior(QTableWidget.SelectRows)
        self.mileage_table.setSelectionMode(QTableWidget.SingleSelection)
        self.mileage_table.setEditTriggers(QTableWidget.NoEditTriggers)
        self.mileage_table.verticalHeader().setVisible(False)
        hdr = self.mileage_table.horizontalHeader()
        hdr.setSectionResizeMode(0, QHeaderView.ResizeToContents)
        hdr.setSectionResizeMode(1, QHeaderView.Stretch)
        hdr.setSectionResizeMode(2, QHeaderView.ResizeToContents)
        hdr.setSectionResizeMode(3, QHeaderView.ResizeToContents)
        hdr.setSectionResizeMode(4, QHeaderView.Stretch)
        for row_idx, (tid, name, cost, qty, note) in enumerate(core.MILEAGE_SHOP_ITEMS):
            self.mileage_table.setItem(row_idx, 0, QTableWidgetItem(str(tid)))
            self.mileage_table.setItem(row_idx, 1, QTableWidgetItem(name))
            self.mileage_table.setItem(row_idx, 2, QTableWidgetItem(str(qty)))
            self.mileage_table.setItem(row_idx, 3, QTableWidgetItem(f'{cost} 积分'))
            self.mileage_table.setItem(row_idx, 4, QTableWidgetItem(note))
        self.mileage_table.selectRow(0)
        self.mileage_table.setMinimumHeight(230)
        l2.addWidget(self.mileage_table)

        redeem_row = QHBoxLayout()
        btn_redeem = QPushButton('🛒 消耗积分兑换所选商品')
        btn_redeem.setObjectName('GoldBtn')
        btn_redeem.clicked.connect(self.redeem_selected_mileage_item)
        redeem_row.addWidget(btn_redeem)
        redeem_row.addStretch()
        l2.addLayout(redeem_row)
        lay.addWidget(c2)
        return _scroll_container(inner)

    # ---------------- Tab 4: Extra Operational Activities & Contracts ----------------
    def _build_extra_activities_tab(self) -> QWidget:
        inner = QWidget()
        lay = QVBoxLayout(inner)
        lay.setContentsMargins(12, 12, 12, 12)
        lay.setSpacing(12)

        # Section A: All 8 Contracts live toggle
        c1, l1 = _make_card('全套 8 项契约特权（在线实时生效 NOTI 66 + NOTI 36）', '无需进游戏商城购买，可随时为账号激活/续期或关闭任意契约')
        top_c = QHBoxLayout()
        btn_all_30 = QPushButton('👑 一键全开 8 项契约 (+30天)')
        btn_all_30.setObjectName('GoldBtn')
        btn_all_30.clicked.connect(lambda: self.apply_contract(255, 30))
        btn_all_7 = QPushButton('全部体验 +7天')
        btn_all_7.setObjectName('DarkBtn')
        btn_all_7.clicked.connect(lambda: self.apply_contract(255, 7))
        btn_all_clear = QPushButton('清除全部契约')
        btn_all_clear.setObjectName('DarkBtn')
        btn_all_clear.clicked.connect(lambda: self.apply_contract(255, 0))
        top_c.addWidget(btn_all_30)
        top_c.addWidget(btn_all_7)
        top_c.addWidget(btn_all_clear)
        top_c.addStretch()
        l1.addLayout(top_c)

        cg = QGridLayout()
        cg.setHorizontalSpacing(10)
        cg.setVerticalSpacing(6)
        for idx, (kind, cname, cdesc) in enumerate(core.CONTRACT_SPECS):
            name_lbl = QLabel(f'{cname} (#{kind})')
            name_lbl.setStyleSheet('font-weight:bold;color:#f5ebd4;')
            desc_lbl = QLabel(cdesc)
            desc_lbl.setStyleSheet('font-size:11px;color:#b2a792;')
            st_lbl = QLabel('未激活')
            st_lbl.setStyleSheet('color:#94a3b8;font-size:11px;')
            self._contract_labels[kind] = st_lbl

            b7 = QPushButton('+7天')
            b7.setObjectName('DarkBtn')
            b7.clicked.connect(lambda checked=False, k=kind: self.apply_contract(k, 7))
            b30 = QPushButton('+30天')
            b30.setObjectName('DarkBtn')
            b30.clicked.connect(lambda checked=False, k=kind: self.apply_contract(k, 30))
            b0 = QPushButton('关')
            b0.setObjectName('DarkBtn')
            b0.clicked.connect(lambda checked=False, k=kind: self.apply_contract(k, 0))

            cg.addWidget(name_lbl, idx, 0)
            cg.addWidget(desc_lbl, idx, 1)
            cg.addWidget(st_lbl, idx, 2)
            cg.addWidget(b7, idx, 3)
            cg.addWidget(b30, idx, 4)
            cg.addWidget(b0, idx, 5)
        l1.addLayout(cg)
        lay.addWidget(c1)

        # Section B: Operational activity toggles + direct claim
        c2, l2 = _make_card('其他官方运营活动（登录器随时开关 & 一键领取）', '每日签到、赛丽亚的欢迎、回归关怀、OTP 安全认证与 100 级冲刺补给，发奖直入背包并实时同步')
        for spec in EXTRA_ACTIVITY_CARDS:
            row_frame = QFrame()
            row_frame.setStyleSheet('QFrame{background:rgba(24,20,36,0.65);border:1px solid rgba(212,175,55,0.22);border-radius:8px;}')
            rl = QHBoxLayout(row_frame)
            rl.setContentsMargins(12, 8, 12, 8)
            rl.setSpacing(10)

            pf = spec['policy_field']
            if pf not in self._extra_chks:
                chk = QCheckBox('启用')
                self._extra_chks[pf] = chk
                rl.addWidget(chk)

            info = QVBoxLayout()
            info.setSpacing(2)
            lt = QLabel(spec['title'])
            lt.setStyleSheet('font-weight:bold;color:#ffe285;border:none;background:transparent;')
            ld = QLabel(spec['desc'])
            ld.setStyleSheet('font-size:11px;color:#cfc4ae;border:none;background:transparent;')
            info.addWidget(lt)
            info.addWidget(ld)
            rl.addLayout(info, 1)

            st = QLabel('可领取')
            st.setStyleSheet('font-size:11px;color:#4ade80;border:none;background:transparent;')
            self._claim_status_lbls[spec['key']] = st
            rl.addWidget(st)

            btn_claim = QPushButton('🎁 立即领取')
            btn_claim.setObjectName('GoldBtn')
            btn_claim.clicked.connect(lambda checked=False, s=spec: self.claim_extra_activity(s))
            rl.addWidget(btn_claim)
            l2.addWidget(row_frame)
        lay.addWidget(c2)
        lay.addStretch()
        return _scroll_container(inner)

    # ---------------- Data Sync & Actions ----------------
    def activate(self):
        self._load_policy_into_ui()
        self.refresh_roles()
        self.refresh_state()

    def refresh_roles(self):
        cur_id = self.selected_role_id()
        self._roles = core.roles()
        self.role_combo.blockSignals(True)
        self.role_combo.clear()
        for rid, name in self._roles:
            self.role_combo.addItem(f'{name}  (#{rid})', rid)
        if cur_id:
            idx = self.role_combo.findData(cur_id)
            if idx >= 0:
                self.role_combo.setCurrentIndex(idx)
        self.role_combo.blockSignals(False)

    def selected_role_id(self) -> int:
        data = self.role_combo.currentData()
        return int(data) if data else (self._roles[0][0] if self._roles else 0)

    def _load_policy_into_ui(self):
        self._policy, self._policy_exists = core.load_events_policy()
        p = self._policy
        self.exp_chk.setChecked(bool(p.get('exp_event_enabled')))
        self.exp_spin.setValue(float(p.get('exp_multiplier', 1.0)))
        self.drop_chk.setChecked(bool(p.get('drop_event_enabled')))
        self.drop_spin.setValue(float(p.get('drop_rate_multiplier', 1.0)))
        self.gold_spin.setValue(float(p.get('gold_multiplier', 1.0)))
        self.card_chk.setChecked(bool(p.get('card_event_enabled')))
        self.card_spin.setValue(float(p.get('card_gold_multiplier', 1.0)))

        fp = p.get('fatigue') or {}
        self.weekend_fp_chk.setChecked(bool(fp.get('weekend_double')))
        self.burning_fp_chk.setChecked(bool(fp.get('burning_enabled')))
        self.zero_fp_chk.setChecked(bool(fp.get('zero_cost')))
        self.bonus_fp_spin.setValue(int(fp.get('bonus_limit', 0)))

        ml = p.get('mileage') or {}
        self.mileage_chk.setChecked(bool(ml.get('enabled')))
        self.mileage_clear_spin.setValue(int(ml.get('dungeon_clear_points', 2)))
        self.mileage_cap_spin.setValue(int(ml.get('daily_cap', 50)))
        self.mileage_rebate_spin.setValue(int(ml.get('shop_rebate_percent', 5)))

        hp = p.get('hell_party') or {}
        self.hell_chk.setChecked(bool(hp.get('enabled')))
        self.hell_chance_spin.setValue(int(hp.get('trigger_chance', 35)))
        self.hell_minlv_spin.setValue(int(hp.get('min_dungeon_level', 1)))
        self.hell_count_spin.setValue(int(hp.get('drop_count', 2)))
        self.hell_epic_spin.setValue(int(hp.get('epic_weight', 85)))
        self.hell_mythic_spin.setValue(int(hp.get('mythic_weight', 15)))

        dc = p.get('daily_challenge') or {}
        self.daily_chk.setChecked(bool(dc.get('enabled')))
        self.daily_kill_spin.setValue(int(dc.get('kill_target', 15)))
        self.daily_clear_spin.setValue(int(dc.get('clear_target', 3)))
        self.daily_gold_spin.setValue(int(dc.get('reward_gold', 200000)))
        self.daily_mileage_spin.setValue(int(dc.get('reward_mileage', 10)))

        wd = p.get('weekly_dungeon') or {}
        self.weekly_chk.setChecked(bool(wd.get('enabled')))
        self.weekly_limit_spin.setValue(int(wd.get('weekly_limit', 6)))
        self.weekly_gold_spin.setValue(int(wd.get('reward_gold', 500000)))
        self.weekly_mileage_spin.setValue(int(wd.get('reward_mileage', 15)))

        ct = p.get('contracts') or {}
        self.contract_growth_chk.setChecked(bool(ct.get('growth_drop_bonus')))
        self.contract_gabriel_chk.setChecked(bool(ct.get('gabriel_bonus')))
        self.contract_rift_chk.setChecked(bool(ct.get('unstable_rift_bonus')))

        ex = p.get('extra') or {}
        self.pcbang_chk.setChecked(bool(ex.get('pcbang_bonus_enabled')))
        for field, chk in self._extra_chks.items():
            chk.setChecked(bool(ex.get(field, True)))

    def collect_policy(self) -> dict:
        extra = {field: chk.isChecked() for field, chk in self._extra_chks.items()}
        extra['pcbang_bonus_enabled'] = self.pcbang_chk.isChecked()
        return {
            'version': 1,
            'exp_event_enabled': self.exp_chk.isChecked(),
            'exp_multiplier': round(self.exp_spin.value(), 2),
            'drop_event_enabled': self.drop_chk.isChecked(),
            'drop_rate_multiplier': round(self.drop_spin.value(), 2),
            'gold_multiplier': round(self.gold_spin.value(), 2),
            'card_event_enabled': self.card_chk.isChecked(),
            'card_gold_multiplier': round(self.card_spin.value(), 2),
            'fatigue': {
                'weekend_double': self.weekend_fp_chk.isChecked(),
                'bonus_limit': int(self.bonus_fp_spin.value()),
                'zero_cost': self.zero_fp_chk.isChecked(),
                'burning_enabled': self.burning_fp_chk.isChecked(),
            },
            'mileage': {
                'enabled': self.mileage_chk.isChecked(),
                'dungeon_clear_points': int(self.mileage_clear_spin.value()),
                'daily_cap': int(self.mileage_cap_spin.value()),
                'shop_rebate_percent': int(self.mileage_rebate_spin.value()),
            },
            'hell_party': {
                'enabled': self.hell_chk.isChecked(),
                'trigger_chance': int(self.hell_chance_spin.value()),
                'min_dungeon_level': int(self.hell_minlv_spin.value()),
                'drop_count': int(self.hell_count_spin.value()),
                'epic_weight': int(self.hell_epic_spin.value()),
                'mythic_weight': int(self.hell_mythic_spin.value()),
            },
            'daily_challenge': {
                'enabled': self.daily_chk.isChecked(),
                'kill_target': int(self.daily_kill_spin.value()),
                'clear_target': int(self.daily_clear_spin.value()),
                'reward_gold': int(self.daily_gold_spin.value()),
                'reward_mileage': int(self.daily_mileage_spin.value()),
                'reward_template': 10327281,
                'reward_amount': 30,
            },
            'weekly_dungeon': {
                'enabled': self.weekly_chk.isChecked(),
                'weekly_limit': int(self.weekly_limit_spin.value()),
                'reset_weekday': 2,
                'reward_gold': int(self.weekly_gold_spin.value()),
                'reward_mileage': int(self.weekly_mileage_spin.value()),
                'reward_template': 10327282,
                'reward_amount': 20,
            },
            'contracts': {
                'growth_drop_bonus': self.contract_growth_chk.isChecked(),
                'gabriel_bonus': self.contract_gabriel_chk.isChecked(),
                'unstable_rift_bonus': self.contract_rift_chk.isChecked(),
            },
            'extra': extra,
        }

    def refresh_state(self):
        if not self._roles:
            self.refresh_roles()
        p = self.collect_policy()
        active_count = sum(1 for v in (
            p['exp_event_enabled'], p['drop_event_enabled'], p['card_event_enabled'],
            p['fatigue']['weekend_double'], p['fatigue']['burning_enabled'], p['fatigue']['zero_cost'],
            p['mileage']['enabled'], p['hell_party']['enabled'],
            p['daily_challenge']['enabled'], p['weekly_dungeon']['enabled'],
            p['extra'].get('pcbang_bonus_enabled', False),
        ) if v)
        if self._policy_exists:
            self.badge_events_state.setText(f'已启用 {active_count} 项热活动')
            self.badge_events_state.setStyleSheet('font-size:15px;font-weight:bold;color:#4ade80;border:none;background:transparent;')
        else:
            self.badge_events_state.setText('未保存（原版默认）')
            self.badge_events_state.setStyleSheet('font-size:15px;font-weight:bold;color:#cbd5e1;border:none;background:transparent;')

        mileage = core.account_mileage()
        self.badge_mileage.setText(f'{mileage:,}')

        rid = self.selected_role_id()
        prog = core.activity_progress(rid) if rid else core.activity_progress(0)
        today = datetime.date.today().isoformat()
        dk = prog['daily_kills'] if prog['day'] == today else 0
        dc = prog['daily_clears'] if prog['day'] == today else 0
        self.badge_daily.setText(f'{dk} 杀 / {dc} 通关')
        wl = int(p['weekly_dungeon']['weekly_limit'])
        self.badge_weekly.setText(f"{prog['weekly_clears']}/{wl} 周常 · {prog['total_hell_clears']} 深渊")

        contracts = core.account_contracts()
        for kind, lbl in self._contract_labels.items():
            rem = contracts.get(kind, 0)
            if rem > 0:
                days = max(1, (rem + 86399) // 86400)
                lbl.setText(f'生效中 · 约 {days} 天')
                lbl.setStyleSheet('color:#4ade80;font-weight:bold;font-size:11px;')
            else:
                lbl.setText('未激活')
                lbl.setStyleSheet('color:#94a3b8;font-size:11px;')

        claims = prog.get('claims') or {}
        for spec in EXTRA_ACTIVITY_CARDS:
            ck = f"{spec['key']}:{today}" if spec['per_day'] else spec['key']
            lbl = self._claim_status_lbls.get(spec['key'])
            if lbl:
                if ck in claims:
                    lbl.setText('已领取 ✓')
                    lbl.setStyleSheet('font-size:11px;color:#94a3b8;border:none;background:transparent;')
                else:
                    lbl.setText('可领取')
                    lbl.setStyleSheet('font-size:11px;color:#4ade80;font-weight:bold;border:none;background:transparent;')

        gd, gd_exists = core.load_global_drop(core.DATA)
        gd_on = gd_exists and (gd['global']['enabled'] or gd['normal_abyss']['enabled'] or gd['lv100_abyss']['enabled'])
        self.gd_status_lbl.setText('当前状态：全局掉落 / 100级智慧的引导 已开启 ✓' if gd_on else '当前状态：未开启（仅原版掉落）')

    def _enqueue_and_track(self, action: str, payload: dict, reason: str):
        job_id = core.submit_op(action, payload, reason=reason)
        self._last_job_id = job_id
        self.result_lbl.setText(f'已提交在线操作（{job_id[:8]}），等待网关执行…')
        self.host.statusBar().showMessage(f'已提交：{reason}')
        QTimer.singleShot(1200, lambda jid=job_id: self._poll_receipt(jid))
        return job_id

    def _poll_receipt(self, job_id: str):
        status, data = core.read_job(job_id)
        if status == 'receipts' and isinstance(data, dict):
            if data.get('error'):
                self.result_lbl.setText(f"操作失败：{data['error']}")
            elif isinstance(data.get('op_receipt'), dict):
                self.result_lbl.setText(data['op_receipt'].get('summary') or '操作已完成。')
            else:
                self.result_lbl.setText('操作已完成。')
            self.refresh_state()

    def save_policy(self):
        pol = self.collect_policy()
        path = core.save_events_policy(pol)
        self._policy = pol
        self._policy_exists = True
        rid = self.selected_role_id()
        if rid:
            self._enqueue_and_track('refresh_sync', {'character_id': rid}, '活动配置保存并实时同步')
        self.result_lbl.setText(f'活动配置已保存至 {path.name}，网关热生效！')
        self.host.statusBar().showMessage('服务器活动配置已保存并热生效。')
        self.refresh_state()

    def apply_recommended_preset(self):
        self._policy = core.default_events_policy(active=True)
        path = core.save_events_policy(self._policy)
        self._policy_exists = True
        self._load_policy_into_ui()
        rid = self.selected_role_id()
        if rid:
            self._enqueue_and_track('refresh_sync', {'character_id': rid}, '一键开启推荐活动并实时同步')
        self.result_lbl.setText(f'已应用推荐全开预设并写入 {path.name}！')
        self.refresh_state()

    def reset_to_vanilla(self):
        self._policy = core.default_events_policy(active=False)
        path = core.save_events_policy(self._policy)
        self._policy_exists = True
        self._load_policy_into_ui()
        rid = self.selected_role_id()
        if rid:
            self._enqueue_and_track('refresh_sync', {'character_id': rid}, '恢复原版默认活动并实时同步')
        self.result_lbl.setText(f'已关闭全部活动并恢复原版默认（{path.name}）。')
        self.refresh_state()

    def _toggle_global_drop(self, enable: bool):
        gd, _ = core.load_global_drop(core.DATA)
        gd['global']['enabled'] = bool(enable)
        gd['normal_abyss']['enabled'] = bool(enable)
        gd['lv100_abyss']['enabled'] = bool(enable)
        core.save_global_drop(gd, core.DATA)
        self.result_lbl.setText('已开启全局装备掉落 + 普通/100级智慧的引导深渊掉落！' if enable else '已关闭全局掉落与深渊加成。')
        self.refresh_state()

    def _adjust_mileage(self, mode: str):
        rid = self.selected_role_id()
        if not rid:
            raise ValueError('请先在游戏内创建角色。')
        val = int(self.mileage_adj_spin.value())
        self._enqueue_and_track(
            'currency',
            {'character_id': rid, 'mileage_mode': mode, 'mileage': val},
            f'GM 积分调整 ({mode} {val})',
        )

    def redeem_selected_mileage_item(self):
        rid = self.selected_role_id()
        if not rid:
            raise ValueError('请先在游戏内创建角色。')
        row = self.mileage_table.currentRow()
        if row < 0 or row >= len(core.MILEAGE_SHOP_ITEMS):
            raise ValueError('请先选择一件积分商品。')
        tid, name, cost, qty, _note = core.MILEAGE_SHOP_ITEMS[row]
        if QMessageBox.question(self, '确认积分兑换', f'消耗 {cost} 积分兑换 {name}（×{qty}）？') != QMessageBox.Yes:
            return
        self._enqueue_and_track(
            'mileage_redeem',
            {
                'character_id': rid,
                'mileage_cost': int(cost),
                'reward_items': [{'template': int(tid), 'amount': int(qty)}],
            },
            f'积分商城兑换 {name}',
        )

    def apply_contract(self, kind: int, days: int):
        rid = self.selected_role_id()
        if not rid:
            raise ValueError('请先在游戏内创建角色。')
        label = '全部 8 项契约' if kind == 255 else f'契约 #{kind}'
        action_txt = '关闭' if days == 0 else f'激活/延长 +{days} 天'
        self._enqueue_and_track(
            'contract',
            {'character_id': rid, 'contract_kind': int(kind), 'contract_days': int(days)},
            f'活动中心：{label}{action_txt}',
        )

    def claim_extra_activity(self, spec: dict):
        rid = self.selected_role_id()
        if not rid:
            raise ValueError('请先在游戏内创建角色。')
        chk = self._extra_chks.get(spec['policy_field'])
        if chk is not None and not chk.isChecked():
            raise ValueError(f"活动「{spec['title']}」当前处于关闭状态，请先勾选启用。")
        payload = {
            'character_id': rid,
            'activity_key': spec['key'],
            'claim_date': datetime.date.today().isoformat() if spec['per_day'] else '',
            'reward_gold': int(spec['reward_gold']),
            'reward_mileage': int(spec['reward_mileage']),
            'reward_items': list(spec['reward_items']),
            'reward_contracts': list(spec['reward_contracts']),
            'reward_contract_days': int(spec['reward_contract_days']),
        }
        self._enqueue_and_track('activity_claim', payload, f"领取活动：{spec['title']}")
