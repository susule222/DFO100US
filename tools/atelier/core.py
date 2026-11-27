import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
import uuid

import psutil
import us100_preflight as pre
from sprites import index

ROOT, SERVER = pre.ROOT, pre.SERVER
DATA = ROOT/'runtime'/'launcher-gm'
CFG = SERVER/'configs'/'us100'
READY = pre.ROOT/'runtime'/'interactive'/'ready.json'
SHELF = ROOT/'runtime'/'mod-library'


def read(path):
    return json.loads(path.read_bytes())


def atomic(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name('.'+uuid.uuid4().hex+'.tmp')
    try:
        tmp.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding='utf-8')
        tmp.replace(path)
    finally:
        tmp.unlink(missing_ok=True)


def db_path():
    config = pre.storage_config()
    return Path(config['db_path']) if config.get('db_path') else SERVER/'runtime'/'us100.db'


def _load_db_tables():
    db = db_path()
    if not db.exists():
        return {}
    return read(db).get('tables', {})


def account_id(username='probe-us100'):
    tables = _load_db_tables()
    for a in tables.get('accounts', {}).values():
        if a.get('username') == username:
            return a.get('id')
    return None


def account_cera(username='probe-us100'):
    tables = _load_db_tables()
    aid = None
    for a in tables.get('accounts', {}).values():
        if a.get('username') == username:
            aid = a.get('id')
            break
    if aid is None:
        return 0
    row = tables.get('account_currency', {}).get(str(aid), {})
    return max(0, int(row.get('cera', 0) or 0))


def roles():
    tables = _load_db_tables()
    accounts = {a['id'] for a in tables.get('accounts', {}).values() if a.get('username') == 'probe-us100'}
    return sorted((r['id'], r['name']) for r in tables.get('characters', {}).values()
                  if r['account_id'] in accounts and not r.get('deleted_at'))


def character_snapshot(role_id):
    tables = _load_db_tables()
    row = tables.get('characters', {}).get(str(role_id))
    if not row or row.get('deleted_at'):
        return None
    state = row.get('state') or {}
    inv = state.get('inventory') or {}
    aid = row.get('account_id')
    cera_row = tables.get('account_currency', {}).get(str(aid), {})
    prefix = f'{role_id}/'
    quests = []
    for k, q in tables.get('character_quests', {}).items():
        if k.startswith(prefix):
            quests.append(dict(
                id=int(q.get('quest_id', 0)),
                status=q.get('status', ''),
                progress=int(q.get('progress', 0)),
                progress_model=q.get('progress_model', ''),
                accepted_at=q.get('accepted_at', ''),
                completed_at=q.get('completed_at') or '',
            ))
    quests.sort(key=lambda x: x['id'])
    return dict(
        id=int(row['id']),
        account_id=int(aid) if aid is not None else 0,
        name=row.get('name', ''),
        profession=int(row.get('profession', 0)),
        level=int(state.get('level', 1) or 1),
        experience=int(state.get('experience', 0) or 0),
        advancement=int(state.get('advancement', 0) or 0),
        skill_points=list(state.get('skill_points') or [0, 0]),
        technique_points=list(state.get('technique_points') or [0, 0]),
        attributes=dict(state.get('attributes') or {}),
        cera=max(0, int(cera_row.get('cera', 0) or 0)),
        gold=int(inv.get('gold', 0) or 0),
        items=list(inv.get('items') or []),
        equipment=list(inv.get('equipment') or []),
        worn=list(inv.get('worn') or []),
        quests=quests,
    )


def submit_op(action, payload, reason='GM 角色属性操作', directory=DATA):
    if action not in ('currency', 'level', 'advancement', 'bag', 'quest', 'avatar'):
        raise ValueError(f'不支持的操作类型: {action}')
    body = dict(payload)
    body['action'] = action
    body['reason'] = (reason or 'GM 角色属性操作').strip()[:160]
    key = uuid.uuid4().hex
    atomic(directory/'pending'/(key+'.json'), body)
    return key


