"""Audit exact resource coverage of all currently visible cards (no gateway writes)."""
from collections import defaultdict
from pathlib import Path
import sys

here=Path(__file__).resolve().parent
sys.path[:0]=[str(here.parents[1]/'runtime'/'atelier-qt'),str(here.parent)]
import core
from epic_ui import GameItem
from epic_data import read_cards
from item_icons import ItemIcons


def main():
    _,cards=read_cards(GameItem)
    assets=ItemIcons(core.pre.client_exe().parent/'ImagePacks2')
    groups=defaultdict(list)
    for item in cards:groups[item.icon].append(item.item_id)
    problems=[];packs=set();good=0
    for ref,ids in groups.items():
        try:
            if ref is None:raise ValueError('物品未声明图标')
            im=assets.read(ref)
            if not im.getbbox():raise ValueError('图帧全透明')
            packs.add(assets.locate(ref).name)
            good+=len(ids)
        except Exception as exc:
            problems.append(dict(ids=ids,img=ref.img if ref else None,frame=ref.frame if ref else None,error=str(exc)))
    report=dict(cards=len(cards),decoded=good,distinct_frames=len(groups)-(None in groups),
                packs=sorted(packs),problems=problems)
    dest=core.ROOT/'runtime'/'atelier-qa'/'item-icons.json'
    core.atomic(dest,report)
    print(f'{good}/{len(cards)} cards; {report["distinct_frames"]} references; {len(packs)} NPKs; {dest}')
    return int(bool(problems))


if __name__=='__main__':sys.exit(main())
