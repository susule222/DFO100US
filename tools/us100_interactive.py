# -*- coding: utf-8 -*-
"""US100 交互启动：预检 → 存储 → 服务端 → 客户端长驻，供人工查看与操作。

用法：python tools/us100_interactive.py [模式 3|13] [markers|normal] [--force]

启动前会跑一遍 tools/us100_preflight.py 的检查（客户端路径与体积、服务端二进制
是否比源码新、7001 是否空闲、数据库是否在本项目内、存储是否在跑、是否已有实例），
任一 FAIL 即停，避免留下孤儿进程。--force 可跳过失败继续。

服务端进程会被放进一个 Job 对象（KILL_ON_JOB_CLOSE），所以启动器崩溃或被杀不会
再留下占着 7001 的孤儿服务端；客户端**不进** Job，退出时照旧保留。
"""
import json
import pathlib
import shutil
import struct
import subprocess
import sys
import time

sys.stdout.reconfigure(encoding='utf-8', errors='replace')
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import us100_preflight as preflight  # noqa: E402

ROOT = preflight.ROOT
SERVER_BIN = ROOT / 'server-us100' / 'bin' / 'wireprobe-us100.exe'
CFG_US100 = ROOT / 'server-us100' / 'configs' / 'us100'
CFG_BASE = ROOT / 'server-us100' / 'configs'
TUTORIAL = ROOT / 'server-us100' / 'runtime' / 'us100_tutorial'
OUT = ROOT / 'runtime' / 'interactive'
SHOT = ROOT / 'tools' / 'shot_clean.ps1'
SHOTS = ROOT / 'runtime' / 'shots'

def _parse_cli(argv):
    pos = []
    account = 'probe-us100'
    require_account = False
    force = False
    no_client = False
    listen_host = '0.0.0.0'
    advertise_host = ''
    i = 1
    while i < len(argv):
        arg = argv[i]
        if arg == '--force':
            force = True
        elif arg == '--no-client':
            no_client = True
        elif arg == '--require-account':
            require_account = True
        elif arg == '--account' and i + 1 < len(argv):
            account = argv[i + 1].strip() or account
            i += 1
        elif arg.startswith('--account='):
            val = arg.split('=', 1)[1].strip()
            if val:
                account = val
        elif arg == '--listen-host' and i + 1 < len(argv):
            listen_host = argv[i + 1].strip() or listen_host
            i += 1
        elif arg.startswith('--listen-host='):
            val = arg.split('=', 1)[1].strip()
            if val:
                listen_host = val
        elif arg == '--advertise-host' and i + 1 < len(argv):
            advertise_host = argv[i + 1].strip()
            i += 1
        elif arg.startswith('--advertise-host='):
            advertise_host = arg.split('=', 1)[1].strip()
        elif not arg.startswith('--'):
            pos.append(arg)
        i += 1
    mode = pos[0] if len(pos) > 0 else '3'
    style = pos[1] if len(pos) > 1 else 'normal'
    return mode, style, force, no_client, account, require_account, listen_host, advertise_host


MODE, STYLE, FORCE, NO_CLIENT, ACCOUNT, REQUIRE_ACCOUNT, LISTEN_HOST, ADVERTISE_HOST = _parse_cli(sys.argv)
try:
    LOG = open(ROOT / 'runtime' / 'us100_interactive.log', 'a', encoding='utf-8')
except OSError:
    # Another launcher already holds the log. Losing the transcript must not
    # abort the run, and it must never be the reason nothing starts.
    LOG = None


def log(msg):
    print(msg, flush=True)
    if LOG is not None:
        LOG.write(msg + '\n')
        LOG.flush()


