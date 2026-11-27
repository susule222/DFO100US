# -*- coding: utf-8 -*-
"""Update the client's ngs_stub.ini, keeping every existing setting.

Adds the dialog-dismiss list used by the rebuilt stub and makes sure ord5 is 0
(the previous value 1 allocated an image per frame and took the client to
22.7 GB). Backs the file up before writing and prints the result.

Usage: python tools/update_stub_ini.py [--apply]
"""
import pathlib
import shutil
import sys
import time

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import us100_preflight as preflight  # noqa: E402

CLIENT = preflight.client_dir() or pathlib.Path(r'D:\DNF\client\100US\DFO')
INI = CLIENT / 'ngs_stub.ini'

apply = '--apply' in sys.argv

if not INI.is_file():
    raise SystemExit('missing: %s' % INI)

original = INI.read_text(encoding='utf-8', errors='replace')
lines = [ln.rstrip('\r\n') for ln in original.splitlines()]

settings = {}
order = []
for line in lines:
    if '=' in line and not line.strip().startswith('#'):
        key = line.split('=', 1)[0].strip()
        settings[key] = line.split('=', 1)[1].strip()
        order.append(key)

print('current settings: %s' % ', '.join('%s=%s' % (k, settings[k]) for k in order))

changed = []
if settings.get('5') != '0':
    settings['5'] = '0'
    changed.append('5=0 (ord5 must not allocate per frame)')
if 'dismiss' not in settings:
    settings['dismiss'] = 'RUN ERROR'
    changed.append('dismiss=RUN ERROR (dialogs the watcher closes)')

if not changed:
    print('nothing to change')
    raise SystemExit(0)

for item in changed:
    print('would set %s' % item)

if not apply:
    print('\ndry run: nothing written. Re-run with --apply')
    raise SystemExit(0)

stamp = time.strftime('%Y%m%d-%H%M%S')
backup = INI.with_name('ngs_stub.ini.%s.bak' % stamp)
shutil.copy2(INI, backup)
print('backup: %s' % backup)

written = ['%s=%s' % (k, settings[k]) for k in order]
if 'dismiss' not in order:
    written.append('dismiss=%s' % settings['dismiss'])
INI.write_text('\n'.join(written) + '\n', encoding='utf-8')
print('\n%s now:\n%s' % (INI, INI.read_text(encoding='utf-8')))
