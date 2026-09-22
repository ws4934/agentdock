#!/usr/bin/env python3
"""Read-only fault simulation: test socket + original Swift monitor. No desktop input."""
import json
import os
from pathlib import Path
import socket
import subprocess
import threading
import time

import sys
ROOT = Path(sys.argv[1]).resolve()
BINARY = Path(sys.argv[2]).resolve()


def run_case(mode: str) -> dict:
    root = ROOT / ('ui-' + mode)
    root.mkdir(mode=0o700, exist_ok=True)
    path = root / 'control.sock'
    if path.exists():
        path.unlink()
    server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    server.bind(str(path))
    os.chmod(path, 0o600)
    server.listen(8)
    server.settimeout(0.2)
    quit_event = threading.Event()
    records = []
    state = {
        'enabled': True, 'monitor_required': True, 'monitor_connected': True,
        'session_id': '0123456789abcdef0123456789abcdef', 'epoch': 2,
        'phase': 'running', 'activity': 'waiting', 'mode': 'background',
        'application': {'pid': 0, 'name': 'AgentDock isolated audit', 'bundle_id': ''},
        'window': {'id': 0, 'pid': 0, 'title': '', 'bounds': {'x': 0, 'y': 0, 'width': 0, 'height': 0}},
        'pointer_sequence': 0, 'active_operations': 0, 'reason': '', 'can_resume': False,
    }

    def serve():
        last_poll = None
        injected = False
        while not quit_event.is_set():
            try:
                connection, _ = server.accept()
            except socket.timeout:
                continue
            except OSError:
                break
            with connection:
                connection.settimeout(2)
                raw = bytearray()
                while len(raw) <= 1_048_576:
                    part = connection.recv(8192)
                    if not part:
                        break
                    raw.extend(part)
                request = json.loads(raw)
                now = time.monotonic()
                params = request.get('params', {})
                op = params.get('operation', '')
                record = {'method': request['method'], 'operation': op}
                if mode == 'stop' and op == 'stop' and not injected:
                    injected = True
                    record['injected_disconnect_before_delivery'] = True
                    records.append(record)
                    # Simulate a transport failure before the command reaches the core.
                    continue
                if last_poll is not None and now - last_poll >= 3 and state['phase'] == 'running':
                    state['phase'] = 'paused'
                    state['reason'] = 'monitor_disconnected'
                    state['can_resume'] = True
                    record['lease_gap_seconds'] = round(now - last_poll, 3)
                if request['method'] == 'computeruse.poll':
                    if params.get('stop_session_id') == state['session_id']:
                        state['phase'] = 'stopped'; state['reason'] = 'user_stopped'; state['can_resume'] = True
                    last_poll = now
                elif op == 'stop':
                    state['phase'] = 'stopped'
                    state['reason'] = 'user_stopped'
                    state['can_resume'] = True
                records.append(record)
                connection.sendall(json.dumps({'id': request['id'], 'result': state}).encode())

    worker = threading.Thread(target=serve, daemon=True)
    worker.start()
    try:
        result = subprocess.run([str(BINARY), str(root), mode], capture_output=True, text=True, timeout=25)
        (root / 'process.log').write_text(result.stdout + result.stderr)
        output = json.loads((root / 'result.json').read_text())
        output['child_exit'] = result.returncode
        output['request_log'] = records.copy()
        (root / 'evidence.json').write_text(json.dumps(output, indent=2))
        return output
    finally:
        quit_event.set()
        server.close()
        worker.join(timeout=2)
        path.unlink(missing_ok=True)


if __name__ == '__main__':
    results = {mode: run_case(mode) for mode in ['stop', 'timer']}
    print(json.dumps(results, indent=2))
    assert all(r.get('passed') and r['child_exit'] == 0 for r in results.values()), 'Computer Use safety regression'