def grant_avatars(role_id, avatar_ids, reason='时装试衣间发放', directory=DATA):
    if role_id not in dict(roles()):
        raise ValueError('请先选择有效的接收角色')
    clean = [int(x) for x in avatar_ids if int(x) > 0]
    if not clean or len(clean) > 16:
        raise ValueError('单次发送时装数量需在 1 到 16 件之间')
    return submit_op('avatar', {'character_id': int(role_id), 'avatar_ids': clean}, reason=reason, directory=directory)


def read_job(job_id, directory=DATA):
    if not job_id:
        return 'missing', None
    for folder in ('receipts', 'pending', 'receipts'):
        try:
            return folder, read(directory/folder/(job_id+'.json'))
        except FileNotFoundError:
            continue
    return 'missing', None


def catalog(loot_rows=None, equipment_rows=None):
    loot = loot_rows if loot_rows is not None else read(CFG/'loot.generated.json')['items'].values()
    slots = read(CFG/'rules.bag.json')['slots']
    items = {}
    blocked = set()
    for r in loot:
        if r['kind']!='stackable':
            continue
        blocked.add(r['id'])
        if r.get('stackable_type') not in slots:
            continue
        cells = r['Script']['cells']
        name = next((b.get('reference','') for a,b in zip(cells,cells[1:]) if a.get('text')=='[name]'),'')
        items[r['id']] = ['材料/道具',r['Script']['path'],name]
    for r in equipment_rows if equipment_rows is not None else read(CFG/'equipment.generated.json')['rows']:
        f = r['Fields']
        kinds = f.get('[equipment type]', [{}])
        d = f.get('[durability]',[])
        rarity = f.get('[rarity]',[])
        if (r['ID'] in blocked or len(f.get('[attach type]',[]))!=1 or not kinds or
            len(rarity)!=1 or rarity[0].get('type')!=0 or rarity[0]['value']<0 or not
            ((len(d)==1 and d[0].get('type')==0 and 0<=d[0]['value']<=65535)
             or (not d and kinds[0].get('text') in ('[amulet]','[wrist]','[ring]','[support]',
                 '[magic stone]','[earring]','[title name]','[talisman]')))):
            continue
        items[r['ID']] = ['装备',r['Path'],f.get('[name]',[{}])[0].get('reference','')]
    return items


def submit(role, ident, amount, reason, items, directory=DATA):
    if role not in dict(roles()) or ident not in items or not 1<=amount<=999 or not reason.strip():
        raise ValueError('请选择有效角色、物品和数量，并填写发放原因')
    key = uuid.uuid4().hex
    atomic(directory/'pending'/(key+'.json'),dict(character_id=role,items=[dict(template=ident,amount=amount)],reason=reason[:160]))
    return key


def save_rule(maps, template, percent, items, maplist, directory=DATA):
    if not 0<=percent<=100 or (percent and (not maps or not set(maps)<=set(maplist)
            or items.get(template,[None])[0]!='装备')):
        raise ValueError('请选择已导入的房间和可入包的装备，概率范围为 0 到 100')
    atomic(directory/'abyss.json',dict(maps=sorted(set(maps)),template=template,percent=percent))


DIFFICULTY_TIERS = (
    (0, '普通', '#94a3b8', 1.0, 12, 1, {'0': 25, '1': 35, '2': 25, '3': 10, '6': 3, '4': 2, '7': 0}),
    (1, '冒险', '#60a5fa', 1.2, 18, 1, {'0': 15, '1': 28, '2': 30, '3': 16, '6': 6, '4': 5, '7': 0}),
    (2, '勇士', '#a78bfa', 1.5, 25, 1, {'0': 8,  '1': 18, '2': 28, '3': 22, '6': 12, '4': 11, '7': 1}),
    (3, '王者', '#f59e0b', 2.0, 35, 2, {'0': 3,  '1': 10, '2': 20, '3': 24, '6': 18, '4': 22, '7': 3}),
    (4, '噩梦', '#f43f5e', 3.0, 50, 2, {'0': 0,  '1': 5,  '2': 12, '3': 20, '6': 20, '4': 35, '7': 8}),
)


