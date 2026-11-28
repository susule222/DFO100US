import json
import sqlite3
from pathlib import Path

import atelier_runtime as rt
from epic_data import JOBS_BY_TOKEN
from item_icons import reference

# Strict DNF rarity progression order (excludes non-rarity "未标注")
RARITY_ORDER = ('普通', '高级', '稀有', '神器', '勇者', '传说', '史诗', '神话')

# Friendly Chinese labels for raw PVF stackable / misc category tokens
CAT_LABELS = {
    'booster': '礼盒/袖珍罐',
    'booster selection': '自选礼盒',
    'booster random': '随机礼盒',
    'cera booster': '商城礼盒',
    'cera package': '商城礼包',
    'usable cera package': '可开启礼包',
    'dungeon and life booster': '副本/生活礼盒',
    'enchant waste': '附魔宝珠/卡片',
    'auto enchant': '自动附魔宝珠',
    'material expert job': '副职业材料',
    'recipe': '装备设计图',
    'dungeon and life recipe': '副本/生活设计图',
    'rune': '符文',
    'avatar emblem': '时装徽章',
    'flag': '公会勋章',
    'flag gem': '守护珠',
    'charm': '护符/挂件',
    'creature': '宠物',
    'creature expitem': '宠物经验道具',
    'artifact red': '宠物装备(红)',
    'artifact blue': '宠物装备(蓝)',
    'artifact green': '宠物装备(绿)',
    'support weapon': '支援武器',
    'unlimited waste': '无限消耗品',
    'town and dungeon': '城镇/副本消耗品',
    'unlimited town and dungeon': '无限城镇/副本道具',
    'expert town potion': '副职业药剂',
    'waste instant item': '即时消耗品',
    'teleport potion': '瞬间移动药剂',
    'npc teleport potion': 'NPC 传送药剂',
    'town status potion': '城镇状态药剂',
    'disguise': '变身药剂',
    'random disguise': '随机变身药剂',
    'dye': '染色剂',
    'contract': '契约道具',
    'feed': '宠物饲料',
    'etc': '其他材料/杂物',
    'unlimited etc': '无限杂物',
    'upgradable legacy': '可升级传承/礼盒',
    'multi upgradable legacy': '多阶升级道具',
    'legacy': '传承/遗物道具',
    'stackable legacy': '可堆叠传承道具',
    'quest receive': '任务接取券',
    'upgrade limit cube': '增幅/强化魔盒',
    'monster tarot card': '怪物卡片',
    'smart drop': '智能掉落道具',
    'coupon': '兑换券',
    'only effect': '特效道具',
    'scale effect': '体型特效',
    'global effect': '全局特效',
    'achievement item': '成就道具',
    'guild contents': '公会道具',
    'virtual': '虚拟道具',
    'getitem use action': '拾取即用道具',
    'remodel lottery': '改造抽奖券',
    'grouped random box': '组合随机礼盒',
    'set': '套装道具',
}

# Clean top-level Chinese item classification groups
ITEM_GROUPS = (
    ('武器', ('武器',)),
    ('防具', ('防具-上衣', '防具-下装', '防具-头肩', '防具-腰带', '防具-鞋子')),
    ('首饰', ('首饰-手镯', '首饰-项链', '首饰-戒指')),
    ('特殊装备', ('特殊装备-辅助装备', '特殊装备-魔法石', '特殊装备-耳环')),
    ('称号与护石', ('称号', '护石', 'rune', 'flag', 'flag gem', 'charm')),
    ('时装装扮', (
        '时装-帽子', '时装-头发', '时装-脸部', '时装-胸部', '时装-上衣',
        '时装-腰部', '时装-下装', '时装-鞋子', '时装-肤色', '时装-武器装扮', '时装-光环',
    )),
    ('礼盒与宝珠', (
        'booster', 'booster selection', 'booster random', 'cera booster',
        'cera package', 'usable cera package', 'dungeon and life booster',
        'enchant waste', 'auto enchant', 'monster tarot card', 'grouped random box',
        'remodel lottery', 'set',
    )),
    ('消耗品与药剂', (
        '消耗品', '投掷物', 'unlimited waste', 'town and dungeon',
        'unlimited town and dungeon', 'expert town potion', 'waste instant item',
        'teleport potion', 'npc teleport potion', 'town status potion',
        'disguise', 'random disguise', 'dye', 'contract', 'feed',
        'only effect', 'scale effect', 'global effect', 'getitem use action',
    )),
    ('材料与任务', (
        '材料', 'material expert job', '任务道具', 'quest receive',
        'recipe', 'dungeon and life recipe', 'avatar emblem',
        'upgrade limit cube', 'etc', 'unlimited etc',
        'legacy', 'upgradable legacy', 'multi upgradable legacy',
        'multi upgradable legacy bonus cera', 'stackable legacy', 'apc legacy',
        'achievement item', 'guild contents', 'coupon', 'smart drop',
        'material instant item', 'item upgrad buff usual upgrade',
    )),
    ('宠物与其它', (
        'creature', 'creature expitem', 'artifact red', 'artifact blue',
        'artifact green', 'support weapon', 'virtual', 'stackable', '未解析',
    )),
)

