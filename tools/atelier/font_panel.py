from pathlib import Path
from PySide6.QtWidgets import (QComboBox,QFileDialog,QFrame,QHBoxLayout,QLabel,
                               QMessageBox,QPushButton,QVBoxLayout)
from game_fonts import GameFonts,ORIGINAL,faces


class FontPanel(QFrame):
    def __init__(self,owner):
        super().__init__();self.owner=owner;self.service=GameFonts(owner.client_exe.parent/'Fonts')
        self.setObjectName('PanelCard');layout=QVBoxLayout(self)
        title=QLabel('游戏字体');title.setObjectName('TitleLabel');layout.addWidget(title)
        self.status=QLabel();self.status.setWordWrap(True);layout.addWidget(self.status)
        row=QHBoxLayout();layout.addLayout(row)
        self.source=QComboBox();self.face=QComboBox()
        row.addWidget(self.source,2);row.addWidget(self.face,1)
        choose=QPushButton('选择本地字体…');choose.setObjectName('DarkBtn');choose.clicked.connect(self.choose);row.addWidget(choose)
        line=QHBoxLayout();layout.addLayout(line)
        self.apply_btn=QPushButton('应用字体');self.apply_btn.setObjectName('GoldBtn')
        self.apply_btn.clicked.connect(self.apply);line.addWidget(self.apply_btn)
        restore=QPushButton('还原原版字体');restore.setObjectName('DarkBtn');restore.clicked.connect(self.restore);line.addWidget(restore)
        line.addStretch();self.source.currentIndexChanged.connect(self.load_faces)
        active=self.service.state().get('file','simsun.ttc')
        for p in sorted(self.service.folder.glob('*')):
            if p.is_file() and p.suffix.lower() in ('.ttf','.ttc') and p.name.lower() not in ORIGINAL:self.add(p)
        if self.source.findText(active)>=0:self.source.setCurrentText(active)
        self.load_faces()
        saved_face=self.service.state().get('face',0)
        if self.source.currentText()==active and saved_face<self.face.count():self.face.setCurrentIndex(saved_face)
        self.refresh()
    def add(self,p):
        val=str(p.resolve())
        if self.source.findData(val)<0:self.source.addItem(p.name,val)
        return self.source.findData(val)
    def load_faces(self,*_):
        self.face.clear();source=self.source.currentData();error=''
        if source:
            try:self.face.addItems(faces(Path(source)))
            except Exception as e:error=str(e)
        if error:self.face.setToolTip(error)
        else:self.face.setToolTip('TTC 字体集合可在这里选字面。')
        self.apply_btn.setEnabled(self.face.count()>0)
    def refresh(self):
        name=self.service.state().get('family')
        if (self.service.root/'pending.json').exists():text='上次操作中断，下次应用或还原时将先恢复。'
        elif any(not (self.service.folder/n).is_file() for n in ORIGINAL):text='缺少启动字体。请退出游戏，选好字体后点「应用字体」。'
        else:text='上次设置：'+(name or '未由登录器设置')
        self.status.setText(text+'  ·  仅修改本机 100US 游戏字体，不安装到 Windows。')
    def choose(self):
        p,_=QFileDialog.getOpenFileName(self,'选择游戏字体',str(self.service.folder),'TrueType (*.ttf *.ttc)')
        if p:self.source.setCurrentIndex(self.add(Path(p)))
    def apply(self):
        source=self.source.currentData();face=self.face.currentIndex()
        if source and face>=0:
            self.owner.start_work('正在生成并校验两个游戏字体入口…',lambda:self.service.apply(source,face))
    def restore(self):
        if QMessageBox.question(self,'还原游戏字体','还原游戏自带的 Tahoma 和 Calibri？导入的源字体将保留。')==QMessageBox.Yes:
            self.owner.start_work('正在还原游戏字体…',self.service.restore)
