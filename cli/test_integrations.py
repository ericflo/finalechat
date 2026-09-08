"""Offline integration contract/recovery tests; optional real Codex config API.

All filesystem and native configuration writes are confined to temporary roots.
No model turns, production HTTP requests, or user configuration writes occur.
"""
import argparse
import base64
import copy
import hashlib
import json
import os
from pathlib import Path
import runpy
import shutil
import tempfile
import unittest
from unittest.mock import patch

CLI = Path(__file__).with_name("finalechat")
LOADED = runpy.run_path(str(CLI), run_name="finalechat_integration_tests")
G = LOADED["cmd_connector"].__globals__


class IntegrationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="finalechat-adapter-test-")
        self.root = Path(self.temp.name)
        self.project = self.root / "project"
        self.project.mkdir()
        self.user = self.root / "claude-user"
        self.user.mkdir()
        self.directory = self.root / "companion"
        self.directory.mkdir()
        self.patch = patch.dict(G, INTEGRATION_DIR=self.root / "integrations", native_version=lambda _name: "fixture/1")
        self.patch.start()
        self.adapter = G["ClaudeSettings"](self.project, user_directory=self.user)
        self.adapter.managed_paths = lambda: [self.root / "managed.json"]

    def tearDown(self):
        self.patch.stop()
        self.temp.cleanup()

    def write(self, path, value):
        G["private_json"](path, value)

    def proposal(self, key="/model", value="fixture-model", op="set"):
        view = self.adapter.snapshot()
        edit = {"key": key, "op": op}
        if op == "set":
            edit["value"] = value
        return {"operation": "settings.apply", "schema_version": view["descriptor"]["schema_version"],
                "expected_version": view["snapshot"]["version"], "generation": "", "edits": [edit]}

    def command(self, proposal=None, **changes):
        proposal = proposal or self.proposal()
        result = {"id": "00000000-0000-7000-8000-000000000001", "resource_id": "fixture-resource",
                  "user_id": "fixture-user", "proposal": proposal,
                  "proposal_sha256": hashlib.sha256(G["canonical_bytes"](proposal)).hexdigest(), "attempts": 1}
        result.update(changes)
        return result

    def execute(self, command):
        return G["execute_settings_command"](self.adapter, self.adapter.grant(), command, self.directory)

    def test_layered_saved_effective_and_managed_values(self):
        self.write(self.user / "settings.json", {"model": "user", "permissions": {"allow": ["Read"]}})
        self.write(self.adapter.path, {"model": "project", "permissions": {"allow": ["Read", "Bash(ls)"]}})
        self.write(self.project / ".claude/settings.local.json", {"model": "local"})
        view = self.adapter.snapshot()
        self.assertEqual(view["snapshot"]["saved"]["/model"], "project")
        self.assertEqual(view["snapshot"]["effective"]["/model"], "local")
        self.assertEqual(view["snapshot"]["effective"]["/permissions/allow"], ["Read", "Bash(ls)"])
        model = next(f for f in view["descriptor"]["fields"] if f["key"] == "/model")
        self.assertFalse(model["writable"])
        self.write(self.root / "managed.json", {"effortLevel": "high"})
        self.assertNotEqual(view["snapshot"]["version"], self.adapter.snapshot()["snapshot"]["version"])
        with self.assertRaises(G["CLIError"]):
            self.adapter.prepare(self.proposal("/effortLevel", "low"), self.adapter.grant())

    def test_credentials_and_unknown_raw_settings_are_not_exported(self):
        self.write(self.adapter.path, {"model": "test", "env": {"ANTHROPIC_API_KEY": "fixture-secret"},
                                       "apiKeyHelper": "echo fixture-secret", "unknown": {"secret": "fixture-secret"}})
        view = self.adapter.snapshot()
        self.assertNotIn("fixture-secret", json.dumps(view))
        self.assertIn("env", view["snapshot"]["details"]["unsupported_fields"])

    def test_unexpected_values_under_known_fields_are_not_exported(self):
        self.write(self.adapter.path, {"model": {"token": "fixture-secret"}, "permissions": {"allow": [{"token": "fixture-secret"}]}})
        view = self.adapter.snapshot()
        self.assertNotIn("fixture-secret", json.dumps(view))
        self.assertEqual(view["snapshot"]["details"]["unsupported_values"], ["/model", "/permissions/allow"])
        for field in view["descriptor"]["fields"]:
            if field["key"] in view["snapshot"]["details"]["unsupported_values"]:
                self.assertFalse(field["writable"])

    def test_local_validator_rejects_hidden_payloads(self):
        view, grant = self.adapter.snapshot(), self.adapter.grant()
        for proposal in (
            dict(self.proposal(op="unset"), edits=[{"key": "/model", "op": "unset", "value": "hidden"}]),
            dict(self.proposal(), parameters={"extra": True}),
            dict(self.proposal(), operation="settings.refresh"),
            dict(self.proposal(), edits=[None]),
        ):
            with self.assertRaises(G["CLIError"]):
                G["validate_local_proposal"](view, proposal, grant)

    def test_adapter_upgrade_changes_snapshot_version(self):
        original = self.adapter.snapshot()["snapshot"]["version"]
        with patch.dict(G, INTEGRATION_ADAPTER_DIGEST="upgraded-fixture"):
            self.assertNotEqual(original, self.adapter.snapshot()["snapshot"]["version"])

    def test_stale_context_rejected_without_replacement(self):
        self.write(self.adapter.path, {"model": "before"})
        proposal = self.proposal()
        self.write(self.user / "settings.json", {"effortLevel": "medium"})
        status, _ = self.execute(self.command(proposal))
        self.assertEqual(status, "conflicted")
        self.assertEqual(json.loads(self.adapter.path.read_bytes())["model"], "before")

    def test_save_duplicate_and_permanent_audit(self):
        self.write(self.adapter.path, {"model": "before", "unknown": {"keep": 42}})
        command = self.command()
        first = self.execute(command)
        modified = self.adapter.path.stat().st_mtime_ns
        self.assertEqual(first[0], "succeeded")
        self.assertFalse(first[1]["effects"][0]["runtime_applied"])
        self.assertEqual(self.execute(dict(command, attempts=2)), first)
        self.assertEqual(self.adapter.path.stat().st_mtime_ns, modified)
        self.assertEqual(json.loads(self.adapter.path.read_bytes())["unknown"], {"keep": 42})
        audit = (self.directory / "settings-audit.jsonl").read_text().splitlines()
        self.assertEqual(len(audit), 1)
        self.assertEqual(json.loads(audit[0])["user_id"], "fixture-user")

    def test_save_before_ack_reconciles_without_second_write(self):
        self.write(self.adapter.path, {"model": "before"})
        command = self.command()
        prepared = self.adapter.prepare(command["proposal"], self.adapter.grant())
        self.adapter.apply_prepared(prepared)
        stamp = self.adapter.path.stat().st_mtime_ns
        self.write(self.directory / "commands" / (command["id"] + ".json"), {
            "identity": {"command_id": command["id"], "resource_key": self.adapter.key,
                         "proposal_digest": command["proposal_sha256"]}, "prepared": prepared, "write_started": True})
        status, result = self.execute(dict(command, attempts=2))
        self.assertEqual(status, "succeeded")
        self.assertTrue(result["reconciled"])
        self.assertEqual(self.adapter.path.stat().st_mtime_ns, stamp)

    def test_missing_journal_redelivery_is_unknown(self):
        status, _ = self.execute(self.command(attempts=2))
        self.assertEqual(status, "unknown")
        self.assertFalse(self.adapter.path.exists())

    def test_unset_removes_empty_parent_and_preserves_other_fields(self):
        self.write(self.adapter.path, {"attribution": {"commit": "by fixture"}, "other": 7})
        status, _ = self.execute(self.command(self.proposal("/attribution/commit", op="unset")))
        self.assertEqual(status, "succeeded")
        self.assertEqual(json.loads(self.adapter.path.read_bytes()), {"other": 7})

    def test_ungranted_class_invalid_type_and_overlap_rejected(self):
        proposal = self.proposal("/permissions/allow", ["Bash(*)"])
        grant = self.adapter.grant()
        grant["classes"] = ["preference"]
        with self.assertRaises(G["CLIError"]):
            self.adapter.prepare(proposal, grant)
        for value in (True, "2", -1, 100000):
            with self.assertRaises(G["CLIError"]):
                self.adapter.prepare(self.proposal("/cleanupPeriodDays", value), self.adapter.grant())
        proposal = self.proposal()
        proposal["edits"].append(copy.deepcopy(proposal["edits"][0]))
        with self.assertRaises(G["CLIError"]):
            self.adapter.prepare(proposal, self.adapter.grant())

    def test_symlink_write_refused(self):
        outside = self.root / "outside.json"
        self.write(outside, {"model": "outside"})
        self.adapter.path.parent.mkdir(parents=True)
        self.adapter.path.symlink_to(outside)
        with self.assertRaises(G["CLIError"]):
            self.adapter.prepare(self.proposal(), self.adapter.grant())
        self.assertEqual(json.loads(outside.read_bytes())["model"], "outside")

    def native_fixture(self, provider="claude-code"):
        transcript = self.root / "fixture-session.jsonl"
        events = [{"type": "user", "sessionId": "fixture-session", "message": {"role": "user", "content": "Hello π 🚀"}},
                  {"type": "assistant", "uuid": "assistant-1", "message": {"role": "assistant", "content": [{"type": "tool_use", "id": "tool-1", "name": "Bash", "input": {"command": "echo fixture"}}]}},
                  {"type": "future-event", "preserve": "all original fields"}]
        if provider == "codex":
            events.insert(0, {"type": "session_meta", "payload": {"id": "fixture-session", "cwd": str(self.project), "cli_version": "fixture"}})
        raw = b"".join(G["canonical_bytes"](event) + b"\n" for event in events)
        transcript.write_bytes(raw + b'{"incomplete":')
        registration = G["validate_registration"](provider, self.project, "fixture-session", transcript)
        return registration, raw

    def export_fixture(self, provider="claude-code"):
        registration, raw = self.native_fixture(provider)
        export = self.root / "export"
        export.mkdir()
        manifest = G["build_native_export"](export, registration, self.adapter.snapshot())
        return export, manifest, raw

    def test_export_preserves_complete_native_bytes_and_subagents(self):
        registration, raw = self.native_fixture()
        root = Path(registration["transcript"]).with_suffix("") / "subagents"
        root.mkdir(parents=True)
        subagent = b'{"type":"assistant","message":{"content":"subagent result"}}\n'
        (root / "agent-fixture.jsonl").write_bytes(subagent)
        export = self.root / "export"
        export.mkdir()
        manifest = G["build_native_export"](export, registration, self.adapter.snapshot())
        self.assertEqual(G["verify_website"](export), manifest)
        self.assertEqual((export / "sessions/fixture-session.jsonl").read_bytes(), raw)
        self.assertEqual((export / "sessions/fixture-session/subagents/agent-fixture.jsonl").read_bytes(), subagent)
        self.assertIn("finalechat.artifact.connect", (export / "index.html").read_text())

    def test_corruption_and_manifest_traversal_rejected(self):
        export, manifest, raw = self.export_fixture()
        (export / "sessions/fixture-session.jsonl").write_bytes(raw.replace(b"Hello", b"HELLO"))
        with self.assertRaises(G["CLIError"]):
            G["verify_website"](export)
        (export / "sessions/fixture-session.jsonl").write_bytes(raw)
        manifest["files"][0]["path"] = "../outside"
        self.write(export / "manifest.json", manifest)
        with self.assertRaises(G["CLIError"]):
            G["verify_website"](export)

    def test_portable_path_parity(self):
        for path in ("../a", "/x", "a//b", "a\\b", "a:stream", "x%2fy", "NUL.txt", "a/CON", "a.", "a/..", "a\x00b"):
            self.assertFalse(G["portable_path"](path), path)
        for path in ("sessions/a.jsonl", "context/π.json", "settings/index.html"):
            self.assertTrue(G["portable_path"](path), path)

    def test_restore_only_verified_source_files_and_no_overwrite(self):
        export, manifest, raw = self.export_fixture("codex")
        target = self.root / "restore"
        args = argparse.Namespace(artifact_command="restore", directory=str(export), output=str(target))
        self.assertEqual(G["cmd_artifact"](args), 0)
        self.assertEqual((target / "sessions/fixture-session.jsonl").read_bytes(), raw)
        self.assertFalse((target / "settings").exists())
        self.assertFalse(json.loads((target / "recovery.json").read_bytes())["runtime_restored"])
        with self.assertRaises(FileExistsError):
            G["cmd_artifact"](args)

    def test_codex_native_identity_and_project_checks(self):
        registration, _raw = self.native_fixture("codex")
        for project, session in ((self.root, "fixture-session"), (self.project, "wrong-id")):
            with self.assertRaises(G["CLIError"]):
                G["validate_registration"]("codex", project, session, registration["transcript"])

    def test_install_hook_spec_and_parsers(self):
        spec = G["hook_spec"]("finalechat hook claude-code")
        self.assertTrue({"SubagentStop", "PreCompact", "ConfigChange"} <= set(spec))
        for argv in (["install", "codex", "--no-mcp"], ["connector", "pair", "claude-code", "--scope", "project_local"], ["artifact", "verify", "/tmp/archive"], ["artifact", "publish", "codex", "fixture-session", "--recreate"]):
            self.assertTrue(callable(G["build_parser"]().parse_args(argv).func))


