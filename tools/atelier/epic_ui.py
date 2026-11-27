"""PySide6 visual layout adapted from the supplied 100US launcher."""
import math
import struct
import zlib
from dataclasses import dataclass, field
from typing import List, Optional

from PySide6.QtCore import QPointF, QRect, QSize, Qt
from PySide6.QtGui import (
    QBrush,
    QColor,
    QImage,
    QLinearGradient,
    QPainter,
    QPen,
    QPixmap,
    QRadialGradient,
)
from PySide6.QtWidgets import (
    QComboBox,
    QDoubleSpinBox,
    QFrame,
    QGraphicsDropShadowEffect,
    QGridLayout,
    QHBoxLayout,
    QLabel,
    QLineEdit,
    QListWidget,
    QMainWindow,
    QPushButton,
    QScrollArea,
    QSlider,
    QSpinBox,
    QStackedWidget,
    QTextEdit,
    QVBoxLayout,
    QWidget,
    QCheckBox,
)

RARITY_COLORS = {
    "神话": "#ff8c00",
    "史诗": "#ffd700",
    "传说": "#ff7847",
    "稀有": "#b366ff",
}

from epic_data import JOBS
from item_icons import IconRef
from source_library import display_cat
import theme_art
import ui_icons


@dataclass
class GameItem:
    item_id: int
    name: str
    rarity: str          # 神话 / 史诗 / 传说 / 稀有
    category: str        # 武器 / 上衣 / 下装 / 头肩 / 腰带 / 鞋子 / 手镯 / 项链 / 戒指 / 辅助装备 / 魔法石 / 耳环 / 消耗品
    jobs: List[str]      # ["全职业"] 或指定职业列表
    armor_type: Optional[str] = None  # 布甲 / 皮甲 / 轻甲 / 重甲 / 板甲 / None
    icon: Optional[IconRef] = None
    desc: str = ""
    search_key: str = field(default="", repr=False)
    grantmode: int = 1


def pix(im):
    im = im.convert('RGBA')
    image = QImage(im.tobytes(), im.width, im.height, im.width * 4, QImage.Format_RGBA8888)
    return QPixmap.fromImage(image.copy())


class ScenicRootWidget(QWidget):
    """Full-window Lv.100 scenic background with dark-gold vignette and subtle celestial filigree."""
    def __init__(self, parent=None):
        super().__init__(parent)
        self.setObjectName("RootWidget")
        self._raw_bg = None
        self._cached_size = None
        self._cached_pm = None

    def set_background_image(self, pil_image):
        self._raw_bg = pil_image
        self._cached_size = None
        self._cached_pm = None
        self.update()

    def paintEvent(self, event):
        p = QPainter(self)
        p.setRenderHint(QPainter.Antialiasing)
        w, h = self.width(), self.height()
        # Base cosmic obsidian gradient
        base = QLinearGradient(0, 0, w, h)
        base.setColorAt(0.0, QColor(9, 8, 16))
        base.setColorAt(0.55, QColor(15, 12, 26))
        base.setColorAt(1.0, QColor(10, 8, 15))
        p.fillRect(self.rect(), base)

        if self._raw_bg is not None and w > 0 and h > 0:
            if self._cached_size != (w, h) or self._cached_pm is None:
                self._cached_pm = pix(theme_art.make_scenic_backdrop(self._raw_bg, w, h))
                self._cached_size = (w, h)
            p.drawPixmap(0, 0, self._cached_pm)

            # Cinematic dark-gold vignette overlay for luxurious readability
            vig = QLinearGradient(0, 0, 0, h)
            vig.setColorAt(0.0, QColor(10, 9, 18, 125))
            vig.setColorAt(0.35, QColor(12, 10, 22, 95))
            vig.setColorAt(1.0, QColor(8, 7, 15, 190))
            p.fillRect(self.rect(), vig)

        # Warm golden top-right ambient aura bloom
        glow = QRadialGradient(QPointF(w * 0.78, h * 0.14), max(w, h) * 0.48)
        glow.setColorAt(0.0, QColor(255, 206, 92, 34))
        glow.setColorAt(0.45, QColor(186, 125, 36, 14))
        glow.setColorAt(1.0, QColor(0, 0, 0, 0))
        p.fillRect(self.rect(), glow)

        # Subtle gold top hairline shimmer
        line_grad = QLinearGradient(0, 0, w, 0)
        line_grad.setColorAt(0.0, QColor(212, 175, 55, 20))
        line_grad.setColorAt(0.5, QColor(255, 224, 130, 140))
        line_grad.setColorAt(1.0, QColor(212, 175, 55, 20))
        p.setPen(QPen(QBrush(line_grad), 1.5))
        p.drawLine(0, 1, w, 1)