def assign_server_to_job(pid):
    """Kill-on-close job so a launcher crash cannot orphan the server.

    Best effort: if any call fails the launcher continues, because a missing job
    is a smaller problem than refusing to start.
    """
    try:
        import ctypes
        from ctypes import wintypes

        class BasicLimit(ctypes.Structure):
            _fields_ = [('PerProcessUserTimeLimit', ctypes.c_int64),
                        ('PerJobUserTimeLimit', ctypes.c_int64),
                        ('LimitFlags', wintypes.DWORD),
                        ('MinimumWorkingSetSize', ctypes.c_size_t),
                        ('MaximumWorkingSetSize', ctypes.c_size_t),
                        ('ActiveProcessLimit', wintypes.DWORD),
                        ('Affinity', ctypes.c_size_t),
                        ('PriorityClass', wintypes.DWORD),
                        ('SchedulingClass', wintypes.DWORD)]

        class IoCounters(ctypes.Structure):
            _fields_ = [('ReadOperationCount', ctypes.c_uint64),
                        ('WriteOperationCount', ctypes.c_uint64),
                        ('OtherOperationCount', ctypes.c_uint64),
                        ('ReadTransferCount', ctypes.c_uint64),
                        ('WriteTransferCount', ctypes.c_uint64),
                        ('OtherTransferCount', ctypes.c_uint64)]

        class ExtendedLimit(ctypes.Structure):
            _fields_ = [('BasicLimitInformation', BasicLimit),
                        ('IoInfo', IoCounters),
                        ('ProcessMemoryLimit', ctypes.c_size_t),
                        ('JobMemoryLimit', ctypes.c_size_t),
                        ('PeakProcessMemoryUsed', ctypes.c_size_t),
                        ('PeakJobMemoryUsed', ctypes.c_size_t)]

        JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x2000
        JobObjectExtendedLimitInformation = 9
        PROCESS_SET_QUOTA, PROCESS_TERMINATE = 0x0100, 0x0001

        kernel32 = ctypes.WinDLL('kernel32', use_last_error=True)
        job = kernel32.CreateJobObjectW(None, None)
        if not job:
            return None
        info = ExtendedLimit()
        info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
        if not kernel32.SetInformationJobObject(job, JobObjectExtendedLimitInformation,
                                                ctypes.byref(info), ctypes.sizeof(info)):
            return None
        handle = kernel32.OpenProcess(PROCESS_SET_QUOTA | PROCESS_TERMINATE, False, pid)
        if not handle:
            return None
        if not kernel32.AssignProcessToJobObject(job, handle):
            return None
        # Keep the handle alive for the process lifetime: closing it kills the job.
        globals()['_SERVER_JOB'] = job
        return job
    except Exception as exc:  # pragma: no cover - platform guard
        log('job object unavailable: %r' % (exc,))
        return None


def record_run(server_pid, port):
    """Record what this run actually executed, so a session can be tied to a build."""
    entry = {
        'server_pid': server_pid,
        'port': port,
        'mode': MODE,
        'style': STYLE,
        'server_binary': str(SERVER_BIN),
        'server_binary_size': SERVER_BIN.stat().st_size if SERVER_BIN.exists() else 0,
        'server_binary_mtime': SERVER_BIN.stat().st_mtime if SERVER_BIN.exists() else 0,
        'newest_source_mtime': preflight.newest_source_mtime()[0],
        'client_exe': str(preflight.client_exe()),
        'client_exe_size': preflight.client_exe().stat().st_size if preflight.client_exe().exists() else 0,
        'started_at': time.strftime('%Y-%m-%dT%H:%M:%S'),
    }
    (OUT / 'run.json').write_text(json.dumps(entry, indent=2, ensure_ascii=False), encoding='utf-8')


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
    pb = integer(2, 1) + string(3, keys) + string(4, b'LAN Local') + integer(7, 1) + integer(8, 1)
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


