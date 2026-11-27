from datetime import datetime, timezone
import hashlib
import hmac
import json
import os
from pathlib import Path
import re
import secrets
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


PBKDF2_SCHEME = 'pbkdf2_sha256'
PBKDF2_ITERATIONS = 100000
PBKDF2_SALT_BYTES = 16
PBKDF2_KEY_BYTES = 32
USERNAME_RE = re.compile(r'^[A-Za-z0-9_.-]{3,32}$')

_ACTIVE_ACCOUNT = None


def db_path():
    config = pre.storage_config()
    return Path(config['db_path']) if config.get('db_path') else SERVER/'runtime'/'us100.db'


def db_exists():
    return db_path().is_file()


def _load_db_tables():
    db = db_path()
    if not db.exists():
        return {}
    return read(db).get('tables', {})


def validate_account_credentials(username, password, confirm_password=None):
    user = (username or '').strip()
    if not USERNAME_RE.fullmatch(user):
        raise ValueError('账号长度需为 3~32 位，仅支持字母、数字、下划线(_)、点(.)或短横线(-)。')
    pwd = password or ''
    if len(pwd) < 4 or len(pwd) > 64:
        raise ValueError('密码长度需为 4~64 位。')
    if confirm_password is not None and pwd != confirm_password:
        raise ValueError('两次输入的密码不一致，请重新确认。')
    return user, pwd


def hash_password(password):
    pwd = password or ''
    if len(pwd) < 4 or len(pwd) > 64:
        raise ValueError('密码长度需为 4~64 位。')
    salt = secrets.token_bytes(PBKDF2_SALT_BYTES)
    dk = hashlib.pbkdf2_hmac('sha256', pwd.encode('utf-8'), salt, PBKDF2_ITERATIONS, dklen=PBKDF2_KEY_BYTES)
    return f'{PBKDF2_SCHEME}${PBKDF2_ITERATIONS}${salt.hex()}${dk.hex()}'


def verify_password(stored, password):
    if not stored or not password:
        return False
    parts = str(stored).split('$')
    if len(parts) != 4 or parts[0] != PBKDF2_SCHEME:
        return False
    try:
        iterations = int(parts[1])
        if iterations < 1000 or iterations > 2000000:
            return False
        salt = bytes.fromhex(parts[2])
        expected = bytes.fromhex(parts[3])
    except ValueError:
        return False
    if not salt or not expected:
        return False
    actual = hashlib.pbkdf2_hmac('sha256', password.encode('utf-8'), salt, iterations, dklen=len(expected))
    return hmac.compare_digest(actual, expected)


def list_accounts(include_dev=False):
    tables = _load_db_tables()
    rows = []
    for raw in tables.get('accounts', {}).values():
        if not isinstance(raw, dict):
            continue
        uname = str(raw.get('username') or '').strip()
        if not uname:
            continue
        has_pwd = bool(raw.get('password_hash'))
        dev_only = bool(raw.get('development_only', False))
        if not include_dev and not has_pwd:
            continue
        rows.append({
            'id': int(raw.get('id', 0) or 0),
            'username': uname,
            'has_password': has_pwd,
            'development_only': dev_only,
            'created_at': str(raw.get('created_at') or ''),
        })
    rows.sort(key=lambda r: (r['id'], r['username'].lower()))
    return rows


def active_account():
    return _ACTIVE_ACCOUNT


def set_active_account(username):
    global _ACTIVE_ACCOUNT
    _ACTIVE_ACCOUNT = (username or '').strip() or None
    return _ACTIVE_ACCOUNT


def logout_account():
    global _ACTIVE_ACCOUNT
    _ACTIVE_ACCOUNT = None