def default_global_drop():
    return {
        'version': 1,
        'global': {
            'enabled': True,
            'difficulties': [
                {
                    'difficulty': idx,
                    'name': name,
                    'enabled': True,
                    'drop_rate_multiplier': mult,
                    'equip_drop_chance': chance,
                    'drop_count': count,
                    'level_match': True,
                    'rarity_weights': dict(weights),
                    'custom_item_ids': [],
                }
                for idx, name, _color, mult, chance, count, weights in DIFFICULTY_TIERS
            ],
        },
        'normal_abyss': {
            'enabled': True,
            'scope': 'all',
            'maps': [],
            'drop_chance': 25,
            'drop_count': 1,
            'pool_mode': 'level_epic',
            'rarity_weights': {'3': 20, '6': 25, '4': 55},
            'custom_item_ids': [],
        },
        'lv100_abyss': {
            'enabled': True,
            'scope': 'all',
            'maps': [],
            'drop_chance': 35,
            'drop_count': 2,
            'epic_weight': 85,
            'mythic_weight': 15,
            'pool_mode': 'lv100_all',
            'custom_item_ids': [],
        },
    }


def load_global_drop(directory=DATA):
    base = default_global_drop()
    p = directory / 'global_drop.json'
    if not p.is_file():
        return base, False
    raw = read(p)
    out = default_global_drop()
    g = raw.get('global') or {}
    out['global']['enabled'] = bool(g.get('enabled', True))
    by_diff = {int(d.get('difficulty', i)): d for i, d in enumerate(g.get('difficulties') or [])}
    merged_diffs = []
    for item in out['global']['difficulties']:
        src = by_diff.get(item['difficulty'])
        if src:
            item['enabled'] = bool(src.get('enabled', item['enabled']))
            item['drop_rate_multiplier'] = max(0.1, min(50.0, float(src.get('drop_rate_multiplier', item['drop_rate_multiplier']))))
            item['equip_drop_chance'] = max(0, min(100, int(src.get('equip_drop_chance', item['equip_drop_chance']))))
            item['drop_count'] = max(1, min(5, int(src.get('drop_count', item['drop_count']))))
            item['level_match'] = bool(src.get('level_match', item['level_match']))
            if isinstance(src.get('rarity_weights'), dict):
                rw = {}
                for k in ('0', '1', '2', '3', '6', '4', '7'):
                    rw[k] = max(0, min(100, int(src['rarity_weights'].get(k, item['rarity_weights'].get(k, 0)))))
                item['rarity_weights'] = rw
            item['custom_item_ids'] = [int(x) for x in (src.get('custom_item_ids') or []) if int(x) > 0]
        merged_diffs.append(item)
    out['global']['difficulties'] = merged_diffs

    na = raw.get('normal_abyss') or {}
    out['normal_abyss']['enabled'] = bool(na.get('enabled', True))
    out['normal_abyss']['scope'] = str(na.get('scope') or 'all')
    out['normal_abyss']['maps'] = [int(x) for x in (na.get('maps') or []) if int(x) > 0]
    out['normal_abyss']['drop_chance'] = max(0, min(100, int(na.get('drop_chance', 25))))
    out['normal_abyss']['drop_count'] = max(1, min(5, int(na.get('drop_count', 1))))
    out['normal_abyss']['pool_mode'] = str(na.get('pool_mode') or 'level_epic')
    if isinstance(na.get('rarity_weights'), dict):
        out['normal_abyss']['rarity_weights'] = {
            k: max(0, min(100, int(na['rarity_weights'].get(k, out['normal_abyss']['rarity_weights'][k]))))
            for k in ('3', '6', '4')
        }
    out['normal_abyss']['custom_item_ids'] = [int(x) for x in (na.get('custom_item_ids') or []) if int(x) > 0]

    la = raw.get('lv100_abyss') or {}
    out['lv100_abyss']['enabled'] = bool(la.get('enabled', True))
    out['lv100_abyss']['scope'] = str(la.get('scope') or 'all')
    out['lv100_abyss']['maps'] = [int(x) for x in (la.get('maps') or []) if int(x) > 0]
    out['lv100_abyss']['drop_chance'] = max(0, min(100, int(la.get('drop_chance', 35))))
    out['lv100_abyss']['drop_count'] = max(1, min(5, int(la.get('drop_count', 2))))
    out['lv100_abyss']['epic_weight'] = max(0, min(100, int(la.get('epic_weight', 85))))
    out['lv100_abyss']['mythic_weight'] = max(0, min(100, int(la.get('mythic_weight', 15))))
    out['lv100_abyss']['pool_mode'] = str(la.get('pool_mode') or 'lv100_all')
    out['lv100_abyss']['custom_item_ids'] = [int(x) for x in (la.get('custom_item_ids') or []) if int(x) > 0]
    return out, True


