"""Narrow byte-preserving display-string patch for the 100US equipment selector."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

from rewrap_us100_pvf import wrap
from us100_status_dstr import (ACT, CLEAR_FONTS, CLIENT, EXE, EXPECTED_TRIAL,
                               GameFonts, PROFILES, REL, core)


ROOT = Path(__file__).resolve().parents[1]
RUNTIME = ROOT / 'runtime'
AUDIT = RUNTIME / 'localize-us100-audit'
BASE_INNER = RUNTIME / 'us100-quest-strings-diagnostic-r1' / 'Script.inner.pvf'
BASE_INNER_HASH = '27e310108e7ffbe030ea4aabb8e6c7f7d8a2a06f5ea314f7084e5dff45090988'
TABLE_HASH = '8f11461e485a8e12be0b392acce6c8d5f197c68c136845c57a3ec35680bc78e6'
TRIAL_DNF_HASH = 'ba09578474ef10db772c8352e9e676ace6fa9c761ef0b29504151540aa1ad032'
ENTRY = 'string/ui.uv.str'
ROWS = {
    'TargetEquipSetting_str_20': ('Sub（子）\\n装备', '辅助\\n装备'),
    'TargetEquipSetting_str_08': ('勒格恩', '军团地下城'),
    'TargetEquipSetting_str_10': ('物品推荐板', '装备推荐'),
    'TargetEquipSetting_str_24': ('额外设备种植', '其他装备获取途径'),
    'item_dictionary_01': ('阶段', '等级'),
    'ItemDictionary_hashtag_searching_title': ('搜索中的标签列表', '已搜索的标签'),
}


def table_edit(data, rows):
    for k, (old, new) in rows.items():
        if any(c in new for c in '\r\n\0') or old.count('\\n') != new.count('\\n'):
            raise ValueError(f'changed text controls for {k}')
        prefix = '\r\n' + k + '>'
        lhs = (prefix + old + '\r\n').encode('utf-16-le')
        rhs = (prefix + new + '\r\n').encode('utf-16-le')
        if data.count(lhs) != 1:
            raise ValueError(f'expected one original row for {k}')
        data = data.replace(lhs, rhs, 1)
    return data


def sha(data):
    return hashlib.sha256(data).hexdigest()


def build(out):
    out = out.resolve()
    live = CLIENT/'Script.pvf'
    if (out.is_relative_to(CLIENT) or core.digest(live) != EXPECTED_TRIAL
            or core.digest(BASE_INNER) != BASE_INNER_HASH):
        raise ValueError('PVF baseline differs')
    out.mkdir(parents=True)
    core.file_copy(live, out/'before-Script.pvf')
    ledger = ['Scope: exact equipment search display keys; no image, font, or filter logic edits']

    def run(label, *args):
        cmd = list(map(str, args))
        p = subprocess.run(cmd, capture_output=True, encoding='utf-8')
        ledger.extend([label, json.dumps(cmd, ensure_ascii=False), f'exit={p.returncode}',
                       'stdout:', p.stdout, 'stderr:', p.stderr])
        p.check_returncode()

    def inspect(label, source, dst):
        run(label, AUDIT/'pvfinspect-localization.exe', '-source', source, '-files',
            ENTRY+',string/dnf.uv.str', '-output', dst)
        records = core.read(dst/'exports.json')
        if records[0]['entry']['archive_path'] != ENTRY or records[1]['raw_sha256'] != TRIAL_DNF_HASH:
            raise ValueError('unrelated DNF string table changed')
        raw = (dst/records[0]['raw_file']).read_bytes()
        text = raw.decode('utf-16-le').splitlines()
        vals = {k:next(s.split('>',1)[1] for s in text if s.startswith(k+'>')) for k in ROWS}
        ledger.append(json.dumps({'native_ui_table_sha256':sha(raw),'labels':vals},ensure_ascii=False))
        return raw

    inner, outer = out/'Script.inner.pvf', out/'Script.pvf'
    try:
        before = inspect('BASELINE', BASE_INNER, out/'baseline')
        if sha(before) != TABLE_HASH:
            raise ValueError('baseline ui.uv.str identity differs')
        after = table_edit(before, ROWS)
        patch_table = out/'replacement-tables'/ENTRY
        patch_table.parent.mkdir(parents=True)
        patch_table.write_bytes(after)
        run('APPLY', AUDIT/'pvflocalize.exe','-source',BASE_INNER,'-dir',out/'replacement-tables','-output',inner)
        run('WRAP',sys.executable,Path(__file__).with_name('rewrap_us100_pvf.py'),
            '--original',CLIENT/'Script.pvf-1','--baseline',RUNTIME/'pvf_source'/'Script.inner.pvf',
            '--candidate',inner,'--output',outer)
        reopened = inspect('MODIFIED',inner,out/'modified')
        undo = {k:(new,old) for k,(old,new) in ROWS.items()}
        if reopened != after or table_edit(reopened,undo) != before:
            raise ValueError('table text diff was not exact')
        result = core.digest(outer)
        core.atomic(out/'PVF.diff.json',{'before':EXPECTED_TRIAL,'after':result,'entry':ENTRY,'rows':ROWS})
        rollback = out/'ROLLBACK.sh'
        source = (Path(__file__).with_name('us100_status_rollback.sh')).read_text(encoding='utf-8')
        rollback.write_text(source.replace('before-dstr.dat','before-Script.pvf').replace('dstr.dat','Script.pvf')
                            .replace('DSTR.diff.json','PVF.diff.json'), encoding='utf-8',newline='\n')
        rollback.chmod(0o755)
        copy=out/'rollback-test.pvf'
        core.file_copy(outer,copy)
        run('ROLLBACK.sh',sys.executable,rollback,copy)
        if core.digest(copy)!=EXPECTED_TRIAL or inspect('ROLLBACK',BASE_INNER,out/'restored')!=before:
            raise ValueError('rollback did not match the verified baseline')
        if table_edit(table_edit(reopened,undo),ROWS)!=reopened:
            raise ValueError('reapplied table differs')
        ledger += ['Rollback SHA-256: '+core.digest(copy),'Reapplied table SHA-256: '+sha(reopened),
                   'Candidate outer SHA-256: '+result,'Whole-file wrapper round-trip verified. No game visual check.']
    finally:
        (out/'VERIFICATION.txt').write_text('\n'.join(ledger)+'\n',encoding='utf-8')
    for name in ('Script.pvf','PVF.diff.json','VERIFICATION.txt','ROLLBACK.sh'):
        p=out/name
        print(p, core.digest(p))


def install(pvf, dstr, rollback=False):
    pvf, dstr = pvf.resolve(), dstr.resolve()
    if any(d.is_relative_to(CLIENT) for d in (pvf,dstr)):
        raise ValueError('need offline candidate directories')
    m = core.read(pvf/'PVF.diff.json'); d = core.read(dstr/'DSTR.diff.json')
    wrap_receipt = core.read(pvf/'Script.manifest.json')
    if (m['before']!=EXPECTED_TRIAL or m['rows']!={k:list(v) for k,v in ROWS.items()}
            or m['after']!=wrap_receipt['candidate_sha256'] or not wrap_receipt['verified_rewrap_round_trip']
            or d['profile']!='equip-filter' or d['before']!=PROFILES['equip-filter'].baseline):
        raise ValueError('unexpected patch identity')
    assets=[(CLIENT/'Script.pvf',pvf/'before-Script.pvf',pvf/'Script.pvf',m),
            (CLIENT/'dstr.dat',dstr/'before-dstr.dat',dstr/'dstr.dat',d)]
    fonts=GameFonts(CLIENT/'Fonts')
    with fonts.locked():
        if (core.client_up() or fonts.hashes()!=CLEAR_FONTS or core.digest(CLIENT/REL)!=ACT
                or core.digest(CLIENT/'DFO.exe')!=EXE):
            raise ValueError('DFO is open or protected fonts/quest atlas/client identity differs')
        for live, before, after, delta in assets:
            if (core.digest(before)!=delta['before'] or core.digest(after)!=delta['after']
                    or core.digest(live)!=delta['after' if rollback else 'before']):
                raise ValueError(f'changed live or offline identity: {live}')
        done=[]
        try:
            for live,before,after,delta in assets:
                src,undo=(before,after) if rollback else (after,before)
                if core.client_up() or core.digest(live)!=core.digest(undo):
                    raise ValueError('DFO reopened or the target changed')
                core.file_copy(src,live)
                done.append((live,undo))
                if core.digest(live)!=core.digest(src):
                    raise ValueError(f'installation mismatch: {live}')
        except Exception:
            if not core.client_up():
                for live,undo in reversed(done):
                    core.file_copy(undo,live)
            raise
        hashes={str(p):core.digest(p) for p,*_ in assets}
        core.atomic(pvf/'deployment.json',{'state':'rollback' if rollback else 'installed_pending_game_check',
                                         'live':hashes,'fonts':fonts.hashes(),'quest_atlas':core.digest(CLIENT/REL)})
        print(json.dumps(hashes,ensure_ascii=False))


if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('action',choices=('build','install','rollback'))
    p.add_argument('--out',type=Path,required=True)
    p.add_argument('--dstr',type=Path)
    a=p.parse_args()
    if a.action=='build':build(a.out)
    elif a.dstr is None:p.error('--dstr is required for install or rollback')
    else:install(a.out,a.dstr,a.action=='rollback')