def create_account(username, password, confirm_password=None):
    """Create a password-protected account and initialize us100.db on first creation."""
    user, pwd = validate_account_credentials(username, password, confirm_password=confirm_password)
    if client_up():
        raise ValueError('游戏客户端正在运行，请先退出游戏后再创建新账号。')
    if gateway() is not None:
        # db100 keeps an in-memory snapshot in wireprobe; stop the idle gateway
        # before writing us100.db so the new account is never overwritten.
        stop_server()

    db = db_path()
    existed = db.is_file()
    if existed:
        doc = read(db)
        if doc.get('magic') != 'US100DB1' or int(doc.get('version', 0) or 0) != 1:
            raise ValueError(f'数据库文件格式不受支持：{db}')
        tables = doc.setdefault('tables', {})
    else:
        doc = {'magic': 'US100DB1', 'version': 1, 'revision': 0, 'tables': {}}
        tables = doc['tables']

    accounts = tables.setdefault('accounts', {})
    meta = tables.setdefault('meta', {})
    want = user.lower()
    max_id = int(meta.get('seq:accounts', 0) or 0)
    for k, row in accounts.items():
        if isinstance(row, dict):
            try:
                max_id = max(max_id, int(row.get('id', 0) or 0))
            except (TypeError, ValueError):
                pass
            if str(row.get('username') or '').strip().lower() == want:
                raise ValueError(f'账号「{user}」已存在，请直接登录或更换其他账号名。')
        try:
            max_id = max(max_id, int(k))
        except (TypeError, ValueError):
            pass

    next_id = max_id + 1
    now_iso = datetime.now(timezone.utc).isoformat().replace('+00:00', 'Z')
    accounts[str(next_id)] = {
        'id': next_id,
        'username': user,
        'password_hash': hash_password(pwd),
        'development_only': False,
        'created_at': now_iso,
    }
    meta['seq:accounts'] = next_id
    doc['revision'] = int(doc.get('revision', 0) or 0) + 1
    atomic(db, doc)
    set_active_account(user)
    return {
        'id': next_id,
        'username': user,
        'db_created': not existed,
        'db_path': str(db),
    }