def save_global_drop(policy, directory=DATA):
    if not isinstance(policy, dict):
        raise ValueError('无效的全局掉落配置')
    g = policy.get('global') or {}
    diffs = g.get('difficulties') or []
    if len(diffs) != 5:
        raise ValueError('全局掉落必须包含 5 个副本难度配置（普通到噩梦）')
    for d in diffs:
        ch = int(d.get('equip_drop_chance', 0))
        if not 0 <= ch <= 100:
            raise ValueError('难度装备掉率必须在 0 到 100 之间')
        cnt = int(d.get('drop_count', 1))
        if not 1 <= cnt <= 5:
            raise ValueError('单次触发掉落件数必须在 1 到 5 之间')
    for key, label in (('normal_abyss', '普通深渊'), ('lv100_abyss', '100级深渊')):
        sec = policy.get(key) or {}
        if not 0 <= int(sec.get('drop_chance', 0)) <= 100:
            raise ValueError(f'{label}触发掉率必须在 0 到 100 之间')
        if not 1 <= int(sec.get('drop_count', 1)) <= 5:
            raise ValueError(f'{label}单次掉落件数必须在 1 到 5 之间')
        if sec.get('enabled') and sec.get('pool_mode') == 'custom' and not sec.get('custom_item_ids'):
            raise ValueError(f'{label}选择了自定义装备池，请至少添加一件装备')
        if sec.get('enabled') and sec.get('scope') == 'maps' and not sec.get('maps'):
            raise ValueError(f'{label}选择了指定房间生效，请至少选择一个房间')
    payload = dict(policy)
    payload['version'] = 1
    atomic(directory / 'global_drop.json', payload)
    return directory / 'global_drop.json'


def client_up():
    return any(str(p.info['name']).lower()=='dfo.exe' for p in psutil.process_iter(['name']))


def gateway():
    try:
        state = read(READY)
        p = psutil.Process(state['pid'])
        if Path(p.exe()).resolve()!=pre.SERVER_BIN.resolve():
            return None
        host,port = state['address'].rsplit(':',1)
        if host not in ('127.0.0.1','127.0.0.2'):
            return None
        if any(c.status=='LISTEN' and c.laddr==(host,int(port)) for c in p.net_connections(kind='tcp')):
            return state
    except (OSError,ValueError,KeyError,psutil.Error):
        pass
    return None


