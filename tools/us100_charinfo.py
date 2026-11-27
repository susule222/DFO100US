# -*- coding: utf-8 -*-
"""Dump the charinfo objects the client builds from the roster rows.

Hooks the charinfo setters, dumps each object's first bytes, and shows the
values the row parser stored. This reveals where name/level/profession land."""
import json
import pathlib
import shutil
import struct
import subprocess
import sys
import time

sys.stdout.reconfigure(encoding='utf-8', errors='replace')

ROOT = pathlib.Path(r'D:\DNF project\service\美服100服务端')
SERVER_BIN = ROOT / 'server-us100' / 'bin' / 'wireprobe-us100.exe'
CFG_US100 = ROOT / 'server-us100' / 'configs' / 'us100'
CFG_BASE = ROOT / 'server-us100' / 'configs'
TUTORIAL = ROOT / 'server-us100' / 'runtime' / 'us100_tutorial'
import pathlib
sys.path.insert(0, str(pathlib.Path(__file__).parent))
from inspect_pe import default_exe
CLIENT_EXE = default_exe()
CLIENT_CWD = r'D:\DNF\client\US100'
OUT = ROOT / 'runtime' / 'charinfo'
RUN_SECONDS = int(sys.argv[1]) if len(sys.argv) > 1 else 200
LOG = open(ROOT / 'runtime' / 'us100_charinfo.log', 'a', encoding='utf-8')


def log(msg):
    print(msg, flush=True)
    LOG.write(msg + '\n')
    LOG.flush()


def varint(v):
    b = bytearray()
    while v >= 128:
        b.append(v & 127 | 128)
        v >>= 7
    b.append(v)
    return bytes(b)


def integer(n, v):
    return varint(n << 3) + varint(v)


def string(n, s):
    return varint(n << 3 | 2) + varint(len(s)) + s


def make_fixture(path):
    keys = bytes((i % 127) + 1 for i in range(1024))
    pb = integer(2, 1) + string(3, keys) + string(4, b'LAN Local')
    plain = struct.pack('<I', len(pb)) + pb
    cipher = bytes((((x ^ 0xb5) >> 6) | ((x ^ 0xb5) << 2)) & 255 for x in plain)
    table = []
    for i in range(256):
        v = i
        for _ in range(8):
            v = (v >> 1) ^ (0x4DB89129 if v & 1 else 0)
        table.append(v)
    crc = 0xFFFFFFFF
    for x in cipher:
        crc = (crc >> 8) ^ table[(crc ^ x) & 255]
    crc ^= 0xFFFFFFFF
    fold = (crc & 255) ^ ((crc >> 8) & 255) ^ ((crc >> 16) & 255) ^ ((crc >> 24) & 255) ^ 0x18
    path.write_bytes(struct.pack('<B H I I I B', 0, 1, 16 + len(cipher), 0, fold, 0) + cipher)


JS = r'''
var exe = Process.findModuleByName('DFO.exe');
if (exe === null) { send('no exe'); } else {
    send('exe at ' + exe.base);
    var dumped = 0;
    function hexDump(ptr, n) {
        var out = [];
        var bytes = new Uint8Array(ptr.readByteArray(n));
        for (var i = 0; i < bytes.length; i++) {
            out.push(('0' + bytes[i].toString(16)).slice(-2));
        }
        return out.join(' ');
    }
    function dumpObj(label, obj) {
        if (dumped >= 4 || obj.isNull()) return;
        dumped++;
        // Dump in three windows so the name string and the +0x121C level
        // field are both visible.
        send(label + ' obj=' + obj);
        send('  +0x000: ' + hexDump(obj, 0x40));
        send('  +0x100: ' + hexDump(obj.add(0x100), 0x60));
        send('  +0x200: ' + hexDump(obj.add(0x200), 0x60));
        send('  +0x300: ' + hexDump(obj.add(0x300), 0x60));
        send('  +0x1200: ' + hexDump(obj.add(0x1200), 0x40));
    }
    // The setters receive the charinfo object in rcx. Dump each unique object
    // once at the first setter call.
    try {
        Interceptor.attach(exe.base.add(0x3c21220), {
            onEnter: function (args) { dumpObj('set_121c', args[0]); }
        });
        Interceptor.attach(exe.base.add(0x3c211e0), {
            onEnter: function (args) { dumpObj('set_vec', args[0]); }
        });
        Interceptor.attach(exe.base.add(0x3f0c5a0), {
            onEnter: function (args) { dumpObj('set_0c5a0', args[0]); }
        });
    } catch (e) { send('hook err ' + e); }
    send('charinfo hooks installed');
}
'''


