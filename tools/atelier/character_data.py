"""US100 profession/advancement metadata, bag slot names, and localized quest catalog."""
from pathlib import Path
import re

import core

PROFESSION_INFO = {
    0: ('鬼剑士(男)', '[swordman]', [(0, '未转职'), (1, '剑魂'), (2, '鬼泣'), (3, '狂战士'), (4, '阿修罗'), (5, '剑影')]),
    1: ('格斗家(女)', '[fighter]', [(0, '未转职'), (1, '气功师'), (2, '散打'), (3, '街霸'), (4, '柔道家')]),
    2: ('神枪手(男)', '[gunner]', [(0, '未转职'), (1, '漫游枪手'), (2, '枪炮师'), (3, '机械师'), (4, '弹药专家'), (5, '合金战士')]),
    3: ('魔法师(女)', '[mage]', [(0, '未转职'), (1, '元素师'), (2, '召唤师'), (3, '战斗法师'), (4, '魔道学者'), (5, '小魔女')]),
    4: ('圣职者(男)', '[priest]', [(0, '未转职'), (1, '圣骑士'), (2, '蓝拳圣使'), (3, '驱魔师'), (4, '复仇者')]),
    5: ('神枪手(女)', '[at gunner]', [(0, '未转职'), (1, '漫游枪手'), (2, '枪炮师'), (3, '机械师'), (4, '弹药专家')]),
    6: ('暗夜使者', '[thief]', [(0, '未转职'), (1, '刺客'), (2, '死灵术士'), (3, '忍者'), (4, '影舞者')]),
    7: ('格斗家(男)', '[at fighter]', [(0, '未转职'), (1, '气功师'), (2, '散打'), (3, '街霸'), (4, '柔道家')]),
    8: ('魔法师(男)', '[at mage]', [(0, '未转职'), (1, '元素爆破师'), (2, '冰结师'), (3, '血法师'), (4, '逐风者'), (5, '次元行者')]),
    9: ('黑暗武士', '[demonic swordman]', [(0, '黑暗武士(外传)')]),
    10: ('缔造者', '[creator mage]', [(0, '缔造者(外传)')]),
    11: ('鬼剑士(女)', '[at swordman]', [(0, '未转职'), (1, '驭剑士'), (2, '暗殿骑士'), (3, '契魔者'), (4, '流浪武士'), (5, '刃影')]),
    12: ('守护者', '[knight]', [(0, '未转职'), (1, '精灵骑士'), (2, '混沌魔灵'), (3, '帕拉丁'), (4, '龙骑士')]),
    13: ('魔枪士', '[demonic lancer]', [(0, '未转职'), (1, '征战者'), (2, '决战者'), (3, '狩猎者'), (4, '暗枪士')]),
    14: ('圣职者(女)', '[at priest]', [(0, '未转职'), (1, '圣骑士'), (2, '异端审判者'), (3, '巫女'), (4, '诱魔者')]),
    15: ('枪剑士', '[gun blader]', [(0, '未转职'), (1, '暗刃'), (2, '特工'), (3, '战线佣兵'), (4, '源能专家')]),
}

WORN_SLOT_NAMES = {
    12: '武器',
    13: '称号',
    14: '上衣',
    15: '头肩',
    16: '下装',
    17: '鞋子',
    18: '腰带',
    19: '项链',
    20: '手镯',
    21: '戒指',
    22: '辅助装备',
    23: '魔法石',
    24: '副武器',
    25: '耳环',
}

QUEST_GRADES = {
    '[epic]': '主线',
    '[common unique]': '普通',
    '[achievement]': '成就',
    '[daily]': '每日',
    '[training]': '修炼',
    '[urgent]': '紧急',
    '[title]': '称号',
    '[normaly repeat]': '重复',
    '[story]': '剧情',
    '[event]': '活动',
}

QUEST_KINDS = {
    '[meet npc]': '对话NPC',
    '[clear map]': '通关指定图',
    '[clear a dungeon]': '通关副本',
    '[condition under clear]': '条件通关',
    '[hunt monster]': '击杀怪物',
    '[seeking]': '收集物品',
    '[seek n meet npc]': '收集并对话',
    '[reach the range]': '到达区域',
    '[look cinematic]': '观看剧情',
    '[clear quest]': '前置任务',
    '[quest clear]': '关联任务',
}

ATTR_LABELS = [
    ('[hp max]', 'HP 上限'),
    ('[mp max]', 'MP 上限'),
    ('[physical attack]', '力量 / 物理'),
    ('[magical attack]', '智力 / 魔法'),
    ('[physical defense]', '体力 / 物防'),
    ('[magical defense]', '精神 / 魔防'),
    ('[attack speed]', '攻击速度'),
    ('[cast speed]', '施放速度'),
    ('[move speed]', '移动速度'),
    ('[hit recovery]', '硬直'),
    ('[inventory limit]', '负重上限'),
    ('[mp regen speed]', 'MP 恢复'),
]


def profession_name(prof_id):
    return PROFESSION_INFO.get(int(prof_id), (f'职业 {prof_id}', '', [(0, '未转职')]))[0]


def profession_token(prof_id):
    return PROFESSION_INFO.get(int(prof_id), ('', '', []))[1]


