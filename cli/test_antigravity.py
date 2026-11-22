"""Native Antigravity contracts, using only a temporary CLI home and a fake model list."""
import argparse
import io
import json
import os
from pathlib import Path
import runpy
import tempfile
import unittest
from unittest.mock import patch

CLI = Path(__file__).with_name("finalechat")
G = runpy.run_path(str(CLI))["install_antigravity"].__globals__
SID = "ede7d1a1-c33e-459f-9f4e-38644275cc5b"


class AntigravityTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.project = self.root / "project"
        self.project.mkdir()
        for mock in [patch.dict(os.environ, HOME=temp.name), patch.dict(G, INTEGRATION_DIR=self.root / "integrations",
                     antigravity_model_labels=lambda: ["Gemini (Low)", "Gemini (High)"], native_version=lambda _: "fixture")]:
            mock.start()
            self.addCleanup(mock.stop)

    def test_settings_save_preserves_unrelated_native_fields(self):
        adapter = G["make_settings_adapter"]("antigravity", self.project)
        G["private_json"](adapter.path, {"model": "Gemini (Low)", "localOnly": {"secret": "fixture-secret"}})
        view = adapter.snapshot()
        self.assertNotIn("fixture-secret", json.dumps(view))
        self.assertEqual(view["snapshot"]["saved"], {"/model": "Gemini (Low)"})
        command = {"schema_version": view["descriptor"]["schema_version"], "expected_version": view["snapshot"]["version"],
                   "operation": "settings.apply", "edits": [{"op": "set", "key": "/model", "value": "Gemini (High)"}]}
        prepared = adapter.prepare(command, adapter.grant())
        adapter.apply_prepared(prepared)
        self.assertEqual(json.loads(adapter.path.read_bytes()), {"model": "Gemini (High)", "localOnly": {"secret": "fixture-secret"}})

    def test_mcp_native_identity_and_explicit_override(self):
        with patch.dict(os.environ, FINALECHAT_THREAD=""):
            self.assertEqual(G["mcp_target"]({}, {"antigravity.google/conversation_id": SID}).ref, "ext:antigravity:" + SID)
        with patch.dict(os.environ, FINALECHAT_THREAD="ext:override"):
            self.assertEqual(G["mcp_target"]({}, {"antigravity.google/conversation_id": SID}).ref, "ext:override")

    def test_hooks_and_mcp_preserve_sibling_settings_on_reinstall(self):
        hooks = self.root / ".gemini/config/hooks.json"
        mcp = hooks.with_name("mcp_config.json")
        G["private_json"](hooks, {"my-hook": {"enabled": False}})
        G["private_json"](mcp, {"mcpServers": {"other": {"command": "true"}, "finalechat": {"disabledTools": ["test"]}}})
        args = argparse.Namespace(settings=None, project=str(self.project), no_mcp=False, artifacts=False)
        with patch("shutil.which", return_value="agy"):
            G["install_antigravity"](args)
            before = hooks.read_bytes()
            G["install_antigravity"](args)
            self.assertEqual(hooks.read_bytes(), before)
            self.assertEqual(json.loads(mcp.read_bytes())["mcpServers"]["finalechat"]["disabledTools"], ["test"])
            G["install_antigravity"](args, remove=True)
        self.assertEqual(json.loads(hooks.read_bytes()), {"my-hook": {"enabled": False}})
        self.assertEqual(json.loads(mcp.read_bytes()), {"mcpServers": {"other": {"command": "true"}}})

    def test_unhandled_pre_tool_hook_delegates_to_native_policy(self):
        with patch("sys.stdin", io.StringIO("{}")), patch("sys.stdout", new_callable=io.StringIO) as output:
            G["hook_antigravity"]("PreToolUse")
            self.assertEqual(json.loads(output.getvalue()), {"decision": "allow"})

    def test_discovery_matches_native_project_and_conversation(self):
        sessions = {SID: {"summary": {"ID": SID, "WorkspaceURIs": [self.project.as_uri()]}}}
        native = G["antigravity_transcript"](SID)
        native.parent.mkdir(parents=True)
        native.write_text(json.dumps({"type": "USER_INPUT", "step_index": 0}) + "\n")
        directory = self.root / "integration"
        with patch.dict(G, antigravity_conversations=lambda: sessions, integration_path=lambda *_: directory):
            G["discover_antigravity_sessions"](self.root, directory)
            self.assertFalse(list(directory.glob("sessions/*.json")))
            G["discover_antigravity_sessions"](self.project, directory)
            [record] = directory.glob("sessions/*.json")
            self.assertEqual(json.loads(record.read_text())["transcript"], str(native))