def main():
    if OUT.exists():
        shutil.rmtree(OUT)
    OUT.mkdir(parents=True)
    fixture = OUT / 'channelinfo.bin'
    make_fixture(fixture)
    rtdir = ROOT / 'server-us100' / 'runtime'
    responses = {str(k): str(rtdir / v) for k, v in (
        (1554, 'precheck_ok.bin'), (1, 'login_ok.bin'),
        (8, 'characters_ok.bin'), (684, 'name_ok.bin'))}
    (OUT / 'responses.json').write_text(json.dumps(responses))
    storage = rtdir / 'storage' / 'local.json'
    cmd = [str(SERVER_BIN), '-fixture', str(fixture), '-output', str(OUT),
           '-game-listen', '127.0.0.2:0', '-responses', str(OUT / 'responses.json'),
           '-account', 'probe-us100', '-roster-v224', '-roster-markers',
           '-character-storage', str(storage),
           '-character-catalog', str(CFG_US100 / 'characters.generated.json'),
           '-character-rules', str(CFG_BASE / 'character-probe.json'),
           '-select-probe-config', str(CFG_BASE / 'select-world-probe.json'),
           '-entry-basic-probe',
           '-town-catalog', str(CFG_US100 / 'town.generated.json'),
           '-town-entry-probe', str(CFG_BASE / 'town-entry-probe.json'),
           '-world-catalog', str(CFG_US100 / 'world.generated.json'),
           '-world-rules', str(CFG_BASE / 'world-probe.json'),
           '-quest-catalog', str(CFG_US100 / 'quests.generated.json'),
           '-vault-rules', str(CFG_BASE / 'vault.generated.json'),
           '-entry-addition-probe',
           '-fatigue-rules', str(CFG_BASE / 'fatigue-probe.json'),
           '-dungeon-catalog', str(CFG_US100 / 'dungeons.generated.json'),
           '-progression-catalog', str(CFG_US100 / 'progression.generated.json'),
           '-progression-rules', str(CFG_US100 / 'rules.experience.json'),
           '-loot-catalog', str(CFG_US100 / 'loot.generated.json'),
           '-loot-rules', str(CFG_US100 / 'rules.drop.json'),
           '-bag-rules', str(CFG_US100 / 'rules.bag.json'),
           '-card-rules', str(CFG_US100 / 'rules.cards.json'),
           '-skill-catalog', str(CFG_US100 / 'skills.generated.json'),
           '-channel-refresh-config', str(CFG_BASE / 'channel.local34.json'),
           '-quest-equipment-catalog', str(CFG_US100 / 'equipment.generated.json'),
           '-equipment-wear-rules', str(CFG_US100 / 'rules.wear.json'),
           '-account-options', str(CFG_BASE / 'account-options.current35.json'),
           '-tutorial-routes', str(TUTORIAL / 'routes.json'),
           '-tutorial-dungeons', str(TUTORIAL / 'dungeons.json'),
           '-solo-party-bootstrap']
    server = subprocess.Popen(cmd, stdout=open(OUT / 'gateway.out', 'wb'),
                              stderr=open(OUT / 'gateway.err', 'wb'),
                              creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0))
    ready = OUT / 'ready.json'
    host, port = '127.0.0.2', 0
    for _ in range(400):
        if server.poll() is not None:
            log('server exited: ' + (OUT / 'gateway.err').read_text(errors='replace')[-2000:])
            return
        if ready.exists():
            st = json.loads(ready.read_text())
            host, port_s = st['address'].split(':')
            port = int(port_s)
            break
        time.sleep(0.05)
    log('server ready at %s:%d' % (host, port))
    payload = '13?%s?%d?probe?00000000000000000000000000000000?0?0?30?0?0?0' % (host, port)

    import frida
    device = frida.get_local_device()
    pid = device.spawn([CLIENT_EXE, payload], cwd=CLIENT_CWD)
    log('[frida] pid=%d' % pid)
    session = device.attach(pid)
    script = session.create_script(JS)

    def on_message(message, data):
        if message.get('type') == 'send':
            log('[js] ' + str(message.get('payload')))
        elif message.get('type') == 'error':
            log('[js-error] ' + str(message.get('description')))

    script.on('message', on_message)
    script.load()
    device.resume(pid)
    log('resumed')
    t0 = time.time()
    while time.time() - t0 < RUN_SECONDS:
        time.sleep(10)
        try:
            r = subprocess.run(['tasklist', '/FI', 'PID eq %d' % pid, '/NH'],
                               capture_output=True, text=True, timeout=15)
            if str(pid) not in r.stdout:
                log('[%ds] client gone' % int(time.time() - t0))
                break
        except Exception:
            break
    try:
        device.kill(pid)
    except Exception:
        pass
    try:
        server.terminate()
    except Exception:
        pass
    log('done')


if __name__ == '__main__':
    main()
