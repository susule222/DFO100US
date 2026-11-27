import ctypes
import sys
import traceback
from pathlib import Path

try:
    base=Path(__file__).resolve().parent
    sys.path.insert(0,str(base.parent))
    sys.path.insert(0,str(base.parents[1]/'runtime'/'atelier-qt'))
    from epic import main
    main()
except Exception:
    report=traceback.format_exc()
    path=Path(__file__).resolve().parents[2]/'runtime'/'launcher-error.log'
    path.parent.mkdir(parents=True,exist_ok=True)
    path.write_text(report,encoding='utf-8')
    ctypes.windll.user32.MessageBoxW(0,report.splitlines()[-1]+'\n'+str(path),'地下城与勇士 · 智慧的引导',16)
    sys.exit(1)
