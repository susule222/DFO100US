"""Visual Avatar Fitting Room with 11-slot interactive paper-doll, weapon/aura previews and icon card grid."""
import json
import random
import struct
import zlib

from PIL import Image, ImageDraw
from PySide6.QtCore import QEvent, QSize, Qt, QSignalBlocker, QTimer
from PySide6.QtWidgets import (
    QCheckBox,
    QComboBox,
    QFrame,
    QGridLayout,
    QHBoxLayout,
    QLabel,
    QLineEdit,
    QListView,
    QListWidget,
    QListWidgetItem,
    QMessageBox,
    QPushButton,
    QVBoxLayout,
    QWidget,
)

from appearance import Stage
from build_library import SLOT_ZH
import core
from epic_data import JOBS_BY_TOKEN
from epic_ui import RARITY_COLORS, pix
from item_icons import reference
import ui_icons

PAPERDOLL_SPECIAL = [
    ('weapon', '武器装扮'),
    ('aura', '光环'),
]
PAPERDOLL_BODY = [
    ('cap', '帽子'),
    ('hair', '头发'),
    ('face', '脸部'),
    ('neck', '胸部'),
    ('coat', '上衣'),
    ('belt', '腰部'),
    ('pants', '下装'),
    ('shoes', '鞋子'),
    ('skin', '肤色'),
]
ALL_SLOTS_ORDER = ['weapon', 'aura', 'cap', 'hair', 'face', 'neck', 'coat', 'belt', 'pants', 'shoes', 'skin']

STAGE_THEMES = [
    ('暗夜舞台', 'dark'),
    ('旅馆暖光', 'warm'),
    ('透明棋盘', 'checker'),
    ('浅灰对照', 'light'),
]


