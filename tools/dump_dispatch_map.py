# -*- coding: utf-8 -*-
"""Read the client's live id->handler dispatch map (memory reads only).

The registration hook in ngs_stub.c logs the map address at startup; this walks
its buckets and lists which command ids are registered right now, which shows
whether a state change (for example entering town) drops the dungeon handlers.

Usage: python tools/dump_dispatch_map.py <map_hex> [want_ids]
"""
import pathlib
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / 'runtime' / 'dispatch_map.txt'
MAP = int(sys.argv[1], 0)
WANT = [int(x) for x in sys.argv[2].split(',')] if len(sys.argv) > 2 else [15, 16, 27, 28, 29, 30]

JS = r'''
var exe = Process.findModuleByName('DFO.exe');
var map = ptr(MAP_HEX);
try {
  var sentinel = map.add(0x10).readPointer();
  var buckets = map.add(0x28).readPointer();
  var mask = map.add(0x40).readPointer();
  var out = ['map=' + map + ' sentinel=' + sentinel + ' buckets=' + buckets + ' mask=' + mask];
  var ids = [];
  for (var i = 0; i <= mask.toInt32() && i < 4096; i++) {
    var node = buckets.add(i * 16).readPointer();
    var guard = 0;
    while (!node.isNull() && node.equals(sentinel) === false && guard++ < 64) {
      var key = 0, handler = null;
      try { key = node.add(0x10).readU32(); } catch (e) {}
      try { handler = node.add(0x18).readPointer(); } catch (e) {}
      ids.push(key);
      node = node.add(8).readPointer();
    }
  }
  out.push('registered=' + ids.length);
  out.push('ids=' + ids.slice(0, 60).join(','));
  var want = WANT_LIST;
  want.forEach(function (w) {
    out.push('want ' + w + ' -> ' + (ids.indexOf(w) >= 0 ? 'PRESENT' : 'ABSENT'));
  });
  send(out.join('\n'));
} catch (e) { send('read failed: ' + e); }
'''

def main():
    import frida
    device = frida.get_local_device()
    target = None
    for p in device.enumerate_processes():
        if p.name.lower() == 'dfo.exe':
            target = p
            break
    if target is None:
        print('DFO.exe is not running')
        return
    session = device.attach(target.pid)
    script = session.create_script(
        JS.replace('MAP_HEX', hex(MAP)).replace('WANT_LIST', '[' + ','.join(str(w) for w in WANT) + ']'))
    lines = []
    script.on('message', lambda m, d: lines.append(str(m.get('payload', m))))
    script.load()
    time.sleep(1.5)
    try:
        session.detach()
    except Exception:
        pass
    text = '\n'.join(lines)
    OUT.write_text(text, encoding='utf-8')
    print(text)


if __name__ == '__main__':
    main()
