"""Third-party website packaging and shared publication recovery."""
import contextlib
import io
import json
import os
import unittest
from pathlib import Path
from unittest.mock import patch

import test_integrations as fixtures
from test_publication import PublicationAPI

G = fixtures.G


class WebsiteTests(unittest.TestCase):
    setUp = fixtures.IntegrationTests.setUp
    tearDown = fixtures.IntegrationTests.tearDown

    def website(self):
        root = self.root / "website"
        root.mkdir()
        (root / "index.html").write_text("<!doctype html><style>body{color:green}</style><p>Archive</p>")
        (root / "settings.html").write_text("<!doctype html><p>Settings snapshot</p>")
        (root / "events.jsonl").write_bytes(b'{"seq":1,"unknown":{"retained":true}}\n')
        (root / "context.json").write_text('{"runtime_known":false}')
        (root / "chart.bin").write_bytes(bytes(range(256)))
        return root

    def args(self, action, root, *extra):
        parser = G["build_parser"]()
        return parser.parse_args(["artifact", action, str(root), *extra])

    def call(self, args):
        with contextlib.redirect_stdout(io.StringIO()):
            return G["cmd_website"](args)

    def test_pack_repack_and_recover_preserve_declared_source_bytes(self):
        root = self.website()
        metadata = self.root / "dataset.json"
        metadata.write_text('{"format":"example.events/v1","session_id":"session-42"}')
        target = self.root / "packed"
        args = self.args("pack", root, "-o", str(target), "--source", "events.jsonl", "--context", "context.json", "--dataset", str(metadata), "--settings-entrypoint", "settings.html")
        self.call(args)
        manifest = G["verify_website"](target)
        self.assertEqual(manifest["dataset"]["session_id"], "session-42")
        self.assertEqual(manifest["settings_entrypoint"], "settings.html")
        roles = {f["path"]: f["role"] for f in manifest["files"]}
        self.assertEqual(roles, {"index.html": "viewer", "settings.html": "viewer", "events.jsonl": "source", "context.json": "context", "chart.bin": "asset"})
        for record in manifest["files"]:
            self.assertEqual((root / record["path"]).read_bytes(), (target / record["path"]).read_bytes())
        second = self.root / "repacked"
        self.call(self.args("pack", target, "-o", str(second)))
        self.assertEqual(manifest, G["verify_website"](second))
        recovered = self.root / "recovered"
        with contextlib.redirect_stdout(io.StringIO()):
            G["cmd_artifact"](self.args("restore", second, "-o", str(recovered)))
        self.assertEqual((recovered / "events.jsonl").read_bytes(), (root / "events.jsonl").read_bytes())
        self.assertFalse((recovered / "index.html").exists())

    def test_upload_freezes_pending_bytes_then_advances_to_fresh_capture(self):
        root = self.website()
        api = PublicationAPI()
        api.base_url = "http://fixture.invalid"
        api.lose_response = True
        args = self.args("upload", root, "--thread", "ext:explicit-session", "--key", "inspector", "--source", "events.jsonl", "--append-only-sources")
        original = (root / "events.jsonl").read_bytes()
        with patch.dict(G, client_from_args=lambda _: api):
            with self.assertRaises(G["APIError"]):
                self.call(args)
            (root / "events.jsonl").write_bytes(original + b'{"seq":2}\n')
            self.call(args)
            self.assertEqual(api.commits[0], api.commits[1])
            self.assertEqual(len(api.commits), 3)
            self.call(args)
            self.assertEqual(len(api.commits), 3)
            source = next(f for f in api.commits[-1]["manifest"]["files"] if f["role"] == "source")
            self.assertEqual(source["sha256"], G["sha256"]((root / "events.jsonl").read_bytes()))
            (root / "events.jsonl").write_bytes(original)
            with self.assertRaises(G["PublicationConflict"]):
                self.call(args)
        self.assertEqual(len(api.commits), 3)
        self.assertEqual(api.registrations, 1)

    def test_upload_requires_explicit_thread_and_respects_remote_deletion(self):
        root = self.website()
        api = PublicationAPI()
        api.base_url = "http://fixture.invalid"
        with patch.dict(G, client_from_args=lambda _: api), patch.dict(os.environ, {}, clear=True):
            with self.assertRaises(G["CLIError"]):
                self.call(self.args("upload", root))
            self.assertEqual(api.registrations, 0)
            args = self.args("upload", root, "--thread", "ext:chosen-session")
            self.call(args)
            old_id = api.current_id
            del api.artifacts[old_id]
            (root / "index.html").write_text("<!doctype html><p>New viewer</p>")
            with self.assertRaises(G["PublicationDeleted"]):
                self.call(args)
            self.assertEqual(api.registrations, 1)
            args.recreate = True
            self.call(args)
            self.assertNotEqual(old_id, api.current_id)

    def test_unsafe_paths_roles_and_nested_output_fail_without_partial_exports(self):
        root = self.website()
        target = self.root / "packed"
        for bad_file, linked_to in (("secret.txt", self.user), ("linked", root / "events.jsonl")):
            (root / bad_file).symlink_to(linked_to)
            with self.assertRaises(G["CLIError"]):
                self.call(self.args("pack", root, "-o", str(target)))
            self.assertFalse(target.exists())
            (root / bad_file).unlink()
        for options in (("--source", "../secret"), ("--source", "index.html"), ("--source", "missing.jsonl")):
            with self.assertRaises(G["CLIError"]):
                self.call(self.args("pack", root, "-o", str(target), *options))
            self.assertFalse(target.exists())
        with self.assertRaises(G["CLIError"]):
            self.call(self.args("pack", root, "-o", str(root / "nested")))
        self.assertFalse((root / "nested").exists())

    def test_capture_detects_concurrent_producer_write(self):
        root = self.website()
        target = self.root / "packed"
        original_add = G["WebsiteExport"].add
        def racing_add(export, name, role, content_type, parts):
            original_add(export, name, role, content_type, parts)
            if name == "events.jsonl":
                with (root / name).open("ab") as out:
                    out.write(b'{"seq":2}\n')
        with patch.object(G["WebsiteExport"], "add", racing_add), self.assertRaises(G["CLIError"]):
            self.call(self.args("pack", root, "-o", str(target), "--source", "events.jsonl"))
        self.assertFalse(target.exists())

    def test_manifest_entrypoint_validation_happens_before_upload(self):
        root = self.website()
        target = self.root / "packed"
        self.call(self.args("pack", root, "-o", str(target)))
        original = G["verify_website"](target)
        for mutation in (lambda m: m.update(entrypoint=None), lambda m: m.update(settings_entrypoint="missing.html"), lambda m: m["files"][0].update(role="unsupported")):
            manifest = json.loads(json.dumps(original))
            mutation(manifest)
            G["private_json"](target / "manifest.json", manifest)
            with self.assertRaises(G["CLIError"]):
                G["verify_website"](target)
