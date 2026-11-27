"""Visual Global Drop (per-difficulty) & Abyss Drop (Normal + Lv.100 Wisdom's Guide) Console."""
from __future__ import annotations

import copy
import json
import struct
import zlib

from PySide6.QtCore import QSignalBlocker, QSize, Qt, Signal
from PySide6.QtGui import QColor, QFont, QLinearGradient, QPainter, QPainterPath, QPen
from PySide6.QtWidgets import (
    QCheckBox,
    QComboBox,
    QDialog,
    QDoubleSpinBox,
    QFormLayout,
    QFrame,
    QGridLayout,
    QHBoxLayout,
    QLabel,
    QLineEdit,
    QListWidget,
    QListWidgetItem,
    QMessageBox,
    QPushButton,
    QScrollArea,
    QSlider,
    QSpinBox,
    QTabWidget,
    QTextEdit,
    QVBoxLayout,
    QWidget,
)

import core
from epic_ui import RARITY_COLORS, pix
from item_icons import reference
import ui_icons


RARITY_META = [
    ('0', '普通', '#d4d4d8'),
    ('1', '高级', '#7dd3fc'),
    ('2', '稀有', '#c084fc'),
    ('3', '神器', '#f472b6'),
    ('6', '传说', '#fb923c'),
    ('4', '史诗', '#facc15'),
    ('7', '神话', '#fb7185'),
]
RARITY_COLOR_BY_KEY = {k: col for k, _n, col in RARITY_META}
RARITY_NAME_BY_KEY = {k: n for k, n, _c in RARITY_META}

DIFF_ACCENTS = {
    0: ('普通', 'NORMAL', '#94a3b8'),
    1: ('冒险', 'EXPERT', '#60a5fa'),
    2: ('勇士', 'MASTER', '#a78bfa'),
    3: ('王者', 'KING', '#f59e0b'),
    4: ('噩梦', 'SLAYER', '#f43f5e'),
}

NORMAL_ABYSS_SCOPES = [
    ('all', '全副本怪物生效（推荐）'),
    ('boss_only', '仅领主 / 精英怪触发'),
    ('maps', '仅指定深渊房间触发'),
]

LV100_ABYSS_SCOPES = [
    ('all', '全副本生效（随时体验 100 级深渊）'),
    ('lv100_dungeons', '仅 100 级 / 高等级副本 (Lv.95+)'),
    ('boss_only', '仅领主 / 精英怪触发'),
    ('maps', '仅指定深渊房间触发'),
]

NORMAL_ABYSS_POOLS = [
    ('level_epic', '按怪物等级匹配 · 经典深渊池 (Lv.1–95)'),
    ('all_epic', '全等级经典深渊池 (神器/传说/史诗)'),
    ('custom', '仅从下方自定义装备池掉落'),
]

LV100_ABYSS_POOLS = [
    ('lv100_all', '100 级全史诗 + 神话池（智慧的引导）'),
    ('lv100_epic', '仅 100 级史诗池'),
    ('lv100_mythic', '仅 100 级神话池（必出神话）'),
    ('custom', '仅从下方自定义 100 级装备池掉落'),
]


def _render_item_pixmap(icons, img: str, frame_idx: int, size: int = 34):
    ref = reference(img, frame_idx)
    if ref is None or icons is None:
        return None
    try:
        pm = pix(icons.read(ref))
        return pm.scaled(size, size, Qt.KeepAspectRatio, Qt.FastTransformation)
    except (OSError, ValueError, KeyError, struct.error, zlib.error):
        return None


def _lookup_equipment_meta(source, item_id: int):
    row = source.db.execute(
        "SELECT id, name, rar, cat, img, frame, path FROM item "
        "WHERE g='equipment' AND id=? ORDER BY grantmode DESC LIMIT 1",
        (int(item_id),),
    ).fetchone()
    if row:
        return dict(
            id=int(row['id']),
            name=row['name'],
            rar=row['rar'],
            cat=row['cat'],
            img=row['img'],
            frame=int(row['frame']),
            path=row['path'],
        )
    return dict(id=int(item_id), name=f'装备 #{item_id}', rar='史诗', cat='装备', img='', frame=-1, path='')


