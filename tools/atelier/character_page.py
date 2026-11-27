"""Character Attributes & Management module (Level, Advancement, Currency, Bag, Quests)."""
import struct
import zlib

from PySide6.QtCore import Qt, QTimer
from PySide6.QtGui import QColor, QIcon
from PySide6.QtWidgets import (
    QAbstractItemView,
    QCheckBox,
    QComboBox,
    QFrame,
    QGridLayout,
    QHBoxLayout,
    QHeaderView,
    QLabel,
    QLineEdit,
    QMessageBox,
    QPushButton,
    QSpinBox,
    QTabWidget,
    QTableWidget,
    QTableWidgetItem,
    QVBoxLayout,
    QWidget,
)

import core
from character_data import (
    ATTR_LABELS,
    QuestCatalog,
    advancement_name,
    advancement_options,
    bag_section_label,
    profession_name,
    profession_token,
    worn_slot_label,
)
from epic_ui import RARITY_COLORS, pix

TABLE_STYLE = """
QTableWidget {
    background-color: #121019;
    color: #f4efe6;
    gridline-color: rgba(212, 175, 55, 0.16);
    border: 1px solid rgba(212, 175, 55, 0.28);
    border-radius: 8px;
    selection-background-color: rgba(212, 175, 55, 0.28);
    selection-color: #ffffff;
}
QHeaderView::section {
    background-color: #1c1826;
    color: #ffd700;
    border: none;
    border-bottom: 1px solid rgba(212, 175, 55, 0.35);
    padding: 6px 8px;
    font-weight: bold;
    font-size: 12px;
}
"""


def _btn(text, slot, gold=False):
    b = QPushButton(text)
    b.setObjectName('GoldBtn' if gold else 'DarkBtn')
    b.clicked.connect(lambda checked=False: slot())
    return b


def _stat_badge(caption):
    card = QFrame()
    card.setStyleSheet(
        "QFrame { background-color: #14111d; border: 1px solid rgba(212, 175, 55, 0.32); border-radius: 8px; }"
    )
    lay = QVBoxLayout(card)
    lay.setContentsMargins(10, 7, 10, 7)
    lay.setSpacing(2)
    cap = QLabel(caption)
    cap.setStyleSheet("color: #a99f8d; font-size: 11px; border: none;")
    val = QLabel("-")
    val.setStyleSheet("color: #ffd700; font-size: 14px; font-weight: bold; border: none;")
    lay.addWidget(cap)
    lay.addWidget(val)
    return card, val


