"""Live desktop services for the supplied PySide6 launcher layout."""
import json
import msvcrt
import os
from pathlib import Path
import sys
import threading

from PIL import Image
from PySide6.QtCore import QObject, QSignalBlocker, QSize, Qt, QTimer, Signal
from PySide6.QtGui import QColor, QIcon, QPalette
from PySide6.QtWidgets import (QApplication,QComboBox,QDialog,QFileDialog,QFormLayout,QFrame,
    QHBoxLayout,QLabel,QLineEdit,QListWidget,QListWidgetItem,QMessageBox,QPushButton,
    QScrollArea,QSpinBox,QTabWidget,QVBoxLayout,QWidget)

import core
from activity_page import ActivityCenterPage
from character_page import CharacterPage
from drop_page import DropConfigPage
from epic_data import read_cards
from epic_ui import GameItem, LauncherMainWindow, STYLESHEET, pix
from item_icons import ItemIcons
from font_panel import FontPanel
import live_resources as lr
from mod_panel import ModWorkshopPanel
from source_library import SourceLibrary
from sprites import Wardrobe, frame, index
import theme_art
import ui_icons
from wardrobe_page import FittingRoom

PRESET=core.DATA/'global_abyss_config.json'
EXTRA_STYLE="""
QLabel, QCheckBox {color:#eee6d7}
QListWidget,QTextEdit,QComboBox QAbstractItemView {
    color:#f5efe3;background:rgba(16,13,26,0.94);
    border:1px solid rgba(212,175,55,0.34);
    selection-background-color:rgba(212,175,55,0.38);
}
QTabWidget::pane {
    border:1px solid rgba(220,180,82,0.36);border-radius:10px;
    background:rgba(14,12,23,0.72);
}
QTabBar::tab {
    background:rgba(24,20,36,0.82);color:#c2b6a0;
    border:1px solid rgba(212,175,55,0.22);
    border-top-left-radius:8px;border-top-right-radius:8px;
    padding:8px 20px;margin-right:3px;font-weight:bold;
}
QTabBar::tab:selected {
    background:qlineargradient(x1:0,y1:0,x2:0,y2:1,stop:0 rgba(235,188,72,0.32),stop:1 rgba(40,28,46,0.92));
    border-color:rgba(245,204,96,0.75);color:#ffe48a;
}
QStatusBar {
    background:rgba(10,9,17,0.94);color:#e4cd9f;
    border-top:1px solid rgba(212,175,55,0.32);font-size:12px;
}
QToolTip {color:#f4eee2;background:#151220;border:1px solid #d4af37;padding:5px;border-radius:6px}
"""


def _enable_dark_titlebar(window):
    """Enable native Windows 10/11 dark window titlebar so no white bar clashes with the dark-gold theme."""
    if sys.platform != 'win32':
        return
    try:
        import ctypes
        hwnd = int(window.winId())
        val = ctypes.c_int(1)
        dwmapi = ctypes.windll.dwmapi
        for attr in (20, 19):  # DWMWA_USE_IMMERSIVE_DARK_MODE (Win11 / Win10 1903+)
            dwmapi.DwmSetWindowAttribute(hwnd, attr, ctypes.byref(val), ctypes.sizeof(val))
    except Exception:
        pass


def btn(text, action, gold=False):
    b=QPushButton(text)
    b.setObjectName('GoldBtn' if gold else 'DarkBtn')
    b.clicked.connect(lambda checked=False: action())
    return b


def panel(title):
    p=QWidget(); layout=QVBoxLayout(p);layout.setContentsMargins(20,16,20,16)
    t=QLabel(title);t.setObjectName('TitleLabel');layout.addWidget(t)
    return p,layout


class WorkerSignals(QObject):
    done=Signal(bool,str)


class ResourceSignals(QObject):
    progress=Signal(str)
    synced=Signal(bool,str)


