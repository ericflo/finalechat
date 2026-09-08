#!/usr/bin/env python3
"""Optional installed-client lifecycle test with an isolated native home.

Model and FinaleChat APIs are local HTTP fixtures with dummy credentials.
No user configuration or account is touched; reported token/cost data is
synthetic. Successful fixtures are removed, failed fixtures retain diagnostics.
Run with `python3 -I scripts/test-native-claude.py`.
"""
import json, os, pathlib, shutil, subprocess, tempfile, threading, sys, uuid
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
from urllib.parse import urlparse
if not shutil.which('claude'):
    print('SKIP: install claude to run its native lifecycle check')
    sys.exit(0)
root = pathlib.Path(tempfile.mkdtemp(prefix='finalechat-native-lifecycle-'))
project = root / 'project'
project.mkdir()
(project / 'fixture.txt').write_text('LOCAL_TOOL_PROOF_123\n')
cli = str(pathlib.Path(__file__).resolve().parents[1] / 'cli/finalechat')
state = {'requests': [], 'messages': [], 'model': [], 'question': None, 'remote': True}

class Handler(BaseHTTPRequestHandler):

    def log_message(self, *args):
        pass

    def do_GET(self):
        self.handle_request()

    def do_POST(self):
        self.handle_request()

    def do_DELETE(self):
        self.handle_request()

    def handle_request(self):
        body = json.loads(self.rfile.read(int(self.headers.get('Content-Length', '0'))) or b'{}')
        p = urlparse(self.path).path
        state['requests'].append((self.command, p, body))
        if p == '/v1/messages/count_tokens':
            return self.reply({'input_tokens': 100})
        if p == '/v1/messages':
            state['model'].append(body)
            tools = {t.get('name') for t in body.get('tools', [])}
            native = json.dumps(body.get('messages', []))
            if 'Read' in tools and 'call-read-fixture' not in native:
                return self.anthropic(body, {'type': 'tool_use', 'id': 'call-read-fixture', 'name': 'Read', 'input': {'file_path': str(project / 'fixture.txt')}})
            if 'AskUserQuestion' in tools and 'call-question-fixture' not in native:
                return self.anthropic(body, {'type': 'tool_use', 'id': 'call-question-fixture', 'name': 'AskUserQuestion', 'input': {'questions': [{'question': 'Which fixture color?', 'header': 'Color', 'options': [{'label': 'Blue', 'description': 'First fixture option'}, {'label': 'Green', 'description': 'Second fixture option'}], 'multiSelect': False}]}})
            return self.anthropic(body, {'type': 'text', 'text': 'NATIVE_FINAL_PROOF_123'})
        if p == '/api/v1/me':
            return self.reply({'user': {'id': 'fixture-user', 'settings': {'remote_mode': state['remote']}}, 'features': {'artifacts.v1': False, 'settings-control.v1': False}})
        if p == '/api/v1/threads':
            return self.reply({'thread': {'id': 'fixture-thread', 'external_id': body.get('external_id'), 'title': 'Fixture'}})
        if p.endswith('/questions') and self.command == 'POST':
            q = {'id': str(uuid.uuid4()), 'status': 'answered', 'options': body.get('options', []), 'answer': {'selected': ['Blue'], 'text': 'Choose Blue from phone'}}
            state['question'] = q
            return self.reply({'question': q})
        if '/questions/' in p:
            return self.reply({'question': state['question']})
        if p.endswith('/messages'):
            if self.command == 'POST':
                m = dict(body, id=str(uuid.uuid4()))
                state['messages'].append(m)
                if body.get('sender', 'agent') == 'agent' and 'NATIVE_FINAL_PROOF' in body.get('body', ''):
                    state['remote'] = False
                return self.reply({'message': m})
            return self.reply({'messages': []})
        if p.endswith('/activity'):
            return self.reply({'applied': True, 'thread': {'id': 'fixture-thread'}})
        return self.reply({'error': {'message': 'unimplemented fixture ' + p}}, 404)

    def reply(self, obj, status=200):
        raw = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def anthropic(self, body, content):
        stop = 'tool_use' if content['type'] == 'tool_use' else 'end_turn'
        msg = {'id': 'msg-fixture-' + str(len(state['model'])), 'type': 'message', 'role': 'assistant', 'model': body['model'], 'content': [content], 'stop_reason': stop, 'stop_sequence': None, 'usage': {'input_tokens': 100, 'output_tokens': 10}}
        if not body.get('stream'):
            return self.reply(msg)
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.end_headers()

        def event(name, obj):
            self.wfile.write(('event: ' + name + '\ndata: ' + json.dumps(obj) + '\n\n').encode())
        event('message_start', {'type': 'message_start', 'message': dict(msg, content=[], stop_reason=None)})
        initial = dict(content, input={}) if content['type'] == 'tool_use' else {'type': 'text', 'text': ''}
        event('content_block_start', {'type': 'content_block_start', 'index': 0, 'content_block': initial})
        delta = {'type': 'input_json_delta', 'partial_json': json.dumps(content['input'])} if content['type'] == 'tool_use' else {'type': 'text_delta', 'text': content['text']}
        event('content_block_delta', {'type': 'content_block_delta', 'index': 0, 'delta': delta})
        event('content_block_stop', {'type': 'content_block_stop', 'index': 0})
        event('message_delta', {'type': 'message_delta', 'delta': {'stop_reason': stop, 'stop_sequence': None}, 'usage': {'output_tokens': 10}})
        event('message_stop', {'type': 'message_stop'})
