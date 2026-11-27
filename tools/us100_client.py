# -*- coding: utf-8 -*-
"""Start only the game client, against the server that is already running.

The launcher normally starts both, but a client can only run one instance at a
time and the game protects its own process: when an old window is still open the
full launcher cannot bring up a new one, and nothing here can close it. So the
server is started on its own (us100_interactive.py --no-client) and this script
brings up the client against it, reading the address the running server actually
bound from runtime/interactive/ready.json.

Usage: python tools/us100_client.py
"""
import json
import pathlib
import socket
import subprocess
import sys
import time
from datetime import datetime

sys.stdout.reconfigure(encoding='utf-8', errors='replace')
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import us100_preflight as preflight  # noqa: E402

ROOT = preflight.ROOT
READY = ROOT / 'runtime' / 'interactive' / 'ready.json'


def running_clients():
    out = subprocess.run(['tasklist', '/FI', 'IMAGENAME eq DFO.exe', '/NH'],
                         capture_output=True, text=True, errors='replace')
    return [l for l in out.stdout.splitlines() if 'DFO.exe' in l]


def _listening(host, port, timeout=0.6):
    with socket.socket() as sock:
        sock.settimeout(timeout)
        return sock.connect_ex((host, int(port))) == 0


def main():
    mode = '3'
    if '--mode' in sys.argv:
        idx = sys.argv.index('--mode')
        if idx + 1 < len(sys.argv) and sys.argv[idx + 1] in ('3', '13'):
            mode = sys.argv[idx + 1]
    if not READY.exists():
        print('no running server: %s is missing' % READY)
        print('start the server first (启动服务端.cmd, or us100_interactive.py 3 <style> --no-client)')
        return 1
    state = json.loads(READY.read_text(encoding='utf-8'))
    host, port = state['address'].split(':')
    ch_addr = state.get('channel_refresh_address') or '127.0.0.1:7001'
    ch_host, ch_port = ch_addr.rsplit(':', 1)
    old = running_clients()
    if old:
        # Not an error of this script's own making: the game refuses to run two
        # instances, and its process cannot be terminated from outside.
        print('a game client is already running — close that window first:')
        for line in old:
            print('  ' + line.strip())
        return 1
    exe = preflight.client_exe()
    if mode == '3':
        if not _listening(ch_host, ch_port):
            print('channel directory server is not listening at %s:%s' % (ch_host, ch_port))
            return 1
        payload = '3?%s?%s?probe?00000000000000000000000000000000?0?0?30?0?0?0' % (ch_host, ch_port)
    else:
        payload = '13?%s?%s?probe?00000000000000000000000000000000?0?0?30?0?0?0' % (host, port)
    print('client %s' % exe)
    print('payload %s' % payload)
    log_path = READY.parent / 'client-launch.jsonl'
    with log_path.open('a', encoding='utf-8') as log:
        started = time.monotonic()
        client = subprocess.Popen([str(exe), payload], cwd=str(exe.parent))
        print(json.dumps({'time': datetime.now().astimezone().isoformat(), 'event': 'start',
                          'pid': client.pid, 'exe': str(exe), 'mode': mode,
                          'address': state['address'], 'channel_refresh_address': ch_addr}),
              file=log, flush=True)
        code = client.wait()
        status = '0x%08X' % (code & 0xffffffff)
        print(json.dumps({'time': datetime.now().astimezone().isoformat(), 'event': 'exit',
                          'pid': client.pid, 'code': status,
                          'seconds': round(time.monotonic() - started, 1)}), file=log)
    print('DFO.exe exited with %s. Log: %s' % (status, log_path))
    return 1 if code else 0


if __name__ == '__main__':
    sys.exit(main())