def login(server_only=False):
    if not server_only and client_up():
        raise ValueError('客户端已经打开；不会启动第二个窗口。')
    if not server_only:
        from game_fonts import ORIGINAL
        directory=pre.client_exe().parent/'Fonts'
        missing=[n for n in ORIGINAL if not (directory/n).is_file()]
        if missing or (ROOT/'runtime'/'atelier-fonts'/'pending.json').is_file():
            detail=('缺少 '+', '.join(missing)) if missing else '字体切换尚未完成'
            raise ValueError(detail+'。请先到「Mod 工坊 → 游戏字体」应用字体或还原原版，再启动游戏。')
    state = gateway()
    py = Path(sys.executable).with_name('python.exe')
    logs = ROOT/'runtime'/'launcher.log'
    with logs.open('ab') as log:
        def spawn(args):
            return subprocess.Popen([str(py),'-u',*args],cwd=ROOT,stdin=subprocess.DEVNULL,
                                    stdout=log,stderr=log,creationflags=subprocess.CREATE_NO_WINDOW)
        if state is None:
            proc = spawn(['tools/us100_interactive.py','3','normal','--no-client'])
            for _ in range(600):
                if proc.poll() is not None:
                    raise ValueError('网关未启动，请查看 runtime/launcher.log 的预检结果')
                state=gateway()
                if state: break
                time.sleep(.25)
            if not state: raise TimeoutError('网关就绪超时；请查看 runtime/launcher.log')
        if server_only:
            if state.get('online_item_grants'):return '网关已就绪，支持在线和离线物品发放。'
            return '网关已就绪，自动处理离线发放。' if state.get('launcher_gm') else '旧网关不支持 GM，请先正常停止旧网关。'
        # This existing entry verifies a single client and records its PID.
        proc=spawn(['tools/us100_client.py'])
        for _ in range(60):
            if proc.poll() is not None: raise ValueError('客户端已退出，请查看 runtime/launcher.log')
            if client_up(): return '客户端已启动，请在游戏窗口选择角色。'
            time.sleep(.25)
        raise TimeoutError('未确认客户端进程，请查看 runtime/launcher.log')


def stop_server():
    """Gracefully stop the local US100 gateway server (wireprobe-us100.exe) and its wrapper."""
    target_bin = pre.SERVER_BIN.resolve()
    targets = []
    seen_pids = set()

    # 1. Include PID recorded in ready.json if it still points to our server binary
    try:
        state = read(READY)
        p = psutil.Process(int(state['pid']))
        if Path(p.exe()).resolve() == target_bin:
            targets.append(p)
            seen_pids.add(p.pid)
    except (OSError, ValueError, KeyError, psutil.Error):
        pass

    # 2. Scan for any other running wireprobe-us100.exe from this workspace
    for p in psutil.process_iter(['pid', 'exe', 'cmdline']):
        try:
            pid = p.info['pid']
            if pid in seen_pids:
                continue
            exe = p.info.get('exe')
            if exe and Path(exe).resolve() == target_bin:
                targets.append(p)
                seen_pids.add(pid)
        except (OSError, psutil.Error):
            continue

    # 3. Also find parent/wrapper python processes running us100_interactive.py in this workspace
    wrappers = []
    root_norm = str(ROOT.resolve()).lower()
    for p in psutil.process_iter(['pid', 'name', 'cmdline', 'cwd']):
        try:
            pid = p.info['pid']
            if pid in seen_pids or pid == os.getpid():
                continue
            cmd = ' '.join(p.info.get('cmdline') or []).lower()
            if 'us100_interactive.py' in cmd:
                cwd = (p.info.get('cwd') or '').lower()
                if not cwd or cwd.startswith(root_norm) or root_norm in cmd:
                    wrappers.append(p)
                    seen_pids.add(pid)
        except (OSError, psutil.Error):
            continue

    if not targets and not wrappers:
        READY.unlink(missing_ok=True)
        return '本地 100 级服务端当前未运行。'

    for proc in (*targets, *wrappers):
        try:
            proc.terminate()
        except psutil.Error:
            pass

    _, alive = psutil.wait_procs([*targets, *wrappers], timeout=4.0)
    for proc in alive:
        try:
            proc.kill()
        except psutil.Error:
            pass
    if alive:
        psutil.wait_procs(alive, timeout=2.0)

    READY.unlink(missing_ok=True)
    return f'已关闭本地 100 级服务端（已停止 {len(targets) or len(wrappers)} 个进程）。'


