"""High-DPI bespoke metallic gold vector icons for the Lv.100 Wisdom's Guide launcher."""
from __future__ import annotations

from functools import lru_cache
import math

from PySide6.QtCore import QPointF, QRectF, Qt
from PySide6.QtGui import (
    QBrush,
    QColor,
    QIcon,
    QLinearGradient,
    QPainter,
    QPainterPath,
    QPen,
    QPixmap,
    QPolygonF,
    QRadialGradient,
)


def _gold_grad(size: float, active: bool = True, accent: str = 'gold') -> QLinearGradient:
    g = QLinearGradient(0, 0, size, size)
    if accent == 'dark':
        g.setColorAt(0.0, QColor('#2b1d07'))
        g.setColorAt(1.0, QColor('#120c03'))
        return g
    if accent == 'abyss':
        if active:
            g.setColorAt(0.0, QColor('#ffe89c'))
            g.setColorAt(0.5, QColor('#d98cff'))
            g.setColorAt(1.0, QColor('#9b59ff'))
        else:
            g.setColorAt(0.0, QColor('#d9c293'))
            g.setColorAt(1.0, QColor('#9d7cc7'))
        return g
    if accent == 'teal':
        g.setColorAt(0.0, QColor('#b7f5e8'))
        g.setColorAt(1.0, QColor('#58b8a6'))
        return g
    if active:
        g.setColorAt(0.0, QColor('#fff4be'))
        g.setColorAt(0.45, QColor('#ffd54f'))
        g.setColorAt(1.0, QColor('#c9891b'))
    else:
        g.setColorAt(0.0, QColor('#e6cf98'))
        g.setColorAt(0.5, QColor('#bfa05e'))
        g.setColorAt(1.0, QColor('#8c6d34'))
    return g


def _star_poly(cx: float, cy: float, r_out: float, r_in: float, points: int, rot_deg: float = -90.0) -> QPolygonF:
    pts = []
    for i in range(points * 2):
        r = r_out if i % 2 == 0 else r_in
        ang = math.radians(rot_deg + i * 180.0 / points)
        pts.append(QPointF(cx + r * math.cos(ang), cy + r * math.sin(ang)))
    return QPolygonF(pts)