def main():
    # Everything that can be known before a single process starts is checked
    # first: the client directory (it was renamed once and 90 files kept the old
    # path), the server binary's freshness (a stale binary silently ran a whole
    # test round), port 7001, the database location, and whether an instance is
    # already up. Nothing here is started while a check is failing.
    log('=== build ===')
    try:
        preflight.build_server()
    except Exception as exc:
        log('server build failed: %r' % (exc,))
        return

    log('=== preflight ===')
    if not preflight.report():
        if not FORCE:
            log('preflight failed; not starting. Fix the items above, or re-run with --force.')
            return
        log('preflight failed but --force was given; continuing anyway')

    log('=== storage ===')
    try:
        preflight.start_storage()
    except Exception as exc:
        log('storage did not start: %r' % (exc,))
        return

    # Keep the previous run's events.jsonl instead of deleting it. Each run
    # used to start by wiping this directory, so the log of the attempt that
    # just failed was always gone by the time it could be read. Exactly one
    # previous run is kept, which bounds the disk usage the same way before.
    if OUT.exists():
        prev = ROOT / 'runtime' / 'interactive.prev'
        if prev.exists():
            shutil.rmtree(prev, ignore_errors=True)
        try:
            OUT.rename(prev)
        except OSError:
            shutil.rmtree(OUT, ignore_errors=True)
    OUT.mkdir(parents=True)
    SHOTS.mkdir(parents=True, exist_ok=True)
    fixture = OUT / 'channelinfo.bin'
    make_fixture(fixture)
    rtdir = ROOT / 'server-us100' / 'runtime'
    responses = {str(k): str(rtdir / v) for k, v in (
        (1554, 'precheck_ok.bin'), (1, 'login_ok.bin'),
        (8, 'characters_ok.bin'), (684, 'name_ok.bin'))}
    (OUT / 'responses.json').write_text(json.dumps(responses))
    storage = rtdir / 'storage' / 'local.json'
    if REQUIRE_ACCOUNT:
        cfg = preflight.storage_config()
        db_file = pathlib.Path(cfg['db_path']) if cfg.get('db_path') else (rtdir / 'us100.db')
        if not db_file.is_file():
            log('database not created yet (%s); create an account in the launcher first.' % db_file)
            return 1
    from atelier_runtime import current as atelier_generation
    atelier = atelier_generation()
    cmd = [str(SERVER_BIN), '-fixture', str(fixture), '-output', str(OUT),
           '-game-listen', '%s:0' % LISTEN_HOST, '-responses', str(OUT / 'responses.json'),
           '-account', ACCOUNT, '-roster-v224',
           '-launcher-gm', str(ROOT / 'runtime' / 'launcher-gm')]
    if ADVERTISE_HOST:
        cmd += ['-advertise-host', ADVERTISE_HOST]
    if REQUIRE_ACCOUNT:
        cmd.append('-require-account')
    if STYLE == 'markers':
        cmd.append('-roster-markers')
    if STYLE == 'probe':
        cmd.append('-roster-layout-probe')
    # Exactly one addition mode is selected. The flags overlap inside the gateway
    # (the marker payload wins only when -entry-addition-probe is also set, and
    # -entry-stat-* only takes effect inside that branch), so appending them from
    # several branches silently produced the stub instead of the intended payload
    # and made three test rounds look like marker runs when they were stub runs.
    addition_modes = {
        'addition': ['-entry-addition-probe'],
        'addition-stat': ['-entry-addition-probe', '-entry-stat-probe'],
        'attr-pair': ['-entry-addition-probe', '-entry-stat-marker', '-entry-addition-hp'],
        # attr-pair plus mode-1 block byte 13 = 1: the settlement receives
        # flag=1, testing the panel-writer trigger hypothesis (2026-09-22
        # capture: writers never fire on flag=0, panel slots stay zero).
        'flag13': ['-entry-addition-probe', '-entry-stat-marker', '-entry-addition-hp', '-entry-addition-flag13'],
        # Normal town entry carries the selected character's native v2.24 stats.
        'normal': ['-entry-addition-probe', '-entry-addition-stats'],
        'stats': ['-entry-addition-probe', '-entry-addition-stats'],
        'marker': ['-entry-addition-probe', '-entry-addition-marker'],
        'marker-stat': ['-entry-addition-probe', '-entry-addition-marker', '-entry-stat-probe'],
        'stat-marker': ['-entry-addition-probe', '-entry-addition-marker', '-entry-stat-marker'],
        'addition-ramp': ['-entry-addition-ramp'],
    }
    cmd += addition_modes.get(STYLE, ['-entry-addition-stub'])
    cmd += ['-character-storage', str(storage),
            '-inject-watch', str(ROOT / 'runtime' / 'inject.bin'),
            # Any-id channel: inject-<id>.bin files are sent as that command id,
            # which is how a candidate channel is tested without a client restart.
            '-inject-watch-dir', str(ROOT / 'runtime' / 'inject'),
            # Absolute: the server's working directory is not this project root,
            # so a relative default silently falls back to the built-in gate set
            # and a config edit appears to have no effect.
            '-gate-set', str(CFG_US100 / 'gate-set.json'),
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
            '-fatigue-rules', str(CFG_BASE / 'fatigue-probe.json'),
            '-dungeon-catalog', str(CFG_US100 / 'dungeons.generated.json'),
            '-progression-catalog', str(CFG_US100 / 'progression.generated.json'),
            '-progression-rules', str(CFG_US100 / 'rules.experience.json'),
            '-loot-catalog', str(atelier/'loot.json' if atelier else CFG_US100/'loot.generated.json'),
            '-loot-rules', str(CFG_US100 / 'rules.drop.json'),
            '-bag-rules', str(CFG_US100 / 'rules.bag.json'),
            '-card-rules', str(CFG_US100 / 'rules.cards.json'),
            '-skill-catalog', str(CFG_US100 / 'skills.generated.json'),
            '-channel-refresh-config', str(CFG_US100 / 'channel.json' if (CFG_US100 / 'channel.json').is_file() else CFG_BASE / 'channel.local34.json'),
            '-quest-equipment-catalog', str(atelier/'equipment.json' if atelier else CFG_US100/'equipment.generated.json'),
            '-equipment-wear-rules', str(CFG_US100 / 'rules.wear.json'),
            '-account-options', str(CFG_BASE / 'account-options.current35.json'),
            '-tutorial-routes', str(TUTORIAL / 'routes.json'),
            '-tutorial-dungeons', str(TUTORIAL / 'dungeons.json'),
            '-solo-party-bootstrap']
    server = subprocess.Popen(cmd, stdout=open(OUT / 'gateway.out', 'wb'),
                              stderr=open(OUT / 'gateway.err', 'wb'),
                              creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0))
    log('server pid=%d' % server.pid)
    if assign_server_to_job(server.pid):
        log('server assigned to a kill-on-close job')
    else:
        log('warning: server is not in a job object; a launcher crash can orphan it')
    ready = OUT / 'ready.json'
    host, port = '127.0.0.2', 0
    channel_host, channel_port = '127.0.0.1', 7001
    for _ in range(400):
        if server.poll() is not None:
            log('server exited: ' + (OUT / 'gateway.err').read_text(errors='replace')[-2000:])
            return
        if ready.exists():
            st = json.loads(ready.read_text())
            host, port_s = st['address'].split(':')
            port = int(port_s)
            ch_addr = st.get('channel_refresh_address')
            if ch_addr and ':' in ch_addr:
                channel_host, ch_port_s = ch_addr.rsplit(':', 1)
                channel_port = int(ch_port_s)
            break
        time.sleep(0.05)
    if not port:
        log('server never became ready')
        server.terminate()
        return
    log('server ready at %s:%d (channel directory %s:%d)' % (host, port, channel_host, channel_port))
    record_run(server.pid, port)
    connect_host = (ADVERTISE_HOST or '127.0.0.1') if host == '0.0.0.0' else host
    connect_ch_host = (ADVERTISE_HOST or '127.0.0.1') if channel_host == '0.0.0.0' else channel_host
    if MODE == '3':
        payload = '3?%s?%d?probe?00000000000000000000000000000000?0?0?30?0?0?0' % (connect_ch_host, channel_port)
    else:
        payload = '13?%s?%d?probe?00000000000000000000000000000000?0?0?30?0?0?0' % (connect_host, port)
    client_exe = preflight.client_exe()
    if NO_CLIENT:
        # Server-only run: the client is started by something else (a frida probe
        # has to spawn it so its hooks exist before the game's own protections
        # run; see tools/probe_dungeon_handlers.py --spawn).
        log('server-only run; payload for a client started elsewhere: %s' % payload)
        log('interactive mode: 保持运行，按 Ctrl+C 结束')
        try:
            while server.poll() is None:
                time.sleep(5)
        except KeyboardInterrupt:
            log('interrupted')
        finally:
            try:
                server.terminate()
            except Exception:
                pass
        log('server stopped')
        return
    client = subprocess.Popen([str(client_exe), payload], cwd=str(client_exe.parent))
    log('client pid=%d payload=%s style=%s' % (client.pid, payload, STYLE))
    log('interactive mode: 保持运行，按 Ctrl+C 结束（客户端退出也会自动结束）')
    client_running = True
    try:
        while True:
            time.sleep(5)
            if client.poll() is not None:
                client_running = False
                log('client exited (exit code %s) — nothing of ours killed it' % client.returncode)
                break
    except KeyboardInterrupt:
        log('interrupted')
    finally:
        # Never kill the client: this cleanup path is what made the game window
        # vanish ("闪退") whenever the tool exited. Leave the game to the user.
        try:
            server.terminate()
        except Exception:
            pass
    log('server stopped (it is also in a kill-on-close job)')
    log('client %s; db100 database is a file — nothing to stop'
        % ('still running, left alone' if client_running else 'already gone'))
    log('done')


if __name__ == '__main__':
    main()