class HeroBannerFrame(QFrame):
    """Luxury AAA Lv.100 Hero Showcase card with scenic backdrop and Neo Awakening illustration."""
    def __init__(self, parent=None):
        super().__init__(parent)
        self.setObjectName("HeroBanner")
        self._bg_raw = None
        self._hero_raw = None
        self._cached_key = None
        self._cached_bg = None
        self._cached_hero = None

    def set_art(self, bg_im, hero_im):
        self._bg_raw = bg_im
        self._hero_raw = hero_im
        self._cached_key = None
        self.update()

    def paintEvent(self, event):
        super().paintEvent(event)
        p = QPainter(self)
        p.setRenderHint(QPainter.Antialiasing)
        p.setRenderHint(QPainter.SmoothPixmapTransform)
        r = self.rect().adjusted(1, 1, -1, -1)
        w, h = r.width(), r.height()
        if w <= 2 or h <= 2:
            return
        from PySide6.QtGui import QPainterPath
        clip = QPainterPath()
        clip.addRoundedRect(r.x(), r.y(), w, h, 14, 14)
        p.setClipPath(clip)

        # Paint crisp Lv.100 scenic art inside banner
        if self._bg_raw is not None:
            if self._cached_key != (w, h):
                iw, ih = self._bg_raw.size
                sc = max(w / iw, h / ih)
                nw, nh = max(w, int(round(iw * sc))), max(h, int(round(ih * sc)))
                scaled = self._bg_raw.resize((nw, nh), theme_art.Image.Resampling.LANCZOS)
                x0 = (nw - w) // 2
                y0 = max(0, int((nh - h) * 0.22))
                self._cached_bg = pix(scaled.crop((x0, y0, x0 + w, y0 + h)))
                if self._hero_raw is not None:
                    hh = max(140, int(h * 1.16))
                    hw = max(140, int(self._hero_raw.width * (hh / self._hero_raw.height)))
                    self._cached_hero = pix(self._hero_raw.resize((hw, hh), theme_art.Image.Resampling.LANCZOS))
                else:
                    self._cached_hero = None
                self._cached_key = (w, h)
            p.drawPixmap(r.topLeft(), self._cached_bg)

        # Left-to-right royal obsidian-gold scrim so controls on left are razor-sharp
        scrim = QLinearGradient(r.left(), r.top(), r.right(), r.top())
        scrim.setColorAt(0.0, QColor(12, 10, 20, 232))
        scrim.setColorAt(0.52, QColor(16, 13, 28, 198))
        scrim.setColorAt(0.78, QColor(18, 14, 30, 118))
        scrim.setColorAt(1.0, QColor(12, 10, 20, 155))
        p.fillRect(r, scrim)

        # Golden rim light halo behind the character illustration on the right
        halo = QRadialGradient(QPointF(r.right() - w * 0.16, r.center().y()), h * 0.72)
        halo.setColorAt(0.0, QColor(255, 215, 105, 88))
        halo.setColorAt(0.55, QColor(210, 145, 42, 30))
        halo.setColorAt(1.0, QColor(0, 0, 0, 0))
        p.fillRect(r, halo)

        # Draw Lv.100 high-res character illustration on the right
        if self._cached_hero is not None:
            hx = r.right() - self._cached_hero.width() + 26
            hy = r.top() - 8
            p.drawPixmap(hx, hy, self._cached_hero)

        # Subtle bottom gradient fade and royal gold frame accents
        bot = QLinearGradient(0, r.bottom() - 48, 0, r.bottom())
        bot.setColorAt(0.0, QColor(12, 10, 20, 0))
        bot.setColorAt(1.0, QColor(12, 10, 20, 165))
        p.fillRect(QRect(r.left(), r.bottom() - 48, w, 48), bot)

        p.setClipping(False)
        frame_grad = QLinearGradient(r.left(), r.top(), r.right(), r.bottom())
        frame_grad.setColorAt(0.0, QColor(255, 224, 130, 195))
        frame_grad.setColorAt(0.5, QColor(212, 175, 55, 115))
        frame_grad.setColorAt(1.0, QColor(255, 210, 95, 180))
        p.setPen(QPen(QBrush(frame_grad), 1.6))
        p.drawRoundedRect(r, 14, 14)