def migrate_account_data(src_username, dst_username, backup=True):
    """Re-assign characters and all account-scoped tables from src_username to dst_username."""
    if client_up():
        raise ValueError('游戏客户端正在运行，请先退出游戏后再迁移存档。')
    if gateway() is not None:
        stop_server()

    db = db_path()
    if not db.is_file():
        raise ValueError(f'数据库文件不存在：{db}')
    doc = read(db)
    tables = doc.setdefault('tables', {})
    accounts = tables.get('accounts', {})

    def _find_acct(name):
        want = (name or '').strip().lower()
        for row in accounts.values():
            if isinstance(row, dict) and str(row.get('username') or '').strip().lower() == want:
                return int(row['id']), str(row['username'])
        return None, None

    src_id, src_name = _find_acct(src_username)
    dst_id, dst_name = _find_acct(dst_username)
    if src_id is None:
        raise ValueError(f'源账号「{src_username}」不存在。')
    if dst_id is None:
        raise ValueError(f'目标账号「{dst_username}」不存在。')
    if src_id == dst_id:
        raise ValueError('源账号与目标账号相同，无需迁移。')

    backup_path = None
    if backup:
        stamp = datetime.now().strftime('%Y%m%d-%H%M%S')
        backup_path = db.with_name(f'{db.name}.bak.{stamp}')
        file_copy(db, backup_path)

    src_key, dst_key = str(src_id), str(dst_id)
    src_prefix, dst_prefix = f'{src_key}/', f'{dst_key}/'
    stats = {'characters': 0, 'live_characters': 0, 'tables_moved': {}}

    # 1. Characters (re-assign account_id; keep wire_ids conflict-free)
    chars = tables.get('characters', {})
    max_wire = 0
    for row in chars.values():
        if isinstance(row, dict) and int(row.get('account_id', 0) or 0) == dst_id:
            max_wire = max(max_wire, int(row.get('wire_id', 0) or 0))
    src_chars = sorted(
        (row for row in chars.values() if isinstance(row, dict) and int(row.get('account_id', 0) or 0) == src_id),
        key=lambda r: (int(r.get('wire_id', 0) or 0), int(r.get('id', 0) or 0)),
    )
    for row in src_chars:
        row['account_id'] = dst_id
        if max_wire > 0:
            max_wire += 1
            row['wire_id'] = max_wire
        stats['characters'] += 1
        if not row.get('deleted_at'):
            stats['live_characters'] += 1

    # 2. Single-key account tables (key == str(account_id))
    def _move_exact(table_name, merge_fn=None):
        tbl = tables.get(table_name)
        if not isinstance(tbl, dict) or src_key not in tbl:
            return
        src_val = tbl.pop(src_key)
        if dst_key in tbl and merge_fn is not None:
            tbl[dst_key] = merge_fn(tbl[dst_key], src_val)
        else:
            if merge_fn is not None:
                tbl[dst_key] = merge_fn(None, src_val)
            else:
                tbl[dst_key] = src_val
        stats['tables_moved'][table_name] = 1

    def _merge_currency(dst_val, src_val):
        out = dict(src_val or {})
        out['account_id'] = dst_id
        if isinstance(dst_val, dict):
            out['cera'] = int(dst_val.get('cera', 0) or 0) + int(out.get('cera', 0) or 0)
            out['mileage'] = int(dst_val.get('mileage', 0) or 0) + int(out.get('mileage', 0) or 0)
        return out

    def _merge_premium(dst_val, src_val):
        out = dict(dst_val or {})
        for k, v in (src_val or {}).items():
            out[k] = max(int(out.get(k, 0) or 0), int(v or 0))
        return out

    def _merge_materials(dst_val, src_val):
        out = dict(dst_val or {})
        for k, v in (src_val or {}).items():
            out[k] = int(out.get(k, 0) or 0) + int(v or 0)
        return out

    def _merge_danji(dst_val, src_val):
        out = dict(src_val or {})
        if isinstance(dst_val, dict):
            out['stack'] = int(dst_val.get('stack', 0) or 0) + int(out.get('stack', 0) or 0)
        return out

    def _fix_acct_field(dst_val, src_val):
        out = dict(src_val or {})
        if 'account_id' in out:
            out['account_id'] = dst_id
        return out

    _move_exact('account_currency', _merge_currency)
    _move_exact('account_mileage_daily', _fix_acct_field)
    _move_exact('us224_account_premium', _merge_premium)
    _move_exact('us224_account_materials', _merge_materials)
    _move_exact('danji_stack_v224', _merge_danji)
    _move_exact('account_basic_keys_v224')
    _move_exact('account_game_options_v224')

    # 3. Prefix-keyed account tables (key == "<account_id>/...")
    for tname in (
        'character_options_v224',
        'character_target_equipment_v224',
        'neo_daily_claims',
        'cashshop_receipts',
        'premium_cart_receipts',
        'shop_contract_reconciliation',
    ):
        tbl = tables.get(tname)
        if not isinstance(tbl, dict):
            continue
        moved = 0
        for old_k in [k for k in list(tbl.keys()) if k.startswith(src_prefix)]:
            val = tbl.pop(old_k)
            new_k = dst_prefix + old_k[len(src_prefix):]
            if isinstance(val, dict):
                if val.get('Account') == src_id:
                    val['Account'] = dst_id
                if val.get('account_id') == src_id:
                    val['account_id'] = dst_id
            tbl[new_k] = val
            moved += 1
        if moved:
            stats['tables_moved'][tname] = moved

    # 4. admin_grants audit records
    grants = tables.get('admin_grants')
    if isinstance(grants, dict):
        moved = 0
        for row in grants.values():
            if isinstance(row, dict) and row.get('account_id') == src_id:
                row['account_id'] = dst_id
                if isinstance(row.get('request'), dict) and row['request'].get('account_id') == src_id:
                    row['request']['account_id'] = dst_id
                moved += 1
        if moved:
            stats['tables_moved']['admin_grants'] = moved

    doc['revision'] = int(doc.get('revision', 0) or 0) + 1
    atomic(db, doc)
    return {
        'src_id': src_id,
        'src_username': src_name,
        'dst_id': dst_id,
        'dst_username': dst_name,
        'backup_path': str(backup_path) if backup_path else None,
        **stats,
    }


