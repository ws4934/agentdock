"""Loopback-only synthetic HTTP fixture; never reads AgentDock configuration."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlparse, parse_qs
import sys
import time

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        path = urlparse(self.path)
        fixture = parse_qs(path.query).get('fixture', [''])[0]
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

server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
Path(sys.argv[1]).write_text(str(server.server_port))
server.serve_forever()