def advancement_options(prof_id):
    return list(PROFESSION_INFO.get(int(prof_id), ('', '', [(0, '未转职')]))[2])


def advancement_name(prof_id, adv_id):
    for gid, label in advancement_options(prof_id):
        if gid == int(adv_id):
            return label
    return f'分支 {adv_id}'


def bag_section_label(slot, list_kind):
    slot = int(slot)
    if list_kind == 'worn':
        return '已穿戴'
    if list_kind == 'equipment':
        return '装备栏'
    if 0 <= slot <= 8:
        return '快捷栏'
    if 65 <= slot <= 120:
        return '消耗品'
    if 121 <= slot <= 176:
        return '材料'
    if 177 <= slot <= 232:
        return '任务道具'
    if 233 <= slot <= 288:
        return '副职业材料'
    if 289 <= slot <= 360:
        return '徽章栏'
    return '其他'


def worn_slot_label(slot):
    return WORN_SLOT_NAMES.get(int(slot), f'穿戴槽位 {slot}')


def _find_string_dir():
    rt = core.ROOT / 'runtime'
    preferred = rt / 'localize-us100-zh-CN-r23' / 'replacement-tables' / 'string'
    if preferred.is_dir():
        return preferred
    candidates = sorted(rt.glob('localize-us100-zh-CN*/replacement-tables/string'), reverse=True)
    return candidates[0] if candidates else None


def _read_str_table(path):
    if not path or not path.is_file():
        return {}
    raw = path.read_bytes()
    if raw[:2] == b'\xff\xfe' or b'\x00' in raw[:32]:
        text = raw.decode('utf-16-le', errors='replace')
    else:
        text = raw.decode('utf-8', errors='replace')
    out = {}
    for line in text.splitlines():
        if '>' in line and not line.lstrip().startswith('//'):
            k, v = line.split('>', 1)
            k = k.strip().lstrip('\ufeff')
            if k:
                out[k] = v.strip()
    return out


_REF_RE = re.compile(r'<\d+::([^>]+)>')


class QuestCatalog:
    """Lazy-loaded US100 quest index with Chinese localized titles."""

    def __init__(self):
        self._loaded = False
        self.entries = {}   # int qid -> dict
        self.ordered = []   # sorted list of dicts

    def ensure_loaded(self):
        if self._loaded:
            return
        self._loaded = True
        strings = {}
        sdir = _find_string_dir()
        if sdir:
            for fname in ('quest.uv.str', 'epicquest.uv.str', 'live_quest.uv.str', 'storybookquest.uv.str'):
                strings.update(_read_str_table(sdir / fname))
        qfile = core.CFG / 'quests.generated.json'
        if not qfile.is_file():
            return
        data = core.read(qfile)
        qmap = data.get('quests') or {}
        for k, d in qmap.items():
            qid = int(k)
            cells = (d.get('script') or {}).get('cells') or []
            grade_raw = ''
            name_ref = ''
            grow_type = -1
            for i, c in enumerate(cells):
                if c.get('type') != 3 or i + 1 >= len(cells):
                    continue
                tag = c.get('text')
                nxt = cells[i + 1]
                if tag == '[grade]' and not grade_raw:
                    grade_raw = nxt.get('text', '')
                elif tag == '[name]' and not name_ref:
                    name_ref = nxt.get('reference') or nxt.get('text') or ''
                elif tag == '[grow type]' and nxt.get('type') == 0:
                    grow_type = int(nxt.get('value', -1))
            title = ''
            if name_ref:
                m = _REF_RE.search(name_ref)
                key = m.group(1) if m else name_ref
                title = strings.get(key, '')
            if not title:
                title = Path((d.get('script') or {}).get('path', f'quest_{qid}')).stem
            grade = QUEST_GRADES.get(grade_raw, grade_raw.strip('[]') or '其他')
            kind_raw = d.get('kind', '')
            kind = QUEST_KINDS.get(kind_raw, kind_raw.strip('[]') or '未知')
            entry = dict(
                id=qid,
                title=title,
                grade=grade,
                grade_raw=grade_raw,
                kind=kind,
                kind_raw=kind_raw,
                min_level=int(d.get('minimum_level', 1) or 1),
                max_level=int(d.get('maximum_level', 100) or 100),
                jobs=list(d.get('jobs') or []),
                grow_type=grow_type,
                search=f'{qid} {title} {grade} {kind}'.casefold(),
            )
            self.entries[qid] = entry
        self.ordered = [self.entries[k] for k in sorted(self.entries)]

    def info(self, qid):
        self.ensure_loaded()
        return self.entries.get(int(qid))

    def title(self, qid):
        it = self.info(qid)
        return it['title'] if it else f'任务 #{qid}'

    def search(self, keyword='', grade='全部类型', job_token='', max_level=0, limit=150):
        self.ensure_loaded()
        kw = (keyword or '').strip().casefold()
        out = []
        for e in self.ordered:
            if grade and grade != '全部类型' and e['grade'] != grade:
                continue
            if job_token and e['jobs'] and '[all]' not in e['jobs'] and job_token not in e['jobs']:
                continue
            if max_level and e['min_level'] > max_level:
                continue
            if kw and kw not in e['search']:
                continue
            out.append(e)
            if len(out) >= limit:
                break
        return out