STYLESHEET = """
QMainWindow {
    background-color: #090810;
    color: #f6f0e4;
    font-family: "Microsoft YaHei UI", "Microsoft YaHei", "Segoe UI", sans-serif;
}
QWidget#RootWidget {
    background-color: transparent;
    color: #f6f0e4;
}
QFrame#PanelCard {
    background-color: rgba(15, 13, 24, 0.78);
    border: 1px solid rgba(228, 188, 86, 0.36);
    border-radius: 13px;
}
QFrame#PanelCard:hover {
    border: 1px solid rgba(255, 215, 100, 0.56);
}
QFrame#HeroBanner {
    background-color: rgba(16, 13, 26, 0.84);
    border: none;
    border-radius: 14px;
}
QFrame#FeatureTile {
    background-color: rgba(16, 14, 27, 0.76);
    border: 1px solid rgba(220, 178, 76, 0.34);
    border-radius: 12px;
}
QFrame#FeatureTile:hover {
    background-color: rgba(30, 24, 46, 0.88);
    border: 1px solid rgba(255, 216, 102, 0.78);
}
QFrame#Sidebar {
    background-color: rgba(10, 9, 17, 0.86);
    border-right: 1px solid rgba(228, 188, 86, 0.36);
}
QPushButton#NavBtn {
    background-color: transparent;
    color: #bfb5a2;
    border: 1px solid transparent;
    border-radius: 10px;
    padding: 10px 14px;
    text-align: left;
    font-size: 13px;
    font-weight: bold;
}
QPushButton#NavBtn:hover {
    background-color: rgba(235, 195, 92, 0.12);
    border: 1px solid rgba(235, 195, 92, 0.30);
    color: #ffe28a;
}
QPushButton#NavBtn[active="true"] {
    background-color: qlineargradient(x1:0, y1:0, x2:1, y2:0,
        stop:0 rgba(255, 215, 0, 0.92), stop:0.022 rgba(255, 215, 0, 0.92),
        stop:0.024 rgba(235, 188, 72, 0.28), stop:0.65 rgba(155, 42, 42, 0.22),
        stop:1 rgba(28, 20, 38, 0.28));
    border: 1px solid rgba(245, 204, 96, 0.85);
    color: #ffe89e;
}
QPushButton#GoldBtn {
    background-color: qlineargradient(x1:0, y1:0, x2:0, y2:1,
        stop:0 #ffe587, stop:0.42 #f0bc3c, stop:1 #b67d16);
    color: #181105;
    border: 1px solid #fff0b3;
    border-radius: 9px;
    padding: 9px 18px;
    font-size: 13px;
    font-weight: bold;
}
QPushButton#GoldBtn:hover {
    background-color: qlineargradient(x1:0, y1:0, x2:0, y2:1,
        stop:0 #fff0ad, stop:0.45 #ffd24d, stop:1 #cf921c);
    border: 1px solid #ffffff;
}
QPushButton#GoldBtn:pressed {
    background-color: qlineargradient(x1:0, y1:0, x2:0, y2:1,
        stop:0 #c8911d, stop:1 #9b6b0e);
}
QPushButton#GoldBtn:disabled {
    background-color: rgba(58, 48, 30, 0.55);
    color: #8c7f66;
    border: 1px solid rgba(180, 150, 82, 0.25);
}
QPushButton#HeroLaunchBtn {
    background-color: qlineargradient(x1:0, y1:0, x2:1, y2:1,
        stop:0 #fff0a8, stop:0.32 #ffd24c, stop:0.68 #e39e1f, stop:1 #b57410);
    color: #191003;
    border: 1px solid #fff7cf;
    border-radius: 10px;
    padding: 12px 28px;
    font-size: 15px;
    font-weight: bold;
    letter-spacing: 1px;
}
QPushButton#HeroLaunchBtn:hover {
    background-color: qlineargradient(x1:0, y1:0, x2:1, y2:1,
        stop:0 #fff8cc, stop:0.4 #ffe169, stop:1 #d99518);
    border: 1px solid #ffffff;
}
QPushButton#DarkBtn {
    background-color: rgba(25, 21, 38, 0.84);
    color: #f3ecde;
    border: 1px solid rgba(224, 184, 82, 0.42);
    border-radius: 8px;
    padding: 7px 14px;
    font-size: 12px;
    font-weight: bold;
}
QPushButton#DarkBtn:hover {
    background-color: rgba(45, 36, 62, 0.92);
    border-color: #ffd700;
    color: #ffe28a;
}
QPushButton#DarkBtn:disabled {
    background-color: rgba(24, 20, 34, 0.45);
    color: #6d6575;
    border: 1px solid rgba(160, 145, 170, 0.20);
}
QLineEdit, QComboBox, QSpinBox, QDoubleSpinBox {
    background-color: rgba(14, 12, 23, 0.86);
    color: #f5efe3;
    border: 1px solid rgba(220, 180, 82, 0.36);
    border-radius: 7px;
    padding: 6px 10px;
    font-size: 12px;
}
QLineEdit:focus, QComboBox:focus, QSpinBox:focus, QDoubleSpinBox:focus {
    border: 1px solid #ffd700;
    background-color: rgba(20, 17, 32, 0.95);
}
QComboBox::drop-down {
    border: none;
    width: 22px;
}
QLabel#TitleLabel {
    font-size: 21px;
    font-weight: bold;
    color: #ffe07d;
}
QLabel#SubLabel {
    font-size: 12px;
    color: #bdb39e;
}
QLabel#EyebrowLabel {
    font-size: 11px;
    font-weight: bold;
    color: #e6be63;
    letter-spacing: 2px;
    background: transparent;
    border: none;
}
QScrollBar:vertical {
    background: rgba(12, 10, 20, 0.55);
    width: 8px;
    margin: 2px;
    border-radius: 4px;
}
QScrollBar::handle:vertical {
    background: rgba(212, 175, 55, 0.45);
    min-height: 28px;
    border-radius: 4px;
}
QScrollBar::handle:vertical:hover {
    background: rgba(255, 215, 0, 0.78);
}
QScrollBar::add-line:vertical, QScrollBar::sub-line:vertical {
    height: 0px;
}
QScrollBar:horizontal {
    background: rgba(12, 10, 20, 0.55);
    height: 8px;
    margin: 2px;
    border-radius: 4px;
}
QScrollBar::handle:horizontal {
    background: rgba(212, 175, 55, 0.45);
    min-width: 28px;
    border-radius: 4px;
}
QScrollBar::handle:horizontal:hover {
    background: rgba(255, 215, 0, 0.78);
}
QScrollBar::add-line:horizontal, QScrollBar::sub-line:horizontal {
    width: 0px;
}
"""


class ItemCardWidget(QFrame):
    def __init__(self, item: GameItem, on_add_clicked, icons, parent=None):
        super().__init__(parent)
        self.item = item
        self.on_add_clicked = on_add_clicked
        color = RARITY_COLORS.get(item.rarity, "#d4af37")

        self.setStyleSheet(
            f"""
            QFrame {{
                background-color: #181522;
                border: 1px solid {color};
                border-radius: 10px;
            }}
            QFrame:hover {{
                background-color: #221d30;
            }}
            """
        )
        self.setToolTip(
            f"ID: {item.item_id}\n名称: {item.name}\n稀有度: {item.rarity}\n"
            f"部位: {item.category}\n职业: {', '.join(item.jobs)}\n"
            f"护甲精通: {item.armor_type or '无'}\n说明: {item.desc}"
        )

        layout = QVBoxLayout(self)
        layout.setContentsMargins(10, 10, 10, 10)
        layout.setSpacing(6)

        top = QHBoxLayout()
        icon_lbl = QLabel("?")
        icon_lbl.setObjectName('ItemIcon')
        icon_lbl.setAlignment(Qt.AlignCenter)
        icon_lbl.setStyleSheet(f"background:#15121a;color:#a59b8b;border:1px solid {color};border-radius:6px")
        icon_lbl.setFixedSize(44, 44)
        if item.icon is None:
            icon_lbl.setToolTip("物品脚本没有可用的 [icon] 引用")
        else:
            source=f"{item.icon.img} · 第 {item.icon.frame} 帧"
            try:
                pm=pix(icons.read(item.icon))
                ratio=icon_lbl.devicePixelRatioF()
                edge=round(40*ratio)
                pm=pm.scaled(edge,edge,Qt.KeepAspectRatio,Qt.FastTransformation)
                pm.setDevicePixelRatio(ratio)
                icon_lbl.setPixmap(pm)
                icon_lbl.setToolTip(source)
            except (OSError, ValueError, KeyError, struct.error, zlib.error) as exc:
                icon_lbl.setToolTip(f"{source}\n图标不可用：{exc}")
        top.addWidget(icon_lbl)

        info_col = QVBoxLayout()
        info_col.setSpacing(2)
        name_lbl = QLabel(item.name)
        name_lbl.setStyleSheet(f"color: {color}; font-weight: bold; font-size: 13px; border:none;")
        name_lbl.setWordWrap(True)

        cat_zh = display_cat(item.category)
        if item.rarity and item.rarity != "未标注":
            meta_txt = f"ID:{item.item_id} | {item.rarity} · {cat_zh}"
        else:
            meta_txt = f"ID:{item.item_id} | {cat_zh}"
        if item.armor_type:
            meta_txt += f" · {item.armor_type}"
        meta_lbl = QLabel(meta_txt)
        meta_lbl.setStyleSheet("color: #a99f8d; font-size: 11px; border:none;")

        info_col.addWidget(name_lbl)
        info_col.addWidget(meta_lbl)
        top.addLayout(info_col, 1)
        layout.addLayout(top)

        job_txt = "职业: " + (", ".join(item.jobs[:2]) + ("..." if len(item.jobs) > 2 else ""))
        job_lbl = QLabel(job_txt)
        job_lbl.setStyleSheet("color: #cfc5b4; font-size: 11px; border:none;")
        layout.addWidget(job_lbl)

        btn = QPushButton("+ 添加物品")
        if not item.grantmode:
            btn.setText("仅浏览 · 当前背包不支持")
            btn.setEnabled(False)
        btn.setStyleSheet(
            """
            QPushButton {
                background-color: rgba(212, 175, 55, 0.18);
                color: #ffd700;
                border: 1px solid rgba(212, 175, 55, 0.55);
                border-radius: 6px;
                padding: 5px;
                font-size: 12px;
                font-weight: bold;
            }
            QPushButton:hover {
                background-color: #ffd700;
                color: #141008;
            }
            """
        )
        btn.clicked.connect(lambda: self.on_add_clicked(self.item))
        layout.addWidget(btn)


