"""Read-only Windows counters for one manually launched DFO.exe session."""
import argparse
import ctypes
import json
from datetime import datetime
from pathlib import Path
from statistics import median
import time
from ctypes import wintypes
import psutil

import us100_preflight as pre

MIB=1024**2


class Performance(ctypes.Structure):
    _fields_=[('cb',wintypes.DWORD)]+[(n,ctypes.c_size_t) for n in
              'commit limit peak physical available cache kernel paged nonpaged pagesize'.split()]+[
              (n,wintypes.DWORD) for n in ('handles','processes','threads')]


def headroom():
    perf=Performance();perf.cb=ctypes.sizeof(perf)
    if ctypes.windll.psapi.GetPerformanceInfo(ctypes.byref(perf),perf.cb):
        return round((perf.limit-perf.commit)*perf.pagesize/MIB)


def summarize(path):
    # A live reader may catch one incomplete trailing line; only parse committed records.
    with path.open(encoding='utf8') as f:
        rows=[json.loads(line) for line in f if line.endswith('\n')]
    samples=[r for r in rows if r['event']=='sample']
    if not samples:return {'samples':0,'last_event':rows[-1]['event'] if rows else None}
    windows={}
    for r in samples:windows.setdefault(int(r['age_s']//60),[]).append(r['private_mib'])
    maxrow=max(samples,key=lambda r:r['private_mib'])
    return dict(samples=len(samples),first=samples[0],last=samples[-1],max_private=maxrow,
                private_median_by_age_min={k:round(median(v),1) for k,v in windows.items()},
                last_event=rows[-1]['event'])


def watch(out,wait,seconds,interval):
    exe=pre.client_exe().resolve()
    events=pre.ROOT/'runtime/interactive/events.jsonl'
    def event_bytes():
        try:return events.stat().st_size
        except FileNotFoundError:return None
    out.parent.mkdir(parents=True,exist_ok=True)
    with out.open('x',encoding='utf8',buffering=1) as log:
        def emit(event,**kw):
            row=dict(time=datetime.now().astimezone().isoformat(timespec='seconds'),event=event,**kw)
            print(json.dumps(row,ensure_ascii=False),file=log)
        start=time.monotonic()
        emit('waiting',exe=str(exe),commit_free_mib=headroom(),timeout=wait)
        proc=None
        while time.monotonic()-start<wait and proc is None:
            for p in psutil.process_iter(['name']):
                if (p.info['name'] or '').lower()!='dfo.exe':continue
                try:
                    if Path(p.exe()).resolve()==exe:proc=p;break
                except (psutil.AccessDenied,psutil.NoSuchProcess):continue
            if proc is None:time.sleep(0.5)
        if proc is None:emit('start_timeout');return
        t0=time.monotonic();base=event_bytes()
        emit('attached',pid=proc.pid,created=proc.create_time(),gateway_bytes=base)
        for _ in range(int(seconds/interval)+1):
            try:
                if not proc.is_running():emit('process_ended',pid=proc.pid);return
                m=proc.memory_info();count=event_bytes()
                row=dict(age_s=round(time.time()-proc.create_time(),1),elapsed_s=round(time.monotonic()-t0,1),
                         private_mib=round(m.private/MIB,1),rss_mib=round(m.rss/MIB,1),
                         peak_commit_mib=round(m.peak_pagefile/MIB,1),commit_free_mib=headroom(),
                         ram_free_mib=round(psutil.virtual_memory().available/MIB),
                         gateway_log_delta=None if count is None or base is None else count-base)
                for name,fn in [('handles',proc.num_handles),('threads',proc.num_threads)]:
                    try:row[name]=fn()
                    except psutil.AccessDenied:pass
                emit('sample',**row)
            except psutil.NoSuchProcess:emit('process_ended',pid=proc.pid);return
            except psutil.AccessDenied as e:emit('access_denied',reason=str(e))
            time.sleep(interval)
        emit('observation_complete',pid=proc.pid)


if __name__=='__main__':
    a=argparse.ArgumentParser(description=__doc__)
    dest=a.add_mutually_exclusive_group(required=True)
    dest.add_argument('--out',type=Path);dest.add_argument('--report',type=Path)
    a.add_argument('--wait',type=int,default=600)
    a.add_argument('--seconds',type=int,default=600);a.add_argument('--interval',type=float,default=2.)
    opt=a.parse_args()
    if opt.interval<0.5 or opt.seconds<=0 or opt.wait<=0:a.error('Invalid observation duration')
    if opt.report:print(json.dumps(summarize(opt.report),ensure_ascii=False,indent=2))
    else:watch(opt.out,opt.wait,opt.seconds,opt.interval)