def _make_backdrop(w, h, theme):
    if theme == 'light':
        return Image.new('RGBA', (w, h), (218, 222, 230, 255))
    if theme == 'checker':
        bg = Image.new('RGBA', (w, h), (34, 34, 44, 255))
        draw = ImageDraw.Draw(bg)
        step = 14
        for y in range(0, h, step):
            for x in range(0, w, step):
                if ((x // step) + (y // step)) % 2 == 0:
                    draw.rectangle((x, y, x + step - 1, y + step - 1), fill=(48, 48, 60, 255))
        return bg
    if theme == 'warm':
        bg = Image.new('RGBA', (w, h), (28, 22, 20, 255))
        draw = ImageDraw.Draw(bg)
        cx, cy = w // 2, int(h * 0.62)
        for r, alpha in [(140, 22), (105, 34), (72, 48)]:
            draw.ellipse((cx - r, cy - r // 2, cx + r, cy + r // 2), fill=(195, 145, 70, alpha))
        draw.ellipse((cx - 88, cy + 48, cx + 88, cy + 78), outline=(212, 175, 55, 130), width=1)
        return bg
    # Default dark stage with subtle golden pedestal ring
    bg = Image.new('RGBA', (w, h), (21, 19, 30, 255))
    draw = ImageDraw.Draw(bg)
    cx, cy = w // 2, int(h * 0.74)
    draw.ellipse((cx - 92, cy - 18, cx + 92, cy + 20), fill=(42, 34, 54, 150), outline=(145, 118, 68, 120), width=1)
    draw.ellipse((cx - 64, cy - 11, cx + 64, cy + 12), outline=(212, 175, 55, 80), width=1)
    return bg


def _load_icon_pixmap(icons, img_path, frame_idx, edge=40):
    ref = reference(img_path, frame_idx)
    if not ref:
        return None, '无图标引用'
    src = f'{ref.img} · 第 {ref.frame} 帧'
    try:
        im = icons.read(ref)
        pm = pix(im).scaled(edge, edge, Qt.KeepAspectRatio, Qt.FastTransformation)
        return pm, src
    except (OSError, ValueError, KeyError, struct.error, zlib.error) as exc:
        return None, f'{src}\n图标不可用：{exc}'


class SlotTile(QFrame):
    """Interactive visual paper-doll slot tile showing the currently worn item's NPK icon."""
    def __init__(self, slot_key, label_text, on_select, on_clear, special=False):
        super().__init__()
        self.slot_key = slot_key
        self.label_text = label_text
        self.on_select = on_select
        self.on_clear = on_clear
        self.special = special
        self.active = False
        self.equipped = False
        self.setCursor(Qt.PointingHandCursor)
        self.setFixedHeight(48 if special else 44)

        lay = QHBoxLayout(self)
        lay.setContentsMargins(5, 3, 5, 3)
        lay.setSpacing(5)

        self.icon_lbl = QLabel()
        self.icon_lbl.setFixedSize(32, 32)
        self.icon_lbl.setAlignment(Qt.AlignCenter)
        lay.addWidget(self.icon_lbl)

        col = QVBoxLayout()
        col.setSpacing(1)
        self.title_lbl = QLabel(label_text)
        self.title_lbl.setStyleSheet('font-size:11px;font-weight:bold;color:#ffd700;border:none;background:transparent;')
        self.item_lbl = QLabel('未穿戴')
        self.item_lbl.setStyleSheet('font-size:10px;color:#8e8578;border:none;background:transparent;')
        col.addWidget(self.title_lbl)
        col.addWidget(self.item_lbl)
        lay.addLayout(col, 1)

        self.clear_btn = QPushButton('×')
        self.clear_btn.setFixedSize(18, 18)
        self.clear_btn.setToolTip(f'卸下 {label_text}')
        self.clear_btn.setStyleSheet(
            'QPushButton{background:rgba(180,55,55,0.45);color:#fff;border:none;border-radius:9px;font-size:11px;padding:0}'
            'QPushButton:hover{background:#d94848;}'
        )
        self.clear_btn.clicked.connect(lambda _: self.on_clear(self.slot_key))
        self.clear_btn.setVisible(False)
        lay.addWidget(self.clear_btn)
        self._apply_style()

    def _apply_style(self):
        if self.active:
            border = '#ffd700'
            bg = 'rgba(212,175,55,0.20)'
        elif self.equipped:
            border = '#d4af37' if self.special else '#a3843e'
            bg = 'rgba(46,38,56,0.92)'
        else:
            border = 'rgba(212,175,55,0.42)' if self.special else 'rgba(130,115,92,0.28)'
            bg = 'rgba(22,19,31,0.86)'
        self.setStyleSheet(f'SlotTile{{background:{bg};border:1px solid {border};border-radius:8px;}}')
        iborder = '#ffd700' if self.equipped else 'rgba(160,145,120,0.3)'
        self.icon_lbl.setStyleSheet(
            f'background:#12101a;color:#6f665a;border:1px solid {iborder};border-radius:6px;font-size:10px;'
        )

    def set_state(self, row, icons, active=False):
        self.active = active
        self.equipped = row is not None
        self.clear_btn.setVisible(self.equipped)
        if row is None:
            self.icon_lbl.clear()
            if self.slot_key == 'weapon':
                self.icon_lbl.setPixmap(ui_icons.render_pixmap('weapon', 22, active=self.active))
            elif self.slot_key == 'aura':
                self.icon_lbl.setPixmap(ui_icons.render_pixmap('aura', 22, active=self.active))
            else:
                self.icon_lbl.setText(SLOT_ZH[self.slot_key][:1])
            self.item_lbl.setText('点击浏览')
            self.item_lbl.setStyleSheet('font-size:10px;color:#8e8578;border:none;background:transparent;')
            self.setToolTip(f'{self.label_text}（未穿戴）\n左键点击快速筛选该部位')
        else:
            pm, tip = _load_icon_pixmap(icons, row['img'], row['frame'], 32)
            if pm:
                self.icon_lbl.setPixmap(pm)
            else:
                self.icon_lbl.clear()
                self.icon_lbl.setText('?')
            name = row['name'] or str(row['id'])
            short = name if len(name) <= 8 else name[:7] + '…'
            self.item_lbl.setText(short)
            self.item_lbl.setStyleSheet('font-size:10px;color:#f4efe6;border:none;background:transparent;')
            self.setToolTip(f"{self.label_text}：{name}\nID: {row['id']}\n{tip}\n左键筛选 · 右键或点 × 卸下")
        self._apply_style()

    def mousePressEvent(self, event):
        if event.button() == Qt.RightButton and self.equipped:
            self.on_clear(self.slot_key)
        elif event.button() == Qt.LeftButton:
            self.on_select(self.slot_key)
        super().mousePressEvent(event)


class CostumeCardWidget(QFrame):
    """Visual card for a single avatar, weapon avatar, aura, or costume set in the Fitting Room grid."""
    def __init__(self, row, icons, worn, on_wear, on_select, on_grant=None):
        super().__init__()
        self.row = row
        rar = row['rar'] or ''
        color = RARITY_COLORS.get(rar, '#d4af37')
        slot_key = row['slot'] or ''
        grp = row['g'] or ''
        if slot_key:
            slot_zh = SLOT_ZH.get(slot_key, slot_key)
        elif grp == 'composed_set':
            slot_zh = '组合套装'
        else:
            slot_zh = '成套礼包'
        has_vis = bool(row['visual'] and row['visual'] not in ('', '[[],[]]', '[[],[],[]]'))

        border = '#ffd700' if worn else color
        bg = '#272033' if worn else '#181522'
        self.setStyleSheet(
            f'CostumeCardWidget{{background:{bg};border:{"2px" if worn else "1px"} solid {border};border-radius:9px;}}'
            f'CostumeCardWidget:hover{{background:#241e33;border-color:#ffd700;}}'
        )
        self.setToolTip(
            f"名称: {row['name']}\n别名: {row['alias'] or '-'}\nID: {row['id']}\n"
            f"部位: {slot_zh} | 稀有度: {rar or '未标注'}\n"
            f"外观图层: {'可视化支持' if has_vis else '透明/克隆无独立贴图'}\n路径: {row['path']}"
        )

        lay = QVBoxLayout(self)
        lay.setContentsMargins(8, 7, 8, 7)
        lay.setSpacing(5)

        top = QHBoxLayout()
        top.setSpacing(7)
        icon_lbl = QLabel('?')
        icon_lbl.setFixedSize(42, 42)
        icon_lbl.setAlignment(Qt.AlignCenter)
        icon_lbl.setStyleSheet(f'background:#12101a;color:#998e7e;border:1px solid {color};border-radius:6px;')
        pm, tip = _load_icon_pixmap(icons, row['img'], row['frame'], 38)
        if pm:
            icon_lbl.setPixmap(pm)
        icon_lbl.setToolTip(tip)
        top.addWidget(icon_lbl)

        info = QVBoxLayout()
        info.setSpacing(2)
        name_txt = row['name'] or str(row['id'])
        if len(name_txt) > 15:
            name_txt = name_txt[:14] + '…'
        name_lbl = QLabel(name_txt)
        name_lbl.setStyleSheet(f'color:{color};font-weight:bold;font-size:12px;border:none;background:transparent;')
        name_lbl.setWordWrap(True)

        rar_tag = f'{rar} · ' if (rar and rar != '未标注') else ''
        if grp == 'composed_set':
            try:
                cnt_parts = len(json.loads(row['visual'] or '[]'))
            except Exception:
                cnt_parts = 0
            badge_txt = f"{rar_tag}组合套装 · {cnt_parts}件"
        else:
            badge_txt = f"{rar_tag}{slot_zh} · {row['id']}"
        if worn:
            badge_txt = '✓ 已穿戴 · ' + slot_zh
        elif not has_vis:
            badge_txt += ' (透明)'
        meta_lbl = QLabel(badge_txt)
        meta_lbl.setStyleSheet(
            f'color:{"#4ade80" if worn else "#a99f8d"};font-size:10px;border:none;background:transparent;'
        )
        info.addWidget(name_lbl)
        info.addWidget(meta_lbl)
        top.addLayout(info, 1)
        lay.addLayout(top)

        btn_row = QHBoxLayout()
        btn_row.setSpacing(5)
        btn = QPushButton('✓ 已试穿' if worn else '✦ 试穿')
        btn.setCursor(Qt.PointingHandCursor)
        btn.setStyleSheet(
            'QPushButton{background:rgba(212,175,55,0.22);color:#ffd700;border:1px solid rgba(212,175,55,0.55);'
            'border-radius:5px;padding:3px 6px;font-size:11px;font-weight:bold;}'
            'QPushButton:hover{background:#ffd700;color:#141008;}'
        )
        btn.clicked.connect(lambda _: on_wear())
        btn_row.addWidget(btn, 3)

        if on_grant is not None:
            send_btn = QPushButton('发给角色')
            send_btn.setCursor(Qt.PointingHandCursor)
            send_btn.setToolTip('实时发送到当前所选角色的装扮栏')
            send_btn.setStyleSheet(
                'QPushButton{background:rgba(45,160,145,0.22);color:#7ef2de;border:1px solid rgba(94,234,212,0.50);'
                'border-radius:5px;padding:3px 6px;font-size:11px;font-weight:bold;}'
                'QPushButton:hover{background:#2dd4bf;color:#081c19;}'
            )
            send_btn.clicked.connect(lambda _: on_grant())
            btn_row.addWidget(send_btn, 2)

        lay.addLayout(btn_row)
        self._on_select = on_select

    def mousePressEvent(self, event):
        if event.button() == Qt.LeftButton and self._on_select:
            self._on_select()
        super().mousePressEvent(event)


class FittingRoom(QWidget):
    def __init__(self, owner):
        super().__init__()
        self.owner = owner
        self.source = owner.source
        self.stage = Stage(owner.ward, self.source)
        self.choice = {}
        self.page = 0
        self.page_size = 24
        self.started = False
        self.aura_ms = 0

        lay = QVBoxLayout(self)
        lay.setContentsMargins(16, 12, 16, 12)
        lay.setSpacing(8)

        # Header row with live PVF/NPK badge
        head = QHBoxLayout()
        title = QLabel('时装试衣间')
        title.setObjectName('TitleLabel')
        head.addWidget(title)
        self.caption = QLabel()
        self.caption.setObjectName('SubLabel')
        head.addWidget(self.caption, 1)
        self.res_badge = QLabel()
        self.res_badge.setStyleSheet('color:#82cabc;font-size:11px;padding:3px 8px;background:#181522;border:1px solid rgba(130,202,188,0.35);border-radius:6px;')
        head.addWidget(self.res_badge)
        res_btn = QPushButton(' 切换 PVF / NPK')
        res_btn.setObjectName('DarkBtn')
        res_btn.setIcon(ui_icons.vector_icon('mods', 14, active=True))
        res_btn.setIconSize(QSize(14, 14))
        res_btn.setStyleSheet('padding:4px 10px;font-size:11px;')
        res_btn.clicked.connect(lambda _: self.owner.switch_page(0))
        head.addWidget(res_btn)
        lay.addLayout(head)

        # Top Filter Bar (keeps job/mode/slot/search attributes compatible with tests)
        bar = QHBoxLayout()
        bar.setSpacing(8)
        self.job = QComboBox()
        for token, zh in JOBS_BY_TOKEN.items():
            if token in self.source.poses:
                self.job.addItem(zh, token)
        self.mode = QComboBox()
        self.mode.addItem('单件装扮', 'single')
        self.mode.addItem('全部套装', 'set')
        self.slot = QComboBox()
        self.slot.addItem('全部部位', '')
        for s in ALL_SLOTS_ORDER:
            self.slot.addItem(SLOT_ZH[s], s)
        self.rarity = QComboBox()
        self.rarity.addItems(['全部稀有度'] + self.source.wardrobe_rarities())
        for w in [self.job, self.mode, self.slot, self.rarity]:
            bar.addWidget(w)
        self.search = QLineEdit()
        self.search.setClearButtonEnabled(True)
        self.search.setPlaceholderText('搜索中英文名称、物品 ID 或脚本路径…')
        bar.addWidget(self.search, 1)
        self.only_visual_chk = QCheckBox('仅显示有贴图')
        self.only_visual_chk.setChecked(False)
        bar.addWidget(self.only_visual_chk)
        lay.addLayout(bar)

        # Main split: Left Visual Stage + 11-Slot Paper-Doll | Right Visual Icon Card Browser
        body = QHBoxLayout()
        body.setSpacing(12)
        lay.addLayout(body, 1)

        # ---- LEFT COLUMN: Stage + Controls + 11 Visual Slots ----
        left_card = QFrame()
        left_card.setObjectName('PanelCard')
        left_card.setFixedWidth(372)
        l_lay = QVBoxLayout(left_card)
        l_lay.setContentsMargins(10, 10, 10, 10)
        l_lay.setSpacing(7)

        stage_bar = QHBoxLayout()
        stage_bar.setSpacing(6)
        self.zoom_combo = QComboBox()
        for lbl, val in [('1.5×', 1.5), ('2.0×', 2.0), ('2.5×', 2.5), ('3.0×', 3.0)]:
            self.zoom_combo.addItem(f'缩放 {lbl}', val)
        self.zoom_combo.setCurrentIndex(1)
        self.bg_combo = QComboBox()
        for lbl, val in STAGE_THEMES:
            self.bg_combo.addItem(lbl, val)
        stage_bar.addWidget(self.zoom_combo)
        stage_bar.addWidget(self.bg_combo)
        l_lay.addLayout(stage_bar)

        self.figure = QLabel()
        self.figure.setAlignment(Qt.AlignCenter)
        self.figure.setMinimumSize(280, 225)
        self.figure.setStyleSheet('background:#15131e;border:1px solid #67513a;border-radius:9px;')
        l_lay.addWidget(self.figure, 1)

        # Visibility toggles
        vis_row = QHBoxLayout()
        vis_row.setSpacing(8)
        self.chk_weapon = QCheckBox('武器装扮')
        self.chk_weapon.setChecked(True)
        self.chk_aura = QCheckBox('光环')
        self.chk_aura.setChecked(True)
        self.chk_aura_anim = QCheckBox('光环动画')
        self.chk_aura_anim.setChecked(True)
        self.chk_defaults = QCheckBox('底衣')
        self.chk_defaults.setChecked(True)
        for chk in (self.chk_weapon, self.chk_aura, self.chk_aura_anim, self.chk_defaults):
            chk.stateChanged.connect(self._on_visual_toggle)
            vis_row.addWidget(chk)
        vis_row.addStretch()
        l_lay.addLayout(vis_row)

        # 11-slot interactive paper-doll
        slot_hdr = QLabel('当前穿戴部位（左键筛选 · 右键或点 × 卸下）')
        slot_hdr.setStyleSheet('color:#d4af37;font-size:11px;font-weight:bold;border:none;')
        l_lay.addWidget(slot_hdr)

        self.slot_tiles = {}
        special_row = QHBoxLayout()
        special_row.setSpacing(6)
        for sk, zh in PAPERDOLL_SPECIAL:
            tile = SlotTile(sk, zh, self._select_slot_pill, self._unequip_slot, special=True)
            self.slot_tiles[sk] = tile
            special_row.addWidget(tile, 1)
        l_lay.addLayout(special_row)

        body_grid = QGridLayout()
        body_grid.setSpacing(5)
        for idx, (sk, zh) in enumerate(PAPERDOLL_BODY):
            tile = SlotTile(sk, zh, self._select_slot_pill, self._unequip_slot, special=False)
            self.slot_tiles[sk] = tile
            body_grid.addWidget(tile, idx // 3, idx % 3)
        l_lay.addLayout(body_grid)

        left_btns = QHBoxLayout()
        left_btns.setSpacing(6)
        rand_btn = QPushButton(' 随机搭配')
        rand_btn.setObjectName('DarkBtn')
        rand_btn.setIcon(ui_icons.vector_icon('shuffle', 15, active=True))
        rand_btn.setIconSize(QSize(15, 15))
        rand_btn.clicked.connect(self.random_outfit)
        reset_left_btn = QPushButton(' 重置基础')
        reset_left_btn.setObjectName('DarkBtn')
        reset_left_btn.setIcon(ui_icons.vector_icon('restore', 15, active=True))
        reset_left_btn.setIconSize(QSize(15, 15))
        reset_left_btn.clicked.connect(self.reset)
        left_btns.addWidget(rand_btn)
        left_btns.addWidget(reset_left_btn)
        l_lay.addLayout(left_btns)

        body.addWidget(left_card)

        # ---- RIGHT COLUMN: Slot Quick-Filter Pills + Visual Icon Card Grid ----
        right_col = QVBoxLayout()
        right_col.setSpacing(7)
        body.addLayout(right_col, 1)

        # Quick visual slot tabs (2 full-width balanced rows covering sets, weapon, aura & all 9 body parts)
        self.pills = {}
        pill_box = QVBoxLayout()
        pill_box.setSpacing(4)
        row1_specs = [
            ('single:', '全部'),
            ('set:', '套装'),
            ('single:weapon', '武器'),
            ('single:aura', '光环'),
            ('single:cap', '帽子'),
            ('single:hair', '头发'),
            ('single:face', '脸部'),
        ]
        row2_specs = [
            ('single:neck', '胸部'),
            ('single:coat', '上衣'),
            ('single:belt', '腰部'),
            ('single:pants', '下装'),
            ('single:shoes', '鞋子'),
            ('single:skin', '肤色'),
        ]
        for specs in (row1_specs, row2_specs):
            r_lay = QHBoxLayout()
            r_lay.setSpacing(4)
            for pkey, plabel in specs:
                pb = QPushButton(plabel)
                pb.setCursor(Qt.PointingHandCursor)
                pb.setProperty('pill_key', pkey)
                pb.clicked.connect(lambda checked=False, k=pkey: self._on_pill_clicked(k))
                self.pills[pkey] = (pb, plabel)
                r_lay.addWidget(pb, 1)
            pill_box.addLayout(r_lay)
        right_col.addLayout(pill_box)

        # Visual card grid (QListWidget in IconMode with CostumeCardWidget items -> 100% test compatible)
        self.rows = QListWidget()
        self.rows.setViewMode(QListView.IconMode)
        self.rows.setResizeMode(QListView.Adjust)
        self.rows.setMovement(QListView.Static)
        self.rows.setSpacing(8)
        self.rows.setWrapping(True)
        self.rows.setUniformItemSizes(True)
        self.rows.setStyleSheet(
            'QListWidget{background:#12101a;border:1px solid rgba(212,175,55,0.28);border-radius:9px;padding:6px;}'
            'QListWidget::item{background:transparent;border:none;}'
            'QListWidget::item:selected{background:transparent;}'
        )
        self.rows.viewport().installEventFilter(self)
        right_col.addWidget(self.rows, 1)

        def button(text, fn, gold=False):
            b = QPushButton(text)
            b.setObjectName('GoldBtn' if gold else 'DarkBtn')
            b.clicked.connect(lambda _: fn())
            return b

        pages = QHBoxLayout()
        right_col.addLayout(pages)
        self.prev = button('◀ 上一页', lambda: self.turn(-1))
        self.next = button('下一页 ▶', lambda: self.turn(1))
        self.count = QLabel()
        self.count.setAlignment(Qt.AlignCenter)
        self.count.setStyleSheet('color:#ffd700;font-weight:bold;')
        pages.addWidget(self.prev)
        pages.addWidget(self.count, 1)
        pages.addWidget(self.next)

        line = QHBoxLayout()
        right_col.addLayout(line)
        self.try_btn = button('✦ 试穿所选', self.try_on, True)
        self.remove_btn = button('卸下所选部位', self.remove)
        line.addWidget(self.try_btn)
        line.addWidget(self.remove_btn)
        line.addWidget(button('重置搭配', self.reset))

        # Live Avatar Grant Bar (real-time push to character's Avatar Inventory / 装扮栏)
        grant_card = QFrame()
        grant_card.setObjectName('PanelCard')
        gl = QHBoxLayout(grant_card)
        gl.setContentsMargins(10, 7, 10, 7)
        gl.setSpacing(7)
        g_lbl = QLabel('接收角色:')
        g_lbl.setStyleSheet('color:#e6cf98;font-size:12px;font-weight:bold;border:none;background:transparent;')
        self.avatar_recipient = QComboBox()
        self.avatar_recipient.setMinimumWidth(140)
        ref_role_btn = QPushButton()
        ref_role_btn.setObjectName('DarkBtn')
        ref_role_btn.setToolTip('刷新角色列表')
        ref_role_btn.setIcon(ui_icons.vector_icon('refresh', 14, active=True))
        ref_role_btn.setIconSize(QSize(14, 14))
        ref_role_btn.setFixedWidth(34)
        ref_role_btn.clicked.connect(self.refresh_roles)

        self.grant_outfit_btn = button('✦ 发送当前整套搭配', self.grant_current_outfit, True)
        self.grant_outfit_btn.setToolTip('将左侧当前穿戴的全部时装（含武器装扮与光环）一次性实时发至角色装扮栏')
        self.grant_selected_btn = button('发送所选时装', self.grant_selected_item)
        self.grant_selected_btn.setToolTip('将当前选中的单件装扮或成套礼包内全部时装实时发至角色装扮栏')

        self.grant_status_lbl = QLabel('实时直发装扮栏（在线无需下线）')
        self.grant_status_lbl.setStyleSheet('color:#82cabc;font-size:11px;border:none;background:transparent;')

        gl.addWidget(g_lbl)
        gl.addWidget(self.avatar_recipient)
        gl.addWidget(ref_role_btn)
        gl.addWidget(self.grant_outfit_btn)
        gl.addWidget(self.grant_selected_btn)
        gl.addWidget(self.grant_status_lbl, 1)
        right_col.addWidget(grant_card)

        self._last_avatar_job = ''
        self._grant_poll_timer = QTimer(self)
        self._grant_poll_timer.setInterval(450)
        self._grant_poll_timer.timeout.connect(self._poll_avatar_grant)
        self.refresh_roles()

        self.detail = QLabel()
        self.detail.setWordWrap(True)
        self.detail.setObjectName('SubLabel')
        lay.addWidget(self.detail)

        # Timers
        self.delay = QTimer(self)
        self.delay.setSingleShot(True)
        self.delay.setInterval(160)
        self.delay.timeout.connect(self.reload)

        self.anim_timer = QTimer(self)
        self.anim_timer.setInterval(120)
        self.anim_timer.timeout.connect(self._tick_aura)

        # Signals
        self.search.textChanged.connect(self.new_search)
        self.slot.currentIndexChanged.connect(self._slot_combo_changed)
        self.rarity.currentIndexChanged.connect(self._rarity_changed)
        self.mode.currentIndexChanged.connect(self.mode_change)
        self.job.currentIndexChanged.connect(self.job_change)
        self.only_visual_chk.stateChanged.connect(self.new_search)
        self.zoom_combo.currentIndexChanged.connect(lambda *_: self.render())
        self.bg_combo.currentIndexChanged.connect(lambda *_: self.render())
        self.rows.itemDoubleClicked.connect(lambda *_: self.try_on())

        self._refresh_resource_badge()

    def sync_sources(self):
        """Hot-reload when PVF catalog or NPK directory changes in real time."""
        self.source = self.owner.source
        self.stage.sync_sources(self.owner.ward, self.source)
        cur_job = self.job.currentData()
        with QSignalBlocker(self.job):
            self.job.clear()
            for token, zh in JOBS_BY_TOKEN.items():
                if token in self.source.poses:
                    self.job.addItem(zh, token)
            idx = self.job.findData(cur_job)
            if idx >= 0:
                self.job.setCurrentIndex(idx)
        cur_rar = self.rarity.currentText()
        with QSignalBlocker(self.rarity):
            self.rarity.clear()
            self.rarity.addItems(['全部稀有度'] + self.source.wardrobe_rarities())
            ridx = self.rarity.findText(cur_rar)
            self.rarity.setCurrentIndex(ridx if ridx >= 0 else 0)
        # Re-resolve worn rows against new source
        refreshed = {}
        for sk, r in self.choice.items():
            nr = self.source.visual('equipment', r['id'])
            if nr is not None:
                refreshed[sk] = nr
        self.choice = refreshed
        self._refresh_resource_badge()
        if self.started:
            self._update_pill_counts()
            self.reload()
            self.render()

    def _refresh_resource_badge(self):
        if hasattr(self.owner, 'resource_badge_text'):
            self.res_badge.setText(self.owner.resource_badge_text())
        else:
            self.res_badge.setText(f'NPK: {self.owner.ward.packs.name}')

    def activate(self):
        self._refresh_resource_badge()
        self.refresh_roles()
        if not self.started:
            self.started = True
            self.job_change()
        else:
            self.reload()
            self.render()

    def refresh_roles(self):
        if not hasattr(self, 'avatar_recipient'):
            return
        old = self.avatar_recipient.currentData()
        with QSignalBlocker(self.avatar_recipient):
            self.avatar_recipient.clear()
            for ident, name in core.roles():
                self.avatar_recipient.addItem(f'{name}  /  {ident}', ident)
            idx = self.avatar_recipient.findData(old)
            if idx >= 0:
                self.avatar_recipient.setCurrentIndex(idx)

    def _grant_index(self, idx):
        self.rows.setCurrentRow(idx)
        self.grant_selected_item()

    def _submit_avatar_grant(self, avatar_ids: list[int], label: str):
        role_id = self.avatar_recipient.currentData()
        if role_id is None:
            raise ValueError('请先创建并选择接收时装的角色。')
        clean_ids = list(dict.fromkeys(int(x) for x in avatar_ids if int(x) > 0))
        if not clean_ids:
            raise ValueError('没有可发送的时装，请先试穿或选择时装。')
        state = core.gateway()
        if state and not state.get('avatar_gm'):
            if QMessageBox.question(
                self,
                '需要重启 100 级服务端',
                '当前运行中的服务端是旧版本进程，尚未加载「实时时装发放」功能。\n\n'
                '是否现在自动重启 100 级服务端？（重启后新开客户端即可实时收发时装）',
            ) == QMessageBox.Yes:
                self.owner.start_work('正在重启本地 100 级服务端以启用实时时装发放…', core.restart_server)
            return
        role_name = self.avatar_recipient.currentText()
        if QMessageBox.question(
            self,
            '确认实时发送时装',
            f'将「{label}」共 {len(clean_ids)} 件时装实时发送到角色 [{role_name}] 的装扮栏？\n\n'
            '在线角色无需下线，网关入账后将即时推送到游戏内「装扮栏」，右键即可穿戴。',
        ) != QMessageBox.Yes:
            return
        job_id = core.grant_avatars(role_id, clean_ids, reason=f'时装试衣间发送：{label}')
        self._last_avatar_job = job_id
        self.owner.last_grant = job_id
        self.grant_status_lbl.setStyleSheet('color:#ffd700;font-size:11px;border:none;background:transparent;')
        self.grant_status_lbl.setText(f'已提交 {len(clean_ids)} 件时装，等待网关实时入账…')
        self.owner.statusBar().showMessage(f'已提交 {len(clean_ids)} 件时装至角色 {role_name} 的装扮栏…')
        self._grant_poll_timer.start()

    def _poll_avatar_grant(self):
        if not self._last_avatar_job:
            self._grant_poll_timer.stop()
            return
        folder, data = core.read_job(self._last_avatar_job)
        if folder == 'receipts' and data is not None:
            self._grant_poll_timer.stop()
            if 'error' in data:
                err_txt = str(data['error'])
                if 'unsupported character op action "avatar"' in err_txt:
                    err_txt = '运行中的服务端为旧版本，请先到首页点击「重启服务端」后再发送'
                self.grant_status_lbl.setStyleSheet('color:#fca5a5;font-size:11px;border:none;background:transparent;')
                self.grant_status_lbl.setText(f'发送失败：{err_txt}')
            else:
                summary = (data.get('op_receipt') or {}).get('summary') or '时装已实时发送至装扮栏'
                self.grant_status_lbl.setStyleSheet('color:#4ade80;font-size:11px;font-weight:bold;border:none;background:transparent;')
                self.grant_status_lbl.setText('✓ ' + summary)
                self.owner.statusBar().showMessage(summary)
        elif folder == 'missing':
            self._grant_poll_timer.stop()

    def grant_current_outfit(self):
        if not self.choice:
            raise ValueError('当前尚未试穿任何时装，请先在右侧点击「试穿」搭配后再发送整套。')
        ordered = [self.choice[s]['id'] for s in ALL_SLOTS_ORDER if s in self.choice and self.choice[s]]
        self._submit_avatar_grant(ordered, f'当前试穿整套（{len(ordered)} 部位）')

    def grant_selected_item(self):
        selected = self.rows.currentItem()
        if selected is None:
            raise ValueError('请先在右侧选择一件时装或套装。')
        ud = selected.data(Qt.UserRole)
        ident = ud[0]
        grp = ud[2] if len(ud) > 2 else ('stackable' if self.mode.currentData() == 'set' else 'equipment')
        if self.mode.currentData() == 'set':
            pack = self.source.visual(grp, ident)
            if not pack or not pack['visual']:
                raise ValueError('该套装未解析出可发送的时装列表。')
            ids = [int(x) for x in json.loads(pack['visual']) if int(x) > 0]
            self._submit_avatar_grant(ids, pack['name'] or f'套装 {ident}')
        else:
            row = self.source.visual('equipment', ident)
            name = row['name'] if row else f'时装 {ident}'
            self._submit_avatar_grant([int(ident)], name)

    def hideEvent(self, event):
        self.anim_timer.stop()
        super().hideEvent(event)

    def _compute_card_size(self) -> QSize:
        vw = self.rows.viewport().width()
        if vw < 200:
            vw = max(560, self.width() - 420)
        spacing = self.rows.spacing()
        # Reserve space for QListWidget padding (12px) + scrollbar safety margin (10px)
        usable = max(360, vw - 22)
        cols = max(3, min(5, usable // 225))
        card_w = max(168, (usable - spacing * (cols + 1)) // cols)
        # Double-check mathematical fit so Qt IconMode never wraps the last column early
        while cols > 1 and (cols * card_w + (cols + 1) * spacing) > usable:
            card_w -= 1
        return QSize(card_w, 96)

    def _fit_card_sizes(self):
        if self.rows.count() == 0:
            return
        card_size = self._compute_card_size()
        if getattr(self, '_last_card_size', None) == card_size:
            return
        self._last_card_size = card_size
        for idx in range(self.rows.count()):
            item = self.rows.item(idx)
            item.setSizeHint(card_size)
            w = self.rows.itemWidget(item)
            if w is not None:
                w.setFixedSize(card_size)
        self.rows.doItemsLayout()

    def eventFilter(self, obj, event):
        if obj is self.rows.viewport() and event.type() == QEvent.Resize and self.started:
            self._fit_card_sizes()
        return super().eventFilter(obj, event)

    def resizeEvent(self, event):
        super().resizeEvent(event)
        if self.started:
            self._fit_card_sizes()

    def showEvent(self, event):
        super().showEvent(event)
        if self.started:
            QTimer.singleShot(0, self._fit_card_sizes)

    def _on_visual_toggle(self, *_):
        self._sync_anim_timer()
        self.render()

    def _sync_anim_timer(self):
        should_run = (
            self.isVisible()
            and self.chk_aura.isChecked()
            and self.chk_aura_anim.isChecked()
            and self.stage.has_aura_animation(self.choice)
        )
        if should_run and not self.anim_timer.isActive():
            self.anim_timer.start()
        elif not should_run and self.anim_timer.isActive():
            self.anim_timer.stop()

    def _tick_aura(self):
        self.aura_ms += self.anim_timer.interval()
        self.render(from_anim=True)

    def _update_pill_styles(self):
        mode = self.mode.currentData() or 'single'
        slot = self.slot.currentData() if mode == 'single' else ''
        active_key = f'{mode}:{slot}'
        for pkey, (pb, _) in self.pills.items():
            is_act = pkey == active_key
            if is_act:
                pb.setStyleSheet(
                    'QPushButton{background:#d4af37;color:#141008;border:1px solid #ffd700;'
                    'border-radius:6px;padding:3px 4px;font-size:11px;font-weight:bold;}'
                )
            else:
                pb.setStyleSheet(
                    'QPushButton{background:#1d1928;color:#d6ccb9;border:1px solid rgba(212,175,55,0.3);'
                    'border-radius:6px;padding:3px 4px;font-size:11px;}'
                    'QPushButton:hover{border-color:#ffd700;color:#ffd700;}'
                )
        self._refresh_paperdoll()

    def _update_pill_counts(self):
        job = self.job.currentData()
        if not job:
            return
        rar = self.rarity.currentText() if hasattr(self, 'rarity') else '全部稀有度'
        counts = self.source.slot_counts(job, rarity=rar)
        total_single = sum(v for k, v in counts.items() if k != 'set')
        for pkey, (pb, base_label) in self.pills.items():
            if pkey == 'single:':
                cnt = total_single
            elif pkey == 'set:':
                cnt = counts.get('set', 0)
            else:
                cnt = counts.get(pkey.split(':', 1)[1], 0)
            pb.setText(f'{base_label} ({cnt})' if cnt else base_label)

    def _rarity_changed(self, *_):
        self._update_pill_counts()
        self.page = 0
        self.reload()

    def _on_pill_clicked(self, pkey):
        mode, slot = pkey.split(':', 1)
        with QSignalBlocker(self.mode), QSignalBlocker(self.slot):
            self.mode.setCurrentIndex(self.mode.findData(mode))
            self.slot.setEnabled(mode == 'single')
            self.remove_btn.setEnabled(mode == 'single')
            self.slot.setCurrentIndex(self.slot.findData(slot) if mode == 'single' else 0)
        self._update_pill_styles()
        self.page = 0
        self.reload()

    def _select_slot_pill(self, slot_key):
        self._on_pill_clicked(f'single:{slot_key}')

    def _unequip_slot(self, slot_key):
        if slot_key in self.choice:
            self.choice.pop(slot_key, None)
            self.render()
            self.reload()

    def _slot_combo_changed(self, *_):
        self._update_pill_styles()
        self.new_search()

    def new_search(self, *_):
        self.page = 0
        self.delay.start()

    def mode_change(self, *_):
        single = self.mode.currentData() == 'single'
        self.slot.setEnabled(single)
        self.remove_btn.setEnabled(single)
        self._update_pill_styles()
        self.new_search()

    def job_change(self, *_):
        self.choice = {}
        self.page = 0
        self._update_pill_counts()
        self._update_pill_styles()
        self.reload()
        self.render()

    def turn(self, delta):
        self.page += delta
        self.reload()

    def _worn_ids(self):
        return {r['id'] for r in self.choice.values() if r}

    def reload(self):
        job = self.job.currentData()
        if not job:
            return
        mode = self.mode.currentData()
        slot_filter = self.slot.currentData() if mode == 'single' else ''
        rar_filter = self.rarity.currentText() if hasattr(self, 'rarity') else '全部稀有度'
        only_vis = self.only_visual_chk.isChecked()
        rows, self.page, total = self.source.costume_cards(
            job, mode, slot_filter, self.search.text(), self.page,
            page_size=self.page_size, only_visual=only_vis, rarity=rar_filter,
        )
        worn_ids = self._worn_ids()
        self.rows.clear()
        card_size = self._compute_card_size()
        self._last_card_size = card_size
        for idx, r in enumerate(rows):
            ident = r['id']
            s = r['slot'] or ''
            grp = r['g'] or ('stackable' if mode == 'set' else 'equipment')
            item = QListWidgetItem()
            item.setData(Qt.UserRole, (ident, s, grp))
            item.setSizeHint(card_size)
            self.rows.addItem(item)
            is_worn = (ident in worn_ids) if mode == 'single' else False
            card = CostumeCardWidget(
                r,
                self.owner.item_icons,
                is_worn,
                on_wear=lambda i=idx: self._wear_index(i),
                on_select=lambda i=idx: self.rows.setCurrentRow(i),
                on_grant=lambda i=idx: self._grant_index(i),
            )
            card.setFixedSize(card_size)
            self.rows.setItemWidget(item, card)
        if rows:
            self.rows.setCurrentRow(0)
        pages = max(1, (total + self.page_size - 1) // self.page_size)
        self.count.setText(f'{total:,} 项  ·  第 {self.page + 1} / {pages} 页')
        self.prev.setEnabled(self.page > 0)
        self.next.setEnabled(self.page + 1 < pages)
        self.try_btn.setEnabled(bool(rows))
        self._update_pill_styles()

    def _wear_index(self, idx):
        self.rows.setCurrentRow(idx)
        self.try_on()

    def try_on(self):
        selected = self.rows.currentItem()
        if selected is None:
            return
        ud = selected.data(Qt.UserRole)
        ident, slot = ud[0], ud[1]
        grp = ud[2] if len(ud) > 2 else ('stackable' if self.mode.currentData() == 'set' else 'equipment')
        if self.mode.currentData() == 'set':
            pack = self.source.visual(grp, ident)
            parts = [self.source.visual('equipment', i) for i in json.loads(pack['visual'])]
            self.choice = {s: r for s, r in self.choice.items() if s in ('weapon', 'aura', 'skin')}
            for r in parts:
                if r:
                    self.choice[r['slot']] = r
        else:
            self.choice[slot] = self.source.visual('equipment', ident)
        self.aura_ms = 0
        self.render()
        self._refresh_worn_highlights()

    def _refresh_worn_highlights(self):
        # Update card borders cheaply by reloading current page only if visible
        self._refresh_paperdoll()
        worn_ids = self._worn_ids()
        for idx in range(self.rows.count()):
            item = self.rows.item(idx)
            ident = item.data(Qt.UserRole)[0]
            w = self.rows.itemWidget(item)
            if w and hasattr(w, 'row'):
                is_worn = (ident in worn_ids) and self.mode.currentData() == 'single'
                color = RARITY_COLORS.get(w.row['rar'] or '', '#d4af37')
                border = '#ffd700' if is_worn else color
                bg = '#272033' if is_worn else '#181522'
                w.setStyleSheet(
                    f'CostumeCardWidget{{background:{bg};border:{"2px" if is_worn else "1px"} solid {border};border-radius:9px;}}'
                    f'CostumeCardWidget:hover{{background:#241e33;border-color:#ffd700;}}'
                )

    def remove(self):
        row = self.rows.currentItem()
        slot = self.slot.currentData() or (row.data(Qt.UserRole)[1] if row else '')
        if self.mode.currentData() == 'single' and slot:
            self.choice.pop(slot, None)
            self.render()
            self._refresh_worn_highlights()

    def reset(self):
        self.choice = {}
        self.aura_ms = 0
        self.render()
        self._refresh_worn_highlights()

    def random_outfit(self):
        job = self.job.currentData()
        if not job:
            return
        new_choice = {}
        for sk in ('cap', 'hair', 'face', 'neck', 'coat', 'belt', 'pants', 'shoes', 'weapon', 'aura'):
            rows, _, _ = self.source.costume_cards(job, 'single', sk, '', 0, page_size=40, only_visual=True)
            if rows:
                pick = random.choice(rows)
                vis = self.source.visual('equipment', pick['id'])
                if vis is not None:
                    new_choice[sk] = vis
        self.choice = new_choice
        self.aura_ms = 0
        self.render()
        self._refresh_worn_highlights()

    def _refresh_paperdoll(self):
        mode = self.mode.currentData() or 'single'
        active_slot = self.slot.currentData() if mode == 'single' else ''
        for sk, tile in self.slot_tiles.items():
            tile.set_state(self.choice.get(sk), self.owner.item_icons, active=(sk == active_slot))

    def render(self, from_anim=False):
        job = self.job.currentData()
        if not job:
            self.figure.setText('当前目录没有可用的职业站姿')
            return
        p = self.source.poses[job]
        if not from_anim:
            self.caption.setText(
                f'{JOBS_BY_TOKEN[job]} · 待机第 {p["Frame"]} 帧 · 已穿戴 {len(self.choice)}/11 部位（支持一键实时发给角色）'
            )
            self._refresh_paperdoll()
            self._sync_anim_timer()
        try:
            use_ms = self.aura_ms if (self.chk_aura_anim.isChecked() and self.stage.has_aura_animation(self.choice)) else None
            im, errs = self.stage.render(
                job,
                self.choice,
                show_aura=self.chk_aura.isChecked(),
                show_weapon=self.chk_weapon.isChecked(),
                show_defaults=self.chk_defaults.isChecked(),
                aura_ms=use_ms,
            )
            zoom = float(self.zoom_combo.currentData() or 2.0)
            target_w = max(290, self.figure.width() - 4)
            target_h = max(270, self.figure.height() - 4)
            sw = max(1, int(im.width * zoom))
            sh = max(1, int(im.height * zoom))
            if sw > target_w - 12 or sh > target_h - 12:
                fit = min((target_w - 12) / im.width, (target_h - 12) / im.height)
                sw = max(1, int(im.width * fit))
                sh = max(1, int(im.height * fit))
            scaled = im.resize((sw, sh), Image.Resampling.NEAREST)
            theme = self.bg_combo.currentData() or 'dark'
            canvas = _make_backdrop(target_w, target_h, theme)
            ox = (target_w - sw) // 2
            oy = (target_h - sh) // 2
            canvas.alpha_composite(scaled, (ox, oy))
            self.figure.setPixmap(pix(canvas))

            if not from_anim:
                outfit = '、'.join(SLOT_ZH[s] + ': ' + r['name'] for s, r in self.choice.items()) or '基础站姿'
                warning = f' · {len(errs)} 个图层未能绘制，悬停查看原因' if errs else ''
                self.detail.setText(outfit[:150] + warning)
                self.detail.setToolTip('\n'.join([outfit, *errs]))
                self.figure.setToolTip('\n'.join(errs))
        except Exception as exc:
            self.figure.setText(str(exc))
            self.detail.setText('未能绘制此搭配')