def _sample_equipment_ids(source, rarities: tuple[str, ...], limit: int = 6, lv100_bias: bool = False) -> list[int]:
    if lv100_bias:
        out: list[int] = []
        # Pick genuine Lv.100 Mythics first (e.g. 军神的心之所念 / 永恒之海 / 时之魅影)
        myth_rows = source.db.execute(
            "SELECT id FROM item WHERE g='equipment' AND grantmode>0 AND slot='' "
            "AND rar='神话' ORDER BY id LIMIT ?",
            (max(1, limit // 2),),
        ).fetchall()
        out.extend(int(r[0]) for r in myth_rows)
        rem = limit - len(out)
        if rem > 0:
            # Fill remaining slots with Lv.100 Epics (high-ID 100-cap epics)
            epic_rows = source.db.execute(
                "SELECT id FROM item WHERE g='equipment' AND grantmode>0 AND slot='' "
                "AND rar='史诗' ORDER BY id DESC LIMIT ?",
                (rem,),
            ).fetchall()
            out.extend(int(r[0]) for r in epic_rows if int(r[0]) not in out)
        return out[:limit]
    placeholders = ','.join('?' for _ in rarities)
    sql = (
        f"SELECT id FROM item WHERE g='equipment' AND grantmode>0 AND slot='' "
        f"AND rar IN ({placeholders}) ORDER BY rank, id LIMIT ?"
    )
    rows = source.db.execute(sql, [*rarities, limit]).fetchall()
    return [int(r[0]) for r in rows]


class RarityBarWidget(QWidget):
    """Stacked horizontal probability bar with glossy metallic styling."""

    def __init__(self, compact: bool = False, parent=None):
        super().__init__(parent)
        self._compact = compact
        self._segments: list[tuple[str, str, int]] = []  # (label, hex_color, weight)
        self.setMinimumHeight(10 if compact else 26)
        self.setMaximumHeight(12 if compact else 28)

    def set_segments(self, segments: list[tuple[str, str, int]]):
        self._segments = [(lbl, col, max(0, int(w))) for lbl, col, w in segments]
        total = sum(w for _l, _c, w in self._segments)
        if total > 0:
            lines = [f'{lbl}: {w * 100.0 / total:.1f}% (权重 {w})' for lbl, _c, w in self._segments if w > 0]
            self.setToolTip('\n'.join(lines))
        else:
            self.setToolTip('权重总和为 0（将回退默认品质）')
        self.update()

    def paintEvent(self, event):
        p = QPainter(self)
        p.setRenderHint(QPainter.Antialiasing, True)
        rect = self.rect().adjusted(0, 1, 0, -1)
        radius = rect.height() / 2.0
        clip = QPainterPath()
        clip.addRoundedRect(rect, radius, radius)
        p.setClipPath(clip)

        p.fillRect(rect, QColor(18, 15, 28, 225))
        total = sum(w for _l, _c, w in self._segments)
        if total > 0:
            x = float(rect.left())
            w_total = float(rect.width())
            active = [(lbl, col, w) for lbl, col, w in self._segments if w > 0]
            for idx, (lbl, col, w) in enumerate(active):
                seg_w = (rect.right() + 1 - x) if idx == len(active) - 1 else (w_total * w / total)
                seg_rect = rect.adjusted(int(round(x - rect.left())), 0, 0, 0)
                seg_rect.setWidth(max(1, int(round(seg_w))))
                base = QColor(col)
                grad = QLinearGradient(seg_rect.left(), seg_rect.top(), seg_rect.left(), seg_rect.bottom())
                grad.setColorAt(0.0, base.lighter(118))
                grad.setColorAt(0.55, base)
                grad.setColorAt(1.0, base.darker(135))
                p.fillRect(seg_rect, grad)
                if not self._compact and seg_w >= 46:
                    pct = w * 100.0 / total
                    p.setPen(QColor('#140f08') if base.lightness() > 140 else QColor('#fff9ec'))
                    font = QFont(self.font())
                    font.setPixelSize(11)
                    font.setBold(True)
                    p.setFont(font)
                    p.drawText(seg_rect, Qt.AlignCenter, f'{lbl} {pct:.0f}%')
                x += seg_w

        p.setClipping(False)
        p.setPen(QPen(QColor(224, 184, 82, 110 if not self._compact else 80), 1.0))
        p.drawRoundedRect(rect, radius, radius)


class DifficultyCard(QFrame):
    """Clickable visual summary card for one DNF dungeon difficulty tier."""

    clicked = Signal(int)

    def __init__(self, diff_idx: int, parent=None):
        super().__init__(parent)
        self.diff_idx = diff_idx
        self._selected = False
        zh, en, accent = DIFF_ACCENTS[diff_idx]
        self._accent = accent
        self.setCursor(Qt.PointingHandCursor)
        self.setMinimumHeight(104)

        lay = QVBoxLayout(self)
        lay.setContentsMargins(11, 9, 11, 9)
        lay.setSpacing(4)

        top = QHBoxLayout()
        top.setSpacing(6)
        self.title_lbl = QLabel(f'{zh}难度')
        self.title_lbl.setStyleSheet(f'font-size:14px;font-weight:bold;color:{accent};border:none;background:transparent;')
        self.state_lbl = QLabel('启用')
        self.state_lbl.setStyleSheet(
            'font-size:10px;font-weight:bold;color:#4ade80;background:rgba(34,197,94,0.16);'
            'border:1px solid rgba(74,222,128,0.45);border-radius:8px;padding:1px 6px;'
        )
        top.addWidget(self.title_lbl)
        top.addStretch()
        top.addWidget(self.state_lbl)
        lay.addLayout(top)

        en_lbl = QLabel(en)
        en_lbl.setStyleSheet('font-size:9px;letter-spacing:1.4px;color:#938a7a;border:none;background:transparent;')
        lay.addWidget(en_lbl)

        self.rate_lbl = QLabel('12% · ×1.0')
        self.rate_lbl.setStyleSheet('font-size:13px;font-weight:bold;color:#f6efe2;border:none;background:transparent;')
        lay.addWidget(self.rate_lbl)

        self.meta_lbl = QLabel('每次 1 件 · 自动池')
        self.meta_lbl.setStyleSheet('font-size:11px;color:#bdb29f;border:none;background:transparent;')
        lay.addWidget(self.meta_lbl)

        self.bar = RarityBarWidget(compact=True)
        lay.addWidget(self.bar)
        self._apply_style()

    def set_selected(self, selected: bool):
        self._selected = selected
        self._apply_style()

    def _apply_style(self):
        if self._selected:
            self.setStyleSheet(
                f"DifficultyCard{{background:qlineargradient(x1:0,y1:0,x2:0,y2:1,"
                f"stop:0 rgba(235,190,74,0.22),stop:1 rgba(26,18,38,0.92));"
                f"border:2px solid {self._accent};border-radius:11px;}}"
            )
        else:
            self.setStyleSheet(
                "DifficultyCard{background:rgba(16,13,26,0.82);"
                "border:1px solid rgba(212,175,55,0.26);border-radius:11px;}"
                f"DifficultyCard:hover{{background:rgba(28,22,44,0.90);border-color:{self._accent};}}"
            )

    def refresh(self, cfg: dict):
        enabled = bool(cfg.get('enabled', True))
        chance = int(cfg.get('equip_drop_chance', 0))
        mult = float(cfg.get('drop_rate_multiplier', 1.0))
        eff = min(100, int(round(chance * mult)))
        cnt = int(cfg.get('drop_count', 1))
        custom_n = len(cfg.get('custom_item_ids') or [])
        if enabled:
            self.state_lbl.setText('已启用')
            self.state_lbl.setStyleSheet(
                'font-size:10px;font-weight:bold;color:#4ade80;background:rgba(34,197,94,0.16);'
                'border:1px solid rgba(74,222,128,0.45);border-radius:8px;padding:1px 6px;'
            )
            self.rate_lbl.setText(f'综合 {eff}%  (×{mult:.1f})')
        else:
            self.state_lbl.setText('已关闭')
            self.state_lbl.setStyleSheet(
                'font-size:10px;font-weight:bold;color:#a1a1aa;background:rgba(113,113,122,0.18);'
                'border:1px solid rgba(161,161,170,0.35);border-radius:8px;padding:1px 6px;'
            )
            self.rate_lbl.setText('未启用掉落')
        pool_txt = f'自选 {custom_n} 件' if custom_n else '智能权重池'
        self.meta_lbl.setText(f'每次 {cnt} 件 · {pool_txt}')
        rw = cfg.get('rarity_weights') or {}
        self.bar.set_segments([(n, c, rw.get(k, 0)) for k, n, c in RARITY_META])

    def mousePressEvent(self, event):
        self.clicked.emit(self.diff_idx)
        super().mousePressEvent(event)


class EquipmentPoolWidget(QFrame):
    """Visual equipment pool showing real NPK icon cards with add/remove controls."""

    changed = Signal()

    def __init__(self, win, title: str, empty_hint: str, default_rarities: tuple[str, ...], lv100_bias: bool = False, parent=None):
        super().__init__(parent)
        self.win = win
        self._ids: list[int] = []
        self._empty_hint = empty_hint
        self._default_rarities = default_rarities
        self._lv100_bias = lv100_bias
        self.setObjectName('EquipPoolBox')
        self.setStyleSheet(
            "QFrame#EquipPoolBox{background:rgba(11,9,20,0.68);"
            "border:1px solid rgba(212,175,55,0.28);border-radius:10px;}"
        )

        lay = QVBoxLayout(self)
        lay.setContentsMargins(10, 9, 10, 9)
        lay.setSpacing(7)

        hdr = QHBoxLayout()
        hdr.setSpacing(6)
        self.title_lbl = QLabel(title)
        self.title_lbl.setStyleSheet('font-size:12px;font-weight:bold;color:#ffe082;border:none;background:transparent;')
        self.count_lbl = QLabel('0 件')
        self.count_lbl.setStyleSheet('font-size:11px;color:#93c5fd;border:none;background:transparent;')
        hdr.addWidget(self.title_lbl)
        hdr.addWidget(self.count_lbl)
        hdr.addStretch()

        pick_btn = QPushButton('+ 可视化选装备')
        pick_btn.setObjectName('GoldBtn')
        pick_btn.setStyleSheet(
            "QPushButton{background:qlineargradient(x1:0,y1:0,x2:0,y2:1,stop:0 #ffe587,stop:1 #c68a1a);"
            "color:#181105;border:1px solid #fff0b3;border-radius:7px;padding:4px 10px;font-size:11px;font-weight:bold;}"
            "QPushButton:hover{background:#ffd74c;}"
        )
        pick_btn.clicked.connect(self._open_picker)

        sample_btn = QPushButton('推荐填充')
        sample_btn.setObjectName('DarkBtn')
        sample_btn.setStyleSheet(
            "QPushButton{background:rgba(28,23,44,0.88);color:#eee6d7;border:1px solid rgba(220,180,82,0.38);"
            "border-radius:7px;padding:4px 8px;font-size:11px;font-weight:bold;}"
            "QPushButton:hover{border-color:#ffd700;color:#ffe28a;}"
        )
        sample_btn.clicked.connect(self._fill_sample)

        clear_btn = QPushButton('清空')
        clear_btn.setObjectName('DarkBtn')
        clear_btn.setStyleSheet(
            "QPushButton{background:rgba(36,18,28,0.82);color:#fca5a5;border:1px solid rgba(248,113,113,0.35);"
            "border-radius:7px;padding:4px 8px;font-size:11px;font-weight:bold;}"
            "QPushButton:hover{border-color:#f87171;}"
        )
        clear_btn.clicked.connect(self.clear_items)

        hdr.addWidget(pick_btn)
        hdr.addWidget(sample_btn)
        hdr.addWidget(clear_btn)
        lay.addLayout(hdr)

        self.scroll = QScrollArea()
        self.scroll.setWidgetResizable(True)
        self.scroll.setMinimumHeight(130)
        self.scroll.setStyleSheet('QScrollArea{border:none;background:transparent;}')
        self.inner = QWidget()
        self.inner.setStyleSheet('background:transparent;')
        self.grid = QGridLayout(self.inner)
        self.grid.setContentsMargins(2, 2, 2, 2)
        self.grid.setSpacing(6)
        self.scroll.setWidget(self.inner)
        lay.addWidget(self.scroll, 1)

    def item_ids(self) -> list[int]:
        return list(self._ids)

    def set_item_ids(self, ids: list[int], emit: bool = False):
        self._ids = list(dict.fromkeys(int(x) for x in ids if int(x) > 0))
        self.rebuild()
        if emit:
            self.changed.emit()

    def clear_items(self):
        if self._ids:
            self._ids = []
            self.rebuild()
            self.changed.emit()

    def _fill_sample(self):
        picked = _sample_equipment_ids(self.win.source, self._default_rarities, limit=6, lv100_bias=self._lv100_bias)
        if not picked:
            return
        merged = list(dict.fromkeys([*self._ids, *picked]))
        self.set_item_ids(merged, emit=True)

    def _open_picker(self):
        dlg = EquipmentPickerDialog(
            self.win,
            initial_rar=self._default_rarities[0] if self._default_rarities else '全部稀有度',
            selected_ids=self._ids,
            parent=self,
        )
        if dlg.exec() == QDialog.Accepted:
            self.set_item_ids(dlg.selected_ids(), emit=True)

    def _remove_id(self, ident: int):
        if ident in self._ids:
            self._ids.remove(ident)
            self.rebuild()
            self.changed.emit()

    def rebuild(self):
        while self.grid.count():
            it = self.grid.takeAt(0)
            w = it.widget()
            if w is not None:
                w.hide()
                w.setParent(None)
                w.deleteLater()
        self.count_lbl.setText(f'{len(self._ids)} 件')
        if not self._ids:
            lbl = QLabel(self._empty_hint)
            lbl.setWordWrap(True)
            lbl.setAlignment(Qt.AlignCenter)
            lbl.setStyleSheet('color:#8f8778;font-size:11px;padding:18px;border:none;background:transparent;')
            self.grid.addWidget(lbl, 0, 0, 1, 2)
            return

        cols = 2
        for idx, ident in enumerate(self._ids):
            meta = _lookup_equipment_meta(self.win.source, ident)
            color = RARITY_COLORS.get(meta['rar'], '#d4af37')
            card = QFrame()
            card.setStyleSheet(
                f"QFrame{{background:rgba(22,18,34,0.92);border:1px solid {color};border-radius:8px;}}"
            )
            cl = QHBoxLayout(card)
            cl.setContentsMargins(7, 6, 7, 6)
            cl.setSpacing(7)

            ico = QLabel()
            ico.setFixedSize(34, 34)
            ico.setAlignment(Qt.AlignCenter)
            ico.setStyleSheet(f"background:#120f1c;border:1px solid {color};border-radius:5px;")
            pm = _render_item_pixmap(self.win.item_icons, meta['img'], meta['frame'], 30)
            if pm:
                ico.setPixmap(pm)
            else:
                ico.setText('★')
            cl.addWidget(ico)

            txt = QVBoxLayout()
            txt.setSpacing(1)
            nm = QLabel(meta['name'])
            nm.setStyleSheet(f'color:{color};font-size:12px;font-weight:bold;border:none;background:transparent;')
            nm.setWordWrap(True)
            sub = QLabel(f'{meta["rar"]} · {meta["cat"]} · {ident}')
            sub.setStyleSheet('color:#aaa08d;font-size:10px;border:none;background:transparent;')
            txt.addWidget(nm)
            txt.addWidget(sub)
            cl.addLayout(txt, 1)

            rm = QPushButton('×')
            rm.setFixedSize(22, 22)
            rm.setCursor(Qt.PointingHandCursor)
            rm.setStyleSheet(
                "QPushButton{background:rgba(120,28,36,0.45);color:#fca5a5;border:1px solid rgba(248,113,113,0.4);"
                "border-radius:11px;font-size:13px;font-weight:bold;padding:0;}"
                "QPushButton:hover{background:#dc2626;color:#ffffff;}"
            )
            rm.clicked.connect(lambda checked=False, item_id=ident: self._remove_id(item_id))
            cl.addWidget(rm)

            r, c = divmod(idx, cols)
            self.grid.addWidget(card, r, c)
        self.grid.setRowStretch((len(self._ids) + 1) // cols + 1, 1)


class EquipmentPickerDialog(QDialog):
    """Visual equipment picker backed by live PVF SQLite + NPK icons."""

    def __init__(self, win, initial_rar: str = '史诗', selected_ids: list[int] | None = None, parent=None):
        super().__init__(parent)
        self.win = win
        self._selected = list(dict.fromkeys(int(x) for x in (selected_ids or []) if int(x) > 0))
        self._page = 0
        self._page_size = 18
        self.setWindowTitle('可视化挑选掉落装备 · 实时 PVF 装备库')
        self.resize(900, 620)
        self.setStyleSheet(
            "QDialog{background:#120f1c;color:#f4eee2;}"
            "QLabel{color:#eee6d7;}"
        )

        lay = QVBoxLayout(self)
        lay.setContentsMargins(16, 14, 16, 14)
        lay.setSpacing(10)

        top = QHBoxLayout()
        self.search = QLineEdit()
        self.search.setPlaceholderText('搜索装备名称、ID 或路径（如 军神 / 永恒之海 / 太刀 / 100390336）…')
        self.search.textChanged.connect(self._reset_and_load)

        self.rar_combo = QComboBox()
        self.rar_combo.addItems(['全部稀有度', '神话', '史诗', '传说', '神器', '稀有', '高级', '普通'])
        idx = self.rar_combo.findText(initial_rar)
        if idx >= 0:
            self.rar_combo.setCurrentIndex(idx)
        self.rar_combo.currentIndexChanged.connect(self._reset_and_load)

        self.cat_combo = QComboBox()
        cats = [c for c in self.win.source.distinct('cat') if not c.startswith('时装-') and c not in ('材料', '消耗品', '任务道具', '投掷物')]
        self.cat_combo.addItems(['全部部位', *cats])
        self.cat_combo.currentIndexChanged.connect(self._reset_and_load)

        top.addWidget(self.search, 3)
        top.addWidget(self.rar_combo, 1)
        top.addWidget(self.cat_combo, 1)
        lay.addLayout(top)

        self.info_lbl = QLabel('')
        self.info_lbl.setStyleSheet('color:#d4c6a8;font-size:12px;')
        lay.addWidget(self.info_lbl)

        scroll = QScrollArea()
        scroll.setWidgetResizable(True)
        scroll.setStyleSheet('QScrollArea{border:1px solid rgba(212,175,55,0.28);border-radius:8px;background:rgba(10,8,18,0.8);}')
        self.grid_host = QWidget()
        self.grid = QGridLayout(self.grid_host)
        self.grid.setContentsMargins(10, 10, 10, 10)
        self.grid.setSpacing(8)
        scroll.setWidget(self.grid_host)
        lay.addWidget(scroll, 1)

        foot = QHBoxLayout()
        self.prev_btn = QPushButton('◀ 上一页')
        self.prev_btn.setObjectName('DarkBtn')
        self.prev_btn.clicked.connect(lambda: self._go(-1))
        self.page_lbl = QLabel('第 1 页')
        self.page_lbl.setStyleSheet('color:#ffd700;font-weight:bold;')
        self.next_btn = QPushButton('下一页 ▶')
        self.next_btn.setObjectName('DarkBtn')
        self.next_btn.clicked.connect(lambda: self._go(1))
        self.sel_lbl = QLabel('')
        self.sel_lbl.setStyleSheet('color:#4ade80;font-weight:bold;')

        ok_btn = QPushButton('确认选择')
        ok_btn.setObjectName('GoldBtn')
        ok_btn.clicked.connect(self.accept)
        cancel_btn = QPushButton('取消')
        cancel_btn.setObjectName('DarkBtn')
        cancel_btn.clicked.connect(self.reject)

        foot.addWidget(self.prev_btn)
        foot.addWidget(self.page_lbl)
        foot.addWidget(self.next_btn)
        foot.addStretch()
        foot.addWidget(self.sel_lbl)
        foot.addWidget(cancel_btn)
        foot.addWidget(ok_btn)
        lay.addLayout(foot)

        self._load_page()

    def selected_ids(self) -> list[int]:
        return list(self._selected)

    def _reset_and_load(self):
        self._page = 0
        self._load_page()

    def _go(self, delta: int):
        self._page = max(0, self._page + delta)
        self._load_page()

    def _toggle_id(self, ident: int):
        if ident in self._selected:
            self._selected.remove(ident)
        else:
            self._selected.append(ident)
        self._load_page()

    def _load_page(self):
        preds = ["g='equipment'", "grantmode>0", "slot=''"]
        args = []
        rar = self.rar_combo.currentText()
        if rar != '全部稀有度':
            preds.append('rar=?')
            args.append(rar)
        cat = self.cat_combo.currentText()
        if cat != '全部部位':
            preds.append('cat=?')
            args.append(cat)
        kw = self.search.text().strip().casefold()
        if kw:
            preds.append("(instr(cast(id as text),?) or instr(lower(name),?) or instr(lower(alias),?) or instr(path,?))")
            args.extend([kw] * 4)
        where = ' WHERE ' + ' AND '.join(preds)
        total = self.win.source.db.execute('SELECT count(*) FROM item' + where, args).fetchone()[0]
        total_pages = max(1, (total + self._page_size - 1) // self._page_size)
        self._page = min(self._page, total_pages - 1)
        rows = self.win.source.db.execute(
            'SELECT id, name, rar, cat, img, frame FROM item' + where + ' ORDER BY rank, id LIMIT ? OFFSET ?',
            [*args, self._page_size, self._page * self._page_size],
        ).fetchall()

        while self.grid.count():
            it = self.grid.takeAt(0)
            w = it.widget()
            if w is not None:
                w.hide()
                w.setParent(None)
                w.deleteLater()

        for idx, row in enumerate(rows):
            ident = int(row['id'])
            chosen = ident in self._selected
            color = RARITY_COLORS.get(row['rar'], '#d4af37')
            card = QFrame()
            bg = 'rgba(235,188,72,0.20)' if chosen else 'rgba(22,18,34,0.90)'
            bd = f'2px solid #ffd700' if chosen else f'1px solid {color}'
            card.setStyleSheet(f"QFrame{{background:{bg};border:{bd};border-radius:8px;}}")
            cl = QHBoxLayout(card)
            cl.setContentsMargins(8, 7, 8, 7)
            cl.setSpacing(8)

            ico = QLabel()
            ico.setFixedSize(36, 36)
            ico.setAlignment(Qt.AlignCenter)
            ico.setStyleSheet(f"background:#120f1c;border:1px solid {color};border-radius:5px;")
            pm = _render_item_pixmap(self.win.item_icons, row['img'], int(row['frame']), 32)
            if pm:
                ico.setPixmap(pm)
            cl.addWidget(ico)

            col = QVBoxLayout()
            col.setSpacing(2)
            nm = QLabel(row['name'])
            nm.setStyleSheet(f'color:{color};font-size:12px;font-weight:bold;border:none;background:transparent;')
            nm.setWordWrap(True)
            sub = QLabel(f'{row["rar"]} · {row["cat"]} · {ident}')
            sub.setStyleSheet('color:#aaa08d;font-size:10px;border:none;background:transparent;')
            col.addWidget(nm)
            col.addWidget(sub)
            cl.addLayout(col, 1)

            btn = QPushButton('✓ 已选' if chosen else '+ 添加')
            btn.setCursor(Qt.PointingHandCursor)
            btn.setStyleSheet(
                ("QPushButton{background:#ffd700;color:#141008;border:none;border-radius:6px;padding:4px 8px;font-size:11px;font-weight:bold;}"
                 if chosen else
                 "QPushButton{background:rgba(212,175,55,0.20);color:#ffd700;border:1px solid rgba(212,175,55,0.55);border-radius:6px;padding:4px 8px;font-size:11px;font-weight:bold;}")
            )
            btn.clicked.connect(lambda checked=False, item_id=ident: self._toggle_id(item_id))
            cl.addWidget(btn)

            r, c = divmod(idx, 3)
            self.grid.addWidget(card, r, c)

        self.info_lbl.setText(f'共 {total:,} 件可入包战斗装备（已自动过滤时装与材料）')
        self.page_lbl.setText(f'第 {self._page + 1} / {total_pages} 页')
        self.prev_btn.setEnabled(self._page > 0)
        self.next_btn.setEnabled(self._page + 1 < total_pages)
        self.sel_lbl.setText(f'当前已选：{len(self._selected)} 件')


class RoomPickerDialog(QDialog):
    """Searchable room ID selector for map-scoped abyss drops."""

    def __init__(self, maps: dict[int, str], selected: list[int], title: str, parent=None):
        super().__init__(parent)
        self.setWindowTitle(title)
        self.resize(620, 480)
        self.setStyleSheet("QDialog{background:#120f1c;color:#f4eee2;}")
        self._selected = set(int(x) for x in selected)
        self._maps = sorted(maps.items())

        lay = QVBoxLayout(self)
        lay.setContentsMargins(14, 12, 14, 12)
        lay.setSpacing(8)
        self.filter_edit = QLineEdit()
        self.filter_edit.setPlaceholderText('筛选房间 ID 或路径（如 prey / wisdom / mirkwood / boss）…')
        self.filter_edit.textChanged.connect(self._populate)
        lay.addWidget(self.filter_edit)

        self.list = QListWidget()
        self.list.setSelectionMode(QListWidget.ExtendedSelection)
        self.list.itemChanged.connect(self._on_item_changed)
        lay.addWidget(self.list, 1)

        row = QHBoxLayout()
        self.count_lbl = QLabel()
        self.count_lbl.setStyleSheet('color:#ffd700;font-weight:bold;')
        clear_b = QPushButton('清空已选')
        clear_b.setObjectName('DarkBtn')
        clear_b.clicked.connect(self._clear)
        ok_b = QPushButton('确定')
        ok_b.setObjectName('GoldBtn')
        ok_b.clicked.connect(self.accept)
        row.addWidget(self.count_lbl)
        row.addStretch()
        row.addWidget(clear_b)
        row.addWidget(ok_b)
        lay.addLayout(row)
        self._populate()

    def _on_item_changed(self, item: QListWidgetItem):
        ident = int(item.data(Qt.UserRole))
        if item.checkState() == Qt.Checked:
            self._selected.add(ident)
        else:
            self._selected.discard(ident)
        self.count_lbl.setText(f'已选 {len(self._selected)} 个房间')

    def _clear(self):
        self._selected.clear()
        self._populate()

    def _populate(self):
        kw = self.filter_edit.text().strip().lower()
        with QSignalBlocker(self.list):
            self.list.clear()
            for ident, path in self._maps:
                if kw and kw not in str(ident) and kw not in path.lower():
                    continue
                it = QListWidgetItem(f'{ident:<10}  {path}')
                it.setData(Qt.UserRole, ident)
                it.setFlags(it.flags() | Qt.ItemIsUserCheckable)
                it.setCheckState(Qt.Checked if ident in self._selected else Qt.Unchecked)
                self.list.addItem(it)
        self.count_lbl.setText(f'已选 {len(self._selected)} 个房间')

    def selected_maps(self) -> list[int]:
        return sorted(self._selected)


class DropConfigPage(QWidget):
    """Unified visual Global Drop (by difficulty) + Abyss Drop (Normal & Lv.100) Console."""

    def __init__(self, win, parent=None):
        super().__init__(parent)
        self.win = win
        self.setObjectName('DropConfigPage')
        self.setStyleSheet('QWidget#DropConfigPage{background:transparent;}')

        self.policy, self._policy_from_disk = core.load_global_drop(core.DATA)
        self._current_diff = 0
        self._loading_ui = False

        root = QVBoxLayout(self)
        root.setContentsMargins(20, 16, 20, 14)
        root.setSpacing(10)

        # Top header bar
        hdr = QHBoxLayout()
        hdr.setSpacing(10)
        h_ico = QLabel()
        h_ico.setPixmap(ui_icons.render_pixmap('abyss', 26, active=True))
        h_ico.setStyleSheet('border:none;background:transparent;')
        hdr.addWidget(h_ico)

        title_col = QVBoxLayout()
        title_col.setSpacing(1)
        t_lbl = QLabel('全局掉落与深渊控制台')
        t_lbl.setObjectName('TitleLabel')
        s_lbl = QLabel('实时生效于本机网关 global_drop.json · 涵盖 5 大副本难度装备掉落、普通深渊与 100 级深渊（智慧的引导）')
        s_lbl.setObjectName('SubLabel')
        title_col.addWidget(t_lbl)
        title_col.addWidget(s_lbl)
        hdr.addLayout(title_col, 1)

        # Quick style presets + primary save button
        preset_combo = QComboBox()
        preset_combo.addItem('快速方案：原版均衡', 'balanced')
        preset_combo.addItem('快速方案：爽快高爆', 'high')
        preset_combo.addItem('快速方案：神话狂欢', 'mythic')
        preset_combo.setMinimumWidth(165)
        self.quick_preset_combo = preset_combo

        apply_preset_btn = QPushButton('套用方案')
        apply_preset_btn.setObjectName('DarkBtn')
        apply_preset_btn.clicked.connect(self._apply_quick_preset)

        self.save_global_btn = QPushButton(' 保存全局与深渊掉落到网关')
        self.save_global_btn.setObjectName('GoldBtn')
        self.save_global_btn.setIcon(ui_icons.vector_icon('abyss', 15, active=True, dark_fg=True))
        self.save_global_btn.setIconSize(QSize(15, 15))
        self.save_global_btn.clicked.connect(self.save_global_policy)

        hdr.addWidget(preset_combo)
        hdr.addWidget(apply_preset_btn)
        hdr.addWidget(self.save_global_btn)
        root.addLayout(hdr)

        # Main Tabs
        self.tabs = QTabWidget()
        self.tabs.addTab(self._build_global_diff_tab(), '全局掉落 · 分难度装备')
        self.tabs.addTab(self._build_abyss_tiers_tab(), '深渊掉落 · 普通深渊 / 100级深渊')
        self.tabs.addTab(self._build_room_rule_tab(), '房间定向掉落 · 兼容规则')
        root.addWidget(self.tabs, 1)

        self._load_policy_into_ui()

    # ---------------- Tab 1: 全局掉落（分难度装备掉落） ----------------
    def _build_global_diff_tab(self) -> QWidget:
        page = QWidget()
        page.setObjectName('GlobalDiffTab')
        page.setStyleSheet('QWidget#GlobalDiffTab{background:transparent;}')
        lay = QVBoxLayout(page)
        lay.setContentsMargins(14, 12, 14, 12)
        lay.setSpacing(10)

        # Master toggle + stats bar
        bar = QHBoxLayout()
        self.global_enable_chk = QCheckBox('启用全局分难度装备掉落（击杀副本战斗怪按当前难度生效）')
        self.global_enable_chk.setStyleSheet('font-size:13px;font-weight:bold;color:#ffe285;')
        self.global_enable_chk.toggled.connect(self._on_global_toggle)
        self.global_summary_lbl = QLabel('')
        self.global_summary_lbl.setStyleSheet('font-size:11px;color:#92d6c8;')
        bar.addWidget(self.global_enable_chk)
        bar.addStretch()
        bar.addWidget(self.global_summary_lbl)
        lay.addLayout(bar)

        # 5-Difficulty Visual Cards Strip
        cards_row = QHBoxLayout()
        cards_row.setSpacing(8)
        self.diff_cards: list[DifficultyCard] = []
        for idx in range(5):
            card = DifficultyCard(idx)
            card.clicked.connect(self._select_difficulty)
            cards_row.addWidget(card, 1)
            self.diff_cards.append(card)
        lay.addLayout(cards_row)

        # Detailed Visual Editor for the selected difficulty
        detail = QHBoxLayout()
        detail.setSpacing(12)

        # Left: Rate + Stacked Rarity Probability Visualizer + Sliders
        left_card = QFrame()
        left_card.setObjectName('PanelCard')
        ll = QVBoxLayout(left_card)
        ll.setContentsMargins(14, 12, 14, 12)
        ll.setSpacing(8)

        d_hdr = QHBoxLayout()
        self.diff_editor_title = QLabel('普通难度 · 装备掉落配置')
        self.diff_editor_title.setStyleSheet('font-size:15px;font-weight:bold;color:#ffe285;border:none;background:transparent;')
        self.diff_enable_chk = QCheckBox('启用本难度')
        self.diff_enable_chk.toggled.connect(self._on_diff_field_changed)
        self.diff_level_match_chk = QCheckBox('按怪物等级匹配 (Lv±15)')
        self.diff_level_match_chk.toggled.connect(self._on_diff_field_changed)
        d_hdr.addWidget(self.diff_editor_title)
        d_hdr.addStretch()
        d_hdr.addWidget(self.diff_enable_chk)
        d_hdr.addWidget(self.diff_level_match_chk)
        ll.addLayout(d_hdr)

        rate_grid = QGridLayout()
        rate_grid.setHorizontalSpacing(10)
        rate_grid.setVerticalSpacing(6)

        self.diff_chance_slider = QSlider(Qt.Horizontal)
        self.diff_chance_slider.setRange(0, 100)
        self.diff_chance_spin = QSpinBox()
        self.diff_chance_spin.setRange(0, 100)
        self.diff_chance_spin.setSuffix(' %')
        self.diff_chance_slider.valueChanged.connect(self.diff_chance_spin.setValue)
        self.diff_chance_spin.valueChanged.connect(self.diff_chance_slider.setValue)
        self.diff_chance_spin.valueChanged.connect(self._on_diff_field_changed)

        self.diff_mult_spin = QDoubleSpinBox()
        self.diff_mult_spin.setRange(0.1, 20.0)
        self.diff_mult_spin.setSingleStep(0.2)
        self.diff_mult_spin.setSuffix(' 倍')
        self.diff_mult_spin.valueChanged.connect(self._on_diff_field_changed)

        self.diff_count_spin = QSpinBox()
        self.diff_count_spin.setRange(1, 5)
        self.diff_count_spin.setSuffix(' 件')
        self.diff_count_spin.valueChanged.connect(self._on_diff_field_changed)

        self.diff_eff_badge = QLabel('综合概率 12%')
        self.diff_eff_badge.setStyleSheet(
            'color:#4ade80;font-weight:bold;font-size:12px;background:rgba(34,197,94,0.14);'
            'border:1px solid rgba(74,222,128,0.4);border-radius:7px;padding:3px 9px;'
        )

        rate_grid.addWidget(QLabel('装备基础掉率:'), 0, 0)
        rate_grid.addWidget(self.diff_chance_slider, 0, 1, 1, 2)
        rate_grid.addWidget(self.diff_chance_spin, 0, 3)

        rate_grid.addWidget(QLabel('难度爆率倍率:'), 1, 0)
        rate_grid.addWidget(self.diff_mult_spin, 1, 1)
        rate_grid.addWidget(QLabel('触发掉落件数:'), 1, 2)
        rate_grid.addWidget(self.diff_count_spin, 1, 3)
        ll.addLayout(rate_grid)

        bar_hdr = QHBoxLayout()
        bar_lbl = QLabel('品级掉落概率可视化分布')
        bar_lbl.setStyleSheet('font-size:12px;font-weight:bold;color:#e8d39c;border:none;background:transparent;')
        bar_hdr.addWidget(bar_lbl)
        bar_hdr.addStretch()
        bar_hdr.addWidget(self.diff_eff_badge)
        ll.addLayout(bar_hdr)

        self.diff_rarity_bar = RarityBarWidget(compact=False)
        ll.addWidget(self.diff_rarity_bar)

        rw_grid = QGridLayout()
        rw_grid.setHorizontalSpacing(10)
        rw_grid.setVerticalSpacing(5)
        self.diff_rw_sliders: dict[str, QSlider] = {}
        self.diff_rw_spins: dict[str, QSpinBox] = {}
        for idx, (rk, rname, rcol) in enumerate(RARITY_META):
            row_i, col_i = divmod(idx, 2)
            base_c = col_i * 3
            lbl = QLabel(f'● {rname}')
            lbl.setStyleSheet(f'color:{rcol};font-weight:bold;font-size:12px;border:none;background:transparent;')
            sl = QSlider(Qt.Horizontal)
            sl.setRange(0, 100)
            sp = QSpinBox()
            sp.setRange(0, 100)
            sp.setFixedWidth(58)
            sl.valueChanged.connect(sp.setValue)
            sp.valueChanged.connect(sl.setValue)
            sp.valueChanged.connect(self._on_diff_field_changed)
            rw_grid.addWidget(lbl, row_i, base_c)
            rw_grid.addWidget(sl, row_i, base_c + 1)
            rw_grid.addWidget(sp, row_i, base_c + 2)
            self.diff_rw_sliders[rk] = sl
            self.diff_rw_spins[rk] = sp
        ll.addLayout(rw_grid)
        ll.addStretch()

        # Right: Visual Equipment Pool for the selected difficulty
        self.diff_pool_widget = EquipmentPoolWidget(
            self.win,
            title='本难度专属额外装备池（可选）',
            empty_hint='未添加专属装备时，自动按左侧各品级权重从实时 PVF 装备库随机掉落。\n点击「+ 可视化选装备」可加入指定装备，与品级池混合掉落。',
            default_rarities=('史诗', '神器', '稀有'),
        )
        self.diff_pool_widget.changed.connect(self._on_diff_pool_changed)

        detail.addWidget(left_card, 12)
        detail.addWidget(self.diff_pool_widget, 10)
        lay.addLayout(detail, 1)
        return page

    # ---------------- Tab 2: 深渊掉落（普通深渊 & 100级深渊） ----------------
    def _build_abyss_tiers_tab(self) -> QWidget:
        page = QWidget()
        page.setObjectName('AbyssTiersTab')
        page.setStyleSheet('QWidget#AbyssTiersTab{background:transparent;}')
        lay = QHBoxLayout(page)
        lay.setContentsMargins(14, 12, 14, 12)
        lay.setSpacing(12)

        # Left Card: 普通深渊掉落
        normal_card = QFrame()
        normal_card.setObjectName('PanelCard')
        nl = QVBoxLayout(normal_card)
        nl.setContentsMargins(14, 12, 14, 12)
        nl.setSpacing(8)

        n_hdr = QHBoxLayout()
        n_tag = QLabel('NORMAL ABYSS')
        n_tag.setStyleSheet(
            'font-size:10px;font-weight:bold;color:#c084fc;background:rgba(168,85,247,0.16);'
            'border:1px solid rgba(192,132,252,0.45);border-radius:6px;padding:2px 7px;'
        )
        n_title = QLabel('普通深渊掉落')
        n_title.setStyleSheet('font-size:16px;font-weight:bold;color:#e9d5ff;border:none;background:transparent;')
        self.na_enable_chk = QCheckBox('启用')
        self.na_enable_chk.setStyleSheet('font-weight:bold;color:#c084fc;')
        self.na_enable_chk.toggled.connect(self._on_na_changed)
        n_hdr.addWidget(n_tag)
        n_hdr.addWidget(n_title)
        n_hdr.addStretch()
        n_hdr.addWidget(self.na_enable_chk)
        nl.addLayout(n_hdr)

        n_sub = QLabel('经典 Lv.1–95 深渊派对掉落 · 支持神器 / 传说 / 史诗品级权重与自定义池')
        n_sub.setStyleSheet('font-size:11px;color:#b8aec4;border:none;background:transparent;')
        nl.addWidget(n_sub)

        n_form = QGridLayout()
        n_form.setHorizontalSpacing(8)
        n_form.setVerticalSpacing(6)

        self.na_scope_combo = QComboBox()
        for val, lbl in NORMAL_ABYSS_SCOPES:
            self.na_scope_combo.addItem(lbl, val)
        self.na_scope_combo.currentIndexChanged.connect(self._on_na_changed)

        self.na_maps_btn = QPushButton('选择房间 (0)')
        self.na_maps_btn.setObjectName('DarkBtn')
        self.na_maps_btn.clicked.connect(self._pick_na_maps)

        self.na_chance_slider = QSlider(Qt.Horizontal)
        self.na_chance_slider.setRange(0, 100)
        self.na_chance_spin = QSpinBox()
        self.na_chance_spin.setRange(0, 100)
        self.na_chance_spin.setSuffix(' %')
        self.na_chance_slider.valueChanged.connect(self.na_chance_spin.setValue)
        self.na_chance_spin.valueChanged.connect(self.na_chance_slider.setValue)
        self.na_chance_spin.valueChanged.connect(self._on_na_changed)

        self.na_count_spin = QSpinBox()
        self.na_count_spin.setRange(1, 5)
        self.na_count_spin.setSuffix(' 件/次')
        self.na_count_spin.valueChanged.connect(self._on_na_changed)

        self.na_pool_combo = QComboBox()
        for val, lbl in NORMAL_ABYSS_POOLS:
            self.na_pool_combo.addItem(lbl, val)
        self.na_pool_combo.currentIndexChanged.connect(self._on_na_changed)

        n_form.addWidget(QLabel('生效范围:'), 0, 0)
        n_form.addWidget(self.na_scope_combo, 0, 1)
        n_form.addWidget(self.na_maps_btn, 0, 2)
        n_form.addWidget(QLabel('触发概率:'), 1, 0)
        n_form.addWidget(self.na_chance_slider, 1, 1)
        n_form.addWidget(self.na_chance_spin, 1, 2)
        n_form.addWidget(QLabel('掉落池:'), 2, 0)
        n_form.addWidget(self.na_pool_combo, 2, 1)
        n_form.addWidget(self.na_count_spin, 2, 2)
        nl.addLayout(n_form)

        self.na_rarity_bar = RarityBarWidget(compact=False)
        nl.addWidget(self.na_rarity_bar)

        na_rw_row = QGridLayout()
        na_rw_row.setHorizontalSpacing(8)
        na_rw_row.setVerticalSpacing(4)
        self.na_rw_spins: dict[str, QSpinBox] = {}
        self.na_rw_sliders: dict[str, QSlider] = {}
        for idx, (rk, rname, rcol) in enumerate([('3', '神器', '#f472b6'), ('6', '传说', '#fb923c'), ('4', '史诗', '#facc15')]):
            lbl = QLabel(f'● {rname}')
            lbl.setStyleSheet(f'color:{rcol};font-weight:bold;font-size:12px;border:none;background:transparent;')
            sl = QSlider(Qt.Horizontal)
            sl.setRange(0, 100)
            sp = QSpinBox()
            sp.setRange(0, 100)
            sp.setFixedWidth(58)
            sl.valueChanged.connect(sp.setValue)
            sp.valueChanged.connect(sl.setValue)
            sp.valueChanged.connect(self._on_na_changed)
            na_rw_row.addWidget(lbl, idx, 0)
            na_rw_row.addWidget(sl, idx, 1)
            na_rw_row.addWidget(sp, idx, 2)
            self.na_rw_sliders[rk] = sl
            self.na_rw_spins[rk] = sp
        nl.addLayout(na_rw_row)

        self.na_pool_widget = EquipmentPoolWidget(
            self.win,
            title='普通深渊可视化装备池',
            empty_hint='自动按上方权重掉落 Lv.1–95 神器/传说/史诗；也可添加心仪装备提升定向出货率。',
            default_rarities=('史诗', '传说', '神器'),
            lv100_bias=False,
        )
        self.na_pool_widget.changed.connect(self._on_na_changed)
        nl.addWidget(self.na_pool_widget, 1)

        # Right Card: 100级深渊掉落（智慧的引导）
        lv100_card = QFrame()
        lv100_card.setObjectName('Lv100AbyssCard')
        lv100_card.setStyleSheet(
            "QFrame#Lv100AbyssCard{background:qlineargradient(x1:0,y1:0,x2:1,y2:1,"
            "stop:0 rgba(36,24,48,0.86),stop:1 rgba(18,12,26,0.90));"
            "border:1px solid rgba(250,204,21,0.55);border-radius:12px;}"
        )
        ll = QVBoxLayout(lv100_card)
        ll.setContentsMargins(14, 12, 14, 12)
        ll.setSpacing(8)

        l_hdr = QHBoxLayout()
        l_tag = QLabel("WISDOM'S GUIDE · LV.100")
        l_tag.setStyleSheet(
            'font-size:10px;font-weight:bold;color:#fde047;background:rgba(250,204,21,0.16);'
            'border:1px solid rgba(250,204,21,0.55);border-radius:6px;padding:2px 7px;'
        )
        l_title = QLabel('100 级深渊掉落')
        l_title.setStyleSheet('font-size:16px;font-weight:bold;color:#ffe580;border:none;background:transparent;')
        self.la_enable_chk = QCheckBox('启用')
        self.la_enable_chk.setStyleSheet('font-weight:bold;color:#ffd700;')
        self.la_enable_chk.toggled.connect(self._on_la_changed)
        l_hdr.addWidget(l_tag)
        l_hdr.addWidget(l_title)
        l_hdr.addStretch()
        l_hdr.addWidget(self.la_enable_chk)
        ll.addLayout(l_hdr)

        l_sub = QLabel('智慧的引导专属掉落 · 实时关联 PVF 100 级全史诗 (5,356 件) 与神话 (111 件) 装备库')
        l_sub.setStyleSheet('font-size:11px;color:#dfd0af;border:none;background:transparent;')
        ll.addWidget(l_sub)

        l_form = QGridLayout()
        l_form.setHorizontalSpacing(8)
        l_form.setVerticalSpacing(6)

        self.la_scope_combo = QComboBox()
        for val, lbl in LV100_ABYSS_SCOPES:
            self.la_scope_combo.addItem(lbl, val)
        self.la_scope_combo.currentIndexChanged.connect(self._on_la_changed)

        self.la_maps_btn = QPushButton('选择房间 (0)')
        self.la_maps_btn.setObjectName('DarkBtn')
        self.la_maps_btn.clicked.connect(self._pick_la_maps)

        self.la_chance_slider = QSlider(Qt.Horizontal)
        self.la_chance_slider.setRange(0, 100)
        self.la_chance_spin = QSpinBox()
        self.la_chance_spin.setRange(0, 100)
        self.la_chance_spin.setSuffix(' %')
        self.la_chance_slider.valueChanged.connect(self.la_chance_spin.setValue)
        self.la_chance_spin.valueChanged.connect(self.la_chance_slider.setValue)
        self.la_chance_spin.valueChanged.connect(self._on_la_changed)

        self.la_count_spin = QSpinBox()
        self.la_count_spin.setRange(1, 5)
        self.la_count_spin.setSuffix(' 件/次')
        self.la_count_spin.valueChanged.connect(self._on_la_changed)

        self.la_pool_combo = QComboBox()
        for val, lbl in LV100_ABYSS_POOLS:
            self.la_pool_combo.addItem(lbl, val)
        self.la_pool_combo.currentIndexChanged.connect(self._on_la_changed)

        l_form.addWidget(QLabel('生效范围:'), 0, 0)
        l_form.addWidget(self.la_scope_combo, 0, 1)
        l_form.addWidget(self.la_maps_btn, 0, 2)
        l_form.addWidget(QLabel('触发概率:'), 1, 0)
        l_form.addWidget(self.la_chance_slider, 1, 1)
        l_form.addWidget(self.la_chance_spin, 1, 2)
        l_form.addWidget(QLabel('100级池:'), 2, 0)
        l_form.addWidget(self.la_pool_combo, 2, 1)
        l_form.addWidget(self.la_count_spin, 2, 2)
        ll.addLayout(l_form)

        self.la_rarity_bar = RarityBarWidget(compact=False)
        ll.addWidget(self.la_rarity_bar)

        la_rw_row = QGridLayout()
        la_rw_row.setHorizontalSpacing(8)
        la_rw_row.setVerticalSpacing(4)

        lbl_e = QLabel('● 100级史诗权重')
        lbl_e.setStyleSheet('color:#facc15;font-weight:bold;font-size:12px;border:none;background:transparent;')
        self.la_epic_slider = QSlider(Qt.Horizontal)
        self.la_epic_slider.setRange(0, 100)
        self.la_epic_spin = QSpinBox()
        self.la_epic_spin.setRange(0, 100)
        self.la_epic_spin.setFixedWidth(58)
        self.la_epic_slider.valueChanged.connect(self.la_epic_spin.setValue)
        self.la_epic_spin.valueChanged.connect(self.la_epic_slider.setValue)
        self.la_epic_spin.valueChanged.connect(self._on_la_changed)

        lbl_m = QLabel('● 100级神话权重')
        lbl_m.setStyleSheet('color:#fb7185;font-weight:bold;font-size:12px;border:none;background:transparent;')
        self.la_mythic_slider = QSlider(Qt.Horizontal)
        self.la_mythic_slider.setRange(0, 100)
        self.la_mythic_spin = QSpinBox()
        self.la_mythic_spin.setRange(0, 100)
        self.la_mythic_spin.setFixedWidth(58)
        self.la_mythic_slider.valueChanged.connect(self.la_mythic_spin.setValue)
        self.la_mythic_spin.valueChanged.connect(self.la_mythic_slider.setValue)
        self.la_mythic_spin.valueChanged.connect(self._on_la_changed)

        self.la_ratio_lbl = QLabel('闪光比：史诗 85% / 神话 15%')
        self.la_ratio_lbl.setStyleSheet('color:#fda4af;font-size:11px;font-weight:bold;border:none;background:transparent;')

        la_rw_row.addWidget(lbl_e, 0, 0)
        la_rw_row.addWidget(self.la_epic_slider, 0, 1)
        la_rw_row.addWidget(self.la_epic_spin, 0, 2)
        la_rw_row.addWidget(lbl_m, 1, 0)
        la_rw_row.addWidget(self.la_mythic_slider, 1, 1)
        la_rw_row.addWidget(self.la_mythic_spin, 1, 2)
        la_rw_row.addWidget(self.la_ratio_lbl, 2, 0, 1, 3)
        ll.addLayout(la_rw_row)

        self.la_pool_widget = EquipmentPoolWidget(
            self.win,
            title='100 级深渊可视化装备池（神话 / 史诗）',
            empty_hint='默认自动从实时 PVF 100 级史诗与神话库掉落；点击「+ 可视化选装备」或「推荐填充」可指定军神/大幽魂等热门神话！',
            default_rarities=('神话', '史诗'),
            lv100_bias=True,
        )
        self.la_pool_widget.changed.connect(self._on_la_changed)
        ll.addWidget(self.la_pool_widget, 1)

        lay.addWidget(normal_card, 1)
        lay.addWidget(lv100_card, 1)
        return page

    # ---------------- Tab 3: 指定房间定向掉落（保留兼容 abyss.json + 预设导出） ----------------
    def _build_room_rule_tab(self) -> QWidget:
        page = QWidget()
        page.setObjectName('RoomRuleTab')
        page.setStyleSheet('QWidget#RoomRuleTab{background:transparent;}')
        lay = QHBoxLayout(page)
        lay.setContentsMargins(14, 12, 14, 12)
        lay.setSpacing(12)

        # Left: Room-specific extra gear (abyss.json)
        left = QFrame()
        left.setObjectName('PanelCard')
        ll = QVBoxLayout(left)
        ll.setContentsMargins(14, 12, 14, 12)
        ll.setSpacing(8)

        lt = QLabel('指定房间定向掉落（兼容 abyss.json）')
        lt.setStyleSheet('font-size:15px;font-weight:bold;color:#ffe285;border:none;background:transparent;')
        ll.addWidget(lt)
        ll.addWidget(QLabel('对指定房间额外掉落一件指定装备，不替换普通掉落与全局掉落。支持 Ctrl/Shift 多选。'))

        room_search = QLineEdit()
        room_search.setPlaceholderText('过滤房间 ID 或地图路径…')
        ll.addWidget(room_search)

        self.win.rooms = QListWidget()
        self.win.rooms.setSelectionMode(QListWidget.ExtendedSelection)
        ll.addWidget(self.win.rooms, 1)

        self.win.saved = core.read(core.DATA / 'abyss.json') if (core.DATA / 'abyss.json').exists() else {}
        targets = self.win.saved.get('maps', [])
        for ident, name in sorted(self.win.maps.items()):
            item = QListWidgetItem(f'{ident}     {name}')
            item.setData(Qt.UserRole, ident)
            self.win.rooms.addItem(item)
            item.setSelected(ident in targets)

        def filter_rooms(text):
            kw = text.strip().lower()
            for i in range(self.win.rooms.count()):
                it = self.win.rooms.item(i)
                it.setHidden(bool(kw) and kw not in it.text().lower())

        room_search.textChanged.connect(filter_rooms)

        gear_row = QHBoxLayout()
        self.room_gear_icon = QLabel()
        self.room_gear_icon.setFixedSize(38, 38)
        self.room_gear_icon.setAlignment(Qt.AlignCenter)
        self.room_gear_icon.setStyleSheet('background:#120f1c;border:1px solid #d4af37;border-radius:6px;')

        self.win.gear = QComboBox()
        self.win.gear.setEditable(True)
        self.win.gear.setInsertPolicy(QComboBox.NoInsert)
        self.win.gear.setSizeAdjustPolicy(QComboBox.AdjustToMinimumContentsLengthWithIcon)
        self.win.gear.setMinimumContentsLength(20)
        for ident, row in self.win.allowed.items():
            if row[0] == '装备':
                self.win.gear.addItem(f'{ident}  /  {self.win.by_id[ident].name}', ident)
        self.win.gear.completer().setFilterMode(Qt.MatchContains)
        found = self.win.gear.findData(self.win.saved.get('template'))
        self.win.gear.setCurrentIndex(found)
        self.win.gear.lineEdit().setPlaceholderText('输入装备 ID 或名称')
        self.win.gear.currentIndexChanged.connect(self._update_room_gear_icon)
        gear_row.addWidget(self.room_gear_icon)
        gear_row.addWidget(self.win.gear, 1)

        form = QFormLayout()
        self.win.chance = QSpinBox()
        self.win.chance.setRange(0, 100)
        self.win.chance.setSuffix(' %')
        self.win.chance.setValue(self.win.saved.get('percent', 0))
        form.addRow('额外掉落装备', gear_row)
        form.addRow('每只战斗怪的额外概率', self.win.chance)
        ll.addLayout(form)

        act_row = QHBoxLayout()
        save_room_btn = QPushButton('保存房间规则')
        save_room_btn.setObjectName('GoldBtn')
        save_room_btn.clicked.connect(lambda: self.win.save_live(False))
        off_room_btn = QPushButton('关闭房间加成')
        off_room_btn.setObjectName('DarkBtn')
        off_room_btn.clicked.connect(lambda: self.win.save_live(True))
        act_row.addWidget(save_room_btn)
        act_row.addWidget(off_room_btn)
        act_row.addStretch()
        ll.addLayout(act_row)
        self._update_room_gear_icon()

        # Right: JSON preset export & live policy JSON preview (keeps full compatibility)
        right = QFrame()
        right.setObjectName('PanelCard')
        rl = QVBoxLayout(right)
        rl.setContentsMargins(14, 12, 14, 12)
        rl.setSpacing(8)

        rt = QLabel('JSON 配置预览与快照导出')
        rt.setStyleSheet('font-size:15px;font-weight:bold;color:#ffe285;border:none;background:transparent;')
        rl.addWidget(rt)

        # Compatibility controls bound to win so existing JSON preset export API keeps working
        self.win.abyss_enable_chk = QCheckBox('快照中启用全局深渊')
        self.win.abyss_enable_chk.setChecked(False)
        self.win.drop_rate_spin = QDoubleSpinBox()
        self.win.drop_rate_spin.setRange(0.1, 50.0)
        self.win.drop_rate_spin.setSingleStep(0.5)
        self.win.drop_rate_spin.setValue(3.0)
        self.win.drop_rate_spin.setSuffix(' 倍')
        self.win.mythic_slider = QSlider(Qt.Horizontal)
        self.win.mythic_slider.setRange(1, 50)
        self.win.mythic_slider.setValue(12)
        self.win.epic_slider = QSlider(Qt.Horizontal)
        self.win.epic_slider.setRange(1, 100)
        self.win.epic_slider.setValue(45)
        self.win.pool_mode_combo = QComboBox()
        self.win.pool_mode_combo.addItems(['100级全史诗+神话全局池', '仅当前职业适配装备池', '自定义指定物品ID池'])
        self.win.custom_ids_edit = QLineEdit()
        self.win.custom_ids_edit.setPlaceholderText('可选：导出快照用装备 ID，逗号分隔')

        pform = QFormLayout()
        pform.addRow(self.win.abyss_enable_chk)
        pform.addRow('快照爆率倍率', self.win.drop_rate_spin)
        pform.addRow('快照神话权重', self.win.mythic_slider)
        pform.addRow('快照史诗权重', self.win.epic_slider)
        pform.addRow('快照池模式', self.win.pool_mode_combo)
        pform.addRow('快照物品ID', self.win.custom_ids_edit)
        rl.addLayout(pform)

        exp_btn = QPushButton('导出兼容快照 JSON')
        exp_btn.setObjectName('DarkBtn')
        exp_btn.clicked.connect(lambda: self.win.save_preset())
        rl.addWidget(exp_btn, 0, Qt.AlignLeft)

        self.win.config_preview = QTextEdit()
        self.win.config_preview.setReadOnly(True)
        self.win.config_preview.setStyleSheet(
            'background-color:#100d18;border:1px solid rgba(212,175,55,0.28);border-radius:8px;font-family:Consolas,monospace;font-size:11px;'
        )
        rl.addWidget(self.win.config_preview, 1)
        self.win.restore_preset()

        lay.addWidget(left, 13)
        lay.addWidget(right, 9)
        return page

    # ---------------- Data Sync & Events ----------------
    def _update_room_gear_icon(self):
        idx = self.win.gear.currentIndex()
        ident = self.win.gear.itemData(idx) if idx >= 0 else None
        if ident and ident in self.win.by_id and self.win.by_id[ident].icon:
            ref = self.win.by_id[ident].icon
            pm = _render_item_pixmap(self.win.item_icons, ref.img, ref.frame, 34)
            if pm:
                self.room_gear_icon.setPixmap(pm)
                return
        self.room_gear_icon.setText('✦')

    def _load_policy_into_ui(self):
        self._loading_ui = True
        try:
            g = self.policy['global']
            self.global_enable_chk.setChecked(bool(g.get('enabled', True)))
            for idx, card in enumerate(self.diff_cards):
                card.refresh(g['difficulties'][idx])
                card.set_selected(idx == self._current_diff)
            self._load_diff_into_editor(self._current_diff)

            na = self.policy['normal_abyss']
            self.na_enable_chk.setChecked(bool(na.get('enabled', True)))
            self._set_combo_data(self.na_scope_combo, na.get('scope', 'all'))
            self.na_maps_btn.setText(f'选择房间 ({len(na.get("maps") or [])})')
            self.na_maps_btn.setEnabled(na.get('scope') == 'maps')
            self.na_chance_spin.setValue(int(na.get('drop_chance', 25)))
            self.na_count_spin.setValue(int(na.get('drop_count', 1)))
            self._set_combo_data(self.na_pool_combo, na.get('pool_mode', 'level_epic'))
            rw = na.get('rarity_weights') or {}
            for rk in ('3', '6', '4'):
                self.na_rw_spins[rk].setValue(int(rw.get(rk, 30)))
            self.na_pool_widget.set_item_ids(na.get('custom_item_ids') or [], emit=False)
            self._refresh_na_visuals()

            la = self.policy['lv100_abyss']
            self.la_enable_chk.setChecked(bool(la.get('enabled', True)))
            self._set_combo_data(self.la_scope_combo, la.get('scope', 'all'))
            self.la_maps_btn.setText(f'选择房间 ({len(la.get("maps") or [])})')
            self.la_maps_btn.setEnabled(la.get('scope') in ('maps', 'lv100_dungeons'))
            self.la_chance_spin.setValue(int(la.get('drop_chance', 35)))
            self.la_count_spin.setValue(int(la.get('drop_count', 2)))
            self._set_combo_data(self.la_pool_combo, la.get('pool_mode', 'lv100_all'))
            self.la_epic_spin.setValue(int(la.get('epic_weight', 85)))
            self.la_mythic_spin.setValue(int(la.get('mythic_weight', 15)))
            self.la_pool_widget.set_item_ids(la.get('custom_item_ids') or [], emit=False)
            self._refresh_la_visuals()
            self._refresh_global_summary()
        finally:
            self._loading_ui = False

    def _set_combo_data(self, combo: QComboBox, value: str):
        idx = combo.findData(value)
        if idx >= 0:
            combo.setCurrentIndex(idx)

    def _load_diff_into_editor(self, diff_idx: int):
        prev = self._loading_ui
        self._loading_ui = True
        try:
            self._current_diff = diff_idx
            cfg = self.policy['global']['difficulties'][diff_idx]
            zh, _en, accent = DIFF_ACCENTS[diff_idx]
            self.diff_editor_title.setText(f'{zh}难度 · 装备掉落配置')
            self.diff_editor_title.setStyleSheet(
                f'font-size:15px;font-weight:bold;color:{accent};border:none;background:transparent;'
            )
            self.diff_enable_chk.setChecked(bool(cfg.get('enabled', True)))
            self.diff_level_match_chk.setChecked(bool(cfg.get('level_match', True)))
            self.diff_chance_spin.setValue(int(cfg.get('equip_drop_chance', 12)))
            self.diff_mult_spin.setValue(float(cfg.get('drop_rate_multiplier', 1.0)))
            self.diff_count_spin.setValue(int(cfg.get('drop_count', 1)))
            rw = cfg.get('rarity_weights') or {}
            for rk, _n, _c in RARITY_META:
                self.diff_rw_spins[rk].setValue(int(rw.get(rk, 0)))
            self.diff_pool_widget.set_item_ids(cfg.get('custom_item_ids') or [], emit=False)
            for idx, card in enumerate(self.diff_cards):
                card.set_selected(idx == diff_idx)
            self._refresh_diff_visuals()
        finally:
            self._loading_ui = prev

    def _select_difficulty(self, diff_idx: int):
        self._load_diff_into_editor(diff_idx)

    def _on_global_toggle(self):
        if self._loading_ui:
            return
        self.policy['global']['enabled'] = self.global_enable_chk.isChecked()
        self._refresh_global_summary()

    def _on_diff_field_changed(self):
        if self._loading_ui:
            return
        cfg = self.policy['global']['difficulties'][self._current_diff]
        cfg['enabled'] = self.diff_enable_chk.isChecked()
        cfg['level_match'] = self.diff_level_match_chk.isChecked()
        cfg['equip_drop_chance'] = self.diff_chance_spin.value()
        cfg['drop_rate_multiplier'] = round(self.diff_mult_spin.value(), 2)
        cfg['drop_count'] = self.diff_count_spin.value()
        cfg['rarity_weights'] = {rk: self.diff_rw_spins[rk].value() for rk, _n, _c in RARITY_META}
        self._refresh_diff_visuals()
        self.diff_cards[self._current_diff].refresh(cfg)
        self._refresh_global_summary()

    def _on_diff_pool_changed(self):
        if self._loading_ui:
            return
        cfg = self.policy['global']['difficulties'][self._current_diff]
        cfg['custom_item_ids'] = self.diff_pool_widget.item_ids()
        self.diff_cards[self._current_diff].refresh(cfg)
        self._refresh_global_summary()

    def _refresh_diff_visuals(self):
        cfg = self.policy['global']['difficulties'][self._current_diff]
        eff = min(100, int(round(cfg['equip_drop_chance'] * cfg['drop_rate_multiplier'])))
        self.diff_eff_badge.setText(f'综合触发概率 {eff}% · 每次 {cfg["drop_count"]} 件')
        rw = cfg['rarity_weights']
        self.diff_rarity_bar.set_segments([(n, c, rw.get(k, 0)) for k, n, c in RARITY_META])

    def _refresh_global_summary(self):
        active_n = sum(1 for d in self.policy['global']['difficulties'] if d.get('enabled'))
        status = '已启用' if self.policy['global']['enabled'] else '已暂停'
        saved_hint = '已同步网关' if self._policy_from_disk else '默认推荐配置（点击保存生效）'
        self.global_summary_lbl.setText(f'全局掉落：{status}（{active_n}/5 难度开启） · {saved_hint}')

    def _on_na_changed(self):
        if self._loading_ui:
            return
        na = self.policy['normal_abyss']
        na['enabled'] = self.na_enable_chk.isChecked()
        na['scope'] = self.na_scope_combo.currentData() or 'all'
        na['drop_chance'] = self.na_chance_spin.value()
        na['drop_count'] = self.na_count_spin.value()
        na['pool_mode'] = self.na_pool_combo.currentData() or 'level_epic'
        na['rarity_weights'] = {rk: self.na_rw_spins[rk].value() for rk in ('3', '6', '4')}
        na['custom_item_ids'] = self.na_pool_widget.item_ids()
        self.na_maps_btn.setEnabled(na['scope'] == 'maps')
        self._refresh_na_visuals()

    def _refresh_na_visuals(self):
        rw = self.policy['normal_abyss']['rarity_weights']
        self.na_rarity_bar.set_segments([
            ('神器', '#f472b6', rw.get('3', 0)),
            ('传说', '#fb923c', rw.get('6', 0)),
            ('史诗', '#facc15', rw.get('4', 0)),
        ])

    def _pick_na_maps(self):
        na = self.policy['normal_abyss']
        dlg = RoomPickerDialog(self.win.maps, na.get('maps') or [], '选择普通深渊生效房间', self)
        if dlg.exec() == QDialog.Accepted:
            na['maps'] = dlg.selected_maps()
            self.na_maps_btn.setText(f'选择房间 ({len(na["maps"])})')

    def _on_la_changed(self):
        if self._loading_ui:
            return
        la = self.policy['lv100_abyss']
        la['enabled'] = self.la_enable_chk.isChecked()
        la['scope'] = self.la_scope_combo.currentData() or 'all'
        la['drop_chance'] = self.la_chance_spin.value()
        la['drop_count'] = self.la_count_spin.value()
        la['pool_mode'] = self.la_pool_combo.currentData() or 'lv100_all'
        la['epic_weight'] = self.la_epic_spin.value()
        la['mythic_weight'] = self.la_mythic_spin.value()
        la['custom_item_ids'] = self.la_pool_widget.item_ids()
        self.la_maps_btn.setEnabled(la['scope'] in ('maps', 'lv100_dungeons'))
        self._refresh_la_visuals()

    def _refresh_la_visuals(self):
        la = self.policy['lv100_abyss']
        ew = int(la.get('epic_weight', 85))
        mw = int(la.get('mythic_weight', 15))
        mode = la.get('pool_mode', 'lv100_all')
        if mode == 'lv100_mythic':
            segs = [('100级神话', '#fb7185', 100)]
            self.la_ratio_lbl.setText('闪光比：仅 100 级神话 (100%)')
        elif mode == 'lv100_epic':
            segs = [('100级史诗', '#facc15', 100)]
            self.la_ratio_lbl.setText('闪光比：仅 100 级史诗 (100%)')
        else:
            segs = [('100级史诗', '#facc15', ew), ('100级神话', '#fb7185', mw)]
            tot = max(1, ew + mw)
            self.la_ratio_lbl.setText(f'闪光比：100 级史诗 {ew * 100.0 / tot:.1f}% · 100 级神话 {mw * 100.0 / tot:.1f}%')
        self.la_rarity_bar.set_segments(segs)

    def _pick_la_maps(self):
        la = self.policy['lv100_abyss']
        dlg = RoomPickerDialog(self.win.maps, la.get('maps') or [], '选择 100 级深渊生效房间', self)
        if dlg.exec() == QDialog.Accepted:
            la['maps'] = dlg.selected_maps()
            self.la_maps_btn.setText(f'选择房间 ({len(la["maps"])})')

    def _apply_quick_preset(self):
        key = self.quick_preset_combo.currentData()
        p = core.default_global_drop()
        if key == 'high':
            for d in p['global']['difficulties']:
                d['equip_drop_chance'] = min(100, d['equip_drop_chance'] + 25)
                d['drop_rate_multiplier'] = round(d['drop_rate_multiplier'] * 1.5, 1)
                d['drop_count'] = min(5, d['drop_count'] + 1)
            p['normal_abyss']['drop_chance'] = 50
            p['normal_abyss']['drop_count'] = 2
            p['lv100_abyss']['drop_chance'] = 65
            p['lv100_abyss']['drop_count'] = 2
            p['lv100_abyss']['epic_weight'] = 75
            p['lv100_abyss']['mythic_weight'] = 25
        elif key == 'mythic':
            for d in p['global']['difficulties']:
                d['equip_drop_chance'] = 80
                d['drop_rate_multiplier'] = 2.0
                d['drop_count'] = 3
                d['rarity_weights'] = {'0': 0, '1': 0, '2': 5, '3': 10, '6': 15, '4': 45, '7': 25}
            p['normal_abyss']['drop_chance'] = 80
            p['normal_abyss']['drop_count'] = 3
            p['normal_abyss']['rarity_weights'] = {'3': 5, '6': 15, '4': 80}
            p['lv100_abyss']['drop_chance'] = 100
            p['lv100_abyss']['drop_count'] = 3
            p['lv100_abyss']['epic_weight'] = 55
            p['lv100_abyss']['mythic_weight'] = 45
        self.policy = p
        self._load_policy_into_ui()
        self.win.statusBar().showMessage(f'已套用「{self.quick_preset_combo.currentText()}」，点击「保存全局与深渊掉落到网关」立即生效。')

    def save_global_policy(self):
        path = core.save_global_drop(copy.deepcopy(self.policy), core.DATA)
        self._policy_from_disk = True
        self._refresh_global_summary()
        self.win.statusBar().showMessage(f'已保存全局与深渊掉落配置到网关：{path.name}（新开副本首杀自动热加载）')
        QMessageBox.information(
            self,
            '全局掉落配置已生效',
            f'已写入本机网关：{path}\n\n'
            '• 全局掉落（普通/冒险/勇士/王者/噩梦 5 难度装备掉落）\n'
            '• 普通深渊掉落 & 100 级深渊（智慧的引导）掉落\n\n'
            '无需重启网关，进入新副本击杀怪物即自动读取最新规则。',
        )

    def sync_sources(self):
        self.diff_pool_widget.rebuild()
        self.na_pool_widget.rebuild()
        self.la_pool_widget.rebuild()
        self._update_room_gear_icon()
