"""Immutable launcher catalog generations, tied to the original gateway inputs."""
import hashlib
import json
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
BASE=ROOT/'server-us100'/'configs'/'us100'
DATA=ROOT/'runtime'/'atelier-data'
INPUTS=('equipment.generated.json','loot.generated.json','rules.bag.json','rules.wear.json')


def inputs():
    return {n:hashlib.sha256((BASE/n).read_bytes()).hexdigest() for n in INPUTS}


def current():
    pointer=DATA/'current.json'
    if not pointer.exists():return None
    meta=json.loads(pointer.read_bytes())
    folder=DATA/meta['generation']
    if folder.resolve().parent!=DATA.resolve() or meta.get('inputs')!=inputs():
        raise ValueError('全量目录与当前基础目录不一致，请重新生成，不要使用旧数据。')
    return folder
