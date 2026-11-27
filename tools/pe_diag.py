# -*- coding: utf-8 -*-
"""Diagnose the client PE reader used by the static analysis tools."""
import pathlib
import sys
import traceback

sys.path.insert(0, str(pathlib.Path(__file__).parent))
print('start', flush=True)
try:
    from inspect_pe import PE, default_exe
    print('import ok', flush=True)
    pe = PE(default_exe())
    print('pe ok base=%s' % hex(pe.base), flush=True)
    data = pe.read(0x1434e26b0, 32)
    print('read len=%d hex=%s' % (len(data), data.hex()), flush=True)
except BaseException as exc:
    print('ERR %r' % (exc,), flush=True)
    traceback.print_exc()
print('done', flush=True)
