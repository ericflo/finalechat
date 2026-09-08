#!/usr/bin/env python3
"""Optional installed-client lifecycle test with an isolated native home.

Model and FinaleChat APIs are local HTTP fixtures with dummy credentials.
No user configuration or account is touched; reported token/cost data is
synthetic. Successful fixtures are removed, failed fixtures retain diagnostics.
Run with `python3 -I scripts/test-native-codex.py`.
"""
import json, os, pathlib, shutil, subprocess, tempfile, threading, sys
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
from urllib.parse import urlparse
if not shutil.which('codex'):
    print('SKIP: install codex to run its native lifecycle check')
    sys.exit(0)
root = pathlib.Path(tempfile.mkdtemp(prefix='finalechat-codex-lifecycle-'))
project = root / 'project'
project.mkdir()
(project / 'fixture.txt').write_text('LOCAL_CODEX_TOOL_PROOF_123\n')
native = root / 'codex'
native.mkdir()
home = root / 'home'
home.mkdir()
state = {'requests': [], 'models': [], 'messages': []}

class Handler(BaseHTTPRequestHandler):

    def log_message(self, *args):
        pass

    def do_GET(self):
        self.handle_request()

    def do_POST(self):
        self.handle_request()

    def handle_request(self):
        body = json.loads(self.rfile.read(int(self.headers.get('Content-Length', '0'))) or b'{}')
        p = urlparse(self.path).path
        state['requests'].append((self.command, p, body))
        if p == '/v1/responses':
            state['models'].append(body)
            input_text = json.dumps(body.get('input', []))
            tools = {}
            for t in body.get('tools', []):
                if t.get('type') == 'namespace':
                    for inner in t.get('tools', []):
                        tools[t['name'] + '.' + inner['name']] = inner
                else:
                    tools[t.get('name')] = t
            if 'call-code-probe' not in input_text:
                name = next((n for n in ['exec_command', 'shell_command', 'shell'] if n in tools), None)
                if name:
                    args = {'cmd': 'cat fixture.txt', 'workdir': str(project)} if name == 'exec_command' else {'command': 'cat fixture.txt', 'workdir': str(project)}
                    if name == 'shell':
                        args['command'] = ['bash', '-c', 'cat fixture.txt']
                    item = {'id': 'fc-code-probe', 'type': 'function_call', 'call_id': 'call-code-probe', 'name': name, 'arguments': json.dumps(args), 'status': 'completed'}
                    return self.respond(item)
            if 'call-mcp-probe' not in input_text:
                name = next((n for n in tools if n and 'finalechat_send' in n), None)
                if name:
                    item = {'id': 'fc-mcp-probe', 'type': 'function_call', 'call_id': 'call-mcp-probe', 'namespace': name.split('.')[0], 'name': name.split('.')[-1], 'arguments': json.dumps({'message': 'NATIVE_CODEX_MCP_PROOF_123'}), 'status': 'completed'}
                    return self.respond(item)
            return self.respond({'id': 'msg-code-probe', 'type': 'message', 'role': 'assistant', 'status': 'completed', 'content': [{'type': 'output_text', 'text': 'NATIVE_CODEX_FINAL_PROOF_123', 'annotations': []}]})
        if p == '/api/v1/me':
            return self.reply({'user': {'id': 'fixture-user', 'settings': {'remote_mode': False}}, 'features': {}})
        if p.endswith('/messages'):
            m = dict(body, id='01a08000-0000-7000-8000-000000000123')
            state['messages'].append(m)
            return self.reply({'message': m, 'thread': {'id': '01a08000-0000-7000-8000-000000000001', 'title': 'Fixture'}})
        if p == '/api/v1/threads':
            return self.reply({'thread': {'id': '01a08000-0000-7000-8000-000000000001', 'title': 'Fixture'}})
        return self.reply({'error': {'message': 'unknown fixture ' + p}}, 404)

    def reply(self, obj, status=200):
        raw = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def respond(self, item):
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.end_headers()
        response = {'id': 'resp-fixture-' + str(len(state['models'])), 'object': 'response', 'created_at': 1788860000, 'model': 'fixture', 'status': 'completed', 'output': [item], 'usage': {'input_tokens': 100, 'output_tokens': 10, 'total_tokens': 110}}

        def event(name, **data):
            self.wfile.write(('event: ' + name + '\ndata: ' + json.dumps(dict(type=name, **data)) + '\n\n').encode())
        event('response.created', response=dict(response, status='in_progress', output=[]))
        event('response.output_item.added', output_index=0, item=item)
        event('response.output_item.done', output_index=0, item=item)
        event('response.completed', response=response)