def authenticate_account(username, password):
    """Verify username and password against us100.db and activate the session."""
    user = (username or '').strip()
    pwd = password or ''
    if not user or not pwd:
        raise ValueError('请输入账号与密码。')
    if not db_exists():
        raise ValueError('尚未创建账号，数据库未生成。请先在主页输入账号及密码并点击「创建账号」。')
    tables = _load_db_tables()
    want = user.lower()
    matched = None
    for row in tables.get('accounts', {}).values():
        if isinstance(row, dict) and str(row.get('username') or '').strip().lower() == want:
            matched = row
            break
    if matched is None:
        raise ValueError(f'账号「{user}」不存在，请先点击「创建账号」。')
    stored_hash = matched.get('password_hash')
    if not stored_hash:
        raise ValueError(f'账号「{user}」未设置密码（旧开发账号），请新建带密码的账号再登录。')
    if not verify_password(stored_hash, pwd):
        raise ValueError('密码错误，请检查后重试。')
    real_name = str(matched['username'])
    set_active_account(real_name)
    return {
        'id': int(matched['id']),
        'username': real_name,
        'created_at': str(matched.get('created_at') or ''),
    }


def _resolve_username(username):
    if username is not None:
        return username
    return active_account()


def account_id(username=None):
    target = _resolve_username(username)
    if not target:
        return None
    want = target.lower()
    tables = _load_db_tables()
    for a in tables.get('accounts', {}).values():
        if str(a.get('username') or '').lower() == want:
            return a.get('id')
    return None


def account_cera(username=None):
    aid = account_id(username)
    if aid is None:
        return 0
    tables = _load_db_tables()
    row = tables.get('account_currency', {}).get(str(aid), {})
    return max(0, int(row.get('cera', 0) or 0))


def account_mileage(username=None):
    aid = account_id(username)
    if aid is None:
        return 0
    tables = _load_db_tables()
    row = tables.get('account_currency', {}).get(str(aid), {})
    return max(0, int(row.get('mileage', 0) or 0))


def account_contracts(username=None):
    aid = account_id(username)
    if aid is None:
        return {}
    tables = _load_db_tables()
    raw = tables.get('us224_account_premium', {}).get(str(aid), {})
    now = int(time.time())
    out = {}
    if isinstance(raw, dict):
        for k, end_ts in raw.items():
            try:
                kind = int(k)
                rem = int(end_ts) - now
                if rem > 0:
                    out[kind] = rem
            except (TypeError, ValueError):
                continue
    return out


def activity_progress(role_id):
    tables = _load_db_tables()
    row = tables.get('character_activity_progress', {}).get(str(role_id))
    if not isinstance(row, dict):
        return {
            'day': '', 'daily_kills': 0, 'daily_clears': 0,
            'daily_kill_claimed': False, 'daily_clear_claimed': False,
            'week': '', 'weekly_clears': 0,
            'total_kills': 0, 'total_clears': 0, 'total_hell_clears': 0,
            'claims': {},
        }
    return {
        'day': str(row.get('day') or ''),
        'daily_kills': int(row.get('daily_kills', 0) or 0),
        'daily_clears': int(row.get('daily_clears', 0) or 0),
        'daily_kill_claimed': bool(row.get('daily_kill_claimed', False)),
        'daily_clear_claimed': bool(row.get('daily_clear_claimed', False)),
        'week': str(row.get('week') or ''),
        'weekly_clears': int(row.get('weekly_clears', 0) or 0),
        'total_kills': int(row.get('total_kills', 0) or 0),
        'total_clears': int(row.get('total_clears', 0) or 0),
        'total_hell_clears': int(row.get('total_hell_clears', 0) or 0),
        'claims': dict(row.get('claims') or {}),
    }


def roles(username=None):
    target = _resolve_username(username)
    if not target:
        return []
    want = target.lower()
    tables = _load_db_tables()
    accounts = {a['id'] for a in tables.get('accounts', {}).values() if str(a.get('username') or '').lower() == want}
    if not accounts:
        return []
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
    if action not in (
        'currency', 'level', 'advancement', 'bag', 'quest', 'avatar',
        'contract', 'mileage_redeem', 'activity_claim', 'refresh_sync',
    ):
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