class CharacterPage(QWidget):
    """Dark-gold character management console integrated into EpicWindow."""

    def __init__(self, host):
        super().__init__(host)
        self.host = host
        self.quests_catalog = QuestCatalog()
        self.snapshot = None
        self.pending_job = ''
        self._bag_rows = []
        self._quest_rows = []
        self._catalog_hits = []
        self._poll_timer = QTimer(self)
        self._poll_timer.setInterval(500)
        self._poll_timer.timeout.connect(self._poll_receipt)

        self._build_ui()

    def _build_ui(self):
        root = QVBoxLayout(self)
        root.setContentsMargins(20, 16, 20, 14)
        root.setSpacing(12)

        # Header
        header = QHBoxLayout()
        title_box = QVBoxLayout()
        title_box.setSpacing(2)
        title = QLabel("角色属性与管理中心")
        title.setObjectName("TitleLabel")
        sub = QLabel("等级 · 转职 · 点券金币 · 背包管理 · 任务管理（网关在线时自动同步，离线排队等待启动处理）")
        sub.setObjectName("SubLabel")
        title_box.addWidget(title)
        title_box.addWidget(sub)
        header.addLayout(title_box, 1)

        self.role_combo = QComboBox()
        self.role_combo.setMinimumWidth(210)
        self.role_combo.currentIndexChanged.connect(self.refresh_snapshot)
        header.addWidget(QLabel("当前角色:"))
        header.addWidget(self.role_combo)
        header.addWidget(_btn("刷新数据", self.activate))
        header.addWidget(_btn("仅启动网关", lambda: self.host.start_work('正在检查网关…', lambda: core.login(True))))
        root.addLayout(header)

        # Top overview strip
        summary_card = QFrame()
        summary_card.setObjectName("PanelCard")
        s_lay = QHBoxLayout(summary_card)
        s_lay.setContentsMargins(12, 10, 12, 10)
        s_lay.setSpacing(10)
        c1, self.badge_job = _stat_badge("职业 / 转职")
        c2, self.badge_level = _stat_badge("等级 / SP / TP")
        c3, self.badge_cera = _stat_badge("账号点券 (Cera)")
        c4, self.badge_gold = _stat_badge("角色金币 (Gold)")
        c5, self.badge_bag = _stat_badge("背包 / 任务")
        for c in (c1, c2, c3, c4, c5):
            s_lay.addWidget(c, 1)
        root.addWidget(summary_card)

        # Sub-tabs
        self.tabs = QTabWidget()
        self.tabs.addTab(self._build_level_job_tab(), "⚔ 等级与转职")
        self.tabs.addTab(self._build_currency_tab(), "💎 点券与金币")
        self.tabs.addTab(self._build_bag_tab(), "🎒 背包管理")
        self.tabs.addTab(self._build_quest_tab(), "📜 任务管理")
        self.tabs.currentChanged.connect(self._on_tab_changed)
        root.addWidget(self.tabs, 1)

        # Receipt / feedback bar
        foot = QFrame()
        foot.setObjectName("PanelCard")
        f_lay = QHBoxLayout(foot)
        f_lay.setContentsMargins(12, 8, 12, 8)
        self.op_status = QLabel("请选择角色并执行操作；提交后自动等待网关回执并刷新面板。")
        self.op_status.setWordWrap(True)
        self.op_status.setStyleSheet("color: #dfc79c; font-size: 12px; border: none;")
        f_lay.addWidget(self.op_status, 1)
        f_lay.addWidget(_btn("查看回执记录", self.host.receipts))
        root.addWidget(foot)

    # ---------------- Tab 0: 等级与转职 ----------------
    def _build_level_job_tab(self):
        page = QWidget()
        lay = QVBoxLayout(page)
        lay.setContentsMargins(14, 14, 14, 14)
        lay.setSpacing(12)

        top = QHBoxLayout()
        top.setSpacing(12)

        # Level card
        lv_card = QFrame()
        lv_card.setObjectName("PanelCard")
        lv_lay = QVBoxLayout(lv_card)
        lv_lay.setContentsMargins(16, 14, 16, 14)
        lv_lay.setSpacing(10)
        lv_title = QLabel("等级、经验与技能点设置")
        lv_title.setStyleSheet("font-size: 15px; font-weight: bold; color: #ffd700; border: none;")
        lv_lay.addWidget(lv_title)

        grid = QGridLayout()
        grid.setHorizontalSpacing(10)
        grid.setVerticalSpacing(8)
        self.level_spin = QSpinBox()
        self.level_spin.setRange(1, 100)
        self.level_spin.setPrefix("Lv. ")

        self.sync_sp_chk = QCheckBox("变级时按目标等级自动重算 SP")
        self.sync_sp_chk.setChecked(True)

        self.override_sp_chk = QCheckBox("自定义 SP:")
        self.sp_spin = QSpinBox()
        self.sp_spin.setRange(0, 65535)
        self.sp_spin.setEnabled(False)
        self.override_sp_chk.toggled.connect(self.sp_spin.setEnabled)

        self.override_tp_chk = QCheckBox("自定义 TP:")
        self.tp_spin = QSpinBox()
        self.tp_spin.setRange(0, 65535)
        self.tp_spin.setEnabled(False)
        self.override_tp_chk.toggled.connect(self.tp_spin.setEnabled)

        grid.addWidget(QLabel("目标等级 (1~100):"), 0, 0)
        grid.addWidget(self.level_spin, 0, 1)
        grid.addWidget(self.sync_sp_chk, 1, 0, 1, 2)
        grid.addWidget(self.override_sp_chk, 2, 0)
        grid.addWidget(self.sp_spin, 2, 1)
        grid.addWidget(self.override_tp_chk, 3, 0)
        grid.addWidget(self.tp_spin, 3, 1)
        lv_lay.addLayout(grid)

        quick_lv = QHBoxLayout()
        for preset in (15, 50, 85, 100):
            label = f"Lv.{preset}" + (" 满级" if preset == 100 else "")
            quick_lv.addWidget(_btn(label, lambda v=preset: self.level_spin.setValue(v)))
        lv_lay.addLayout(quick_lv)

        quick_sp = QHBoxLayout()
        quick_sp.addWidget(_btn("SP 10000", lambda: self._preset_sp(10000)))
        quick_sp.addWidget(_btn("SP 满(65535)", lambda: self._preset_sp(65535)))
        quick_sp.addWidget(_btn("TP 50", lambda: self._preset_tp(50)))
        lv_lay.addLayout(quick_sp)

        lv_lay.addWidget(_btn("⚡ 应用等级与技能点", self._apply_level, gold=True))
        lv_lay.addStretch()
        top.addWidget(lv_card, 1)

        # Advancement card
        adv_card = QFrame()
        adv_card.setObjectName("PanelCard")
        adv_lay = QVBoxLayout(adv_card)
        adv_lay.setContentsMargins(16, 14, 16, 14)
        adv_lay.setSpacing(10)
        adv_title = QLabel("职业转职管理")
        adv_title.setStyleSheet("font-size: 15px; font-weight: bold; color: #ffd700; border: none;")
        adv_lay.addWidget(adv_title)

        self.adv_current_lbl = QLabel("当前基础职业：- · 当前转职：-")
        self.adv_current_lbl.setStyleSheet("color: #cfc5b4; border: none;")
        adv_lay.addWidget(self.adv_current_lbl)

        adv_row = QHBoxLayout()
        adv_row.addWidget(QLabel("目标转职分支:"))
        self.adv_combo = QComboBox()
        adv_row.addWidget(self.adv_combo, 1)
        adv_lay.addLayout(adv_row)

        self.reset_skills_chk = QCheckBox("转职时重置已学技能并按当前等级返还 SP（推荐）")
        self.reset_skills_chk.setChecked(True)
        adv_lay.addWidget(self.reset_skills_chk)

        hint = QLabel("说明：转职后将自动解锁对应分支的技能学习、装备穿戴与转职任务。\n角色列表职业名与城镇外观需返回角色选择界面重新进城生效。")
        hint.setWordWrap(True)
        hint.setStyleSheet("color: #a99f8d; font-size: 11px; border: none;")
        adv_lay.addWidget(hint)

        adv_lay.addWidget(_btn("✦ 确认执行转职", self._apply_advancement, gold=True))
        adv_lay.addStretch()
        top.addWidget(adv_card, 1)
        lay.addLayout(top)

        # Attributes readonly overview
        attr_card = QFrame()
        attr_card.setObjectName("PanelCard")
        a_lay = QVBoxLayout(attr_card)
        a_lay.setContentsMargins(16, 12, 16, 12)
        a_title = QLabel("角色当前基础属性快照（按源表等级成长计算）")
        a_title.setStyleSheet("font-size: 13px; font-weight: bold; color: #ffd700; border: none;")
        a_lay.addWidget(a_title)

        a_grid = QGridLayout()
        a_grid.setHorizontalSpacing(16)
        a_grid.setVerticalSpacing(6)
        self.attr_labels = {}
        for idx, (key, label) in enumerate(ATTR_LABELS):
            r, c = divmod(idx, 4)
            k_lbl = QLabel(label + ":")
            k_lbl.setStyleSheet("color: #a99f8d; font-size: 12px; border: none;")
            v_lbl = QLabel("-")
            v_lbl.setStyleSheet("color: #f4efe6; font-weight: bold; font-size: 12px; border: none;")
            a_grid.addWidget(k_lbl, r, c * 2)
            a_grid.addWidget(v_lbl, r, c * 2 + 1)
            self.attr_labels[key] = v_lbl
        a_lay.addLayout(a_grid)
        lay.addWidget(attr_card)
        lay.addStretch()
        return page

    # ---------------- Tab 1: 点券与金币 ----------------
    def _build_currency_tab(self):
        page = QWidget()
        lay = QHBoxLayout(page)
        lay.setContentsMargins(14, 14, 14, 14)
        lay.setSpacing(14)

        # Cera card
        cera_card = QFrame()
        cera_card.setObjectName("PanelCard")
        c_lay = QVBoxLayout(cera_card)
        c_lay.setContentsMargins(18, 16, 18, 16)
        c_lay.setSpacing(10)
        c_title = QLabel("点券管理 (Cera · 账号共享)")
        c_title.setStyleSheet("font-size: 15px; font-weight: bold; color: #ffd700; border: none;")
        c_lay.addWidget(c_title)

        self.cera_now_lbl = QLabel("当前余额：0 点券")
        self.cera_now_lbl.setStyleSheet("font-size: 18px; font-weight: bold; color: #4ade80; border: none;")
        c_lay.addWidget(self.cera_now_lbl)

        self.cera_mode = QComboBox()
        self.cera_mode.addItem("增加 / 扣除点券（填负数可扣除）", "add")
        self.cera_mode.addItem("直接设为指定点券余额", "set")
        c_lay.addWidget(QLabel("操作方式:"))
        c_lay.addWidget(self.cera_mode)

        self.cera_input = QLineEdit("100000")
        self.cera_input.setPlaceholderText("输入整数，如 100000 或 -5000")
        c_lay.addWidget(QLabel("点券数额:"))
        c_lay.addWidget(self.cera_input)

        q1 = QHBoxLayout()
        q1.addWidget(_btn("+1万", lambda: self._set_currency_input(self.cera_mode, self.cera_input, 'add', 10000)))
        q1.addWidget(_btn("+10万", lambda: self._set_currency_input(self.cera_mode, self.cera_input, 'add', 100000)))
        q1.addWidget(_btn("+100万", lambda: self._set_currency_input(self.cera_mode, self.cera_input, 'add', 1000000)))
        q1.addWidget(_btn("设为999万", lambda: self._set_currency_input(self.cera_mode, self.cera_input, 'set', 9990000)))
        c_lay.addLayout(q1)

        c_lay.addWidget(_btn("💎 确认修改点券", self._apply_cera, gold=True))
        c_note = QLabel("说明：点券保存在 account_currency 表（全账号角色共享）。游戏内商城余额在选角进城时下发，修改后返回选角重进即可刷新显示。")
        c_note.setWordWrap(True)
        c_note.setStyleSheet("color: #a99f8d; font-size: 11px; border: none;")
        c_lay.addWidget(c_note)
        c_lay.addStretch()
        lay.addWidget(cera_card, 1)

        # Gold card
        gold_card = QFrame()
        gold_card.setObjectName("PanelCard")
        g_lay = QVBoxLayout(gold_card)
        g_lay.setContentsMargins(18, 16, 18, 16)
        g_lay.setSpacing(10)
        g_title = QLabel("金币管理 (Gold · 当前角色)")
        g_title.setStyleSheet("font-size: 15px; font-weight: bold; color: #ffd700; border: none;")
        g_lay.addWidget(g_title)

        self.gold_now_lbl = QLabel("当前金币：0")
        self.gold_now_lbl.setStyleSheet("font-size: 18px; font-weight: bold; color: #ffd700; border: none;")
        g_lay.addWidget(self.gold_now_lbl)

        self.gold_mode = QComboBox()
        self.gold_mode.addItem("增加 / 扣除金币（填负数可扣除）", "add")
        self.gold_mode.addItem("直接设为指定金币数额", "set")
        g_lay.addWidget(QLabel("操作方式:"))
        g_lay.addWidget(self.gold_mode)

        self.gold_input = QLineEdit("10000000")
        self.gold_input.setPlaceholderText("输入整数，上限 4,294,967,295")
        g_lay.addWidget(QLabel("金币数额:"))
        g_lay.addWidget(self.gold_input)

        q2 = QHBoxLayout()
        q2.addWidget(_btn("+100万", lambda: self._set_currency_input(self.gold_mode, self.gold_input, 'add', 1000000)))
        q2.addWidget(_btn("+1000万", lambda: self._set_currency_input(self.gold_mode, self.gold_input, 'add', 10000000)))
        q2.addWidget(_btn("+1亿", lambda: self._set_currency_input(self.gold_mode, self.gold_input, 'add', 100000000)))
        q2.addWidget(_btn("设为1亿", lambda: self._set_currency_input(self.gold_mode, self.gold_input, 'set', 100000000)))
        g_lay.addLayout(q2)

        g_lay.addWidget(_btn("🪙 确认修改金币", self._apply_gold, gold=True))
        g_note = QLabel("说明：金币写入角色背包槽位 0，网关在线时通过实时背包同步包推送到客户端，无需退游戏。")
        g_note.setWordWrap(True)
        g_note.setStyleSheet("color: #a99f8d; font-size: 11px; border: none;")
        g_lay.addWidget(g_note)
        g_lay.addStretch()
        lay.addWidget(gold_card, 1)
        return page

    # ---------------- Tab 2: 背包管理 ----------------
    def _build_bag_tab(self):
        page = QWidget()
        lay = QVBoxLayout(page)
        lay.setContentsMargins(12, 12, 12, 12)
        lay.setSpacing(10)

        fbar = QHBoxLayout()
        self.bag_filter_combo = QComboBox()
        self.bag_filter_combo.addItems(['全部分区', '已穿戴', '装备栏', '消耗品', '材料', '任务道具', '副职业材料', '快捷栏'])
        self.bag_filter_combo.currentTextChanged.connect(self._render_bag_table)

        self.bag_search = QLineEdit()
        self.bag_search.setPlaceholderText("搜索背包内物品名称、模板 ID 或槽位…")
        self.bag_search.textChanged.connect(self._render_bag_table)

        self.bag_count_lbl = QLabel("共 0 项")
        self.bag_count_lbl.setObjectName("SubLabel")

        fbar.addWidget(QLabel("分区:"))
        fbar.addWidget(self.bag_filter_combo)
        fbar.addWidget(self.bag_search, 1)
        fbar.addWidget(self.bag_count_lbl)
        lay.addLayout(fbar)

        self.bag_table = QTableWidget(0, 6)
        self.bag_table.setHorizontalHeaderLabels(['分区', '槽位', '物品名称', '模板 ID', '数量 / 耐久', '类型'])
        self.bag_table.setStyleSheet(TABLE_STYLE)
        self.bag_table.setSelectionBehavior(QAbstractItemView.SelectRows)
        self.bag_table.setSelectionMode(QAbstractItemView.SingleSelection)
        self.bag_table.setEditTriggers(QAbstractItemView.NoEditTriggers)
        self.bag_table.verticalHeader().setVisible(False)
        hdr = self.bag_table.horizontalHeader()
        hdr.setSectionResizeMode(0, QHeaderView.ResizeToContents)
        hdr.setSectionResizeMode(1, QHeaderView.ResizeToContents)
        hdr.setSectionResizeMode(2, QHeaderView.Stretch)
        hdr.setSectionResizeMode(3, QHeaderView.ResizeToContents)
        hdr.setSectionResizeMode(4, QHeaderView.ResizeToContents)
        hdr.setSectionResizeMode(5, QHeaderView.ResizeToContents)
        self.bag_table.itemSelectionChanged.connect(self._on_bag_row_selected)
        lay.addWidget(self.bag_table, 1)

        row1 = QHBoxLayout()
        self.bag_qty_spin = QSpinBox()
        self.bag_qty_spin.setRange(1, 9999)
        self.bag_qty_spin.setPrefix("数量: ")
        row1.addWidget(self.bag_qty_spin)
        row1.addWidget(_btn("修改选中数量", self._bag_set_amount, gold=True))
        row1.addWidget(_btn("删除选中项", self._bag_delete_selected))
        row1.addWidget(_btn("卸下穿戴装备", self._bag_unequip_selected))
        row1.addStretch()
        row1.addWidget(_btn("整理合并背包", self._bag_sort))
        row1.addWidget(_btn("修复全部耐久", self._bag_repair_all))
        lay.addLayout(row1)

        row2 = QHBoxLayout()
        row2.addWidget(QLabel("批量清空分区:"))
        self.bag_clear_combo = QComboBox()
        for label, key in [
            ('装备栏 (不含已穿戴)', 'equipment'),
            ('消耗品栏', 'throw'),
            ('材料栏', 'material'),
            ('任务道具栏', 'quest'),
            ('副职业材料栏', 'expert'),
            ('全部背包 (保留金币与已穿戴)', 'all'),
        ]:
            self.bag_clear_combo.addItem(label, key)
        row2.addWidget(self.bag_clear_combo)
        row2.addWidget(_btn("清空所选分区", self._bag_clear_category))
        row2.addStretch()
        row2.addWidget(_btn("前往物品可视化添加 →", lambda: self.host.switch_page(1)))
        lay.addLayout(row2)
        return page

    # ---------------- Tab 3: 任务管理 ----------------
    def _build_quest_tab(self):
        page = QWidget()
        lay = QVBoxLayout(page)
        lay.setContentsMargins(12, 12, 12, 12)
        lay.setSpacing(10)

        # Character quests filter bar
        top_bar = QHBoxLayout()
        self.quest_status_filter = QComboBox()
        self.quest_status_filter.addItems(['进行中任务', '已完成任务', '全部角色任务'])
        self.quest_status_filter.currentTextChanged.connect(self._render_quest_table)

        self.quest_owned_search = QLineEdit()
        self.quest_owned_search.setPlaceholderText("筛选当前角色任务 ID 或中文名称…")
        self.quest_owned_search.textChanged.connect(self._render_quest_table)

        self.quest_count_lbl = QLabel("进行中 0 · 已完成 0")
        self.quest_count_lbl.setObjectName("SubLabel")

        top_bar.addWidget(QLabel("视图:"))
        top_bar.addWidget(self.quest_status_filter)
        top_bar.addWidget(self.quest_owned_search, 1)
        top_bar.addWidget(self.quest_count_lbl)
        lay.addLayout(top_bar)

        self.quest_table = QTableWidget(0, 6)
        self.quest_table.setHorizontalHeaderLabels(['任务 ID', '任务名称', '类型', '目标种类', '状态', '进度 / 要求等级'])
        self.quest_table.setStyleSheet(TABLE_STYLE)
        self.quest_table.setSelectionBehavior(QAbstractItemView.SelectRows)
        self.quest_table.setSelectionMode(QAbstractItemView.SingleSelection)
        self.quest_table.setEditTriggers(QAbstractItemView.NoEditTriggers)
        self.quest_table.verticalHeader().setVisible(False)
        qhdr = self.quest_table.horizontalHeader()
        qhdr.setSectionResizeMode(0, QHeaderView.ResizeToContents)
        qhdr.setSectionResizeMode(1, QHeaderView.Stretch)
        qhdr.setSectionResizeMode(2, QHeaderView.ResizeToContents)
        qhdr.setSectionResizeMode(3, QHeaderView.ResizeToContents)
        qhdr.setSectionResizeMode(4, QHeaderView.ResizeToContents)
        qhdr.setSectionResizeMode(5, QHeaderView.ResizeToContents)
        lay.addWidget(self.quest_table, 1)

        # Row actions for owned quests
        act1 = QHBoxLayout()
        act1.addWidget(_btn("✔ 选中置为可交付", lambda: self._quest_complete_obj(False), gold=True))
        act1.addWidget(_btn("✔ 全部进行中可交付", lambda: self._quest_complete_obj(True)))
        self.quest_reward_chk = QCheckBox("完成时尝试发奖")
        act1.addWidget(self.quest_reward_chk)
        act1.addWidget(_btn("★ 直接完成选中", self._quest_finish_selected))
        act1.addWidget(_btn("★ 全部进行中直接完成", self._quest_finish_all_active))
        act1.addWidget(_btn("✖ 放弃/重置选中", self._quest_abandon_selected))
        lay.addLayout(act1)

        # Skip epic quests & catalog search card
        cat_card = QFrame()
        cat_card.setObjectName("PanelCard")
        c_lay = QVBoxLayout(cat_card)
        c_lay.setContentsMargins(12, 10, 12, 10)
        c_lay.setSpacing(8)

        epic_row = QHBoxLayout()
        epic_row.addWidget(QLabel("主线快捷跳过:"))
        self.epic_level_spin = QSpinBox()
        self.epic_level_spin.setRange(1, 100)
        self.epic_level_spin.setPrefix("截至 Lv.")
        epic_row.addWidget(self.epic_level_spin)
        epic_row.addWidget(_btn("⏩ 一键完成该等级及以下主线任务", self._quest_skip_epic, gold=True))
        epic_row.addStretch()
        c_lay.addLayout(epic_row)

        search_row = QHBoxLayout()
        search_row.addWidget(QLabel("目录任务搜索:"))
        self.quest_cat_grade = QComboBox()
        self.quest_cat_grade.addItems(['全部类型', '主线', '普通', '每日', '成就', '修炼', '称号'])
        self.quest_cat_search = QLineEdit()
        self.quest_cat_search.setPlaceholderText("输入任务 ID 或中文名称关键词，点击搜索…")
        self.quest_cat_search.returnPressed.connect(self._search_quest_catalog)
        search_row.addWidget(self.quest_cat_grade)
        search_row.addWidget(self.quest_cat_search, 1)
        search_row.addWidget(_btn("搜索目录", self._search_quest_catalog))
        c_lay.addLayout(search_row)

        pick_row = QHBoxLayout()
        self.quest_cat_combo = QComboBox()
        self.quest_cat_combo.setMinimumWidth(360)
        self.quest_force_chk = QCheckBox("强制接取(忽略等级)")
        self.quest_force_chk.setChecked(True)
        pick_row.addWidget(self.quest_cat_combo, 1)
        pick_row.addWidget(self.quest_force_chk)
        pick_row.addWidget(_btn("+ 接取选中目录任务", self._quest_accept_catalog))
        pick_row.addWidget(_btn("直接标记完成", self._quest_finish_catalog))
        c_lay.addLayout(pick_row)

        lay.addWidget(cat_card)
        return page

    # ---------------- Lifecycle & Refresh ----------------
    def activate(self):
        old_id = self.role_combo.currentData()
        if old_id is None and hasattr(self.host, 'recipient'):
            old_id = self.host.recipient.currentData()
        self.role_combo.blockSignals(True)
        self.role_combo.clear()
        for ident, name in core.roles():
            self.role_combo.addItem(f'{name}  /  ID:{ident}', ident)
        idx = self.role_combo.findData(old_id)
        if idx >= 0:
            self.role_combo.setCurrentIndex(idx)
        elif self.role_combo.count() > 0:
            self.role_combo.setCurrentIndex(0)
        self.role_combo.blockSignals(False)
        self.refresh_snapshot()
        if self.tabs.currentIndex() == 3 and self.quest_cat_combo.count() == 0:
            self._search_quest_catalog()

    def _on_tab_changed(self, idx):
        if idx == 3:
            self._render_quest_table()
            if self.quest_cat_combo.count() == 0:
                self._search_quest_catalog()

    def refresh_snapshot(self):
        role_id = self.role_combo.currentData()
        if role_id is None:
            self.snapshot = None
            self.badge_job.setText("未找到角色")
            self.badge_level.setText("-")
            self.badge_cera.setText(f"{core.account_cera():,}")
            self.badge_gold.setText("-")
            self.badge_bag.setText("-")
            return
        snap = core.character_snapshot(role_id)
        self.snapshot = snap
        if snap is None:
            self.badge_job.setText("角色数据不存在")
            return

        pname = profession_name(snap['profession'])
        aname = advancement_name(snap['profession'], snap['advancement'])
        sp0 = snap['skill_points'][0] if snap['skill_points'] else 0
        tp0 = snap['technique_points'][0] if snap['technique_points'] else 0
        active_q = sum(1 for q in snap['quests'] if q['status'] == 'accepted')
        done_q = sum(1 for q in snap['quests'] if q['status'] == 'completed')
        bag_total = len(snap['items']) + len(snap['equipment']) + len(snap['worn'])

        self.badge_job.setText(f"{pname} · {aname}")
        self.badge_level.setText(f"Lv.{snap['level']}  ·  SP {sp0}  ·  TP {tp0}")
        self.badge_cera.setText(f"{snap['cera']:,}")
        self.badge_gold.setText(f"{snap['gold']:,}")
        self.badge_bag.setText(f"物品 {bag_total}  ·  任务 {active_q}/{done_q}")

        # Update Level/Job controls
        self.level_spin.setValue(max(1, min(100, snap['level'])))
        self.epic_level_spin.setValue(max(1, min(100, snap['level'])))
        self.sp_spin.setValue(max(0, min(65535, sp0)))
        self.tp_spin.setValue(max(0, min(65535, tp0)))
        self.adv_current_lbl.setText(f"当前基础职业：{pname} (ID {snap['profession']})  ·  当前转职：{aname} (分支 {snap['advancement']})")
        self.adv_combo.clear()
        for gid, label in advancement_options(snap['profession']):
            self.adv_combo.addItem(f"{gid} - {label}", gid)
        aidx = self.adv_combo.findData(snap['advancement'])
        if aidx >= 0:
            self.adv_combo.setCurrentIndex(aidx)

        attrs = snap.get('attributes') or {}
        for key, lbl in self.attr_labels.items():
            val = attrs.get(key)
            lbl.setText(f"{val:g}" if isinstance(val, (int, float)) else "-")

        # Update currency labels
        self.cera_now_lbl.setText(f"当前余额：{snap['cera']:,} 点券")
        self.gold_now_lbl.setText(f"当前金币：{snap['gold']:,}")

        # Update Bag & Quest tables
        self._render_bag_table()
        if self.tabs.currentIndex() == 3:
            self._render_quest_table()
        else:
            self.quest_count_lbl.setText(f"进行中 {active_q} · 已完成 {done_q}")

    def _require_role(self):
        role_id = self.role_combo.currentData()
        if role_id is None:
            raise ValueError("请先在上方选择目标角色。")
        return int(role_id)

    def _submit(self, action, payload, reason):
        job_id = core.submit_op(action, payload, reason=reason)
        self.pending_job = job_id
        self.host.last_grant = job_id
        state = core.gateway()
        if state and state.get('character_gm'):
            self.op_status.setText(f"已提交 [{action}]（单号 {job_id[:8]}），等待网关实时入账…")
            self._poll_timer.start()
        elif state:
            self.op_status.setText(
                f"已排队（单号 {job_id[:8]}），但当前运行的网关为旧版（不支持角色属性 GM）。请停止并重启网关后自动处理。"
            )
            self._poll_timer.start()
        else:
            self.op_status.setText(
                f"已排队（单号 {job_id[:8]}），网关当前离线。点击右上角「仅启动网关」或启动游戏后将自动处理。"
            )
        self.host.statusBar().showMessage(f"已提交角色属性指令 {job_id[:8]}")

    def _poll_receipt(self):
        if not self.pending_job:
            self._poll_timer.stop()
            return
        folder, data = core.read_job(self.pending_job)
        if folder == 'receipts' and data is not None:
            self._poll_timer.stop()
            if 'error' in data:
                msg = f"操作失败：{data['error']}"
                self.op_status.setText(msg)
                self.host.statusBar().showMessage(msg)
                QMessageBox.warning(self, '角色属性操作未完成', str(data['error']))
            else:
                rec = data.get('op_receipt') or {}
                summary = rec.get('summary') or '已完成'
                hint = '（部分外观/点券显示需返回选角重进刷新）' if rec.get('relog_hint') else '（已同步至数据库与在线会话）'
                msg = f"✔ 已生效：{summary} {hint}"
                self.op_status.setText(msg)
                self.host.statusBar().showMessage(msg)
            self.refresh_snapshot()
            self.host.refresh_delivery(core.gateway())

    # ---------------- Level & Advancement Actions ----------------
    def _preset_sp(self, val):
        self.override_sp_chk.setChecked(True)
        self.sp_spin.setValue(val)

    def _preset_tp(self, val):
        self.override_tp_chk.setChecked(True)
        self.tp_spin.setValue(val)

    def _apply_level(self):
        role_id = self._require_role()
        lv = self.level_spin.value()
        payload = {
            'character_id': role_id,
            'level': lv,
            'sync_sp': self.sync_sp_chk.isChecked(),
        }
        if self.override_sp_chk.isChecked():
            payload['sp'] = self.sp_spin.value()
        if self.override_tp_chk.isChecked():
            payload['tp'] = self.tp_spin.value()
        if QMessageBox.question(self, '确认修改等级', f'将当前角色等级调整为 Lv.{lv}？') != QMessageBox.Yes:
            return
        self._submit('level', payload, f'设置等级 Lv.{lv}')

    def _apply_advancement(self):
        role_id = self._require_role()
        adv = self.adv_combo.currentData()
        if adv is None:
            raise ValueError('请选择有效的转职分支。')
        label = self.adv_combo.currentText()
        reset = self.reset_skills_chk.isChecked()
        note = '并重置已学技能返还 SP' if reset else '保留已学技能'
        if QMessageBox.question(self, '确认执行转职', f'将当前角色转职为【{label}】（{note}）？') != QMessageBox.Yes:
            return
        self._submit('advancement', {
            'character_id': role_id,
            'advancement': int(adv),
            'reset_skills': reset,
        }, f'转职 {label}')

    # ---------------- Currency Actions ----------------
    def _set_currency_input(self, combo, line_edit, mode_key, value):
        idx = combo.findData(mode_key)
        if idx >= 0:
            combo.setCurrentIndex(idx)
        line_edit.setText(str(value))

    def _parse_int(self, text, label):
        raw = text.strip().replace(',', '')
        if not raw or not (raw.isdigit() or (raw.startswith('-') and raw[1:].isdigit())):
            raise ValueError(f'{label}必须输入整数。')
        return int(raw)

    def _apply_cera(self):
        role_id = self.role_combo.currentData() or 0
        mode = self.cera_mode.currentData()
        val = self._parse_int(self.cera_input.text(), '点券数额')
        if mode == 'set' and val < 0:
            raise ValueError('点券余额不能为负数。')
        desc = f'设为 {val:,}' if mode == 'set' else f'变动 {val:+,}'
        if QMessageBox.question(self, '确认修改点券', f'确认将账号点券 {desc}？') != QMessageBox.Yes:
            return
        self._submit('currency', {
            'character_id': int(role_id),
            'cera_mode': mode,
            'cera': val,
        }, f'点券 {desc}')

    def _apply_gold(self):
        role_id = self._require_role()
        mode = self.gold_mode.currentData()
        val = self._parse_int(self.gold_input.text(), '金币数额')
        if mode == 'set' and val < 0:
            raise ValueError('金币数额不能为负数。')
        desc = f'设为 {val:,}' if mode == 'set' else f'变动 {val:+,}'
        if QMessageBox.question(self, '确认修改金币', f'确认将当前角色金币 {desc}？') != QMessageBox.Yes:
            return
        self._submit('currency', {
            'character_id': role_id,
            'gold_mode': mode,
            'gold': val,
        }, f'金币 {desc}')

    # ---------------- Bag Management ----------------
    def _lookup_item_meta(self, template_id):
        card = self.host.by_id.get(template_id)
        if card is not None:
            return card.name, card.rarity, card.category, card.icon
        row = self.host.source.db.execute(
            'select name, rar, cat, img, frame from item where id=? order by grantmode desc limit 1',
            (template_id,),
        ).fetchone()
        if row:
            from item_icons import reference
            return row['name'], row['rar'], row['cat'], reference(row['img'], row['frame'])
        return f'模板 #{template_id}', '普通', '未知', None

    def _item_icon(self, icon_ref):
        if icon_ref is None:
            return None
        try:
            pm = pix(self.host.item_icons.read(icon_ref)).scaled(22, 22, Qt.KeepAspectRatio, Qt.FastTransformation)
            return QIcon(pm)
        except (OSError, ValueError, KeyError, struct.error, zlib.error):
            return None

    def _render_bag_table(self):
        self._bag_rows = []
        self.bag_table.setRowCount(0)
        if not self.snapshot:
            self.bag_count_lbl.setText("共 0 项")
            return
        section_filter = self.bag_filter_combo.currentText()
        kw = self.bag_search.text().strip().casefold()

        raw_entries = []
        for eq in sorted(self.snapshot['worn'], key=lambda x: x.get('slot', 0)):
            raw_entries.append(('worn', int(eq['slot']), int(eq['template']), int(eq.get('durability', 0))))
        for eq in sorted(self.snapshot['equipment'], key=lambda x: x.get('slot', 0)):
            raw_entries.append(('equipment', int(eq['slot']), int(eq['template']), int(eq.get('durability', 0))))
        for it in sorted(self.snapshot['items'], key=lambda x: x.get('slot', 0)):
            raw_entries.append(('items', int(it['slot']), int(it['Template']), int(it['Amount'])))

        for list_kind, slot, tpl, val in raw_entries:
            sec = bag_section_label(slot, list_kind)
            if section_filter != '全部分区' and sec != section_filter:
                continue
            name, rar, cat, icon_ref = self._lookup_item_meta(tpl)
            slot_text = f"{slot} ({worn_slot_label(slot)})" if list_kind == 'worn' else str(slot)
            if kw and kw not in f"{name} {tpl} {slot_text} {sec} {cat}".casefold():
                continue
            self._bag_rows.append(dict(list_kind=list_kind, slot=slot, template=tpl, value=val, name=name, section=sec))
            r = self.bag_table.rowCount()
            self.bag_table.insertRow(r)
            self.bag_table.setItem(r, 0, QTableWidgetItem(sec))
            self.bag_table.setItem(r, 1, QTableWidgetItem(slot_text))
            name_item = QTableWidgetItem(name)
            color = RARITY_COLORS.get(rar)
            if color:
                name_item.setForeground(QColor(color))
            ic = self._item_icon(icon_ref)
            if ic:
                name_item.setIcon(ic)
            self.bag_table.setItem(r, 2, name_item)
            self.bag_table.setItem(r, 3, QTableWidgetItem(str(tpl)))
            val_text = f"耐久 {val}" if list_kind in ('worn', 'equipment') else f"× {val}"
            self.bag_table.setItem(r, 4, QTableWidgetItem(val_text))
            self.bag_table.setItem(r, 5, QTableWidgetItem(cat))

        self.bag_count_lbl.setText(f"显示 {len(self._bag_rows)} / 共 {len(raw_entries)} 项")

    def _selected_bag_row(self):
        row = self.bag_table.currentRow()
        if row < 0 or row >= len(self._bag_rows):
            raise ValueError("请先在背包列表中选中一行物品。")
        return self._bag_rows[row]

    def _on_bag_row_selected(self):
        row = self.bag_table.currentRow()
        if 0 <= row < len(self._bag_rows) and self._bag_rows[row]['list_kind'] == 'items':
            self.bag_qty_spin.setValue(max(1, min(9999, self._bag_rows[row]['value'])))

    def _bag_set_amount(self):
        role_id = self._require_role()
        row = self._selected_bag_row()
        if row['list_kind'] != 'items':
            raise ValueError("只有可堆叠道具/材料支持修改数量。")
        qty = self.bag_qty_spin.value()
        self._submit('bag', {
            'character_id': role_id,
            'op': 'set_amount',
            'slot': row['slot'],
            'amount': qty,
        }, f"修改槽位 {row['slot']} 数量 → {qty}")

    def _bag_delete_selected(self):
        role_id = self._require_role()
        row = self._selected_bag_row()
        if row['list_kind'] == 'worn':
            raise ValueError("已穿戴装备不能直接删除，请先点击「卸下穿戴装备」再删除。")
        if QMessageBox.question(self, '确认删除物品', f"永久删除【{row['name']}】（槽位 {row['slot']}）？") != QMessageBox.Yes:
            return
        self._submit('bag', {
            'character_id': role_id,
            'op': 'delete_slot',
            'slot': row['slot'],
            'list': row['list_kind'],
        }, f"删除背包槽位 {row['slot']}")

    def _bag_unequip_selected(self):
        role_id = self._require_role()
        row = self._selected_bag_row()
        if row['list_kind'] != 'worn':
            raise ValueError("请选中「已穿戴」区域的装备后再卸下。")
        self._submit('bag', {
            'character_id': role_id,
            'op': 'unequip_slot',
            'slot': row['slot'],
        }, f"卸下穿戴槽位 {row['slot']}")

    def _bag_sort(self):
        role_id = self._require_role()
        self._submit('bag', {'character_id': role_id, 'op': 'sort'}, '整理合并背包')

    def _bag_repair_all(self):
        role_id = self._require_role()
        self._submit('bag', {'character_id': role_id, 'op': 'repair_all'}, '修复全部装备耐久')

    def _bag_clear_category(self):
        role_id = self._require_role()
        cat = self.bag_clear_combo.currentData()
        label = self.bag_clear_combo.currentText()
        if QMessageBox.question(self, '确认清空背包', f'确定清空【{label}】？此操作不可撤销。') != QMessageBox.Yes:
            return
        self._submit('bag', {'character_id': role_id, 'op': 'clear_category', 'category': cat}, f'清空 {label}')

    # ---------------- Quest Management ----------------
    def _render_quest_table(self):
        self._quest_rows = []
        self.quest_table.setRowCount(0)
        if not self.snapshot:
            self.quest_count_lbl.setText("进行中 0 · 已完成 0")
            return
        all_q = self.snapshot['quests']
        active_cnt = sum(1 for q in all_q if q['status'] == 'accepted')
        done_cnt = sum(1 for q in all_q if q['status'] == 'completed')
        self.quest_count_lbl.setText(f"进行中 {active_cnt} · 已完成 {done_cnt}")

        mode = self.quest_status_filter.currentText()
        kw = self.quest_owned_search.text().strip().casefold()
        for q in all_q:
            if mode == '进行中任务' and q['status'] != 'accepted':
                continue
            if mode == '已完成任务' and q['status'] != 'completed':
                continue
            info = self.quests_catalog.info(q['id'])
            title = info['title'] if info else f"任务 #{q['id']}"
            grade = info['grade'] if info else '-'
            kind = info['kind'] if info else '-'
            min_lv = info['min_level'] if info else 1
            if kw and kw not in f"{q['id']} {title} {grade} {kind}".casefold():
                continue
            self._quest_rows.append(dict(id=q['id'], title=title, status=q['status'], progress=q['progress']))
            r = self.quest_table.rowCount()
            self.quest_table.insertRow(r)
            self.quest_table.setItem(r, 0, QTableWidgetItem(str(q['id'])))
            t_item = QTableWidgetItem(title)
            if grade == '主线':
                t_item.setForeground(QColor('#ffd700'))
            self.quest_table.setItem(r, 1, t_item)
            self.quest_table.setItem(r, 2, QTableWidgetItem(grade))
            self.quest_table.setItem(r, 3, QTableWidgetItem(kind))
            st_text = '进行中' if q['status'] == 'accepted' else '已完成' if q['status'] == 'completed' else q['status']
            st_item = QTableWidgetItem(st_text)
            st_item.setForeground(QColor('#4ade80' if q['status'] == 'completed' else '#facc15'))
            self.quest_table.setItem(r, 4, st_item)
            prog_text = (f"可交付 (0) · Lv.{min_lv}" if q['progress'] == 0 else f"剩余 {q['progress']} · Lv.{min_lv}") if q['status'] == 'accepted' else f"已结案 · Lv.{min_lv}"
            self.quest_table.setItem(r, 5, QTableWidgetItem(prog_text))

    def _selected_quest_row(self):
        row = self.quest_table.currentRow()
        if row < 0 or row >= len(self._quest_rows):
            raise ValueError("请先在上方任务列表中选中一条任务。")
        return self._quest_rows[row]

    def _quest_complete_obj(self, all_active=False):
        role_id = self._require_role()
        if all_active:
            self._submit('quest', {'character_id': role_id, 'op': 'complete_objective', 'quest_id': 0}, '全部进行中任务目标置为可交付')
        else:
            q = self._selected_quest_row()
            self._submit('quest', {'character_id': role_id, 'op': 'complete_objective', 'quest_id': q['id']}, f"完成任务 {q['id']} 目标")

    def _quest_finish_selected(self):
        role_id = self._require_role()
        q = self._selected_quest_row()
        grant = self.quest_reward_chk.isChecked()
        self._submit('quest', {
            'character_id': role_id,
            'op': 'finish',
            'quest_id': q['id'],
            'grant_reward': grant,
        }, f"直接完成任务 {q['id']}")

    def _quest_finish_all_active(self):
        role_id = self._require_role()
        if QMessageBox.question(self, '确认批量完成', '将当前角色所有进行中任务直接标记为已完成？') != QMessageBox.Yes:
            return
        self._submit('quest', {'character_id': role_id, 'op': 'finish_all_active'}, '完成全部进行中任务')

    def _quest_abandon_selected(self):
        role_id = self._require_role()
        q = self._selected_quest_row()
        if QMessageBox.question(self, '确认放弃/重置', f"删除任务【{q['id']} - {q['title']}】的记录？") != QMessageBox.Yes:
            return
        self._submit('quest', {'character_id': role_id, 'op': 'abandon', 'quest_id': q['id']}, f"放弃/重置任务 {q['id']}")

    def _quest_skip_epic(self):
        role_id = self._require_role()
        lv = self.epic_level_spin.value()
        if QMessageBox.question(self, '确认跳过主线', f'将当前职业 Lv.{lv} 及以下的所有主线任务标记为已完成？') != QMessageBox.Yes:
            return
        self._submit('quest', {'character_id': role_id, 'op': 'complete_epic_to_level', 'max_level': lv}, f'完成 Lv.{lv} 及以下主线')

    def _search_quest_catalog(self):
        job_tok = profession_token(self.snapshot['profession']) if self.snapshot else ''
        hits = self.quests_catalog.search(
            keyword=self.quest_cat_search.text(),
            grade=self.quest_cat_grade.currentText(),
            job_token=job_tok,
            limit=120,
        )
        self._catalog_hits = hits
        self.quest_cat_combo.clear()
        for e in hits:
            self.quest_cat_combo.addItem(f"[{e['id']}] ({e['grade']} · Lv.{e['min_level']}) {e['title']} [{e['kind']}]", e['id'])
        if not hits:
            self.quest_cat_combo.addItem('无匹配任务', None)

    def _quest_accept_catalog(self):
        role_id = self._require_role()
        qid = self.quest_cat_combo.currentData()
        if not qid:
            raise ValueError('请先搜索并选择一条目录任务。')
        self._submit('quest', {
            'character_id': role_id,
            'op': 'accept',
            'quest_id': int(qid),
            'force': self.quest_force_chk.isChecked(),
        }, f'接取任务 {qid}')

    def _quest_finish_catalog(self):
        role_id = self._require_role()
        qid = self.quest_cat_combo.currentData()
        if not qid:
            raise ValueError('请先搜索并选择一条目录任务。')
        self._submit('quest', {
            'character_id': role_id,
            'op': 'finish',
            'quest_id': int(qid),
            'grant_reward': self.quest_reward_chk.isChecked(),
        }, f'直接完成目录任务 {qid}')