server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
base = 'http://127.0.0.1:' + str(server.server_port)
(native / 'config.toml').write_text('model = "fixture"\nmodel_provider = "fixture"\napproval_policy = "never"\nsandbox_mode = "workspace-write"\n[model_providers.fixture]\nname = "Fixture"\nbase_url = "' + base + '/v1"\nwire_api = "responses"\nrequires_openai_auth = false\nsupports_websockets = false\nrequest_max_retries = 0\nstream_max_retries = 0\n[analytics]\nenabled = false\n[feedback]\nenabled = false\n')
env = {k: os.environ[k] for k in ['PATH', 'LANG', 'TERM'] if k in os.environ}
env.update(HOME=str(home), CODEX_HOME=str(native), XDG_CONFIG_HOME=str(root / 'xdg-config'), XDG_CACHE_HOME=str(root / 'xdg-cache'), FINALECHAT_TOKEN='fc_fixture', FINALECHAT_URL=base, HTTPS_PROXY=base, HTTP_PROXY=base, NO_PROXY='127.0.0.1,localhost')
print('FIXTURE_ROOT', root, flush=True)
try:
    cli = str(pathlib.Path(__file__).resolve().parents[1] / 'cli/finalechat')
    install = subprocess.run([sys.executable, cli, 'install', 'codex', '--project', str(project)], env=env, cwd=project, capture_output=True, text=True, timeout=30)
    (root / 'install.txt').write_text(install.stdout + install.stderr)
    with (native / 'config.toml').open('a') as f:
        f.write('\n[mcp_servers.finalechat.tools.finalechat_send]\napproval_mode = "approve"\n')
    run = subprocess.run([shutil.which('codex'), 'exec', '--skip-git-repo-check', '--json', 'Read fixture.txt and send the result with FinaleChat, then finish.'], env=env, cwd=project, capture_output=True, text=True, timeout=60)
    (root / 'run.txt').write_text(run.stdout + run.stderr)
    assert install.returncode == 0, install.stderr
    assert run.returncode == 0, run.stderr
    lines = [json.loads(line) for line in run.stdout.splitlines() if line.startswith('{')]
    sid = next((line['thread_id'] for line in lines if line['type'] == 'thread.started'))
    assert any((line.get('item', {}).get('type') == 'mcp_tool_call' and line['item']['status'] == 'completed' for line in lines)), 'native MCP call did not succeed'
    assert len(state['messages']) == 1
    assert any(('/ext%3Acodex%3A' + sid + '/messages' in path for method, path, _ in state['requests'])), 'MCP default did not use native session identity'
    transcript = next(native.glob('sessions/**/*' + sid + '*.jsonl'))
    original = transcript.read_bytes()
    assert b'LOCAL_CODEX_TOOL_PROOF_123' in original and b'01a08000-0000-7000-8000-000000000123' in original
    exported = root / 'archive'
    exported_run = subprocess.run([sys.executable, cli, 'artifact', 'export', 'codex', sid, '--project', str(project), '-o', str(exported)], env=env, cwd=project, capture_output=True, text=True, timeout=30)
    assert exported_run.returncode == 0, exported_run.stdout + exported_run.stderr
    assert (exported / 'sessions' / transcript.name).read_bytes() == original
    resumed = subprocess.run([shutil.which('codex'), 'exec', 'resume', '--skip-git-repo-check', '--json', sid, 'RESUME_NATIVE_PROOF: finish the fixture.'], env=env, cwd=project, capture_output=True, text=True, timeout=60)
    (root / 'resume.txt').write_text(resumed.stdout + resumed.stderr)
    assert resumed.returncode == 0, resumed.stderr
    assert transcript.read_bytes().startswith(original), 'native resume rewrote the published prefix'
    assert (exported / 'sessions' / transcript.name).read_bytes() == original
    reinstalled = subprocess.run([sys.executable, cli, 'install', 'codex', '--project', str(project)], env=env, cwd=project, capture_output=True, text=True, timeout=30)
    assert reinstalled.returncode == 0, reinstalled.stderr
    assert 'approval_mode = "approve"' in (native / 'config.toml').read_text(), 'reinstall replaced the locally selected tool policy'
    removed = subprocess.run([sys.executable, cli, 'uninstall', 'codex', '--project', str(project)], env=env, cwd=project, capture_output=True, text=True, timeout=30)
    assert removed.returncode == 0, removed.stderr
    remaining = (native / 'config.toml').read_text()
    assert 'mcp_servers.finalechat' not in remaining
    assert 'model = "fixture"' in remaining and 'model_providers.fixture' in remaining
    print('PASS native Codex local tool, MCP credentials and thread routing, exact rollout export, resume, reinstall/uninstall preservation', flush=True)
finally:
    (root / 'state.json').write_text(json.dumps(state, indent=2))
    server.shutdown()
    server.server_close()
print('MODELS', len(state['models']), 'MESSAGES', len(state['messages']))
shutil.rmtree(root)