def _draw_icon(p: QPainter, name: str, s: float, active: bool, dark_fg: bool) -> None:
    p.setRenderHint(QPainter.Antialiasing, True)
    c = s / 2.0
    grad = _gold_grad(s, active=active, accent='dark' if dark_fg else ('abyss' if name == 'abyss' else 'gold'))
    pen_w = max(1.4, s * 0.075)
    pen = QPen(QBrush(grad), pen_w, Qt.SolidLine, Qt.RoundCap, Qt.RoundJoin)
    fill = QBrush(grad)

    if not dark_fg and active:
        halo = QRadialGradient(QPointF(c, c), s * 0.5)
        halo.setColorAt(0.0, QColor(255, 215, 90, 58 if name != 'abyss' else 48))
        halo.setColorAt(1.0, QColor(0, 0, 0, 0))
        p.fillRect(QRectF(0, 0, s, s), halo)

    if name == 'login':
        # Crossed ornate greatswords
        for sign in (-1, 1):
            p.save()
            p.translate(c, c)
            p.rotate(sign * 42)
            blade = QPainterPath()
            blade.moveTo(0, -s * 0.42)
            blade.lineTo(s * 0.085, -s * 0.24)
            blade.lineTo(s * 0.065, s * 0.14)
            blade.lineTo(-s * 0.065, s * 0.14)
            blade.lineTo(-s * 0.085, -s * 0.24)
            blade.closeSubpath()
            p.setPen(QPen(QColor('#fff7d1') if active else QColor('#d6bd82'), max(0.8, s * 0.03)))
            p.setBrush(fill)
            p.drawPath(blade)
            # Crossguard & hilt
            p.setPen(pen)
            p.drawLine(QPointF(-s * 0.16, s * 0.14), QPointF(s * 0.16, s * 0.14))
            p.drawLine(QPointF(0, s * 0.14), QPointF(0, s * 0.34))
            p.setBrush(QColor('#ff5e5e') if active else QColor('#b35959'))
            p.setPen(Qt.NoPen)
            p.drawEllipse(QPointF(0, s * 0.36), s * 0.055, s * 0.055)
            p.restore()

    elif name == 'items':
        # Faceted Mythic Gem / Treasure Crystal
        outer = QPolygonF([
            QPointF(c, s * 0.10),
            QPointF(s * 0.86, s * 0.36),
            QPointF(c, s * 0.90),
            QPointF(s * 0.14, s * 0.36),
        ])
        p.setPen(pen)
        p.setBrush(QColor(255, 210, 80, 52) if active else QColor(190, 155, 85, 32))
        p.drawPolygon(outer)
        inner = QPolygonF([
            QPointF(c, s * 0.10),
            QPointF(s * 0.65, s * 0.36),
            QPointF(c, s * 0.90),
            QPointF(s * 0.35, s * 0.36),
        ])
        p.setBrush(fill)
        p.drawPolygon(inner)
        p.setPen(QPen(QColor('#fff8d6') if active else QColor('#d8c08a'), max(0.9, s * 0.04)))
        p.drawLine(QPointF(s * 0.14, s * 0.36), QPointF(s * 0.86, s * 0.36))

    elif name == 'abyss':
        # Celestial Wisdom Abyss Vortex + 4-point mythic star
        p.setPen(QPen(QBrush(grad), max(1.3, s * 0.07)))
        p.setBrush(Qt.NoBrush)
        p.drawEllipse(QPointF(c, c), s * 0.36, s * 0.36)
        p.setPen(QPen(QColor('#f6d8ff') if active else QColor('#bda7d9'), max(0.9, s * 0.045), Qt.DashLine))
        p.drawEllipse(QPointF(c, c), s * 0.24, s * 0.24)
        p.setPen(Qt.NoPen)
        p.setBrush(fill)
        p.drawPolygon(_star_poly(c, c, s * 0.26, s * 0.09, 4))

    elif name == 'wardrobe':
        # Royal Imperial Crown
        crown = QPainterPath()
        crown.moveTo(s * 0.14, s * 0.74)
        crown.lineTo(s * 0.12, s * 0.32)
        crown.lineTo(s * 0.34, s * 0.50)
        crown.lineTo(c, s * 0.20)
        crown.lineTo(s * 0.66, s * 0.50)
        crown.lineTo(s * 0.88, s * 0.32)
        crown.lineTo(s * 0.86, s * 0.74)
        crown.closeSubpath()
        p.setPen(pen)
        p.setBrush(fill)
        p.drawPath(crown)
        # Crown band & jewels
        p.setPen(QPen(QColor('#fff7d1') if active else QColor('#d4bc84'), max(1.0, s * 0.045)))
        p.drawLine(QPointF(s * 0.16, s * 0.82), QPointF(s * 0.84, s * 0.82))
        p.setPen(Qt.NoPen)
        p.setBrush(QColor('#5eead4') if active else QColor('#6faea4'))
        p.drawEllipse(QPointF(c, s * 0.58), s * 0.06, s * 0.06)

    elif name == 'mods':
        # Wisdom's Guide Precision Golden Gearwheel
        teeth = 8
        pts = []
        for i in range(teeth * 4):
            ang = math.radians(i * 360.0 / (teeth * 4))
            r = s * 0.42 if (i % 4 in (0, 1)) else s * 0.31
            pts.append(QPointF(c + r * math.cos(ang), c + r * math.sin(ang)))
        gear = QPainterPath()
        gear.addPolygon(QPolygonF(pts))
        gear.closeSubpath()
        hole = QPainterPath()
        hole.addEllipse(QPointF(c, c), s * 0.16, s * 0.16)
        gear = gear.subtracted(hole)
        p.setPen(QPen(QColor('#fff6cc') if active else QColor('#bfa05e'), max(0.8, s * 0.035)))
        p.setBrush(fill)
        p.drawPath(gear)

    elif name == 'character':
        # Awakened Hero Winged Star Emblem
        p.setPen(pen)
        p.setBrush(fill)
        p.drawPolygon(_star_poly(c, c, s * 0.41, s * 0.18, 5))
        p.setPen(Qt.NoPen)
        p.setBrush(QColor('#fffbe0') if active else QColor('#e6d2a2'))
        p.drawEllipse(QPointF(c, c), s * 0.085, s * 0.085)

    elif name == 'brand_emblem':
        # Lv.100 Wisdom's Guide Winged Gear Crest
        _draw_icon(p, 'mods', s * 0.86, True, False)
        p.setPen(Qt.NoPen)
        p.setBrush(_gold_grad(s, True, 'gold'))
        p.drawPolygon(_star_poly(s * 0.43, s * 0.43, s * 0.20, s * 0.07, 4))

    elif name == 'launch':
        # Sharp Lightning Bolt
        bolt = QPolygonF([
            QPointF(s * 0.56, s * 0.08),
            QPointF(s * 0.22, s * 0.54),
            QPointF(s * 0.48, s * 0.54),
            QPointF(s * 0.40, s * 0.92),
            QPointF(s * 0.78, s * 0.44),
            QPointF(s * 0.52, s * 0.44),
        ])
        p.setPen(Qt.NoPen)
        p.setBrush(fill)
        p.drawPolygon(bolt)

    elif name == 'merge':
        # Stacked PVF Layers + Merge Arrow
        p.setPen(pen)
        p.setBrush(Qt.NoBrush)
        p.drawRoundedRect(QRectF(s * 0.16, s * 0.56, s * 0.68, s * 0.26), s * 0.07, s * 0.07)
        p.drawLine(QPointF(c, s * 0.14), QPointF(c, s * 0.52))
        p.drawLine(QPointF(s * 0.34, s * 0.38), QPointF(c, s * 0.54))
        p.drawLine(QPointF(s * 0.66, s * 0.38), QPointF(c, s * 0.54))

    elif name == 'import_pkg':
        # Plus inside sleek box
        p.setPen(pen)
        p.setBrush(Qt.NoBrush)
        p.drawRoundedRect(QRectF(s * 0.16, s * 0.16, s * 0.68, s * 0.68), s * 0.14, s * 0.14)
        p.drawLine(QPointF(c, s * 0.32), QPointF(c, s * 0.68))
        p.drawLine(QPointF(s * 0.32, c), QPointF(s * 0.68, c))

    elif name == 'refresh':
        p.setPen(pen)
        p.setBrush(Qt.NoBrush)
        arc_r = QRectF(s * 0.18, s * 0.18, s * 0.64, s * 0.64)
        p.drawArc(arc_r, 35 * 16, 260 * 16)
        arrow = QPolygonF([QPointF(s * 0.84, s * 0.22), QPointF(s * 0.82, s * 0.44), QPointF(s * 0.63, s * 0.34)])
        p.setPen(Qt.NoPen)
        p.setBrush(fill)
        p.drawPolygon(arrow)

    elif name == 'restore':
        p.setPen(pen)
        p.setBrush(Qt.NoBrush)
        arc_r = QRectF(s * 0.18, s * 0.18, s * 0.64, s * 0.64)
        p.drawArc(arc_r, -110 * 16, 260 * 16)
        arrow = QPolygonF([QPointF(s * 0.16, s * 0.22), QPointF(s * 0.18, s * 0.44), QPointF(s * 0.37, s * 0.34)])
        p.setPen(Qt.NoPen)
        p.setBrush(fill)
        p.drawPolygon(arrow)

    elif name == 'folder':
        path = QPainterPath()
        path.moveTo(s * 0.14, s * 0.26)
        path.lineTo(s * 0.42, s * 0.26)
        path.lineTo(s * 0.50, s * 0.36)
        path.lineTo(s * 0.86, s * 0.36)
        path.lineTo(s * 0.86, s * 0.78)
        path.lineTo(s * 0.14, s * 0.78)
        path.closeSubpath()
        p.setPen(pen)
        p.setBrush(QColor(255, 210, 80, 45) if not dark_fg else fill)
        p.drawPath(path)

    elif name == 'up':
        p.setPen(pen)
        p.drawLine(QPointF(s * 0.22, s * 0.64), QPointF(c, s * 0.34))
        p.drawLine(QPointF(c, s * 0.34), QPointF(s * 0.78, s * 0.64))

    elif name == 'down':
        p.setPen(pen)
        p.drawLine(QPointF(s * 0.22, s * 0.36), QPointF(c, s * 0.66))
        p.drawLine(QPointF(c, s * 0.66), QPointF(s * 0.78, s * 0.36))

    elif name == 'wallpaper':
        p.setPen(pen)
        p.setBrush(Qt.NoBrush)
        p.drawRoundedRect(QRectF(s * 0.14, s * 0.20, s * 0.72, s * 0.60), s * 0.08, s * 0.08)
        mtn = QPolygonF([QPointF(s * 0.20, s * 0.74), QPointF(s * 0.42, s * 0.46), QPointF(s * 0.58, s * 0.62), QPointF(s * 0.70, s * 0.50), QPointF(s * 0.82, s * 0.74)])
        p.setPen(Qt.NoPen)
        p.setBrush(fill)
        p.drawPolygon(mtn)
        p.drawEllipse(QPointF(s * 0.68, s * 0.34), s * 0.065, s * 0.065)

    elif name == 'weapon':
        # Single ornate blade
        p.save()
        p.translate(c, c)
        p.rotate(40)
        blade = QPainterPath()
        blade.moveTo(0, -s * 0.44)
        blade.lineTo(s * 0.10, -s * 0.22)
        blade.lineTo(s * 0.075, s * 0.16)
        blade.lineTo(-s * 0.075, s * 0.16)
        blade.lineTo(-s * 0.10, -s * 0.22)
        blade.closeSubpath()
        p.setPen(QPen(QColor('#fff7d1'), max(0.8, s * 0.03)))
        p.setBrush(fill)
        p.drawPath(blade)
        p.setPen(pen)
        p.drawLine(QPointF(-s * 0.18, s * 0.16), QPointF(s * 0.18, s * 0.16))
        p.drawLine(QPointF(0, s * 0.16), QPointF(0, s * 0.36))
        p.restore()

    elif name == 'aura':
        # Radiant Halo Ring + Sparkles
        p.setPen(pen)
        p.setBrush(Qt.NoBrush)
        p.drawEllipse(QRectF(s * 0.14, s * 0.30, s * 0.72, s * 0.40))
        p.setPen(Qt.NoPen)
        p.setBrush(fill)
        p.drawPolygon(_star_poly(c, s * 0.26, s * 0.17, s * 0.06, 4))
        p.drawPolygon(_star_poly(s * 0.76, s * 0.66, s * 0.11, s * 0.04, 4))

    elif name == 'shuffle':
        p.setPen(pen)
        p.setBrush(Qt.NoBrush)
        p.drawLine(QPointF(s * 0.18, s * 0.32), QPointF(s * 0.78, s * 0.70))
        p.drawLine(QPointF(s * 0.18, s * 0.70), QPointF(s * 0.78, s * 0.32))
        p.setPen(Qt.NoPen)
        p.setBrush(fill)
        p.drawPolygon(_star_poly(s * 0.78, s * 0.32, s * 0.13, s * 0.05, 4))

    elif name == 'server':
        # Dual-tier sleek server node emblem
        p.setPen(pen)
        p.setBrush(QColor(255, 210, 80, 36) if not dark_fg else fill)
        p.drawRoundedRect(QRectF(s * 0.16, s * 0.18, s * 0.68, s * 0.26), s * 0.07, s * 0.07)
        p.drawRoundedRect(QRectF(s * 0.16, s * 0.56, s * 0.68, s * 0.26), s * 0.07, s * 0.07)
        p.setPen(Qt.NoPen)
        p.setBrush(QColor('#4ade80') if active else QColor('#94a3b8'))
        p.drawEllipse(QPointF(s * 0.30, s * 0.31), s * 0.055, s * 0.055)
        p.drawEllipse(QPointF(s * 0.30, s * 0.69), s * 0.055, s * 0.055)

    elif name == 'server_start':
        # Play triangle inside crisp ring
        p.setPen(pen)
        p.setBrush(Qt.NoBrush)
        p.drawEllipse(QPointF(c, c), s * 0.38, s * 0.38)
        tri = QPolygonF([
            QPointF(s * 0.40, s * 0.30),
            QPointF(s * 0.72, c),
            QPointF(s * 0.40, s * 0.70),
        ])
        p.setPen(Qt.NoPen)
        p.setBrush(fill)
        p.drawPolygon(tri)

    elif name == 'server_stop':
        # Power-off symbol in crisp crimson-gold accent
        stop_col = QColor('#fca5a5') if not dark_fg else QColor('#2b1d07')
        spen = QPen(stop_col, max(1.6, s * 0.085), Qt.SolidLine, Qt.RoundCap, Qt.RoundJoin)
        p.setPen(spen)
        p.setBrush(Qt.NoBrush)
        p.drawArc(QRectF(s * 0.20, s * 0.22, s * 0.60, s * 0.60), 130 * 16, 280 * 16)
        p.drawLine(QPointF(c, s * 0.14), QPointF(c, s * 0.48))

    elif name == 'events':
        # Radiant festival star & celebratory crown emblem
        p.setPen(pen)
        p.setBrush(QColor(255, 210, 80, 44) if active else QColor(190, 155, 85, 24))
        p.drawEllipse(QPointF(c, c), s * 0.40, s * 0.40)
        p.setPen(QPen(QColor('#fff7d1') if active else QColor('#d6bd82'), max(0.8, s * 0.03)))
        p.setBrush(fill)
        p.drawPolygon(_star_poly(c, c, s * 0.30, s * 0.12, 4))
        p.setPen(Qt.NoPen)
        p.drawEllipse(QPointF(s * 0.78, s * 0.22), s * 0.06, s * 0.06)


@lru_cache(maxsize=128)
def render_pixmap(name: str, size: int = 20, active: bool = True, dark_fg: bool = False) -> QPixmap:
    dpr = 2
    px = size * dpr
    pm = QPixmap(px, px)
    pm.fill(Qt.transparent)
    p = QPainter(pm)
    try:
        _draw_icon(p, name, float(px), active, dark_fg)
    finally:
        p.end()
    pm.setDevicePixelRatio(dpr)
    return pm


@lru_cache(maxsize=128)
def vector_icon(name: str, size: int = 20, active: bool = True, dark_fg: bool = False) -> QIcon:
    return QIcon(render_pixmap(name, size, active, dark_fg))