class LauncherMainWindow(QMainWindow):
    def __init__(self):
        super().__init__()
        self.setWindowTitle("地下城与勇士 · 智慧的引导 (Lv.100 神话纪元)")
        self.resize(1180, 760)
        self.items: List[GameItem] = self.load_items()
        self.filtered_items: List[GameItem] = list(self.items)
        self.current_page: int = 1
        self.page_size: int = 12
        self.added_queue: List[dict] = []

        self._init_ui()
        self.apply_filters(reset_page=True)

    def _init_ui(self):
        self.root_widget = ScenicRootWidget()
        self.setCentralWidget(self.root_widget)

        main_layout = QHBoxLayout(self.root_widget)
        main_layout.setContentsMargins(0, 0, 0, 0)
        main_layout.setSpacing(0)

        # 左侧导航栏（半透明暗金琉璃）
        sidebar = QFrame()
        sidebar.setObjectName("Sidebar")
        sidebar.setFixedWidth(226)
        side_layout = QVBoxLayout(sidebar)
        side_layout.setContentsMargins(14, 18, 14, 16)
        side_layout.setSpacing(8)

        emblem_box = QFrame()
        emblem_box.setStyleSheet(
            "QFrame{background:qlineargradient(x1:0,y1:0,x2:1,y2:1,"
            "stop:0 rgba(235,188,72,0.22), stop:1 rgba(120,28,36,0.16));"
            "border:1px solid rgba(240,198,92,0.45);border-radius:11px;}"
        )
        em_row = QHBoxLayout(emblem_box)
        em_row.setContentsMargins(11, 10, 11, 10)
        em_row.setSpacing(9)
        em_ico = QLabel()
        em_ico.setPixmap(ui_icons.render_pixmap('brand_emblem', 34, active=True))
        em_ico.setStyleSheet("border:none;background:transparent;")
        em_row.addWidget(em_ico)
        em_lay = QVBoxLayout()
        em_lay.setSpacing(1)
        eyebrow = QLabel("DNF · LEVEL 100")
        eyebrow.setStyleSheet("font-size:10px;font-weight:bold;color:#e6c267;letter-spacing:1.4px;border:none;background:transparent;")
        brand = QLabel("智慧的引导")
        brand.setStyleSheet("font-size:19px;font-weight:bold;color:#ffe285;border:none;background:transparent;")
        sub_brand = QLabel("神话纪元 · 阿拉德终端")
        sub_brand.setStyleSheet("font-size:11px;color:#c7bca6;border:none;background:transparent;")
        em_lay.addWidget(eyebrow)
        em_lay.addWidget(brand)
        em_lay.addWidget(sub_brand)
        em_row.addLayout(em_lay, 1)
        side_layout.addWidget(emblem_box)
        side_layout.addSpacing(6)

        self.nav_btns: List[QPushButton] = []
        self._nav_icon_names = ["login", "items", "abyss", "wardrobe", "mods", "character"]
        nav_titles = [
            " 登录与启动",
            " 物品可视化添加",
            " 全局掉落设置",
            " 时装试衣间",
            " Mod 工坊",
            " 角色属性",
        ]
        for idx, (text, iname) in enumerate(zip(nav_titles, self._nav_icon_names)):
            btn = QPushButton(text)
            btn.setObjectName("NavBtn")
            btn.setCursor(Qt.PointingHandCursor)
            btn.setIcon(ui_icons.vector_icon(iname, 20, active=(idx == 0)))
            btn.setIconSize(QSize(20, 20))
            btn.setProperty("active", idx == 0)
            btn.clicked.connect(lambda checked=False, i=idx: self.switch_page(i))
            side_layout.addWidget(btn)
            self.nav_btns.append(btn)

        side_layout.addStretch()

        # 100级官方壁纸快捷切换
        bg_card = QFrame()
        bg_card.setObjectName("PanelCard")
        bg_lay = QVBoxLayout(bg_card)
        bg_lay.setContentsMargins(10, 8, 10, 9)
        bg_lay.setSpacing(5)
        bg_hdr = QHBoxLayout()
        bg_hdr.setSpacing(5)
        bg_ico = QLabel()
        bg_ico.setPixmap(ui_icons.render_pixmap('wallpaper', 15, active=True))
        bg_ico.setStyleSheet("border:none;background:transparent;")
        bg_lbl = QLabel("100级场景背景")
        bg_lbl.setStyleSheet("color:#e6c267;font-size:11px;font-weight:bold;border:none;background:transparent;")
        bg_hdr.addWidget(bg_ico)
        bg_hdr.addWidget(bg_lbl)
        bg_hdr.addStretch()
        self.wallpaper_combo = QComboBox()
        for key, label, *_ in theme_art.WALLPAPERS:
            self.wallpaper_combo.addItem(label, key)
        bg_lay.addLayout(bg_hdr)
        bg_lay.addWidget(self.wallpaper_combo)
        side_layout.addWidget(bg_card)

        status_card = QFrame()
        status_card.setObjectName("PanelCard")
        st_lay = QVBoxLayout(status_card)
        st_lay.setContentsMargins(10, 9, 10, 9)
        st_lay.setSpacing(4)
        st_title = QLabel("● 100级服务端状态")
        st_title.setStyleSheet("color: #4ade80; font-weight: bold; font-size: 12px; border:none;background:transparent;")
        self.gateway_label = st_info = QLabel("正在查询本地网关…")
        st_info.setWordWrap(True)
        st_info.setStyleSheet("color: #cbc0ab; font-size: 11px; border:none;background:transparent;")
        st_lay.addWidget(st_title)
        st_lay.addWidget(st_info)
        side_layout.addWidget(status_card)

        main_layout.addWidget(sidebar)

        # 右侧主内容区（透明以便透出 100 级背景原画）
        self.stack = QStackedWidget()
        self.stack.setStyleSheet("QStackedWidget{background:transparent;}")
        self.stack.addWidget(self._build_home_page())
        self.stack.addWidget(self._build_item_page())
        self.stack.addWidget(self._build_abyss_page())
        self.stack.addWidget(self.build_wardrobe())
        self.stack.addWidget(self.build_mods())
        self.stack.addWidget(self.build_character())
        main_layout.addWidget(self.stack, 1)

    def switch_page(self, index: int):
        self.stack.setCurrentIndex(index)
        for i, btn in enumerate(self.nav_btns):
            is_act = (i == index)
            btn.setProperty("active", is_act)
            if hasattr(self, "_nav_icon_names") and i < len(self._nav_icon_names):
                btn.setIcon(ui_icons.vector_icon(self._nav_icon_names[i], 20, active=is_act))
            btn.style().unpolish(btn)
            btn.style().polish(btn)

    # ---------------- 页面 0：登录与启动 ----------------
    def _build_home_page(self) -> QWidget:
        page = QWidget()
        page.setObjectName("HomePage")
        page.setStyleSheet("QWidget#HomePage{background:transparent;}")
        layout = QVBoxLayout(page)
        layout.setContentsMargins(24, 20, 24, 18)
        layout.setSpacing(14)

        # 史诗级 100 版本主视觉横幅（内嵌 1600x900 100 级原画 + 角色高清立绘 + 鎏金启动区）
        self.hero_banner = HeroBannerFrame()
        self.hero_banner.setMinimumHeight(262)
        h_lay = QVBoxLayout(self.hero_banner)
        h_lay.setContentsMargins(26, 22, 26, 22)
        h_lay.setSpacing(12)

        eyebrow = QLabel("DUNGEON & FIGHTER  ·  LEVEL 100  ·  GUIDE OF WISDOM")
        eyebrow.setObjectName("EyebrowLabel")
        h_lay.addWidget(eyebrow)

        title = QLabel("地下城与勇士 · 智慧的引导")
        title.setStyleSheet(
            "font-size: 28px; font-weight: bold; color: #ffe793; "
            "background: transparent; border: none;"
        )
        h_lay.addWidget(title)

        sub = QLabel("100级神话纪元 · 实时 PVF/NPK 驱动 · 高清可视化试衣间与角色控制台")
        sub.setStyleSheet("font-size: 13px; color: #dfd4be; background: transparent; border: none;")
        h_lay.addWidget(sub)

        # 账号 / 线路 / 客户端路径信息条（放在半透明暗金条中，右侧留白展示 100 级角色立绘）
        conn_box = QFrame()
        conn_box.setMaximumWidth(640)
        conn_box.setStyleSheet(
            "QFrame{background:rgba(12,10,20,0.72);border:1px solid rgba(235,192,86,0.34);border-radius:10px;}"
        )
        c_lay = QVBoxLayout(conn_box)
        c_lay.setContentsMargins(14, 10, 14, 10)
        c_lay.setSpacing(8)

        form_row = QHBoxLayout()
        form_row.setSpacing(8)
        self.acc_input = QLineEdit()
        self.acc_input.setPlaceholderText("输入账号 / 角色名")
        self.acc_input.setText("probe-us100")
        self.acc_input.setReadOnly(True)

        self.srv_combo = QComboBox()
        self.srv_combo.addItem("智慧的引导 · 本地服 (Lv.100)")

        self.exe_path_input = QLineEdit()
        self.exe_path_input.setText(str(self.client_exe))
        self.exe_path_input.setReadOnly(True)

        lbl_acc = QLabel("本地账号:")
        lbl_acc.setStyleSheet("color:#d6c7a8;border:none;background:transparent;")
        lbl_srv = QLabel("线路:")
        lbl_srv.setStyleSheet("color:#d6c7a8;border:none;background:transparent;")
        form_row.addWidget(lbl_acc)
        form_row.addWidget(self.acc_input, 1)
        form_row.addWidget(lbl_srv)
        form_row.addWidget(self.srv_combo, 1)
        c_lay.addLayout(form_row)

        exe_row = QHBoxLayout()
        exe_row.setSpacing(8)
        lbl_exe = QLabel("客户端路径:")
        lbl_exe.setStyleSheet("color:#d6c7a8;border:none;background:transparent;")
        exe_row.addWidget(lbl_exe)
        exe_row.addWidget(self.exe_path_input, 1)
        c_lay.addLayout(exe_row)
        h_lay.addWidget(conn_box)

        btn_row = QHBoxLayout()
        btn_row.setSpacing(10)
        launch_btn = QPushButton(" 立即进入 100 级世界")
        launch_btn.setObjectName("HeroLaunchBtn")
        launch_btn.setIcon(ui_icons.vector_icon("launch", 18, active=True, dark_fg=True))
        launch_btn.setIconSize(QSize(18, 18))
        launch_btn.setCursor(Qt.PointingHandCursor)
        glow_fx = QGraphicsDropShadowEffect(launch_btn)
        glow_fx.setBlurRadius(22)
        glow_fx.setOffset(0, 2)
        glow_fx.setColor(QColor(255, 205, 64, 145))
        launch_btn.setGraphicsEffect(glow_fx)
        launch_btn.clicked.connect(self._on_launch_clicked)

        go_item_btn = QPushButton(" 物品可视化添加")
        go_item_btn.setObjectName("DarkBtn")
        go_item_btn.setIcon(ui_icons.vector_icon("items", 16, active=True))
        go_item_btn.setIconSize(QSize(16, 16))
        go_item_btn.setCursor(Qt.PointingHandCursor)
        go_item_btn.clicked.connect(lambda: self.switch_page(1))

        go_abyss_btn = QPushButton(" 全局掉落设置")
        go_abyss_btn.setObjectName("DarkBtn")
        go_abyss_btn.setIcon(ui_icons.vector_icon("abyss", 16, active=True))
        go_abyss_btn.setIconSize(QSize(16, 16))
        go_abyss_btn.setCursor(Qt.PointingHandCursor)
        go_abyss_btn.clicked.connect(lambda: self.switch_page(2))

        btn_row.addWidget(launch_btn)
        btn_row.addWidget(go_item_btn)
        btn_row.addWidget(go_abyss_btn)
        btn_row.addStretch()
        h_lay.addLayout(btn_row)

        layout.addWidget(self.hero_banner)
        self.decorate_home(layout)
        layout.addStretch()
        return page

    # ---------------- 页面 1：物品可视化添加 ----------------
    def _build_item_page(self) -> QWidget:
        page = QWidget()
        layout = QVBoxLayout(page)
        layout.setContentsMargins(20, 18, 20, 18)
        layout.setSpacing(12)

        header = QHBoxLayout()
        title = QLabel("物品可视化添加")
        title.setObjectName("TitleLabel")
        self.result_count_lbl = QLabel("共 0 件物品")
        self.result_count_lbl.setObjectName("SubLabel")
        header.addWidget(title)
        header.addStretch()
        header.addWidget(self.result_count_lbl)
        layout.addLayout(header)

        # 物品分类快捷切换条
        self._group_pills = {}
        pill_row = QHBoxLayout()
        pill_row.setSpacing(6)
        group_names = ["全部分类"] + self.source.item_groups()
        for gname in group_names:
            pb = QPushButton(gname)
            pb.setCursor(Qt.PointingHandCursor)
            pb.setCheckable(True)
            pb.setChecked(gname == "全部分类")
            pb.setStyleSheet(
                "QPushButton{background:rgba(20,16,32,0.82);color:#cfc4ae;"
                "border:1px solid rgba(212,175,55,0.24);border-radius:13px;padding:4px 10px;font-size:11px;font-weight:bold;}"
                "QPushButton:hover{border-color:#ffd700;color:#ffe28a;}"
                "QPushButton:checked{background:qlineargradient(x1:0,y1:0,x2:0,y2:1,"
                "stop:0 rgba(242,196,76,0.92),stop:1 rgba(182,126,22,0.92));"
                "color:#181105;border-color:#fff0b3;}"
            )
            pb.clicked.connect(lambda checked=False, g=gname: self._select_item_group(g))
            pill_row.addWidget(pb)
            self._group_pills[gname] = pb
        pill_row.addStretch()
        layout.addLayout(pill_row)

        # 筛选栏：关键词、物品分类、部位/细分、稀有度（仅真实稀有度）、目标职业、仅匹配职业、仅可发放、单次数量
        filter_card = QFrame()
        filter_card.setObjectName("PanelCard")
        f_lay = QGridLayout(filter_card)
        f_lay.setContentsMargins(12, 10, 12, 10)
        f_lay.setHorizontalSpacing(10)
        f_lay.setVerticalSpacing(8)

        self.search_input = QLineEdit()
        self.search_input.setPlaceholderText("搜索物品名称或 ID...")
        self.search_input.textChanged.connect(lambda: self.apply_filters(reset_page=True))

        self.group_combo = QComboBox()
        self.group_combo.addItems(group_names)
        self.group_combo.currentTextChanged.connect(self._on_group_combo_changed)

        self.category_combo = QComboBox()
        self._populate_category_combo("全部分类")
        self.category_combo.currentIndexChanged.connect(lambda: self.apply_filters(reset_page=True))

        self.rarity_combo = QComboBox()
        self.rarity_combo.addItems(["全部稀有度"] + self.source.rarities())
        self.rarity_combo.currentTextChanged.connect(lambda: self.apply_filters(reset_page=True))

        self.job_combo = QComboBox()
        self.job_combo.addItems(JOBS)
        self.job_combo.currentTextChanged.connect(lambda: self.apply_filters(reset_page=True))

        self.only_job_chk = QCheckBox("仅显示当前职业匹配")
        self.only_job_chk.setChecked(True)
        self.only_job_chk.stateChanged.connect(lambda: self.apply_filters(reset_page=True))

        self.only_grantable_chk = QCheckBox("仅显示可发放")
        self.only_grantable_chk.setChecked(False)
        self.only_grantable_chk.stateChanged.connect(lambda: self.apply_filters(reset_page=True))

        self.qty_spin = QSpinBox()
        self.qty_spin.setRange(1, 999)
        self.qty_spin.setValue(1)
        self.qty_spin.setPrefix("数量: ")

        reset_btn = QPushButton("重置")
        reset_btn.setObjectName("DarkBtn")
        reset_btn.clicked.connect(self._reset_filters)

        f_lay.addWidget(QLabel("关键词:"), 0, 0)
        f_lay.addWidget(self.search_input, 0, 1)
        f_lay.addWidget(QLabel("物品分类:"), 0, 2)
        f_lay.addWidget(self.group_combo, 0, 3)
        f_lay.addWidget(QLabel("部位/细分:"), 0, 4)
        f_lay.addWidget(self.category_combo, 0, 5)

        f_lay.addWidget(QLabel("稀有度:"), 1, 0)
        f_lay.addWidget(self.rarity_combo, 1, 1)
        f_lay.addWidget(QLabel("目标职业:"), 1, 2)
        f_lay.addWidget(self.job_combo, 1, 3)
        chk_box = QHBoxLayout()
        chk_box.setSpacing(10)
        chk_box.addWidget(self.only_job_chk)
        chk_box.addWidget(self.only_grantable_chk)
        f_lay.addLayout(chk_box, 1, 4, 1, 2)

        f_lay.addWidget(self.qty_spin, 0, 6)
        f_lay.addWidget(reset_btn, 1, 6)

        layout.addWidget(filter_card)

        # 中部：左侧物品网格 + 右侧已选添加队列
        body = QHBoxLayout()
        body.setSpacing(12)

        left_box = QVBoxLayout()
        scroll = QScrollArea()
        scroll.setWidgetResizable(True)
        scroll.setStyleSheet("QScrollArea { border: none; background: transparent; }")
        self.grid_container = QWidget()
        self.grid_layout = QGridLayout(self.grid_container)
        self.grid_layout.setSpacing(10)
        scroll.setWidget(self.grid_container)
        left_box.addWidget(scroll, 1)

        # 分页控制栏
        page_bar = QHBoxLayout()
        self.prev_btn = QPushButton("◀ 上一页")
        self.prev_btn.setObjectName("DarkBtn")
        self.prev_btn.clicked.connect(self._prev_page)

        self.page_info_lbl = QLabel("第 1 / 1 页")
        self.page_info_lbl.setStyleSheet("color: #ffd700; font-weight: bold;")

        self.page_jump_spin = QSpinBox()
        self.page_jump_spin.setMinimum(1)
        self.page_jump_spin.setPrefix("跳至第 ")
        self.page_jump_spin.setSuffix(" 页")

        jump_btn = QPushButton("跳转")
        jump_btn.setObjectName("DarkBtn")
        jump_btn.clicked.connect(self._jump_page)

        self.next_btn = QPushButton("下一页 ▶")
        self.next_btn.setObjectName("DarkBtn")
        self.next_btn.clicked.connect(self._next_page)

        page_bar.addWidget(self.prev_btn)
        page_bar.addWidget(self.page_info_lbl)
        page_bar.addStretch()
        page_bar.addWidget(self.page_jump_spin)
        page_bar.addWidget(jump_btn)
        page_bar.addWidget(self.next_btn)
        left_box.addLayout(page_bar)

        body.addLayout(left_box, 3)

        # 右侧添加队列
        right_card = QFrame()
        right_card.setObjectName("PanelCard")
        right_card.setFixedWidth(270)
        r_lay = QVBoxLayout(right_card)
        r_lay.setContentsMargins(12, 12, 12, 12)
        r_lay.setSpacing(8)

        r_title = QLabel("待发送物品队列")
        r_title.setStyleSheet("font-size: 14px; font-weight: bold; color: #ffd700; border:none;")
        r_lay.addWidget(r_title)
        self.configure_recipient(r_lay)

        self.queue_list = QListWidget()
        self.queue_list.setStyleSheet("background-color: #121019; border: 1px solid rgba(212,175,55,0.25); border-radius: 6px;")
        r_lay.addWidget(self.queue_list, 1)

        send_btn = QPushButton("✔ 确认添加至角色/背包")
        send_btn.setObjectName("GoldBtn")
        send_btn.clicked.connect(self._confirm_add_items)

        clear_btn = QPushButton("清空队列")
        clear_btn.setObjectName("DarkBtn")
        clear_btn.clicked.connect(self._clear_queue)

        r_lay.addWidget(send_btn)
        r_lay.addWidget(clear_btn)
        self.configure_receipts(r_lay)
        body.addWidget(right_card)

        layout.addLayout(body, 1)
        return page

    def _is_job_matched(self, item: GameItem, selected_job: str) -> bool:
        if selected_job == "全部职业":
            return True
        if "全职业" in item.jobs or selected_job in item.jobs:
            return True
        return False

    def _populate_category_combo(self, group: str, keep_raw: str = ""):
        prev = self.category_combo.blockSignals(True)
        try:
            self.category_combo.clear()
            self.category_combo.addItem("全部部位", "全部部位")
            for raw_cat, label in self.source.categories_for_group(group):
                self.category_combo.addItem(label, raw_cat)
            if keep_raw:
                idx = self.category_combo.findData(keep_raw)
                if idx >= 0:
                    self.category_combo.setCurrentIndex(idx)
        finally:
            self.category_combo.blockSignals(prev)

    def _select_item_group(self, group: str):
        for gname, pb in self._group_pills.items():
            pb.setChecked(gname == group)
        if self.group_combo.currentText() != group:
            self.group_combo.setCurrentText(group)
        else:
            self._populate_category_combo(group)
            self.apply_filters(reset_page=True)

    def _on_group_combo_changed(self, group: str):
        for gname, pb in self._group_pills.items():
            pb.setChecked(gname == group)
        self._populate_category_combo(group)
        self.apply_filters(reset_page=True)

    def _reset_filters(self):
        self.search_input.clear()
        self.group_combo.setCurrentIndex(0)
        self.rarity_combo.setCurrentIndex(0)
        self.category_combo.setCurrentIndex(0)
        self.job_combo.setCurrentIndex(0)
        self.only_job_chk.setChecked(False)
        self.only_grantable_chk.setChecked(False)
        self.apply_filters(reset_page=True)

    def apply_filters(self, reset_page: bool = False):
        kw = self.search_input.text().strip().lower()
        group = self.group_combo.currentText() if hasattr(self, "group_combo") else "全部分类"
        rar = self.rarity_combo.currentText()
        cat = self.category_combo.currentData() or self.category_combo.currentText()
        job = self.job_combo.currentText()
        only_job = self.only_job_chk.isChecked()
        only_grantable = self.only_grantable_chk.isChecked() if hasattr(self, "only_grantable_chk") else False

        self.filtered_items = self.source.match(
            kw, cat, rar, job if only_job else "全部职业",
            group=group, only_grantable=only_grantable,
        )
        if reset_page:
            self.current_page = 1
        self._render_page()

    def _render_page(self):
        total = len(self.filtered_items)
        total_pages = max(1, math.ceil(total / self.page_size))
        self.current_page = max(1, min(self.current_page, total_pages))

        start = (self.current_page - 1) * self.page_size
        end = start + self.page_size
        page_items = self.filtered_items[start:end]

        # 清空旧卡片
        while self.grid_layout.count():
            child = self.grid_layout.takeAt(0)
            if child.widget():
                child.widget().hide()
                child.widget().deleteLater()

        cols = 3
        for idx, item in enumerate(page_items):
            r, c = divmod(idx, cols)
            card = ItemCardWidget(item, self._on_add_item_to_queue, self.item_icons)
            self.grid_layout.addWidget(card, r, c)

        self.result_count_lbl.setText(f"共 {total} 件匹配物品")
        self.page_info_lbl.setText(f"第 {self.current_page} / {total_pages} 页")
        self.page_jump_spin.setMaximum(total_pages)
        self.page_jump_spin.setValue(self.current_page)

        self.prev_btn.setEnabled(self.current_page > 1)
        self.next_btn.setEnabled(self.current_page < total_pages)

    def _prev_page(self):
        self.current_page -= 1
        self._render_page()

    def _next_page(self):
        self.current_page += 1
        self._render_page()

    def _jump_page(self):
        self.current_page = self.page_jump_spin.value()
        self._render_page()

    def _clear_queue(self):
        self.added_queue.clear()
        self.queue_list.clear()

    # ---------------- 页面 2：全局深渊掉落 ----------------
    def build_preset_page(self) -> QWidget:
        page = QWidget()
        layout = QVBoxLayout(page)
        layout.setContentsMargins(24, 24, 24, 24)
        layout.setSpacing(16)

        title = QLabel("全局深渊预设")
        title.setObjectName("TitleLabel")
        sub = QLabel("保留原稿的倍率、权重与自定义池 JSON 格式，仅导出预设，不修改网关。生效规则在「网关规则」设置。")
        sub.setObjectName("SubLabel")
        layout.addWidget(title)
        layout.addWidget(sub)

        card = QFrame()
        card.setObjectName("PanelCard")
        c_lay = QGridLayout(card)
        c_lay.setContentsMargins(20, 20, 20, 20)
        c_lay.setHorizontalSpacing(16)
        c_lay.setVerticalSpacing(14)

        self.abyss_enable_chk = QCheckBox("在导出预设中启用深渊掉落")
        self.abyss_enable_chk.setChecked(False)
        self.abyss_enable_chk.setStyleSheet("font-size: 14px; font-weight: bold; color: #ffd700;")

        self.drop_rate_spin = QDoubleSpinBox()
        self.drop_rate_spin.setRange(0.1, 50.0)
        self.drop_rate_spin.setSingleStep(0.5)
        self.drop_rate_spin.setValue(3.0)
        self.drop_rate_spin.setSuffix(" 倍")

        self.mythic_slider = QSlider(Qt.Horizontal)
        self.mythic_slider.setRange(1, 50)
        self.mythic_slider.setValue(12)
        self.mythic_lbl = QLabel("12%")
        self.mythic_slider.valueChanged.connect(lambda v: self.mythic_lbl.setText(f"{v}%"))

        self.epic_slider = QSlider(Qt.Horizontal)
        self.epic_slider.setRange(1, 100)
        self.epic_slider.setValue(45)
        self.epic_lbl = QLabel("45%")
        self.epic_slider.valueChanged.connect(lambda v: self.epic_lbl.setText(f"{v}%"))

        self.pool_mode_combo = QComboBox()
        self.pool_mode_combo.addItems(["100级全史诗+神话全局池", "仅当前职业适配装备池", "自定义指定物品ID池"])

        self.custom_ids_edit = QLineEdit()
        self.custom_ids_edit.setPlaceholderText("可选：输入指定掉落物品ID，用逗号分隔，如 100101,100201")

        c_lay.addWidget(self.abyss_enable_chk, 0, 0, 1, 3)
        c_lay.addWidget(QLabel("全局爆率倍率:"), 1, 0)
        c_lay.addWidget(self.drop_rate_spin, 1, 1)

        c_lay.addWidget(QLabel("神话出货权重:"), 2, 0)
        c_lay.addWidget(self.mythic_slider, 2, 1)
        c_lay.addWidget(self.mythic_lbl, 2, 2)

        c_lay.addWidget(QLabel("史诗出货权重:"), 3, 0)
        c_lay.addWidget(self.epic_slider, 3, 1)
        c_lay.addWidget(self.epic_lbl, 3, 2)

        c_lay.addWidget(QLabel("全局掉落池模式:"), 4, 0)
        c_lay.addWidget(self.pool_mode_combo, 4, 1, 1, 2)

        c_lay.addWidget(QLabel("指定物品池(ID):"), 5, 0)
        c_lay.addWidget(self.custom_ids_edit, 5, 1, 1, 2)

        layout.addWidget(card)

        btn_row = QHBoxLayout()
        save_btn = QPushButton("💾 保存并生成全局深渊配置 (JSON)")
        save_btn.setObjectName("GoldBtn")
        save_btn.clicked.connect(lambda: self.save_preset())
        btn_row.addWidget(save_btn)
        btn_row.addStretch()
        layout.addLayout(btn_row)

        self.config_preview = QTextEdit()
        self.config_preview.setReadOnly(True)
        self.config_preview.setMaximumHeight(170)
        self.config_preview.setStyleSheet("background-color: #121019; border: 1px solid rgba(212,175,55,0.3); border-radius: 8px;")
        layout.addWidget(self.config_preview)

        self.restore_preset()
        return page
