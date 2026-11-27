import math
import os
from pathlib import Path
import queue
import random
import sys
import threading
import tkinter as tk
from tkinter import filedialog, messagebox, ttk

sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
from PIL import Image, ImageTk
import core as c
from sprites import Wardrobe, SLOTS, index, frame

BG='#10141c'
SIDE='#131923'
PANEL='#1a232e'
GOLD='#edbc76'
WHITE='#f7edda'
MUTED='#a8b1b6'
TEAL='#82cabc'
FONT='Microsoft YaHei UI'
SLOT_ZH=dict(zip(SLOTS,'肤色 鞋子 下装 上衣 腰部 胸部 脸部 头发 帽子'.split()))
PAGES=['启航大厅','物品发放','时装试衣间','深渊掉落','Mod 工坊']


def label(parent,text,size=10,color=WHITE,bg=None,bold=False,**kw):
    return tk.Label(parent,text=text,font=(FONT,size,'bold' if bold else 'normal'),fg=color,
                    bg=bg or parent['bg'],anchor='w',**kw)


def button(parent,text,command,primary=False):
    return tk.Button(parent,text=text,command=command,bg=GOLD if primary else '#26333e',
                     fg=BG if primary else WHITE,activebackground='#f4d39c',activeforeground=BG,
                     relief='flat',bd=0,padx=15,pady=7,cursor='hand2',font=(FONT,10,'bold'))


def row(parent):
    f=tk.Frame(parent,bg=parent['bg']); f.pack(fill='x',pady=5); return f


def entry(parent,default='',width=20):
    e=tk.Entry(parent,bg='#202b38',fg=WHITE,insertbackground=GOLD,relief='flat',
               font=(FONT,11),width=width,highlightthickness=1,highlightbackground='#3a4653')
    e.insert(0,str(default));return e


def listing(parent, height=8, multi=False):
    f=tk.Frame(parent,bg=BG);f.pack(fill='both',expand=True,pady=4)
    box=tk.Listbox(f,height=height,bg=PANEL,fg=WHITE,selectbackground='#395a60',
                   selectforeground='#ffffff',font=(FONT,10),relief='flat',borderwidth=0,
                   highlightthickness=0,activestyle='none',exportselection=False,
                   selectmode='extended' if multi else 'browse')
    bar=ttk.Scrollbar(f,command=box.yview)
    box.configure(yscrollcommand=bar.set)
    bar.pack(side='right',fill='y');box.pack(fill='both',expand=True)
    return box


