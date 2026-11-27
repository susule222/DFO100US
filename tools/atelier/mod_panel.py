"""90JP-style Unified PVF + NPK Mod Workshop UI panel."""
import os
from pathlib import Path

from PySide6.QtCore import QSize, Qt
from PySide6.QtWidgets import (
    QFileDialog,
    QFrame,
    QHBoxLayout,
    QLabel,
    QListWidget,
    QListWidgetItem,
    QMessageBox,
    QPushButton,
    QVBoxLayout,
)

from mod_workshop import ModWorkshop
import ui_icons


class ModWorkshopPanel(QFrame):
    """90JP-style Mod Manager: collection/sub-module check, priority order, merge into Script.pvf + NPK sync."""

    def __init__(self, owner):
        super().__init__()
        self.owner = owner
        self.setObjectName('PanelCard')
        self.workshop = ModWorkshop(owner.client_exe.parent)
        self.modules = []

        lay = QVBoxLayout(self)
        lay.setContentsMargins(16, 13, 16, 13)
        lay.setSpacing(8)

        # Header
        hdr = QHBoxLayout()
        hdr.setSpacing(7)
        h_ico = QLabel()
        h_ico.setPixmap(ui_icons.render_pixmap('mods', 18, active=True))
        h_ico.setStyleSheet('border:none;background:transparent;')
        hdr.addWidget(h_ico)
        title = QLabel('Mod 工坊 · 合并进入 PVF（90JP 规范）')
        title.setStyleSheet('font-size:15px;font-weight:bold;color:#ffe285;border:none;background:transparent;')
        hdr.addWidget(title)
        hdr.addStretch()
        self.merge_badge = QLabel('原版 Script.pvf')
        self.merge_badge.setStyleSheet(
            'color:#92d6c8;font-size:11px;padding:3px 9px;background:rgba(16,14,26,0.85);'
            'border:1px solid rgba(146,214,200,0.4);border-radius:6px;'
        )
        hdr.addWidget(self.merge_badge)
        lay.addLayout(hdr)

        sub = QLabel(
            f'MOD 目录：{self.workshop.mod_dir}  ·  支持 90JP 合集 ZIP（含 PVF/ 与 NPK/ 及两级 version.json）、'
            '标准 PVF/NPK ZIP 与独立 NPK；排序靠前者优先级更高（普通文件、.lst 索引与 .str 词条冲突时高位胜出）。'
        )
        sub.setObjectName('SubLabel')
        sub.setWordWrap(True)
        lay.addWidget(sub)

        # Top toolbar
        bar = QHBoxLayout()
        bar.setSpacing(8)

        def mkbtn(text, fn, gold=False, icon_name=''):
            b = QPushButton((' ' + text) if icon_name else text)
            b.setObjectName('GoldBtn' if gold else 'DarkBtn')
            b.setCursor(Qt.PointingHandCursor)
            if icon_name:
                b.setIcon(ui_icons.vector_icon(icon_name, 15, active=True, dark_fg=gold))
                b.setIconSize(QSize(15, 15))
            b.clicked.connect(lambda checked=False: fn())
            return b

        bar.addWidget(mkbtn('导入 Mod (ZIP / NPK)', self.import_mod, True, 'import_pkg'))
        bar.addWidget(mkbtn('重新扫描', self.refresh, False, 'refresh'))
        bar.addWidget(mkbtn('打开 MOD 目录', self.open_mod_dir, False, 'folder'))
        bar.addStretch()
        bar.addWidget(mkbtn('上移优先级', lambda: self.move_selected(-1), False, 'up'))
        bar.addWidget(mkbtn('下移优先级', lambda: self.move_selected(1), False, 'down'))
        lay.addLayout(bar)

        # Mod list with checkboxes
        self.list = QListWidget()
        self.list.itemChanged.connect(self._on_item_toggled)
        lay.addWidget(self.list, 1)

        # Bottom merge / restore action bar
        bot = QHBoxLayout()
        bot.setSpacing(10)
        self.merge_btn = mkbtn('保存并合并到 PVF', self.apply_merge, True, 'merge')
        self.restore_btn = mkbtn('还原原版 PVF 与 NPK', self.restore_original, False, 'restore')
        bot.addWidget(self.merge_btn)
        bot.addWidget(self.restore_btn)
        bot.addStretch()
        self.summary_lbl = QLabel()
        self.summary_lbl.setObjectName('SubLabel')
        bot.addWidget(self.summary_lbl)
        lay.addLayout(bot)

        self.refresh()

    def open_mod_dir(self):
        p = self.workshop.ensure_mod_dir()
        os.startfile(p)

    def refresh(self):
        self.list.blockSignals(True)
        cur_item = self.list.currentItem()
        cur_id = cur_item.data(Qt.UserRole) if cur_item else None
        self.list.clear()
        self.modules = self.workshop.scan()
        for idx, m in enumerate(self.modules):
            tags = []
            if m['pvf_entries']:
                tags.append(f'PVF:{len(m["pvf_entries"])}')
            if m['npk_entries']:
                tags.append(f'NPK:{len(m["npk_entries"])}')
            if m['error']:
                tags.append('异常:' + m['error'])
            elif m['missing_deps']:
                tags.append('缺依赖:' + ','.join(m['missing_deps']))
            badge = '  |  '.join(tags) if tags else '无组件'
            col = f"[{m['collection_name']}] " if m['collection_name'] else ''
            text = f"#{idx + 1:02d}   {col}{m['name']}  v{m['version']}    ·    {badge}"
            item = QListWidgetItem(text)
            item.setData(Qt.UserRole, m['id'])
            item.setFlags(item.flags() | Qt.ItemIsUserCheckable | Qt.ItemIsSelectable | Qt.ItemIsEnabled)
            item.setCheckState(Qt.Checked if m['enabled'] else Qt.Unchecked)
            tip_lines = [
                f"ID: {m['id']}",
                f"合集: {m['collection_name']}",
                f"文件: {Path(m['source_path']).name}",
                f"说明: {m['description'] or '无'}",
                f"PVF 文件: {len(m['pvf_entries'])} · NPK 资源: {len(m['npk_entries'])}",
            ]
            if m['dependencies']:
                tip_lines.append('依赖: ' + ', '.join(m['dependencies']))
            if m['missing_deps']:
                tip_lines.append('⚠ 缺失依赖（本次合并将被排除）: ' + ', '.join(m['missing_deps']))
            if m['error']:
                tip_lines.append('⚠ 错误: ' + m['error'])
            item.setToolTip('\n'.join(tip_lines))
            self.list.addItem(item)
            if m['id'] == cur_id:
                self.list.setCurrentItem(item)

        if not self.modules:
            placeholder = QListWidgetItem('MOD 目录中暂无模组 · 点击「导入 Mod (ZIP / NPK)」或将 90JP 合集 ZIP 放入 MOD 目录后重新扫描')
            placeholder.setFlags(Qt.NoItemFlags)
            self.list.addItem(placeholder)
        elif self.list.currentRow() < 0:
            self.list.setCurrentRow(0)
        self.list.blockSignals(False)
        self._update_badges()

    def _update_badges(self):
        state = self.workshop.load_state()
        rep = state.get('pvf_report')
        if state.get('applied_pvf_sha256') and isinstance(rep, dict):
            self.merge_badge.setText(
                f"已合并 PVF · 覆盖 {rep.get('replaced_files', 0)} / 新增 {rep.get('added_files', 0)} 文件 "
                f"({len(state.get('enabled_ids') or [])} 模组)"
            )
            self.merge_badge.setStyleSheet(
                'color:#ffe48a;font-size:11px;font-weight:bold;padding:3px 9px;background:rgba(68,52,22,0.88);'
                'border:1px solid #ffd700;border-radius:6px;'
            )
        else:
            npk_n = len(state.get('installed_npks') or {})
            txt = f'原版 Script.pvf · 已部署 {npk_n} 个 Mod NPK' if npk_n else '原版 Script.pvf（未合并 Mod）'
            self.merge_badge.setText(txt)
            self.merge_badge.setStyleSheet(
                'color:#92d6c8;font-size:11px;padding:3px 9px;background:rgba(16,14,26,0.85);'
                'border:1px solid rgba(146,214,200,0.4);border-radius:6px;'
            )
        eff = [m for m in self.modules if m['effective']]
        pvf_cnt = sum(len(m['pvf_entries']) for m in eff)
        npk_cnt = sum(len(m['npk_entries']) for m in eff)
        self.summary_lbl.setText(f'已勾选有效模组：{len(eff)} 个（待合并 PVF 文件 {pvf_cnt} 个 · NPK {npk_cnt} 个）')

    def _persist_current_selection(self):
        order = [m['id'] for m in self.modules]
        enabled = []
        for i in range(self.list.count()):
            it = self.list.item(i)
            mid = it.data(Qt.UserRole)
            if mid and it.checkState() == Qt.Checked:
                enabled.append(mid)
        self.workshop.save_selection(order, enabled)

    def _on_item_toggled(self, item):
        if not item.data(Qt.UserRole):
            return
        self._persist_current_selection()
        self.refresh()

    def move_selected(self, delta: int):
        row = self.list.currentRow()
        if row < 0 or row >= len(self.modules):
            return
        target = row + delta
        if not (0 <= target < len(self.modules)):
            return
        self.modules[row], self.modules[target] = self.modules[target], self.modules[row]
        self._persist_current_selection()
        cur_id = self.modules[target]['id']
        self.refresh()
        for i in range(self.list.count()):
            if self.list.item(i).data(Qt.UserRole) == cur_id:
                self.list.setCurrentRow(i)
                break

    def import_mod(self):
        path, _ = QFileDialog.getOpenFileName(
            self,
            '导入 Mod 包（90JP 合集 ZIP 或 NPK 资源）',
            str(self.workshop.ensure_mod_dir()),
            'Mod 包 (*.zip *.npk *.NPK);;全部文件 (*.*)',
        )
        if not path:
            return
        def work():
            dst = self.workshop.import_mod_file(Path(path))
            return f'已导入 Mod：{dst.name}（勾选后点击「保存并合并到 PVF」生效）。'
        self.owner.start_work('正在校验并导入 Mod 包…', work)

    def apply_merge(self):
        eff = [m for m in self.modules if m['effective']]
        pvf_cnt = sum(len(m['pvf_entries']) for m in eff)
        npk_cnt = sum(len(m['npk_entries']) for m in eff)
        msg = (
            f'将合并 {len(eff)} 个有效模组（PVF 文件 {pvf_cnt} 个、NPK {npk_cnt} 个）到客户端 Script.pvf，'
            '首次合并会自动备份原版 Script.pvf，并在写入前完成整包回读校验。\n\n确认执行？'
            if eff else
            '当前没有勾选任何有效模组，确认清除已合并的 Mod 并还原为原版 Script.pvf 与 NPK？'
        )
        if QMessageBox.question(self, '确认保存并合并到 PVF', msg) != QMessageBox.Yes:
            return

        def work():
            res = self.workshop.apply_merge(progress=lambda m: self.owner.res_signals.progress.emit(m))
            rep = res.get('pvf_report')
            if rep:
                return (
                    f'PVF 合并与回读校验完成：覆盖 {rep["replaced_files"]} / 新增 {rep["added_files"]} 文件'
                    f'（.lst 合并 {rep["merged_lists"]}、.str 合并 {rep["merged_strings"]}），'
                    f'部署 {res["npk_count"]} 个 NPK。登录器将自动同步最新 PVF 索引。'
                )
            return f'已完成资源同步（原版 Script.pvf · 部署 {res["npk_count"]} 个 NPK）。'

        self.owner.start_work('正在将 Mod 合并入 Script.pvf 并回读校验…', work)

    def restore_original(self):
        if QMessageBox.question(
            self,
            '还原原版 PVF 与 NPK',
            '确认还原客户端为未合并 Mod 的原版 Script.pvf，并移除/恢复全部 Mod NPK？',
        ) != QMessageBox.Yes:
            return

        def work():
            return self.workshop.restore_original(progress=lambda m: self.owner.res_signals.progress.emit(m))

        self.owner.start_work('正在还原原版 Script.pvf 与 NPK…', work)
