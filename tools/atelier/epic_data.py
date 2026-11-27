"""Map real US100 catalog fields into the supplied launcher's card model."""
from pathlib import Path
import core
from item_icons import from_cells, reference

JOBS_BY_TOKEN = dict(zip(
    ('[all]','[swordman]','[at swordman]','[fighter]','[at fighter]','[gunner]',
     '[at gunner]','[mage]','[at mage]','[priest]','[at priest]','[thief]','[knight]',
     '[demonic lancer]','[gun blader]','[creator mage]','[demonic swordman]'),
    ('全职业','鬼剑士(男)','鬼剑士(女)','格斗家(女)','格斗家(男)','神枪手(男)',
     '神枪手(女)','魔法师(女)','魔法师(男)','圣职者(男)','圣职者(女)','暗夜使者','守护者',
     '魔枪士','枪剑士','缔造者','黑暗武士')))
JOBS = ['全部职业', *list(JOBS_BY_TOKEN.values())[1:]]
RARITIES = {0:'普通',1:'高级',2:'稀有',3:'神器',4:'史诗',5:'勇者',6:'传说',7:'神话'}
TYPES = dict(zip('weapon coat pants shoulder waist shoes wrist amulet ring support magic_stone earring title_name talisman'.split(),
                 '武器 防具-上衣 防具-下装 防具-头肩 防具-腰带 防具-鞋子 首饰-手镯 首饰-项链 首饰-戒指 特殊装备-辅助装备 特殊装备-魔法石 特殊装备-耳环 称号 护石'.split()))
ARMORS={'cloth':'布甲','leather':'皮甲','light':'轻甲','heavy':'重甲','plate':'板甲','lightarmor':'轻甲','heavyarmor':'重甲'}


def read_cards(model):
    loot = core.read(core.CFG/'loot.generated.json')['items']
    catalog = core.read(core.CFG/'equipment.generated.json')
    equipment = catalog['rows']
    icon_file=Path(__file__).with_name('equipment_icons.json')
    extra=core.read(icon_file) if icon_file.exists() else {}
    source_icons=extra.get('rows',{}) if extra.get('source')==catalog['source']['checksum'] else {}
    allowed = core.catalog(loot.values(),equipment)
    definitions = {r['ID']:r for r in equipment}
    name_file=Path(__file__).with_name('names.json')
    names=core.read(name_file) if name_file.exists() else {}
    cards=[]
    for ident,(group,path,ref) in allowed.items():
        armor=None
        if group=='装备':
            f=definitions[ident]['Fields']
            icon=from_cells(f.get('[icon]',[]))
            if icon is None:
                spec=source_icons.get(path,{})
                icon=reference(spec.get('img'),spec.get('frame'))
            token=f['[equipment type]'][0].get('text','').strip('[]')
            category=TYPES.get(token.replace(' ','_'),token or group)
            rarity=f['[rarity]'][0]['value']
            jobs=[JOBS_BY_TOKEN.get(t['text'],t['text']) for t in f.get('[usable job]',[]) if t.get('text')]
            if not jobs:jobs=['未标注']
            armor=next((ARMORS[k] for k in Path(path).parts if k in ARMORS),None)
        else:
            data=loot[str(ident)]
            cells=data['Script']['cells']
            icon=next((from_cells(cells[n+1:n+3]) for n,t in enumerate(cells)
                       if t.get('type')==3 and t.get('text')=='[icon]'),None)
            rarity=data.get('Rarity')
            category={'[material]':'材料','[quest]':'任务道具','[throw]':'投掷物','[consumable]':'消耗品'}.get(data.get('stackable_type'),group)
            jobs=['全职业']
        aliases=names.get(str(ident),[])
        name=aliases[-1] if aliases else Path(path).stem
        search=' '.join([str(ident),*aliases,path,ref]).casefold()
        cards.append(model(ident,name,RARITIES.get(rarity,f'品级 {rarity}'),category,jobs,armor,
                           icon=icon,desc=' / '.join([*aliases,path]),search_key=search))
    display_order={'神话':0,'史诗':1,'传说':2,'神器':3,'稀有':4}
    cards.sort(key=lambda it:(str(it.item_id) not in names,display_order.get(it.rarity,5),it.category,it.item_id))
    return allowed,cards
