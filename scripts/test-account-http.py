#!/usr/bin/env python3
"""Real HTTPS proxy account checks. Usage: python3 scripts/test-account-http.py /path/to/subconv-next"""
import http.client
import http.cookiejar
import http.server
import json
import os
from pathlib import Path
import socket
import sys
import ssl
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request

binary = sys.argv[1] if len(sys.argv) > 1 else '/tmp/scn-account-http-binary'
def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]

with tempfile.TemporaryDirectory(prefix='scn-http-registration-') as folder:
    base = Path(folder)
    config = base / 'config.json'
    config.write_text('{}')
    backend_port = free_port()
    frontend_port = free_port()
    origin = f'https://localhost:{frontend_port}'
    cert, key = base / 'cert.pem', base / 'key.pem'
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=localhost', '-keyout', str(key), '-out', str(cert)], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    process = None
    def start(enabled=True):
        global process
        env = dict(os.environ, SUBCONV_ACCESS_TOKEN='test-master-token-with-32-characters', SUBCONV_REGISTRATION_ENABLED=str(enabled).lower())
        process = subprocess.Popen([binary, 'serve', '--config', str(config), '--host', '127.0.0.1', '--port', str(backend_port), '--data-dir', str(base / 'data'), '--public-base-url', origin], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        for _ in range(100):
            try:
                with urllib.request.urlopen(f'http://127.0.0.1:{backend_port}/healthz', timeout=1) as response:
                    if response.status == 200: return
            except (OSError, urllib.error.URLError):
                pass
            if process.poll() is not None:
                raise RuntimeError(process.stderr.read().decode())
            time.sleep(.05)
        raise RuntimeError('backend did not become healthy')
    def stop():
        global process
        if process is not None:
            process.terminate()
            process.wait(timeout=10)
            process.stderr.close()
            process = None

    class Proxy(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_): pass
        def forward(self):
            body = self.rfile.read(int(self.headers.get('Content-Length', '0')))
            headers = {name: value for name, value in self.headers.items() if name.lower() not in ('connection', 'content-length')}
            headers['X-Forwarded-Proto'] = 'https'
            connection = http.client.HTTPConnection('127.0.0.1', backend_port, timeout=10)
            connection.request(self.command, self.path, body=body, headers=headers)
            response = connection.getresponse()
            payload = response.read()
            self.send_response(response.status)
            for name, value in response.getheaders():
                if name.lower() not in ('connection', 'transfer-encoding', 'content-length'):
                    self.send_header(name, value)
            self.send_header('Content-Length', str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            connection.close()
        do_GET = forward
        do_POST = forward
        do_PATCH = forward
        do_PUT = forward
        do_DELETE = forward

    proxy = http.server.ThreadingHTTPServer(('127.0.0.1', frontend_port), Proxy)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(str(cert), str(key))
    proxy.socket = tls.wrap_socket(proxy.socket, server_side=True)
    thread = threading.Thread(target=proxy.serve_forever, daemon=True)
    thread.start()
    def client():
        jar = http.cookiejar.CookieJar()
        # The temporary test proxy has a self-signed localhost certificate.
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), urllib.request.HTTPSHandler(context=ssl._create_unverified_context()))
        return opener, jar
    def request(opener, method, path, payload=None, csrf=None):
        data = json.dumps(payload).encode() if payload is not None else None
        headers = {'Origin': origin, 'Accept': 'application/json', 'Content-Type': 'application/json', 'Sec-Fetch-Site': 'same-origin'}
        if csrf: headers['X-SubConv-CSRF'] = csrf
        req = urllib.request.Request(origin + path, data=data, headers=headers, method=method)
        try: response = opener.open(req, timeout=15)
        except urllib.error.HTTPError as error: response = error
        with response:
            raw = response.read()
            content = json.loads(raw) if response.headers.get('Content-Type', '').startswith('application/json') else raw.decode()
            return response.status, content
    try:
        start()
        alice, alice_jar = client()
        bob, _ = client()
        admin, _ = client()
        anonymous, _ = client()
        status, health = request(anonymous, 'GET', '/healthz')
        assert status == 200 and health['ok'] and 'data_dir' not in health and 'version' not in health
        status, app_js = request(anonymous, 'GET', '/app.js')
        assert status == 401
        status, login_html = request(anonymous, 'GET', '/login')
        assert status == 200 and 'register-mode' in login_html and 'confirm-password' in login_html
        status, login_js = request(anonymous, 'GET', '/login.js')
        assert status == 200 and '/api/auth/register' in login_js
        password = '  my actual test password!  '
        status, alice_session = request(alice, 'POST', '/api/auth/register', {'username':'alice','password':password,'confirm_password':password})
        assert status == 201 and alice_session['role'] == 'user'
        assert any(cookie.secure and cookie.name == 'subconv_management_session' for cookie in alice_jar)
        status, session = request(alice, 'GET', '/api/auth/session')
        assert status == 200 and session['authenticated'] and session['user_id'] == alice_session['user_id']
        status, alice_workspace = request(alice, 'POST', '/api/workspaces', csrf=session['csrf_token'])
        assert status == 200
        alice_id = alice_workspace['workspace_id']
        status, user_config = request(alice, 'GET', '/api/config?workspace='+alice_id)
        assert status == 200
        converted = user_config['config']
        converted['inline'] = [{'name':'manual','enabled':True,'content':'ss://YWVzLTI1Ni1nY206cGFzc0BleGFtcGxlLmNvbTo0NDM=#ss-node'}]
        status, result = request(alice, 'PUT', '/api/config?workspace='+alice_id, converted, session['csrf_token'])
        assert status == 200, result
        status, publication = request(alice, 'POST', '/api/refresh?workspace='+alice_id, csrf=session['csrf_token'])
        assert status == 200 and publication['subscription_url'].startswith(origin+'/s/'), publication
        status, yaml = request(anonymous, 'GET', publication['subscription_url'][len(origin):])
        assert status == 200 and 'ss-node' in yaml
        status, bob_session = request(bob, 'POST', '/api/auth/register', {'username':'bob','password':password,'confirm_password':password})
        assert status == 201
        status, bob_workspace = request(bob, 'POST', '/api/workspaces', csrf=bob_session['csrf_token'])
        assert status == 200
        status, result = request(bob, 'GET', '/api/config?workspace='+alice_id)
        assert status == 404
        status, result = request(bob, 'POST', '/api/workspaces/'+bob_workspace['workspace_id']+'/bind-publish', {'publish_id':publication['publish_id']}, bob_session['csrf_token'])
        assert status == 404
        status, result = request(bob, 'POST', '/api/auth/register', {'username':'ALICE','password':password,'confirm_password':password})
        assert status == 409
        status, result = request(admin, 'POST', '/api/auth/login', {'username':'admin','password':'test-master-token-with-32-characters'})
        assert status == 200 and result['role'] == 'admin'
        admin_session = result
        status, health = request(admin, 'GET', '/healthz')
        assert status == 200 and health['data_dir'] == str(base / 'data')
        status, health = request(alice, 'GET', '/healthz')
        assert status == 200 and 'data_dir' not in health and 'version' not in health
        status, account_js = request(admin, 'GET', '/account.js')
        assert status == 200 and 'ADMINISTRATOR_SCRIPT_ONLY' in account_js
        status, users = request(admin, 'GET', '/api/users')
        assert status == 200 and [user['username'] for user in users['users']] == ['alice', 'bob']
        assert 'password' not in json.dumps(users) and 'session_version' not in json.dumps(users)
        status, result = request(alice, 'GET', '/api/users')
        assert status == 403
        root_change = {'current_password':'test-master-token-with-32-characters','new_password':'web-root-change123','confirm_password':'web-root-change123'}
        status, result = request(admin, 'POST', '/api/auth/password', root_change, admin_session['csrf_token'])
        assert status == 403 and result['error']['code'] == 'ADMINISTRATOR_SCRIPT_ONLY'
        old_alice, old_jar = client()
        for cookie in alice_jar: old_jar.set_cookie(cookie)
        password_change = {'current_password':password,'new_password':'new-alice-password123','confirm_password':'new-alice-password123'}
        status, result = request(alice, 'POST', '/api/auth/password', password_change)
        assert status == 403
        status, new_session = request(alice, 'POST', '/api/auth/password', password_change, session['csrf_token'])
        assert status == 200 and new_session['csrf_token'] != session['csrf_token'] and new_session['role'] == 'user'
        status, old_session = request(old_alice, 'GET', '/api/auth/session')
        assert status == 200 and not old_session['authenticated']
        status, result = request(alice, 'GET', '/api/config?workspace='+alice_id)
        assert status == 200
        for disabled in (True, False):
            status, result = request(admin, 'PATCH', '/api/users/'+bob_session['user_id'], {'disabled':disabled}, admin_session['csrf_token'])
            assert status == 200
            status, old_session = request(bob, 'GET', '/api/auth/session')
            assert status == 200 and not old_session['authenticated']
            if disabled:
                status, result = request(bob, 'POST', '/api/auth/login', {'username':'bob','password':password})
                assert status == 401
        status, result = request(admin, 'POST', '/api/users/'+bob_session['user_id']+'/password', {'new_password':'new-bob-password123','confirm_password':'new-bob-password123'}, admin_session['csrf_token'])
        assert status == 200
        status, result = request(bob, 'POST', '/api/auth/login', {'username':'bob','password':password})
        assert status == 401
        status, result = request(anonymous, 'GET', publication['subscription_url'][len(origin):])
        assert status == 200 and 'ss-node' in result
        status, result = request(admin, 'POST', '/api/auth/logout')
        assert status == 403
        status, result = request(admin, 'POST', '/api/auth/logout', csrf=admin_session['csrf_token'])
        assert status == 200 and result['ok']
        status, result = request(admin, 'GET', '/api/auth/session')
        assert status == 200 and not result['authenticated']
        accounts_file = base / 'data/accounts.json'
        saved = accounts_file.read_bytes()
        assert accounts_file.stat().st_mode & 0o777 == 0o600 and password.encode() not in saved
        stop()
        start(enabled=False)
        status, session = request(alice, 'GET', '/api/auth/session')
        assert status == 200 and session['authenticated'] and not session['registration_enabled']
        status, result = request(bob, 'POST', '/api/auth/login', {'username':'bob','password':'new-bob-password123'})
        assert status == 200 and result['role'] == 'user'
        status, result = request(anonymous, 'POST', '/api/auth/register', {'username':'carol','password':password,'confirm_password':password})
        assert status == 403
        assert accounts_file.read_bytes() == saved
        print('Real HTTPS proxy checks passed: admin-only user listing and management, script-only admin credentials, user password change and session revocation, disabled logins, password reset, private health metadata, admin logout and CSRF, frontend assets, signup, Secure cookies, CSRF, conversion/publication, two-account isolation, existing admin login, restart persistence and closed registration.')
    finally:
        stop()
        proxy.shutdown()
        proxy.server_close()
        thread.join(timeout=5)
