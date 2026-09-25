"""Loopback-only synthetic HTTP fixture; never reads AgentDock configuration."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlparse, parse_qs
import json
import sys
import time

class Handler(BaseHTTPRequestHandler):
    writes = 0
    ui_writes = 0
    ui_tasks = {
        'tsk_0000000000000011': dict(id='tsk_0000000000000011', title='Completed fixture / 已完成示例', status='completed', goal='Synthetic task for native UI verification', revision='tsk1:' + 'a' * 64),
        'tsk_0000000000000022': dict(id='tsk_0000000000000022', title='Active fixture / 进行中示例', status='active', goal='Must be preserved by completed cleanup', revision='tsk1:' + 'a' * 64),
        'tsk_0000000000000033': dict(id='tsk_0000000000000033', title='Another completed fixture / 另一项已完成', status='completed', goal='Selected only by displayed completed cleanup', revision='tsk1:' + 'a' * 64),
    }

    def json_response(self, payload, status=200):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def ui_get(self, path):
        if path.path == '/internal/runtime/tasks':
            status = parse_qs(path.query).get('status', [''])[0]
            tasks = [task for task in Handler.ui_tasks.values()
                     if (bool(task.get('archived_at')) if status == 'archived'
                         else not task.get('archived_at') and (not status or task['status'] == status))]
            self.json_response(dict(ok=True, tasks=tasks, partial=False))
        else:
            task = Handler.ui_tasks.get(path.path.rsplit('/', 1)[-1])
            if not task:
                self.json_response(dict(ok=False), 404)
                return
            self.json_response(dict(ok=True, task=task, revision=task['revision'], job_receipts=[], jobs_available=True, jobs_partial=False))

    def ui_post(self):
        request = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        Handler.ui_writes += 1
        results = []
        for ref in request['tasks']:
            task = Handler.ui_tasks.get(ref['id'])
            code = None
            if task is None:
                code = 'TASK_NOT_FOUND'
            elif task['revision'] != ref['revision']:
                code = 'TASK_CONFLICT'
            elif request['action'] == 'delete' and task['status'] != 'completed':
                code = 'TASK_NOT_COMPLETED'
            else:
                if request['action'] == 'delete':
                    del Handler.ui_tasks[ref['id']]
                else:
                    task['archived_at'] = '2026-09-25T00:00:00Z' if request['action'] == 'archive' else None
                    task['revision'] = 'tsk1:' + format(Handler.ui_writes, '064x')
            results.append(dict(task_id=ref['id'], ok=code is None, code=code))
        changed = sum(item['ok'] for item in results)
        self.json_response(dict(ok=True, action=request['action'], results=results, changed=changed, failed=len(results)-changed))
    def log_message(self, *args):
        pass

    def do_GET(self):
        path = urlparse(self.path)
        fixture = parse_qs(path.query).get('fixture', [''])[0]
        if fixture == 'ui-stats':
            self.json_response(dict(writes=Handler.ui_writes, ids=list(Handler.ui_tasks)))
            return
        if self.headers.get('Authorization') == 'Bearer synthetic-ui-token':
            self.ui_get(path)
            return
        if fixture == 'slow':
            time.sleep(1)
        if fixture == 'redirect':
            self.send_response(302)
            self.send_header('Location', '/must-not-follow')
            self.end_headers()
            return
        if path.path == '/must-not-follow':
            self.send_error(500)
            return
        if fixture == 'write-count':
            body = str(Handler.writes).encode()
            self.send_response(200)
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        token = self.headers.get('Authorization')
        health = path.path == '/healthz'
        if (health and token is not None) or (not health and token != 'Bearer synthetic-test-token'):
            self.send_error(403)
            return
        body = b'x' * 5000 if fixture.startswith('large') else (b'health-without-token' if health else b'authenticated')
        self.send_response(200)
        if fixture != 'large-stream':
            self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        try:
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def do_POST(self):
        if self.path == '/internal/runtime/tasks/manage' and self.headers.get('Authorization') == 'Bearer synthetic-ui-token':
            self.ui_post()
            return
        if self.path != '/internal/runtime/tasks/manage' or self.headers.get('Authorization') != 'Bearer synthetic-test-token':
            self.send_error(403)
            return
        if self.headers.get('Content-Type') != 'application/json':
            self.send_error(415)
            return
        request = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        assert len(request['tasks']) == 1
        assert request['tasks'][0]['revision'] == 'tsk1:' + 'a' * 64
        Handler.writes += 1
        if request['action'] == 'restore':
            self.send_response(302)
            self.send_header('Location', '/must-not-follow')
            self.end_headers()
            return
        task_id = request['tasks'][0]['id'] if request['action'] == 'archive' else 'tsk_ffffffffffffffff'
        body = json.dumps(dict(ok=True, action=request['action'], changed=1, failed=0,
                               results=[dict(task_id=task_id, ok=True)])).encode()
        self.send_response(200)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
Path(sys.argv[1]).write_text(str(server.server_port))
server.serve_forever()