server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
base = 'http://127.0.0.1:' + str(server.server_port)
native = root / 'claude'
native.mkdir()
env = {k: os.environ[k] for k in ['PATH', 'LANG', 'TERM'] if k in os.environ}
env.update(HOME=str(root / 'home'), CLAUDE_CONFIG_DIR=str(native), XDG_CONFIG_HOME=str(root / 'xdg-config'), XDG_CACHE_HOME=str(root / 'xdg-cache'), ANTHROPIC_API_KEY='fixture-only', ANTHROPIC_BASE_URL=base, FINALECHAT_TOKEN='fc_fixture', FINALECHAT_URL=base, FINALECHAT_STATUS='off', FINALECHAT_ASK_WAIT='5', FINALECHAT_REPLY_WAIT='1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1', DISABLE_AUTOUPDATER='1', HTTPS_PROXY=base, HTTP_PROXY=base, NO_PROXY='127.0.0.1,localhost')
(root / 'home').mkdir()
(native / 'settings.json').write_text(json.dumps({'hooks': {'SessionStart': [{'hooks': [{'type': 'command', 'command': 'true'}]}]}, 'spinnerTipsEnabled': False}))
(native / '.claude.json').write_text(json.dumps({'hasCompletedOnboarding': True}))
(root / 'home' / '.claude.json').write_text(json.dumps({'hasCompletedOnboarding': True}))
print('FIXTURE_ROOT', root, flush=True)
try:
    unrelated = subprocess.run([shutil.which('claude'), 'mcp', 'add', '--scope', 'user', 'unrelated-fixture', '--', 'true'], env=env, cwd=project, capture_output=True, text=True, timeout=30)
    assert unrelated.returncode == 0, unrelated.stderr
    install = subprocess.run([sys.executable, cli, 'install', 'claude-code', '--settings', str(native / 'settings.json'), '--project', str(project)], env=env, cwd=project, capture_output=True, text=True, timeout=30)
    (root / 'install.txt').write_text(install.stdout + install.stderr)
    sid = str(uuid.uuid4())
    args = [shutil.which('claude'), '-p', '--input-format', 'stream-json', '--permission-prompt-tool', 'stdio', '--verbose', '--model', 'claude-sonnet-4-6', '--session-id', sid, '--permission-mode', 'default', '--allowedTools', 'Read', '--tools', 'Read,AskUserQuestion', '--strict-mcp-config', '--mcp-config', '{"mcpServers":{}}', '--output-format', 'stream-json']
    run = subprocess.run(args, input=json.dumps({'type': 'user', 'message': {'role': 'user', 'content': 'Read fixture.txt, ask the fixture color, and give the final answer.'}}) + '\n', env=env, cwd=project, capture_output=True, text=True, timeout=60)
    (root / 'run.txt').write_text(run.stdout + run.stderr)
    assert run.returncode == 0, run.stderr
    assert state['question'], 'native AskUserQuestion was not advertised or routed through the hook'
    assert 'LOCAL_TOOL_PROOF_123' in json.dumps(state['model'][-1]['messages'])
    assert 'The user answered from their phone' in json.dumps(state['model'][-1]['messages'])
    transcript = next(native.glob('projects/*/' + sid + '.jsonl'))
    original = transcript.read_bytes()
    exported = root / 'archive'
    exported_run = subprocess.run([sys.executable, cli, 'artifact', 'export', 'claude-code', sid, '--project', str(project), '--transcript', str(transcript), '-o', str(exported)], env=env, cwd=project, capture_output=True, text=True, timeout=30)
    assert exported_run.returncode == 0, exported_run.stdout + exported_run.stderr
    assert (exported / 'sessions' / transcript.name).read_bytes() == original
    resumed_args = list(args)
    resumed_args[resumed_args.index('--session-id')] = '--resume'
    resumed = subprocess.run(resumed_args, input=json.dumps({'type': 'user', 'message': {'role': 'user', 'content': 'RESUME_NATIVE_PROOF: continue and finish.'}}) + '\n', env=env, cwd=project, capture_output=True, text=True, timeout=60)
    (root / 'resume.txt').write_text(resumed.stdout + resumed.stderr)
    assert resumed.returncode == 0, resumed.stderr
    assert transcript.read_bytes().startswith(original), 'native resume rewrote the published prefix'
    assert (exported / 'sessions' / transcript.name).read_bytes() == original
    assert len([m for m in state['messages'] if m.get('body') == 'NATIVE_FINAL_PROOF_123']) == 2
    assert any((m.get('meta', {}).get('kind') == 'session_end' for m in state['messages']))
    reinstalled = subprocess.run([sys.executable, cli, 'install', 'claude-code', '--settings', str(native / 'settings.json'), '--project', str(project)], env=env, cwd=project, capture_output=True, text=True, timeout=60)
    assert reinstalled.returncode == 0, reinstalled.stderr
    hooks = json.loads((native / 'settings.json').read_text())['hooks']['SessionStart']
    assert len(hooks) == 2, 'reinstall duplicated hooks or erased unrelated hooks'
    removed = subprocess.run([sys.executable, cli, 'uninstall', 'claude-code', '--settings', str(native / 'settings.json'), '--project', str(project)], env=env, cwd=project, capture_output=True, text=True, timeout=60)
    assert removed.returncode == 0, removed.stderr
    remaining = json.loads((native / 'settings.json').read_text())
    assert remaining['hooks']['SessionStart'] == [{'hooks': [{'type': 'command', 'command': 'true'}]}]
    assert remaining['spinnerTipsEnabled'] is False
    check = subprocess.run([shutil.which('claude'), 'mcp', 'get', 'unrelated-fixture'], env=env, cwd=project, capture_output=True, text=True, timeout=30)
    assert check.returncode == 0, 'uninstall removed unrelated MCP registration'
    check = subprocess.run([shutil.which('claude'), 'mcp', 'get', 'finalechat'], env=env, cwd=project, capture_output=True, text=True, timeout=30)
    assert check.returncode != 0, 'uninstall left FinaleChat MCP registered'
    print('PASS native Claude hooks, local tool, phone answer, exact source export, resume, reinstall/uninstall preservation', flush=True)
finally:
    (root / 'state.json').write_text(json.dumps(state, indent=2))
    server.shutdown()
    server.server_close()
print('MODELS', len(state['model']), 'MESSAGES', len(state['messages']), 'QUESTION', bool(state['question']))
shutil.rmtree(root)
