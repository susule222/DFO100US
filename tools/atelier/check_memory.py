"""Exercise the real Qt widgets in an isolated process, never submit GM requests."""
import argparse
import gc
import json
import os
from pathlib import Path
import sys

HERE=Path(__file__).resolve().parent
sys.path[:0]=[str(HERE.parents[1]/'runtime'/'atelier-qt'),str(HERE.parent)]
os.environ.setdefault('QT_QPA_PLATFORM','offscreen')
import psutil
from PySide6.QtCore import QCoreApplication,QEvent,QTimer
from PySide6.QtWidgets import QApplication,QDialog
import epic
from epic_ui import ItemCardWidget


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--cycles',type=int,default=24)
    parser.add_argument('--batches',type=int,default=3)
    parser.add_argument('--out',type=Path)
    args=parser.parse_args()
    app=QApplication([]);epic.apply_theme(app)
    w=epic.EpicWindow();w.timer.stop()
    process=psutil.Process();results=[]
    def drain():
        app.processEvents()
        QCoreApplication.sendPostedEvents(None,QEvent.DeferredDelete)
    def sample(phase,batch):
        drain();gc.collect()
        mem=process.memory_info()
        row=dict(phase=phase,batch=batch,private_mib=round(mem.private/1048576,2),
                 handles=process.num_handles(),cards=len(w.findChildren(ItemCardWidget)),
                 dialogs=len(w.findChildren(QDialog)),widgets=len(app.allWidgets()))
        results.append(row);print(json.dumps(row),flush=True)
    v=w.fitting
    w.switch_page(3)
    src=v.source
    costume=src.visual('equipment',501600816)
    aura=src.visual('equipment',501590377)
    def browse(i):
        w.current_page=1+i%4;w._render_page()
    def fit(i):
        v.choice=({'weapon':costume,'aura':aura} if i%2 else {})
        v.render()
    def receipts(i):
        QTimer.singleShot(0,lambda:app.activeModalWidget().accept())
        w.receipts()
    for name,action in [('browse',browse),('fit',fit),('receipts',receipts)]:
        for i in range(4):action(i);drain()
        sample(name,0)
        for batch in range(1,args.batches+1):
            for i in range(args.cycles):action(i);drain()
            sample(name,batch)
    if args.out:args.out.write_text(json.dumps(results,indent=2),encoding='utf8')
    w.close()


if __name__=='__main__':main()