class Atlas:
    def __init__(self,root):
        self.root=root
        root.title('阿拉德星图  |  DFO · US100')
        root.configure(bg=BG)
        root.geometry('1020x690')
        root.minsize(890,630)
        ico=c.pre.client_exe().parent/'DFO.ico'
        if ico.exists(): root.iconbitmap(str(ico))
        self.packs=c.pre.client_exe().parent/'ImagePacks2'
        self.ward=Wardrobe(self.packs)
        self.library=c.Library(self.packs)
        self.items=c.catalog()
        path=Path(__file__).with_name('names.json')
        self.names=c.read(path) if path.exists() else {}
        self.maps={int(k):v['path'] for k,v in c.read(c.CFG/'dungeons.generated.json')['maps'].items()}
        self.searchrows={i:(self.name(i)+' '+r[1]+' '+str(i)+' '+r[0]).casefold() for i,r in self.items.items()}
        self.mail=queue.Queue();self.busy=False;self.page=-1;self.gear=0
        self.selected={};self.job='swordman' if 'swordman' in self.ward.jobs else next(iter(self.ward.jobs),'')
        self.nav=[]
        sidebar=tk.Frame(root,bg=SIDE,width=177);sidebar.pack(side='left',fill='y');sidebar.pack_propagate(False)
        label(sidebar,'A  /  100',23,GOLD,bold=True).pack(anchor='w',padx=20,pady=(27,0))
        label(sidebar,'ARAD   ARCHIVE',9,TEAL).pack(anchor='w',padx=22,pady=(0,34))
        for i,name in enumerate(PAGES):
            b=button(sidebar,f'0{i+1}    {name}',lambda j=i:self.show(j))
            b.configure(anchor='w',pady=12);b.pack(fill='x',padx=8,pady=2);self.nav.append(b)
        label(sidebar,'LOCAL REALM\nUS  /  2.24',9,MUTED,justify='left').pack(side='bottom',anchor='w',padx=20,pady=24)
        outer=tk.Frame(root,bg=BG);outer.pack(fill='both',expand=True)
        self.status=label(outer,'本地工作区就绪',10,TEAL,bg='#192728',padx=14,pady=8)
        self.status.pack(side='bottom',fill='x')
        self.body=tk.Frame(outer,bg=BG);self.body.pack(fill='both',expand=True,padx=24,pady=16)
        self.show(0);root.after(150,self.poll)

    def name(self,i):
        names=self.names.get(str(i),[])
        return ' / '.join(reversed(names)) or Path(self.items[i][1]).stem

    def note(self,s): self.status.config(text=s)

    def poll(self):
        while not self.mail.empty():
            ok,msg=self.mail.get();self.busy=False;self.note(msg)
            if not ok: messagebox.showerror('操作未完成',msg,parent=self.root)
            elif self.page in (0,4): self.show(self.page)
        self.root.after(250,self.poll)

    def run(self,fn,msg):
        if self.busy: self.note('上一项操作尚未完成。');return
        self.busy=True;self.note(msg)
        def worker():
            try: self.mail.put((True,fn() or '操作已完成。'))
            except Exception as e: self.mail.put((False,str(e)))
        threading.Thread(target=worker,daemon=True).start()

    def show(self,n):
        self.page=n
        for w in self.body.winfo_children(): w.destroy()
        for i,b in enumerate(self.nav): b.configure(bg='#29383e' if i==n else SIDE,fg=GOLD if i==n else MUTED)
        page=self.body
        label(page,f'ARAD FIELD ATLAS    /    0{n+1}     ─────────',9,GOLD,bold=True).pack(anchor='w')
        label(page,PAGES[n],22,bold=True).pack(anchor='w',pady=(1,7))
        [self.home,self.grants,self.wardrobe,self.abyss,self.mods][n](page)

    def home(self,p):
        canvas=tk.Canvas(p,height=250,bg='#1a252c',bd=0,highlightthickness=0)
        canvas.pack(fill='x',pady=(4,12))
        def paint(event):
            w,h=event.width,event.height
            canvas.delete('all')
            for y in range(0,h,3):
                t=y/h; color='#%02x%02x%02x'%(int(19+14*t),int(34+9*t),int(42+5*t))
                canvas.create_rectangle(0,y,w,y+3,fill=color,outline='')
            cx,cy=w-135,h//2
            rand=random.Random(224)
            for _ in range(95):
                x,y=rand.randrange(w),rand.randrange(h)
                canvas.create_line(x,y,x+1,y+1,fill=rand.choice(['#a58961','#44666c','#668082']))
            for r in (67,92,102,147,153):
                canvas.create_oval(cx-r,cy-r,cx+r,cy+r,outline='#73674f' if r%2 else '#3d6063')
            for k in range(48):
                a=k*math.pi/24
                canvas.create_line(cx+103*math.cos(a),cy+103*math.sin(a),
                                   cx+(111 if k%4 else 120)*math.cos(a),cy+(111 if k%4 else 120)*math.sin(a),fill='#9c8056')
            if self.hero: canvas.create_image(cx-4,h+10,image=self.hero,anchor='s')
            for x,y,txt,sz,col in [(22,23,'THE  ARAD  CHRONICLES',10,GOLD),(19,70,'阿拉德远征录',28,WHITE),
                                    (23,132,'本地冒险终端   /   SEASON 100',11,TEAL),
                                    (23,211,'FIELD EDITION     ◇     VOL. 01',9,MUTED)]:
                canvas.create_text(x,y,text=txt,fill=col,anchor='nw',font=(FONT,sz,'bold'))
            canvas.create_line(23,189,155,189,fill=GOLD,width=2)
        self.hero=None
        try:
            pack=self.packs/'sprite_interface_skillcutscene.NPK'
            name=next(n for n in index(pack) if n.endswith('/ghost_berserker.img'))
            im,_=frame(pack,name);im.thumbnail((295,265),Image.Resampling.LANCZOS);self.hero=ImageTk.PhotoImage(im)
        except (OSError,ValueError,StopIteration):pass
        canvas.bind('<Configure>',paint)
        state=c.gateway()
        r=row(p)
        label(r,'GATEWAY   /   '+('在线' if state else '待启动'),12,TEAL,bold=True).pack(side='left')
        button(r,'刷新',lambda:self.show(0)).pack(side='right')
        roster=c.roles()
        label(p,f'本地账号   probe-us100     ·     已建角色   {len(roster)}').pack(anchor='w')
        label(p,('当前是旧版网关：GM 排队至新版网关启动。' if state and not state.get('launcher_gm')
                 else '登录器会复用在线网关，不会强制关闭正在运行的游戏。'),10,MUTED).pack(anchor='w',pady=5)
        r=row(p);button(r,'进入游戏   ▸',lambda:self.run(c.login,'正在核对网关并启动 100US 客户端…'),True).pack(side='left')
        button(r,'打开运行目录',lambda:os.startfile(c.ROOT/'runtime')).pack(side='left',padx=8)

    def picker(self,p,only_gear=False):
        r=row(p);label(r,'检索').pack(side='left',padx=(0,8))
        q=entry(r);q.pack(side='left',fill='x',expand=True)
        label(r,'  ID / 中英文名 / 路径',9,MUTED).pack(side='left')
        box=listing(p,5); shown=[]
        def refresh(*_):
            words=q.get().casefold().split()
            shown[:]=[i for i,s in self.searchrows.items() if (not only_gear or self.items[i][0]=='装备')
                      and all(t in s for t in words)][:180]
            box.delete(0,'end')
            for i in shown: box.insert('end',f'  {i}    {self.name(i)}    ·    {self.items[i][0]}')
            if shown:box.selection_set(0)
        q.bind('<KeyRelease>',refresh);refresh()
        def chosen():
            if not box.curselection(): raise ValueError('请先选择物品')
            return shown[box.curselection()[0]]
        return chosen

    def grants(self,p):
        label(p,'使用当前角色库；发放有独立回执，角色在线时先排队。',10,MUTED).pack(anchor='w')
        r=row(p);label(r,'接收角色   ').pack(side='left')
        roster=c.roles();who=ttk.Combobox(r,values=[f'{i}  /  {s}' for i,s in roster],state='readonly',width=23)
        who.pack(side='left')
        if roster:who.current(0)
        chosen=self.picker(p)
        r=row(p);label(r,'数量  ').pack(side='left');amount=entry(r,1,6);amount.pack(side='left')
        label(r,'    原因  ').pack(side='left'); reason=entry(r,'GM 发放');reason.pack(side='left',fill='x',expand=True)
        def send():
            if who.current()<0:raise ValueError('尚无角色，请在游戏中先创建角色')
            i=chosen();role,name=roster[who.current()];qty=int(amount.get())
            if not messagebox.askyesno('确认发放',f'向 {name} 发放 {self.name(i)} × {qty}？',parent=self.root):return
            k=c.submit(role,i,qty,reason.get(),self.items)
            self.note('已排队 '+k[:8]+'；支持在线发放的网关无需退出游戏，入包以回执为准。')
        r=row(p);button(r,'加入发放队列',send,True).pack(side='left')
        button(r,'查看发放回执',self.history).pack(side='left',padx=8)
        button(r,'仅启动网关',lambda:self.run(lambda:c.login(True),'正在核对本地网关…')).pack(side='left')

    def history(self):
        win=tk.Toplevel(self.root);win.title('发放队列与回执');win.geometry('770x430');win.configure(bg=BG)
        box=listing(win,15)
        def refresh():
            box.delete(0,'end')
            files=list((c.DATA/'pending').glob('*.json'))+list((c.DATA/'receipts').glob('*.json'))
            for f in sorted(files,key=lambda x:x.stat().st_mtime,reverse=True)[:150]:
                try:d=c.read(f)
                except FileNotFoundError:continue
                if 'error' in d:s='拒绝：'+d['error']
                elif 'receipt' in d:s='已入包：'+str(d['receipt'].get('items',[]))
                else:s=f"排队 · 角色 {d['character_id']} · {d['items'][0]['template']} × {d['items'][0]['amount']}"
                box.insert('end','  '+f.stem[:10]+'    '+s)
            if not files:box.insert('end','  尚无操作记录')
        button(win,'刷新记录',refresh).pack(pady=5);refresh()

    def wardrobe(self,p):
        if not self.job:
            label(p,'未发现支持的客户端时装包，请检查 DFO_CLIENT_DIR。').pack();return
        r=row(p);label(r,'职业资源   ').pack(side='left')
        jobs=ttk.Combobox(r,values=list(self.ward.jobs),state='readonly',width=21);jobs.set(self.job);jobs.pack(side='left')
        def job_changed(_):
            self.job=jobs.get();self.selected={};self.show(2)
        jobs.bind('<<ComboboxSelected>>',job_changed)
        label(r,'    站姿首帧  /  本地 NPK',10,TEAL).pack(side='left')
        panel=tk.Frame(p,bg=BG);panel.pack(fill='both',expand=True,pady=8)
        stage=tk.Canvas(panel,width=232,height=285,bg='#19282d',highlightthickness=1,highlightbackground='#445456')
        stage.pack(side='left',fill='both',expand=True)
        controls=tk.Frame(panel,bg=BG);controls.pack(side='right',fill='y',padx=(10,0))
        def render(*_):
            stage.delete('all');w=max(210,stage.winfo_width());h=max(180,stage.winfo_height())
            stage.create_line(w//2,15,w//2,h-16,fill='#34484c',dash=(2,5))
            stage.create_oval(w//2-86,h//2+99,w//2+86,h//2+121,outline='#796c55')
            try:
                im,used=self.ward.compose(self.job,self.selected)
                scale=min(3,(w-28)/im.width,(h-54)/im.height)
                im=im.resize((max(1,int(im.width*scale)),max(1,int(im.height*scale))),Image.Resampling.NEAREST)
                self.preview=ImageTk.PhotoImage(im);stage.create_image(w//2,h//2,image=self.preview)
                stage.create_text(10,h-14,text=f'ATELIER    /    {len(used):02} LAYERS',anchor='w',fill=TEAL)
            except Exception as e:stage.create_text(w//2,h//2,width=w-30,text=str(e),fill=GOLD)
        for slot in SLOTS:
            r=row(controls);label(r,SLOT_ZH[slot],9,MUTED).pack(side='left',padx=(0,5))
            _,names=self.ward.choices(self.job,slot)
            vals=['-']+[Path(n).stem for n in names]
            combo=ttk.Combobox(r,values=vals,width=21,state='readonly')
            current=self.selected.get(slot,names[0] if names else '-')
            combo.set(Path(current).stem);combo.pack(side='left')
            def change(_,slot=slot,combo=combo,names=names):
                v=combo.current();self.selected[slot]=names[v-1] if v>0 else '-';render()
            combo.bind('<<ComboboxSelected>>',change)
        stage.bind('<Configure>',render)
        label(p,'试衣不写入角色，预览不等于已获得时装。',9,MUTED).pack(anchor='w')

    def abyss(self,p):
        policy=c.read(c.DATA/'abyss.json') if (c.DATA/'abyss.json').is_file() else {}
        label(p,'额外掉落，不替换原表。仅对选中房间内已确认死亡的战斗怪生效。',10,MUTED).pack(anchor='w')
        label(p,'01    深渊房间范围  ·  Ctrl / Shift 多选',10,TEAL).pack(anchor='w',pady=(5,0))
        rooms=listing(p,4,True);ids=sorted(self.maps)
        for n,i in enumerate(ids):
            rooms.insert('end',f'  {i}    {self.maps[i]}')
            if i in policy.get('maps',[]):rooms.selection_set(n);rooms.see(n)
        label(p,'02    奖励装备',10,TEAL).pack(anchor='w')
        chosen=self.picker(p,True)
        r=row(p);label(r,'额外掉率 %   ').pack(side='left')
        pc=entry(r,policy.get('percent',0),6);pc.pack(side='left')
        def save(off=False):
            pct=0 if off else int(pc.get());item=chosen() if pct else policy.get('template',0)
            maps=[ids[n] for n in rooms.curselection()]
            if pct and not messagebox.askyesno('确认规则',f'{len(maps)} 个房间，每只战斗怪有 {pct}% 概率额外掉落 {self.name(item)}。保存？',parent=self.root):return
            c.save_rule(maps,item,pct,self.items,self.maps)
            self.note('规则已保存，新副本首杀时读取；当前已开始掉落的副本不变。');self.show(3)
        button(r,'保存规则',save,True).pack(side='left',padx=8)
        button(r,'关闭加成',lambda:save(True)).pack(side='left')
        item=policy.get('template',0)
        label(p,(f"已保存：{policy['percent']}% · {len(policy['maps'])} 个房间 · {self.name(item) if item in self.items else item}"
                 if policy.get('percent') else '当前关闭：普通地图保持原掉落。'),9,GOLD).pack(anchor='w')

    def mods(self,p):
        label(p,'导入 NPK 后按需启用。同名原包先备份，停用时按校验值恢复。',10,MUTED).pack(anchor='w')
        r=row(p);label(r,'资源分类   ').pack(side='left')
        kind=ttk.Combobox(r,state='readonly',values=['时装','宠物','其他'],width=10);kind.current(0);kind.pack(side='left')
        def select():
            path=filedialog.askopenfilename(parent=self.root,title='导入 NPK 资源',filetypes=[('Neople 资源包','*.npk')])
            category=kind.get()
            if path:self.run(lambda:(self.library.import_pack(path,category),'已导入，尚未启用。')[1],'正在校验并导入资源包…')
        button(r,'导入 NPK',select,True).pack(side='left',padx=8)
        lib=self.library.entries();keys=list(lib);box=listing(p,9)
        for k,v in lib.items():
            box.insert('end',f"  {dict(on='已启用',off='已停用',installing='待恢复').get(v['state'],'?')}    {v['kind']}    /    {v['name']}")
        if keys:box.selection_set(0)
        else:box.insert('end','  库中暂无 Mod。可以先导入一个时装或宠物 NPK，无需启动游戏。')
        def switch(on):
            if not keys or not box.curselection():raise ValueError('请先导入并选择 Mod')
            key=keys[box.curselection()[0]];v=lib[key]
            if not messagebox.askyesno('确认资源切换',('启用：' if on else '停用：')+v['target']+'？',parent=self.root):return
            self.run(lambda:(self.library.switch(key,on),'资源切换完成，下次启动游戏时加载。')[1],'正在校验资源和备份…')
        r=row(p);button(r,'启用所选',lambda:switch(True),True).pack(side='left')
        button(r,'停用并还原',lambda:switch(False)).pack(side='left',padx=8)
        button(r,'打开资源库',lambda:os.startfile(self.library.root)).pack(side='left')
        label(p,'资源包不会自动注册新道具 ID、宠物技能或服务端数据。',9,MUTED).pack(anchor='w')


def main():
    import msvcrt
    c.DATA.mkdir(parents=True,exist_ok=True)
    lock=(c.DATA/'window.lock').open('a+b')
    root=tk.Tk()
    try:msvcrt.locking(lock.fileno(),msvcrt.LK_NBLCK,1)
    except OSError:
        root.withdraw();messagebox.showinfo('阿拉德星图','登录器已经打开。',parent=root);return
    root.report_callback_exception=lambda typ,err,tb:messagebox.showerror('操作未完成',str(err),parent=root)
    try:
        Atlas(root);root.mainloop()
    finally:lock.close()


if __name__=='__main__':main()