class EpicWindow(LauncherMainWindow):
    def load_items(self):
        self.res_cfg=lr.load_config()
        self.pvf_path,self.pvf_mode=lr.resolve_pvf_path(self.res_cfg)
        self.pvf_fp=lr.pvf_fingerprint(self.pvf_path)
        if self.pvf_fp and not lr.is_catalog_synced(self.pvf_fp):
            lr.try_fast_activate(self.pvf_fp)
        self.source=SourceLibrary(GameItem)
        self.client_exe=core.pre.client_exe()
        self.npk_dir,self.extra_npk_dirs,self.npk_mode=lr.resolve_npk_dirs(self.res_cfg)
        self.npk_snap=lr.npk_snapshot(self.npk_dir,self.extra_npk_dirs)
        self.ward=Wardrobe(self.npk_dir,self.extra_npk_dirs)
        self.item_icons=ItemIcons(self.npk_dir,self.extra_npk_dirs)
        self.library=core.Library(self.npk_dir)
        self.maps={int(k):v['path'] for k,v in core.read(core.CFG/'dungeons.generated.json')['maps'].items()}
        self.allowed,cards=read_cards(GameItem)
        self.by_id={it.item_id:it for it in cards}
        self.busy=False
        self._syncing_pvf=False
        self._sync_status_msg=''
        return cards

    def __init__(self):
        super().__init__()
        self.signals=WorkerSignals(self);self.signals.done.connect(self.finished)
        self.res_signals=ResourceSignals(self)
        self.res_signals.progress.connect(self._on_pvf_progress)
        self.res_signals.synced.connect(self._on_pvf_synced)
        self.timer=QTimer(self);self.timer.setInterval(5000);self.timer.timeout.connect(self.refresh_status)
        self.timer.start();self.refresh_status()
        self.resource_timer=QTimer(self);self.resource_timer.setInterval(3000)
        self.resource_timer.timeout.connect(self._poll_live_resources)
        self.resource_timer.start()
        QTimer.singleShot(350,self._poll_live_resources)
        self.statusBar().showMessage('就绪 · 本地账号 probe-us100')
        icon=self.client_exe.parent/'DFO.ico'
        if icon.is_file():self.setWindowIcon(QIcon(str(icon)))
        saved_wp=self.res_cfg.get('wallpaper','wisdom')
        idx=self.wallpaper_combo.findData(saved_wp)
        if idx>=0:
            with QSignalBlocker(self.wallpaper_combo):
                self.wallpaper_combo.setCurrentIndex(idx)
        self.wallpaper_combo.currentIndexChanged.connect(self._on_wallpaper_changed)
        self._apply_lv100_artwork()
        _enable_dark_titlebar(self)
        self._refresh_resource_ui()

    def _on_wallpaper_changed(self,*_):
        key=self.wallpaper_combo.currentData() or 'wisdom'
        self.res_cfg['wallpaper']=key
        lr.save_config(self.res_cfg)
        self._apply_lv100_artwork()
        self.statusBar().showMessage(f'已切换 100 级场景背景：{self.wallpaper_combo.currentText()}')

    def _apply_lv100_artwork(self):
        key=self.wallpaper_combo.currentData() or self.res_cfg.get('wallpaper','wisdom')
        wp=theme_art.load_wallpaper(self.npk_dir, key, tuple(self.extra_npk_dirs))
        hero=theme_art.load_hero_illust(self.npk_dir, tuple(self.extra_npk_dirs))
        if hasattr(self,'root_widget'):
            self.root_widget.set_background_image(wp)
        if hasattr(self,'hero_banner'):
            self.hero_banner.set_art(wp, hero)

    def resource_badge_text(self):
        pvf_name=self.pvf_fp['name'] if self.pvf_fp else '未选PVF'
        pvf_st='同步中…' if self._syncing_pvf else ('实时最新' if lr.is_catalog_synced(self.pvf_fp) else '待同步')
        return f'PVF: {pvf_name} ({pvf_st}) · NPK: {self.npk_dir.name} ({self.npk_snap["count"]:,})'

    def _refresh_resource_ui(self):
        if hasattr(self,'pvf_status_lbl'):
            synced=lr.is_catalog_synced(self.pvf_fp)
            txt=self._sync_status_msg or lr.format_pvf_summary(self.pvf_fp,self.pvf_mode,synced)
            self.pvf_status_lbl.setText(txt)
            self.pvf_status_lbl.setToolTip(str(self.pvf_path or ''))
            self.pvf_status_lbl.setStyleSheet(
                f'color:{"#4ade80" if synced and not self._syncing_pvf else "#ffd700"};font-size:12px;border:none;'
            )
        if hasattr(self,'npk_status_lbl'):
            mode_zh='自动实时' if self.npk_mode=='auto' else '手动指定'
            self.npk_status_lbl.setText(f'{self.npk_dir}  ({self.npk_snap["count"]:,} 个 NPK · {mode_zh})')
            self.npk_status_lbl.setToolTip(str(self.npk_dir))
        if hasattr(self,'fitting'):
            self.fitting._refresh_resource_badge()

    def _poll_live_resources(self):
        if not self.isVisible():
            return
        self.check_live_resources(auto_sync=os.environ.get('ATELIER_NO_AUTO_SYNC')!='1')

    def check_live_resources(self, auto_sync=True):
        """Detect live PVF & NPK changes on disk and hot-reload without restarting."""
        cfg=lr.load_config()
        self.res_cfg=cfg
        # 1. Check PVF
        new_pvf,pvf_mode=lr.resolve_pvf_path(cfg)
        new_fp=lr.pvf_fingerprint(new_pvf)
        pvf_changed=(new_fp != self.pvf_fp) or (pvf_mode != self.pvf_mode)
        self.pvf_path,self.pvf_mode,self.pvf_fp=new_pvf,pvf_mode,new_fp
        if new_fp and not self._syncing_pvf:
            if not lr.is_catalog_synced(new_fp):
                fast=lr.try_fast_activate(new_fp)
                if fast is not None:
                    self._reload_catalog(f'已实时切换 PVF：{new_fp["name"]}')
                elif auto_sync:
                    self._start_pvf_sync(new_pvf, force=False)
            elif pvf_changed:
                self._reload_catalog(f'已切换到实时 PVF：{new_fp["name"]}')
        # 2. Check NPK
        new_npk,new_extras,npk_mode=lr.resolve_npk_dirs(cfg)
        snap=lr.npk_snapshot(new_npk,new_extras)
        if snap['token'] != self.npk_snap['token'] or new_npk != self.npk_dir or npk_mode != self.npk_mode:
            self.npk_dir,self.extra_npk_dirs,self.npk_mode,self.npk_snap=new_npk,new_extras,npk_mode,snap
            self._apply_npk_change(silent=False)
        else:
            self._refresh_resource_ui()

    def _apply_npk_change(self, silent=False):
        self.ward.set_packs(self.npk_dir,self.extra_npk_dirs)
        self.item_icons.set_packs(self.npk_dir,self.extra_npk_dirs)
        self.library=core.Library(self.npk_dir)
        if hasattr(self,'fitting'):
            self.fitting.sync_sources()
        if hasattr(self,'drop_page'):
            self.drop_page.sync_sources()
        if self.stack.currentIndex()==1:
            self._render_page()
        self._apply_lv100_artwork()
        self._refresh_resource_ui()
        if not silent:
            self.statusBar().showMessage(f'已实时加载 NPK 目录：{self.npk_dir}（{self.npk_snap["count"]:,} 个包）')

    def _reload_catalog(self, message=''):
        old=self.source
        self.source=SourceLibrary(GameItem)
        old.close()
        # Refresh rarity & linked category dropdowns without losing current selection if valid
        cur_rar=self.rarity_combo.currentText()
        with QSignalBlocker(self.rarity_combo):
            self.rarity_combo.clear()
            self.rarity_combo.addItems(['全部稀有度']+self.source.rarities())
            idx=self.rarity_combo.findText(cur_rar)
            self.rarity_combo.setCurrentIndex(idx if idx>=0 else 0)
        cur_group=self.group_combo.currentText() if hasattr(self,'group_combo') else '全部分类'
        cur_cat=self.category_combo.currentData() or self.category_combo.currentText()
        self._populate_category_combo(cur_group, keep_raw=cur_cat)
        self.apply_filters(reset_page=False)
        if hasattr(self,'fitting'):
            self.fitting.sync_sources()
        if hasattr(self,'drop_page'):
            self.drop_page.sync_sources()
        self._refresh_resource_ui()
        if message:
            self.statusBar().showMessage(message)

    def _start_pvf_sync(self, pvf_path, force=False):
        if self._syncing_pvf or not pvf_path:
            return
        self._syncing_pvf=True
        self._sync_status_msg=f'正在同步最新 PVF：{Path(pvf_path).name}…'
        self._refresh_resource_ui()
        self.statusBar().showMessage(self._sync_status_msg)
        def worker():
            try:
                meta=lr.sync_pvf(Path(pvf_path), progress=lambda m:self.res_signals.progress.emit(m), force=force)
                cnt=sum((meta.get('counts') or {}).values())
                self.res_signals.synced.emit(True, f'已实时同步 PVF：{Path(pvf_path).name}（{cnt:,} 条目，{meta.get("sets",0):,} 套时装）')
            except Exception as exc:
                self.res_signals.synced.emit(False, f'PVF 同步失败：{exc}')
        threading.Thread(target=worker, daemon=True).start()

    def _on_pvf_progress(self, msg):
        self._sync_status_msg=msg
        self.statusBar().showMessage(msg)
        self._refresh_resource_ui()

    def _on_pvf_synced(self, ok, msg):
        self._syncing_pvf=False
        self._sync_status_msg=''
        if ok:
            self.pvf_fp=lr.pvf_fingerprint(self.pvf_path)
            self._reload_catalog(msg)
        else:
            self._refresh_resource_ui()
            self.statusBar().showMessage(msg)
            QMessageBox.warning(self,'PVF 实时同步未完成',msg)

    def _choose_pvf(self):
        start_dir=str(self.pvf_path.parent) if self.pvf_path else str(self.client_exe.parent)
        path,_=QFileDialog.getOpenFileName(self,'选择实时 PVF 文件',start_dir,'PVF 脚本包 (*.pvf *.PVF)')
        if not path:
            return
        p=Path(path)
        self.res_cfg['pvf_mode']='manual'
        self.res_cfg['pvf_path']=str(p)
        lr.save_config(self.res_cfg)
        self.pvf_path,self.pvf_mode=p,'manual'
        self.pvf_fp=lr.pvf_fingerprint(p)
        fast=lr.try_fast_activate(self.pvf_fp)
        if fast is not None:
            self._reload_catalog(f'已实时切换到 PVF：{p.name}')
        else:
            self._start_pvf_sync(p, force=False)

    def _use_auto_pvf(self):
        self.res_cfg['pvf_mode']='auto'
        self.res_cfg['pvf_path']=''
        lr.save_config(self.res_cfg)
        self.check_live_resources(auto_sync=True)
        self.statusBar().showMessage('PVF 已切换为自动跟随最新客户端 Script.pvf')

    def _force_sync_pvf(self):
        self.pvf_path,self.pvf_mode=lr.resolve_pvf_path(self.res_cfg)
        self.pvf_fp=lr.pvf_fingerprint(self.pvf_path)
        if not self.pvf_path:
            QMessageBox.warning(self,'未找到 PVF','未检测到可用的 PVF 文件，请点击「选择 PVF…」手动指定。')
            return
        self._start_pvf_sync(self.pvf_path, force=True)

    def _choose_npk_dir(self):
        folder=QFileDialog.getExistingDirectory(self,'选择 NPK 资源目录（ImagePacks2）',str(self.npk_dir))
        if not folder:
            return
        p=Path(folder)
        self.res_cfg['npk_mode']='manual'
        self.res_cfg['npk_dir']=str(p)
        lr.save_config(self.res_cfg)
        self.npk_dir,self.npk_mode=p,'manual'
        self.npk_snap=lr.npk_snapshot(self.npk_dir,self.extra_npk_dirs)
        self._apply_npk_change(silent=False)

    def _use_auto_npk(self):
        self.res_cfg['npk_mode']='auto'
        self.res_cfg['npk_dir']=''
        lr.save_config(self.res_cfg)
        self.npk_dir,self.extra_npk_dirs,self.npk_mode=lr.resolve_npk_dirs(self.res_cfg)
        self.npk_snap=lr.npk_snapshot(self.npk_dir,self.extra_npk_dirs)
        self._apply_npk_change(silent=False)

    def _refresh_npk_now(self, silent=False):
        self.npk_dir,self.extra_npk_dirs,self.npk_mode=lr.resolve_npk_dirs(self.res_cfg)
        self.npk_snap=lr.npk_snapshot(self.npk_dir,self.extra_npk_dirs)
        self._apply_npk_change(silent=silent)

    def refresh_status(self):
        state=core.gateway()
        if state:
            if state.get('avatar_gm'):
                gm_txt='支持在线物品、角色属性与时装'
            elif state.get('character_gm'):
                gm_txt='在线物品与属性（重启服务端启用实时时装）'
            elif state.get('online_item_grants'):
                gm_txt='在线物品（重启网关启用角色属性）'
            elif state.get('launcher_gm'):
                gm_txt='仅离线'
            else:
                gm_txt='旧版不支持'
            self.gateway_label.setText('网关在线  /  '+state['address']+'\nGM 发放：'+gm_txt)
        else:
            self.gateway_label.setText('网关离线\n可一键启动或仅启动网关')
        if hasattr(self,'home_srv_badge'):
            if state:
                pid=state.get('pid','-')
                self.home_srv_badge.setText('● 运行中')
                self.home_srv_badge.setStyleSheet(
                    'color:#4ade80;font-size:12px;font-weight:bold;background:rgba(34,197,94,0.16);'
                    'border:1px solid rgba(74,222,128,0.45);border-radius:8px;padding:3px 10px;'
                )
                self.home_srv_detail.setText(f'网关地址 {state["address"]}  ·  PID {pid}  ·  GM：{gm_txt}')
                self.srv_start_btn.setEnabled(False)
                self.srv_restart_btn.setEnabled(True)
                self.srv_stop_btn.setEnabled(True)
            else:
                self.home_srv_badge.setText('● 未启动')
                self.home_srv_badge.setStyleSheet(
                    'color:#fca5a5;font-size:12px;font-weight:bold;background:rgba(239,68,68,0.16);'
                    'border:1px solid rgba(248,113,113,0.45);border-radius:8px;padding:3px 10px;'
                )
                self.home_srv_detail.setText('100 级本地服务端未运行 · 可点击「启动服务端」一键拉起网关')
                self.srv_start_btn.setEnabled(True)
                self.srv_restart_btn.setEnabled(False)
                self.srv_stop_btn.setEnabled(False)
        self.refresh_delivery(state)

    def _on_start_server_clicked(self):
        self.start_work('正在启动本地 100 级服务端网关…', lambda: core.login(True))

    def _on_restart_server_clicked(self):
        if core.client_up():
            if QMessageBox.question(
                self, '确认重启服务端',
                '检测到游戏客户端正在运行，重启服务端会导致当前连接断开。\n确定要重启 100 级服务端吗？'
            ) != QMessageBox.Yes:
                return
        self.start_work('正在重启本地 100 级服务端网关…', core.restart_server)

    def _on_stop_server_clicked(self):
        if core.client_up():
            if QMessageBox.question(
                self, '确认关闭服务端',
                '检测到游戏客户端正在运行，关闭服务端会断开当前游戏连接。\n确定要关闭 100 级服务端吗？'
            ) != QMessageBox.Yes:
                return
        self.start_work('正在关闭本地 100 级服务端网关…', core.stop_server)

    def start_work(self,description,work):
        if self.busy:
            self.statusBar().showMessage('上一项操作尚未完成。');return
        self.busy=True;self.statusBar().showMessage(description)
        def run():
            try:self.signals.done.emit(True,work() or '操作完成')
            except Exception as exc:self.signals.done.emit(False,str(exc))
        threading.Thread(target=run,daemon=True).start()

    def finished(self,ok,message):
        self.busy=False;self.statusBar().showMessage(message)
        self.refresh_status();self.refresh_mods();self.font_panel.refresh()
        self._refresh_npk_now(silent=True)
        if ok and hasattr(self,'mod_panel') and ('PVF' in message or '原版' in message):
            self.check_live_resources(auto_sync=os.environ.get('ATELIER_NO_AUTO_SYNC')!='1')
        if not ok:QMessageBox.warning(self,'操作未完成',message)

    def closeEvent(self,event):
        if self.busy:
            self.statusBar().showMessage('操作进行中，请在完成后关闭窗口。');event.ignore()
        else:
            self.resource_timer.stop()
            super().closeEvent(event)

    def _on_launch_clicked(self):
        self.start_work('正在检查网关并启动 100US 客户端…',core.login)

    def decorate_home(self,layout):
        # 100 级服务端启停快捷控制条
        srv_card=QFrame();srv_card.setObjectName('PanelCard')
        sl=QHBoxLayout(srv_card);sl.setContentsMargins(18,10,18,10);sl.setSpacing(10)
        s_ico=QLabel();s_ico.setPixmap(ui_icons.render_pixmap('server',18,active=True))
        s_ico.setStyleSheet('border:none;background:transparent;')
        sl.addWidget(s_ico)
        s_title=QLabel('100 级服务端控制')
        s_title.setStyleSheet('font-size:14px;font-weight:bold;color:#ffe285;border:none;background:transparent;')
        sl.addWidget(s_title)
        self.home_srv_badge=QLabel('● 检测中')
        sl.addWidget(self.home_srv_badge)
        self.home_srv_detail=QLabel('正在检测本地网关状态…')
        self.home_srv_detail.setStyleSheet('font-size:12px;color:#d6cbb7;border:none;background:transparent;')
        sl.addWidget(self.home_srv_detail,1)

        self.srv_start_btn=btn(' 启动服务端',self._on_start_server_clicked,True)
        self.srv_start_btn.setIcon(ui_icons.vector_icon('server_start',15,active=True,dark_fg=True))
        self.srv_start_btn.setIconSize(QSize(15,15))
        self.srv_start_btn.setCursor(Qt.PointingHandCursor)

        self.srv_restart_btn=btn(' 重启服务端',self._on_restart_server_clicked)
        self.srv_restart_btn.setIcon(ui_icons.vector_icon('refresh',15,active=True))
        self.srv_restart_btn.setIconSize(QSize(15,15))
        self.srv_restart_btn.setCursor(Qt.PointingHandCursor)

        self.srv_stop_btn=btn(' 关闭服务端',self._on_stop_server_clicked)
        self.srv_stop_btn.setIcon(ui_icons.vector_icon('server_stop',15,active=True))
        self.srv_stop_btn.setIconSize(QSize(15,15))
        self.srv_stop_btn.setCursor(Qt.PointingHandCursor)
        self.srv_stop_btn.setStyleSheet(
            "QPushButton{background:rgba(76,20,30,0.82);color:#fecaca;"
            "border:1px solid rgba(248,113,113,0.52);border-radius:8px;padding:7px 14px;font-size:12px;font-weight:bold;}"
            "QPushButton:hover{background:rgba(153,27,27,0.92);border-color:#fca5a5;color:#ffffff;}"
            "QPushButton:disabled{background:rgba(28,22,34,0.45);color:#756b7d;border-color:rgba(160,145,170,0.22);}"
        )

        sl.addWidget(self.srv_start_btn)
        sl.addWidget(self.srv_restart_btn)
        sl.addWidget(self.srv_stop_btn)
        layout.addWidget(srv_card)

        res_card=QFrame();res_card.setObjectName('PanelCard')
        rl=QVBoxLayout(res_card);rl.setContentsMargins(18,13,18,13);rl.setSpacing(7)
        r_hdr=QHBoxLayout();r_hdr.setSpacing(7)
        r_ico=QLabel();r_ico.setPixmap(ui_icons.render_pixmap('refresh',16,active=True))
        r_ico.setStyleSheet('border:none;background:transparent;')
        r_hdr.addWidget(r_ico)
        r_title=QLabel('实时资源源 · PVF 与 NPK 自动检测与切换')
        r_title.setStyleSheet('font-size:14px;font-weight:bold;color:#ffe285;border:none;background:transparent;')
        r_hdr.addWidget(r_title)
        r_sub=QLabel('实时追踪客户端文件变动 · 自动热重载')
        r_sub.setStyleSheet('font-size:11px;color:#92d6c8;border:none;background:transparent;')
        r_hdr.addStretch();r_hdr.addWidget(r_sub)
        rl.addLayout(r_hdr)

        pvf_row=QHBoxLayout();pvf_row.setSpacing(8)
        lp=QLabel('PVF 脚本源:');lp.setStyleSheet('color:#d4c6a8;border:none;background:transparent;')
        pvf_row.addWidget(lp)
        self.pvf_status_lbl=QLabel('检测中…');self.pvf_status_lbl.setWordWrap(True)
        pvf_row.addWidget(self.pvf_status_lbl,1)
        pvf_row.addWidget(btn('自动最新',self._use_auto_pvf))
        pvf_row.addWidget(btn('选择 PVF…',self._choose_pvf))
        sync_b=btn(' 立即同步',self._force_sync_pvf,True)
        sync_b.setIcon(ui_icons.vector_icon('refresh',15,active=True,dark_fg=True));sync_b.setIconSize(QSize(15,15))
        pvf_row.addWidget(sync_b)
        rl.addLayout(pvf_row)

        npk_row=QHBoxLayout();npk_row.setSpacing(8)
        ln=QLabel('NPK 资源包:');ln.setStyleSheet('color:#d4c6a8;border:none;background:transparent;')
        npk_row.addWidget(ln)
        self.npk_status_lbl=QLabel('检测中…');self.npk_status_lbl.setWordWrap(True)
        self.npk_status_lbl.setStyleSheet('color:#82cabc;font-size:12px;border:none;background:transparent;')
        npk_row.addWidget(self.npk_status_lbl,1)
        npk_row.addWidget(btn('自动客户端',self._use_auto_npk))
        npk_row.addWidget(btn('选择 NPK 目录…',self._choose_npk_dir))
        npk_row.addWidget(btn('刷新 NPK',lambda:self._refresh_npk_now(False)))
        rl.addLayout(npk_row)
        layout.addWidget(res_card)

        # 三栏快捷功能殿堂卡片
        tiles_row=QHBoxLayout();tiles_row.setSpacing(12)
        feature_specs=[
            ('wardrobe','可视化时装试衣间','11 部位人偶 · 武器装扮 · 动态光环 · 万套礼包',3,'进入试衣间'),
            ('character','角色属性与背包','等级 / SP / TP / 转职 / 点券金币 / 任务一键管理',5,'管理角色'),
            ('mods','Mod 工坊与字体','90JP 规范合并入 PVF · 字体定制 · 原版一键还原',4,'打开工坊'),
        ]
        for icon_name,heading,desc,page_idx,cta in feature_specs:
            tile=QFrame();tile.setObjectName('FeatureTile')
            tl=QVBoxLayout(tile);tl.setContentsMargins(16,13,16,13);tl.setSpacing(6)
            hrow=QHBoxLayout();hrow.setSpacing(7)
            hi=QLabel();hi.setPixmap(ui_icons.render_pixmap(icon_name,18,active=True))
            hi.setStyleSheet('border:none;background:transparent;')
            ht=QLabel(heading)
            ht.setStyleSheet('font-size:14px;font-weight:bold;color:#ffe285;border:none;background:transparent;')
            hrow.addWidget(hi);hrow.addWidget(ht);hrow.addStretch()
            dt=QLabel(desc);dt.setWordWrap(True)
            dt.setStyleSheet('font-size:11px;color:#c4b8a2;border:none;background:transparent;')
            tl.addLayout(hrow);tl.addWidget(dt);tl.addStretch()
            act=btn(f'{cta}  →',lambda idx=page_idx:self.switch_page(idx))
            act.setCursor(Qt.PointingHandCursor)
            tl.addWidget(act,0,Qt.AlignLeft)
            tiles_row.addWidget(tile,1)
        layout.addLayout(tiles_row)

    def configure_recipient(self,layout):
        self.recipient=QComboBox();layout.addWidget(QLabel('接收角色  /  入队时锁定'));layout.addWidget(self.recipient)
        self.refresh_roles();layout.addWidget(btn('刷新角色',self.refresh_roles))
        self.reason=QLineEdit('GM 发放');self.reason.setMaxLength(160);self.reason.setPlaceholderText('发放原因')
        layout.addWidget(self.reason)

    def refresh_roles(self):
        old=self.recipient.currentData();self.recipient.clear()
        for ident,name in core.roles():self.recipient.addItem(f'{name}  /  {ident}',ident)
        n=self.recipient.findData(old)
        if n>=0:self.recipient.setCurrentIndex(n)

    def configure_receipts(self,layout):
        latest=(0,'')
        for folder in ('pending','receipts'):
            for p in (core.DATA/folder).glob('*.json'):
                try:latest=max(latest,(p.stat().st_mtime_ns,p.stem))
                except FileNotFoundError:pass
        self.last_grant=latest[1]
        self.delivery=QLabel();self.delivery.setWordWrap(True);layout.addWidget(self.delivery)
        layout.addWidget(btn('查询网关回执',self.receipts))
        layout.addWidget(btn('仅启动网关',lambda:self.start_work('正在检查网关…',lambda:core.login(True))))

    def grant_text(self,d):
        if 'error' in d:return '发放失败：'+str(d['error'])
        if 'op_receipt' in d:
            rec=d['op_receipt']
            return '角色属性已生效：'+str(rec.get('summary') or rec.get('action') or '完成')
        if d.get('action'):
            return f'角色属性排队中：[{d["action"]}] 角色 {d.get("character_id","-")} · {d.get("reason","")}'
        confirmed='receipt' in d
        items=d.get('receipt',d).get('items',[])
        if not items:return '回执无物品明细，请核对网关记录'
        it=items[0];ident=it['Template' if confirmed else 'template'];n=it['Amount' if confirmed else 'amount']
        info=self.source.db.execute('select name,cat from item where id=? order by grantmode desc limit 1',(ident,)).fetchone()
        name=f'{info[0]}（{info[1]}）' if info else str(ident)
        if confirmed:return f'已入包：{name} ×{n} · 槽位 '+', '.join(map(str,it.get('Slots',[])))
        return f'已排队，尚未入包：角色 {d["character_id"]} · {name} ×{n}'

    def refresh_delivery(self,state):
        if not self.last_grant:
            self.delivery.setText('确认提交后，在这里查看到账状态。');return
        # The gateway may publish the receipt and remove pending between reads.
        for folder in ('receipts','pending','receipts'):
            try:d=core.read(core.DATA/folder/(self.last_grant+'.json'))
            except FileNotFoundError:continue
            msg=self.grant_text(d)
            if folder=='pending':
                msg+='\n'+('网关处理中，在线角色无需退出。请勿重复提交。' if state and state.get('online_item_grants') else
                            '所有游戏客户端正常退出后，网关才会处理。请勿重复提交。' if state and state.get('launcher_gm')
                            else '请启动支持 GM 的网关处理原单，无需重发。')
            self.delivery.setText('最近一笔 · '+msg);return
        self.delivery.setText('未找到原单或回执，请先查询记录，不要重复提交。')

    def _on_add_item_to_queue(self,item):
        if not item.grantmode:raise ValueError('该源物品不支持普通背包发放。')
        role=self.recipient.currentData()
        if role is None:raise ValueError('请先创建并选择接收角色。')
        qty=self.qty_spin.value();target=self.recipient.currentText()
        self.added_queue.append((role,item.item_id,qty))
        self.queue_list.addItem(f'{item.name} × {qty} → {target}')

    def _confirm_add_items(self):
        if not self.added_queue:raise ValueError('请先将物品加入待发送队列。')
        reason=self.reason.text().strip()
        if not reason:raise ValueError('请填写发放原因。')
        if QMessageBox.question(self,'确认发放',f'将列表中的 {len(self.added_queue)} 笔物品提交给所示角色？每笔独立入账。')!=QMessageBox.Yes:return
        submitted=0
        while self.added_queue:
            role,ident,amount=self.added_queue[0]
            self.last_grant=core.submit(role,ident,amount,reason,self.source)
            self.added_queue.pop(0);self.queue_list.takeItem(0);submitted+=1
        self.refresh_delivery(core.gateway())
        self.statusBar().showMessage(f'已提交 {submitted} 笔；以网关入包回执为准。')

    def receipts(self):
        dialog=QDialog(self);dialog.setWindowTitle('网关发放回执');dialog.resize(720,400)
        v=QVBoxLayout(dialog);rows=QListWidget();v.addWidget(rows)
        def refresh():
            rows.clear()
            files=[]
            for folder in ('pending','receipts'):
                for p in (core.DATA/folder).glob('*.json'):
                    try:files.append((p.stat().st_mtime,p))
                    except FileNotFoundError:pass
            for _,p in sorted(files,reverse=True)[:150]:
                try:d=core.read(p)
                except FileNotFoundError:continue
                s=self.grant_text(d)
                rows.addItem(f'{p.stem[:10]}    {s}')
            if not rows.count():rows.addItem('暂无发放记录')
        v.addWidget(btn('刷新',refresh));refresh();dialog.exec()

    def _build_abyss_page(self):
        self.drop_page=DropConfigPage(self)
        return self.drop_page

    def save_live(self,disable=False):
        pct=0 if disable else self.chance.value()
        text=self.gear.currentText().strip()
        idx=self.gear.findText(text,Qt.MatchExactly)
        ident=self.gear.itemData(idx) if idx>=0 else int(text) if text.isdecimal() else 0
        maps=[v.data(Qt.UserRole) for v in self.rooms.selectedItems()]
        if pct and (not maps or self.allowed.get(ident,[None])[0]!='装备'):
            raise ValueError('请选中至少一个房间，并选取有效的装备。')
        if QMessageBox.question(self,'确认房间规则',f'保存 {pct}% 额外掉率到本机网关？')!=QMessageBox.Yes:return
        core.save_rule(maps,ident or 0,pct,self.allowed,self.maps)
        self.chance.setValue(pct);self.statusBar().showMessage('已保存。新副本首杀时读取；原掉落表不变。')

    def preset_value(self):
        raw=self.custom_ids_edit.text().replace('，',',').strip()
        ids=[]
        for token in raw.split(',') if raw else []:
            if not token.strip().isdecimal():raise ValueError('物品池只能填写以逗号分隔的装备 ID。')
            ident=int(token)
            if self.allowed.get(ident,[None])[0]!='装备':raise ValueError(f'模板 {ident} 不在可发装备目录中。')
            if ident not in ids:ids.append(ident)
        if self.pool_mode_combo.currentIndex()==2 and not ids:raise ValueError('自定义池不能为空。')
        return dict(scope='GLOBAL_ABYSS',enabled=self.abyss_enable_chk.isChecked(),drop_rate_multiplier=self.drop_rate_spin.value(),
                    mythic_weight_percent=self.mythic_slider.value(),epic_weight_percent=self.epic_slider.value(),
                    pool_mode=self.pool_mode_combo.currentText(),custom_item_ids=ids)

    def restore_preset(self):
        if PRESET.is_file():
            d=core.read(PRESET)
            self.abyss_enable_chk.setChecked(bool(d.get('enabled')))
            self.drop_rate_spin.setValue(d.get('drop_rate_multiplier',3))
            self.mythic_slider.setValue(d.get('mythic_weight_percent',12));self.epic_slider.setValue(d.get('epic_weight_percent',45))
            self.pool_mode_combo.setCurrentText(d.get('pool_mode',''))
            self.custom_ids_edit.setText(','.join(map(str,d.get('custom_item_ids',[]))))
            self.config_preview.setPlainText(json.dumps(d,ensure_ascii=False,indent=2))
        else:self.config_preview.setPlainText('尚未导出预设；打开此页不会写入任何掉落配置。')

    def save_preset(self):
        d=self.preset_value();core.atomic(PRESET,d)
        self.config_preview.setPlainText(json.dumps(d,ensure_ascii=False,indent=2))
        self.statusBar().showMessage(f'仅导出预设，未修改网关规则：{PRESET}')
        QMessageBox.information(self,'预设已导出',f'{PRESET}\n此文件不会改变游戏内掉率。')

    def build_wardrobe(self):
        self.fitting=FittingRoom(self);self.figure=self.fitting.figure
        return self.fitting

    def build_character(self):
        self.character_page=CharacterPage(self)
        return self.character_page

    def build_activities(self):
        self.activity_page=ActivityCenterPage(self)
        return self.activity_page

    def switch_page(self,index):
        super().switch_page(index)
        if index==1:self._render_page()
        if index==3:self.fitting.activate()
        if index==4:self.refresh_mods();self.font_panel.refresh()
        if index==5:self.character_page.activate()
        if index==6:self.activity_page.activate()

    def build_mods(self):
        page,lay=panel('Mod 工坊')
        self.font_panel=FontPanel(self);lay.addWidget(self.font_panel)
        self.mod_panel=ModWorkshopPanel(self);lay.addWidget(self.mod_panel,1)
        self.mods=self.mod_panel.list
        return page

    def refresh_mods(self):
        if hasattr(self,'mod_panel'):
            self.mod_panel.refresh()