_GROUP_CAT_MAP = {name: cats for name, cats in ITEM_GROUPS}
_ALL_ASSIGNED_CATS = {c for _n, cats in ITEM_GROUPS for c in cats}


def display_cat(cat: str) -> str:
    return CAT_LABELS.get(cat, cat)


class Matches:
    def __init__(self,library,pred,args):
        self.lib,self.pred,self.args=library,pred,args
        self.count=library.db.execute('select count(*) from item '+pred,args).fetchone()[0]
    def __len__(self):return self.count
    def __getitem__(self,key):
        if not isinstance(key,slice):
            if not 0<=key<self.count:raise IndexError(key)
            return self[key:key+1][0]
        start,stop,step=key.indices(self.count)
        if step!=1:raise ValueError('invalid page stride')
        query='select * from item '+self.pred+' order by rank,id,g limit ? offset ?'
        return [self.lib.card(r) for r in self.lib.db.execute(query,[*self.args,max(0,stop-start),start])]
    def __contains__(self,item):
        return self.lib.db.execute('select 1 from item '+self.pred+' and id=? limit 1',[*self.args,item.item_id]).fetchone() is not None


class SourceLibrary:
    def __init__(self,model,folder=None):
        self.folder=Path(folder) if folder else rt.current()
        if self.folder is None:raise ValueError('全量索引尚未生成，请运行 build_library.py')
        self.db=sqlite3.connect((self.folder/'items.sqlite').as_uri()+'?mode=ro',uri=True)
        self.db.row_factory=sqlite3.Row;self.model=model
        self.poses=json.loads(self.db.execute("select v from meta where k='poses'").fetchone()[0])
        row=self.db.execute("select v from meta where k='pvf_meta'").fetchone()
        self.pvf_meta=json.loads(row[0]) if row else {}
    def close(self):
        try:self.db.close()
        except Exception:pass
    def distinct(self,key):
        if key not in ('cat','rar'):raise ValueError(key)
        if key=='rar':
            return self.rarities()
        return [r[0] for r in self.db.execute("select distinct "+key+" from item where g!='composed_set' order by 1")]
    def rarities(self):
        present={r[0] for r in self.db.execute("select distinct rar from item where g!='composed_set'")}
        return [r for r in RARITY_ORDER if r in present]
    def item_groups(self):
        return [name for name,_ in ITEM_GROUPS]
    def categories_for_group(self,group='全部分类'):
        present={r[0] for r in self.db.execute("select distinct cat from item where g!='composed_set'")}
        if group=='全部分类' or group not in _GROUP_CAT_MAP:
            ordered=[]
            for _g,cats in ITEM_GROUPS:
                for c in cats:
                    if c in present and c not in ordered:
                        ordered.append(c)
            for c in sorted(present - set(ordered)):
                ordered.append(c)
            return [(c,display_cat(c)) for c in ordered]
        cats=[c for c in _GROUP_CAT_MAP[group] if c in present]
        if group=='宠物与其它':
            extra=sorted(present - _ALL_ASSIGNED_CATS)
            cats.extend(c for c in extra if c not in cats)
        return [(c,display_cat(c)) for c in cats]
    def card(self,r):
        jobs=[JOBS_BY_TOKEN.get(x,x) for x in r['jobs'].split('|') if x]
        if not jobs:jobs=['全职业' if r['g']=='stackable' else '未标注']
        return self.model(r['id'],r['name'],r['rar'],r['cat'],jobs,icon=reference(r['img'],r['frame']),
                          desc=r['alias']+' / '+r['path'],search_key=f"{r['id']} {r['alias']} {r['path']}".casefold(),
                          grantmode=r['grantmode'])
    def match(self,kw,cat,rar,job,group='全部分类',only_grantable=False):
        pred=["g!='composed_set'"];args=[]
        if only_grantable:
            pred.append('grantmode>0')
        if group and group!='全部分类' and group in _GROUP_CAT_MAP:
            cats=list(_GROUP_CAT_MAP[group])
            if group=='宠物与其它':
                placeholders=','.join('?' for _ in _ALL_ASSIGNED_CATS)
                if cats:
                    c_ph=','.join('?' for _ in cats)
                    pred.append(f'(cat in ({c_ph}) or cat not in ({placeholders}))')
                    args.extend(cats)
                    args.extend(sorted(_ALL_ASSIGNED_CATS))
                else:
                    pred.append(f'cat not in ({placeholders})')
                    args.extend(sorted(_ALL_ASSIGNED_CATS))
            elif cats:
                c_ph=','.join('?' for _ in cats)
                pred.append(f'cat in ({c_ph})')
                args.extend(cats)
        if cat and cat!='全部部位':pred.append('cat=?');args.append(cat)
        if rar and rar!='全部稀有度':pred.append('rar=?');args.append(rar)
        if job and job!='全部职业':
            pred.append("(instr('|'||jobs||'|',?) or instr(jobs,'[all]') or (g='stackable' and jobs=''))")
            args.append('|'+next(k for k,v in JOBS_BY_TOKEN.items() if v==job)+'|')
        if kw:
            pred.append("(instr(cast(id as text),?) or instr(lower(name),?) or instr(lower(alias),?) or instr(path,?))")
            args.extend([kw.casefold()]*4)
        return Matches(self,'where '+' and '.join(pred),args)
    def grantmode(self,ident):
        return self.db.execute('select coalesce(max(grantmode),0) from item where id=?',(ident,)).fetchone()[0]
    def __contains__(self,ident):return self.grantmode(ident)>0
    def visual(self,g,i):
        return self.db.execute('select * from item where g=? and id=?',(g,i)).fetchone()
    def wardrobe_rarities(self):
        present={r[0] for r in self.db.execute('select distinct i.rar from wardrobe w join item i on i.g=w.g and i.id=w.id')}
        return [r for r in RARITY_ORDER if r in present]
    def slot_counts(self,job,rarity='全部稀有度'):
        out={}
        if rarity and rarity!='全部稀有度':
            sql=("select w.mode, w.slot, count(*) from wardrobe w join item i on i.g=w.g and i.id=w.id "
                 "where w.job in (?, '[all]') and i.rar=? group by w.mode, w.slot")
            rows=self.db.execute(sql,(job,rarity))
        else:
            rows=self.db.execute(
                "select mode, slot, count(*) from wardrobe where job in (?, '[all]') group by mode, slot",
                (job,),
            )
        for mode,slot,cnt in rows:
            out['set' if mode=='set' else slot]=cnt
        return out
    def _costume_query(self,job,mode,slot,words,only_visual=False,rarity='全部稀有度'):
        pred="w.job in (?, '[all]') and w.mode=?";args=[job,mode]
        if slot:pred+=' and w.slot=?';args.append(slot)
        if rarity and rarity!='全部稀有度':
            pred+=' and i.rar=?';args.append(rarity)
        if only_visual and mode=='single':
            pred+=" and i.visual is not null and i.visual not in ('','[[],[]]','[[],[],[]]')"
        if words:
            pred+=" and (instr(lower(i.name),?) or instr(lower(i.alias),?) or instr(i.path,?) or instr(cast(i.id as text),?))"
            args.extend([words.strip().casefold()]*4)
        src=' from wardrobe w join item i on i.g=w.g and i.id=w.id where '+pred
        if mode=='set':
            order=(" order by case when i.visual is not null and i.visual not in ('','[[],[]]','[[],[],[]]') then 0 else 1 end, "
                   "case when lower(trim(i.name)) in ('测试','test','') then 2 "
                   "when i.name like '组合套装 #%' or i.name like '稀有装扮套装 #%' then 1 else 0 end, "
                   "case i.rar when '神话' then 0 when '史诗' then 1 when '传说' then 2 when '神器' then 3 "
                   "when '稀有' then 4 when '高级' then 5 when '普通' then 6 else 7 end, "
                   "case when w.g='composed_set' then 0 else 1 end, w.id")
        else:
            order=(" order by case when i.visual is not null and i.visual not in ('','[[],[]]','[[],[],[]]') then 0 else 1 end, "
                   "case when lower(trim(i.name)) in ('测试','test','') then 1 else 0 end, w.id")
        return src,args,order
    def costumes(self,job,mode,slot,words,page,page_size=60,only_visual=False,rarity='全部稀有度'):
        src,args,order=self._costume_query(job,mode,slot,words,only_visual,rarity)
        total=self.db.execute('select count(*)'+src,args).fetchone()[0]
        page=max(0,min(page,(max(1,total)-1)//page_size))
        rows=self.db.execute('select i.id,i.name,w.slot'+src+order+' limit ? offset ?',
                             args+[page_size,page*page_size]).fetchall()
        return rows,page,total
    def costume_cards(self,job,mode,slot,words,page,page_size=24,only_visual=False,rarity='全部稀有度'):
        src,args,order=self._costume_query(job,mode,slot,words,only_visual,rarity)
        total=self.db.execute('select count(*)'+src,args).fetchone()[0]
        page=max(0,min(page,(max(1,total)-1)//page_size))
        rows=self.db.execute(
            'select i.id,i.name,i.alias,i.path,i.rar,i.img,i.frame,i.visual,i.grantmode,w.slot,w.g'
            +src+order+' limit ? offset ?',
            args+[page_size,page*page_size],
        ).fetchall()
        return rows,page,total