CONTRACT_SPECS = (
    (118, 'Neo 尊享特权 Plus', '273疲劳 · +10%通关经验 · 翻牌金币 · 每日礼盒/租赁武器'),
    (120, 'VIP 会员契约', '通关额外黄金翻牌金币奖励'),
    (22,  '霸王之契约', '可穿戴高于自身等级 +10 级装备'),
    (27,  '达人之契约', '可学习高于自身等级 +5 级技能'),
    (79,  '成长之契约', '+20% 通关经验 · +20% 怪物基础掉落率'),
    (92,  '晶体契约', '晶体契约计时与客户端槽位激活'),
    (73,  '加百利之契约', '通关有 35% 概率触发加百利额外材料与金币补给'),
    (125, '不稳定裂缝契约', '深渊派对房间额外增加 +1 件史诗/神话掉落判定'),
)

# Verified US100 PVF etc/mileageshop.etc & stackable catalog rewards
MILEAGE_SHOP_ITEMS = (
    (590704678, '[M] 忠实材料礼袋', 10, 1, 'etc/mileageshop.etc 官方积分商品'),
    (590704679, '[M] 忠实材料礼袋 (10个装)', 100, 1, 'etc/mileageshop.etc 官方积分商品'),
    (50020670,  '3倍成长药剂 [30分钟]', 10, 1, 'etc/mileageshop.etc 刷图升级加速'),
    (10000844,  '账号绑定大喇叭', 5, 1, 'etc/mileageshop.etc'),
    (590704706, '[M] 神器符文自选礼盒', 75, 1, 'etc/mileageshop.etc 官方积分商品'),
    (590704707, '[M] 圣痕护石自选礼盒', 150, 1, 'etc/mileageshop.etc 官方积分商品'),
    (10326026,  '[M] 胜·深渊之鳞装备自选礼盒', 250, 1, 'etc/mileageshop.etc 换装装备'),
    (10326027,  '[M] 胜·深渊之鳞武器S自选礼盒', 500, 1, 'etc/mileageshop.etc 换装武器'),
    (10307650,  '[M] 稀有装扮属性变更券', 200, 1, 'etc/mileageshop.etc'),
    (10334101,  '[M] 一次性材质转换器', 300, 1, 'etc/mileageshop.etc'),
    (10327281,  '炉岩核 ×50', 20, 50, 'US100 强化/养成通用材料'),
    (10327282,  '协调结晶体 ×30', 30, 30, 'US100 增幅/养成通用材料'),
)


def default_events_policy(active=False):
    return {
        'version': 1,
        'exp_event_enabled': bool(active),
        'exp_multiplier': 2.0 if active else 1.0,
        'drop_event_enabled': bool(active),
        'drop_rate_multiplier': 2.0 if active else 1.0,
        'gold_multiplier': 2.0 if active else 1.0,
        'card_event_enabled': bool(active),
        'card_gold_multiplier': 2.0 if active else 1.0,
        'fatigue': {
            'weekend_double': bool(active),
            'bonus_limit': 0,
            'zero_cost': False,
            'burning_enabled': bool(active),
        },
        'mileage': {
            'enabled': bool(active),
            'dungeon_clear_points': 2,
            'daily_cap': 50,
            'shop_rebate_percent': 5,
        },
        'hell_party': {
            'enabled': bool(active),
            'trigger_chance': 35,
            'min_dungeon_level': 1,
            'drop_count': 2,
            'epic_weight': 85,
            'mythic_weight': 15,
        },
        'daily_challenge': {
            'enabled': bool(active),
            'kill_target': 15,
            'clear_target': 3,
            'reward_gold': 200000,
            'reward_mileage': 10,
            'reward_template': 10327281,
            'reward_amount': 30,
        },
        'weekly_dungeon': {
            'enabled': bool(active),
            'weekly_limit': 6,
            'reset_weekday': 2,
            'reward_gold': 500000,
            'reward_mileage': 15,
            'reward_template': 10327282,
            'reward_amount': 20,
        },
        'contracts': {
            'growth_drop_bonus': bool(active),
            'gabriel_bonus': bool(active),
            'unstable_rift_bonus': bool(active),
        },
        'extra': {
            'daily_checkin_enabled': True,
            'seria_welcome_enabled': True,
            'returnee_care_enabled': True,
            'otp_security_enabled': True,
            'pcbang_bonus_enabled': False,
        },
    }