def apply_theme(app):
    app.setStyle('Fusion')
    palette=app.palette()
    for role,value in [(QPalette.Window,'#100e15'),(QPalette.WindowText,'#efe7d8'),(QPalette.Base,'#191620'),
                       (QPalette.Text,'#efe7d8'),(QPalette.Button,'#292330'),(QPalette.ButtonText,'#efe7d8'),
                       (QPalette.Highlight,'#856b39'),(QPalette.HighlightedText,'#ffffff')]:
        palette.setColor(role,QColor(value))
    app.setPalette(palette);app.setStyleSheet(STYLESHEET+EXTRA_STYLE)


def main():
    app=QApplication(sys.argv);apply_theme(app)
    core.DATA.mkdir(parents=True,exist_ok=True);lock=(core.DATA/'window.lock').open('a+b')
    try:msvcrt.locking(lock.fileno(),msvcrt.LK_NBLCK,1)
    except OSError:
        lock.close();QMessageBox.information(None,'地下城与勇士 · 智慧的引导','登录器已经运行。若打开的是旧版，请先关闭旧窗口再启动。');return
    def report(typ,err,tb):
        import traceback
        trace=''.join(traceback.format_exception(typ,err,tb))
        (core.ROOT/'runtime'/'launcher-error.log').write_text(trace,encoding='utf-8')
        QMessageBox.warning(app.activeWindow(),'操作未完成',str(err))
    sys.excepthook=report
    try:
        win=EpicWindow()
        size=app.primaryScreen().availableGeometry()
        win.resize(min(1180,size.width()-24),min(760,size.height()-48))
        win.show();app.exec()
    finally:lock.close()