def restart_server():
    """Stop the running US100 gateway (if any) and launch a fresh instance."""
    stop_server()
    return login(server_only=True)


def digest(path):
    with path.open('rb') as f:
        return hashlib.file_digest(f,'sha256').hexdigest()


def file_copy(src,dst):
    dst.parent.mkdir(parents=True,exist_ok=True)
    with tempfile.NamedTemporaryFile(dir=dst.parent,delete=False,suffix='.part') as f:
        tmp=Path(f.name)
    try:
        shutil.copyfile(src,tmp)
        tmp.replace(dst)
    finally:
        tmp.unlink(missing_ok=True)


class Library:
    def __init__(self,packs,root=SHELF):
        self.packs,self.root=packs,root
        self.root.mkdir(parents=True,exist_ok=True)

    def entries(self):
        return {p.parent.name:read(p) for p in sorted(self.root.glob('*/mod.json'))
                if re.fullmatch('[a-f0-9]{32}',p.parent.name)}

    def import_pack(self,src,kind):
        src=Path(src)
        if kind not in ('时装','宠物','其他') or src.suffix.lower()!='.npk' or not src.is_file():
            raise ValueError('仅支持时装、宠物或其他 NPK 资源')
        index(src)
        sha=digest(src)
        if any(m['sha']==sha and m['target'].lower()==src.name.lower() for m in self.entries().values()):
            raise ValueError('这个包已在库中')
        key=uuid.uuid4().hex
        folder=self.root/key
        file_copy(src,folder/'package.npk')
        if digest(folder/'package.npk')!=sha: raise ValueError('导入期间源文件发生变化')
        atomic(folder/'mod.json',dict(version=1,name=src.stem,kind=kind,target=src.name,sha=sha,state='off'))
        return key

    def switch(self,key,on):
        if client_up(): raise ValueError('请先正常退出游戏再切换 Mod')
        lib=self.entries()
        m=lib[key]
        name=m['target']
        if Path(name).name!=name or ':' in name or not name.lower().endswith('.npk'):
            raise ValueError('非法资源文件名')
        folder=self.root/key
        dst=self.packs/name
        if not self.packs.is_dir() or dst.resolve().parent!=self.packs.resolve() or dst.is_symlink():
            raise ValueError('无效的客户端资源目录')
        if any(k!=key and v['target'].lower()==name.lower() and v['state']!='off' for k,v in lib.items()):
            raise ValueError('同名资源已由其他 Mod 接管')
        record=folder/'mod.json'
        src,backup=folder/'package.npk',folder/'original.bak'
        current=digest(dst) if dst.exists() else None
        if m['state']=='off' and not on: return
        if on:
            if digest(src)!=m['sha']: raise ValueError('库存包校验失败')
            if m['state']=='off':
                if current is not None:
                    file_copy(dst,backup)
                    if digest(backup)!=current: raise ValueError('备份校验失败')
                m.update(state='installing',original=current)
                atomic(record,m)
            if current not in (m['original'],m['sha']): raise ValueError('客户端文件已被外部修改，未覆盖')
            if current!=m['sha']: file_copy(src,dst)
            if digest(dst)!=m['sha']: raise ValueError('安装校验失败')
            m['state']='on'
        else:
            orig=m['original']
            if current not in (orig,m['sha']): raise ValueError('客户端文件已被外部修改，未覆盖')
            if orig is not None and (not backup.exists() or digest(backup)!=orig): raise ValueError('备份缺失或校验失败')
            if current!=orig:
                if orig is None: dst.unlink()
                else: file_copy(backup,dst)
            m['state']='off'
        atomic(record,m)