def load_events_policy(directory=None):
    directory = DATA if directory is None else directory
    p = directory / 'events.json'
    if not p.is_file():
        return default_events_policy(active=False), False
    raw = read(p)
    out = default_events_policy(active=False)
    out['exp_event_enabled'] = bool(raw.get('exp_event_enabled', False))
    out['exp_multiplier'] = max(1.0, min(10.0, float(raw.get('exp_multiplier', 1.0) or 1.0)))
    out['drop_event_enabled'] = bool(raw.get('drop_event_enabled', False))
    out['drop_rate_multiplier'] = max(1.0, min(10.0, float(raw.get('drop_rate_multiplier', 1.0) or 1.0)))
    out['gold_multiplier'] = max(1.0, min(10.0, float(raw.get('gold_multiplier', 1.0) or 1.0)))
    out['card_event_enabled'] = bool(raw.get('card_event_enabled', False))
    out['card_gold_multiplier'] = max(1.0, min(5.0, float(raw.get('card_gold_multiplier', 1.0) or 1.0)))
    for section in ('fatigue', 'mileage', 'hell_party', 'daily_challenge', 'weekly_dungeon', 'contracts', 'extra'):
        src = raw.get(section)
        if isinstance(src, dict):
            for k, default_v in out[section].items():
                if k in src:
                    if isinstance(default_v, bool):
                        out[section][k] = bool(src[k])
                    elif isinstance(default_v, int):
                        out[section][k] = int(src[k])
                    else:
                        out[section][k] = src[k]
    return out, True


def save_events_policy(policy, directory=None):
    directory = DATA if directory is None else directory
    if not isinstance(policy, dict):
        raise ValueError('无效的服务器活动配置')
    payload = default_events_policy(active=False)
    payload['exp_event_enabled'] = bool(policy.get('exp_event_enabled', False))
    payload['exp_multiplier'] = round(max(1.0, min(10.0, float(policy.get('exp_multiplier', 1.0) or 1.0))), 2)
    payload['drop_event_enabled'] = bool(policy.get('drop_event_enabled', False))
    payload['drop_rate_multiplier'] = round(max(1.0, min(10.0, float(policy.get('drop_rate_multiplier', 1.0) or 1.0))), 2)
    payload['gold_multiplier'] = round(max(1.0, min(10.0, float(policy.get('gold_multiplier', 1.0) or 1.0))), 2)
    payload['card_event_enabled'] = bool(policy.get('card_event_enabled', False))
    payload['card_gold_multiplier'] = round(max(1.0, min(5.0, float(policy.get('card_gold_multiplier', 1.0) or 1.0))), 2)
    for section in ('fatigue', 'mileage', 'hell_party', 'daily_challenge', 'weekly_dungeon', 'contracts', 'extra'):
        src = policy.get(section)
        if isinstance(src, dict):
            for k, default_v in payload[section].items():
                if k in src:
                    if isinstance(default_v, bool):
                        payload[section][k] = bool(src[k])
                    elif isinstance(default_v, int):
                        payload[section][k] = int(src[k])
    payload['version'] = 1
    atomic(directory / 'events.json', payload)
    return directory / 'events.json'


def client_up():
    return any(str(p.info['name']).lower()=='dfo.exe' for p in psutil.process_iter(['name']))


def gateway():
    try:
        import ipaddress
        state = read(READY)
        p = psutil.Process(state['pid'])
        if Path(p.exe()).resolve()!=pre.SERVER_BIN.resolve():
            return None
        host,port = state['address'].rsplit(':',1)
        ipaddress.IPv4Address(host)
        if any(c.status=='LISTEN' and len(c.laddr)>=2 and c.laddr[1]==int(port) and c.laddr[0] in (host,'0.0.0.0') for c in p.net_connections(kind='tcp')):
            return state
    except (OSError,ValueError,KeyError,psutil.Error):
        pass
    return None


