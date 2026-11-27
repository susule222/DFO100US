# -*- coding: utf-8 -*-
"""List ALL registrations for a given command id (both dispatcher maps)."""
import json
import pathlib
import sys

rows = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding='utf-8'))
want = int(sys.argv[2], 0)
for i, r in enumerate(rows):
    if r['id'] == want:
        print('[%d] handler=%s id_src=%s call_va=%s' % (
            i, r.get('handler'), r.get('id_src'), r.get('call_va')))
