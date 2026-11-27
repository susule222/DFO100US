# -*- coding: utf-8 -*-
"""Create the US100 wear-rules binding from the reference slot map."""
import json
import pathlib

ROOT = pathlib.Path(r'D:\DNF project\service\美服100服务端\server-us100')
US100 = '53ed298dbd69f43e28cbe4b3fa09a4c77d02eaf31d579e27e9477663c924fe69'
src = ROOT / 'configs' / 'equipment-wear.current35.json'
dst = ROOT / 'configs' / 'us100' / 'rules.wear.json'
d = json.loads(src.read_text(encoding='utf-8'))
d['source'] = US100
d['us100_note'] = ('Slot numbers come from the native equipment-type initializer '
                   'recorded as native_evidence; only the archive-version binding was '
                   'moved to the US100 Script.pvf.')
dst.write_text(json.dumps(d, indent=2, ensure_ascii=False), encoding='utf-8')
print('wrote %s slots=%d source=%s' % (dst.name, len(d['slots']), d['source'][:12]))