def login(server_only=False, account=None):
    acct = (account if account is not None else active_account())
    if not db_exists() or not acct:
        raise ValueError('请先在主页创建账号及密码并登录，生成数据库后方可进入游戏选频道游玩。')
    if account_id(acct) is None:
        raise ValueError(f'账号「{acct}」不在当前数据库中，请重新创建或登录账号。')
    if not server_only and client_up():
        raise ValueError('客户端已经打开；不会启动第二个窗口。')
    if not server_only:
        try:
            import fix_channel_select_ui as _fcu
            _fcu.apply_staged_if_pending()
        except Exception:
            pass
        try:
            import deploy_stub as _dstub
            _dstub.apply_staged_if_pending()
        except Exception:
            pass
        try:
            import localize_event_tooltips as _let
            _let.apply_staged_if_pending()
        except Exception:
            pass
        from game_fonts import ORIGINAL
        directory=pre.client_exe().parent/'Fonts'
        missing=[n for n in ORIGINAL if not (directory/n).is_file()]
        if missing or (ROOT/'runtime'/'atelier-fonts'/'pending.json').is_file():
            detail=('缺少 '+', '.join(missing)) if missing else '字体切换尚未完成'
            raise ValueError(detail+'。请先到「Mod 工坊 → 游戏字体」应用字体或还原原版，再启动游戏。')
    state = gateway()
    if state is not None and str(state.get('account') or '').lower() != acct.lower():
        if client_up():
            raise ValueError(f'客户端正在运行其他账号（{state.get("account") or "未知"}），请先退出游戏后再切换账号。')
        stop_server()
        state = None
    py = Path(sys.executable).with_name('python.exe')
    logs = ROOT/'runtime'/'launcher.log'
    with logs.open('ab') as log:
        def spawn(args):
            return subprocess.Popen([str(py),'-u',*args],cwd=ROOT,stdin=subprocess.DEVNULL,
                                    stdout=log,stderr=log,creationflags=subprocess.CREATE_NO_WINDOW)
        if state is None:
            log_offset = logs.stat().st_size if logs.exists() else 0
            proc = spawn(['tools/us100_interactive.py','3','normal','--no-client','--account',acct,'--require-account'])
            for _ in range(600):
                if proc.poll() is not None:
                    detail = ''
                    try:
                        with logs.open('rb') as rf:
                            rf.seek(log_offset)
                            chunk = rf.read().decode('utf-8', errors='replace').strip()
                        if chunk:
                            lines = [ln.strip() for ln in chunk.splitlines() if ln.strip()]
                            if lines:
                                detail = '\n' + '\n'.join(lines[-4:])
                    except OSError:
                        pass
                    raise ValueError('网关未启动，请查看 runtime/launcher.log 的预检结果' + detail)
                state=gateway()
                if state: break
                time.sleep(.25)
            if not state: raise TimeoutError('网关就绪超时；请查看 runtime/launcher.log')
        if server_only:
            if state.get('online_item_grants'):return f'网关已就绪（账号：{acct}），支持在线和离线物品发放。'
            return f'网关已就绪（账号：{acct}），自动处理离线发放。' if state.get('launcher_gm') else '旧网关不支持 GM，请先正常停止旧网关。'
        # This existing entry verifies a single client and records its PID.
        proc=spawn(['tools/us100_client.py'])
        for _ in range(60):
            if proc.poll() is not None: raise ValueError('客户端已退出，请查看 runtime/launcher.log')
            if client_up(): return f'客户端已启动（账号：{acct}），请在游戏窗口选择频道并进入游戏。'
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


def restart_server(account=None):
    """Stop the running US100 gateway (if any) and launch a fresh instance for the active account."""
    acct = (account if account is not None else active_account())
    if not db_exists() or not acct:
        raise ValueError('请先在主页创建账号及密码并登录，生成数据库后方可重启服务端。')
    stop_server()
    return login(server_only=True, account=acct)


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