@unittest.skipUnless(os.environ.get("FINALECHAT_TEST_NATIVE_CODEX") == "1" and shutil.which("codex"), "opt in to installed Codex API tests")
class NativeCodexTests(unittest.TestCase):
    def test_real_native_config_write_unset_conflict_and_secret_exclusion(self):
        with tempfile.TemporaryDirectory(prefix="finalechat-native-codex-test-") as directory:
            root = Path(directory)
            native = root / "native-home"
            native.mkdir()
            path = native / "config.toml"
            path.write_text('model = "fixture-only"\nmodel_reasoning_effort = "medium"\n[model_providers.fixture]\nname = "Fixture"\nbase_url = "http://127.0.0.1:1"\nhttp_headers = { Authorization = "fixture-secret" }\n')
            environment = dict(os.environ)
            # Configure the native product's documented test home per-process;
            # no shell/global HOME or user Codex configuration is changed.
            environment["CODEX_HOME"] = str(native)
            rpc = G["CodexRPC"](root, environment=environment)
            adapter = G["CodexSettings"](root, rpc=rpc)
            try:
                view = adapter.snapshot()
                self.assertNotIn("fixture-secret", json.dumps(view))
                proposal = {"operation": "settings.apply", "schema_version": view["descriptor"]["schema_version"],
                            "expected_version": view["snapshot"]["version"], "generation": "",
                            "edits": [{"key": "/model_reasoning_effort", "op": "unset"}]}
                prepared = adapter.prepare(proposal, adapter.grant())
                adapter.apply_prepared(prepared)
                self.assertTrue(adapter.matches_prepared(prepared))
                self.assertNotIn("model_reasoning_effort", path.read_text())
                self.assertIn("fixture-secret", path.read_text())
                with self.assertRaises(G["NativeRPCError"]):
                    adapter.apply_prepared(prepared)
            finally:
                adapter.close()


if __name__ == "__main__":
    unittest.main()
