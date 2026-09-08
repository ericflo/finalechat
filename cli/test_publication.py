"""Native publication recovery tests with an in-memory HTTP contract fixture."""
import copy
import unittest
from pathlib import Path
from unittest.mock import patch

import test_integrations as fixtures

G = fixtures.G


class PublicationAPI:
    def __init__(self):
        self.artifacts, self.blobs, self.commits = {}, {}, []
        self.registrations = 0
        self.current_id = None
        self.before_commit = None
        self.lose_response = False

    def request(self, method, path, body=None, **_):
        if method == "PUT" and path.startswith("/api/v1/threads/"):
            if self.current_id not in self.artifacts:
                self.registrations += 1
                self.current_id = "artifact-" + str(self.registrations)
                self.artifacts[self.current_id] = {"artifact": {"id": self.current_id, "thread_id": "thread-" + str(self.registrations), "current_revision_id": None}, "revision": None, "keys": {}}
            return {"artifact": copy.deepcopy(self.artifacts[self.current_id]["artifact"])}
        artifact_id = path.split("/")[4]
        if artifact_id not in self.artifacts:
            raise G["APIError"](404, "not_found", "fixture artifact deleted")
        state = self.artifacts[artifact_id]
        if method == "GET":
            return copy.deepcopy(state)
        if path.endswith("/blobs/check"):
            return {"missing": [h for h in body["hashes"] if h not in self.blobs]}
        if path.endswith("/revisions"):
            self.commits.append(copy.deepcopy(body))
            if self.before_commit:
                callback, self.before_commit = self.before_commit, None
                callback()
            key = body["client_key"]
            if key in state["keys"]:
                return {"revision": copy.deepcopy(state["keys"][key])}
            if body["previous_revision_id"] != state["artifact"]["current_revision_id"]:
                raise G["APIError"](409, "conflict", "fixture parent advanced")
            revision = {"id": "revision-" + str(len(self.commits)), "manifest": copy.deepcopy(body["manifest"])}
            state["revision"] = state["keys"][key] = revision
            state["artifact"]["current_revision_id"] = revision["id"]
            if self.lose_response:
                self.lose_response = False
                raise G["APIError"](503, "busy", "fixture lost acknowledgement")
            return {"revision": copy.deepcopy(revision)}
        raise AssertionError((method, path))

    def request_bytes(self, method, path, data, **_):
        assert method == "PUT"
        digest = path.rsplit("/", 1)[1]
        assert G["sha256"](data) == digest
        self.blobs[digest] = data
        return {}


class PublicationTests(unittest.TestCase):
    setUp = fixtures.IntegrationTests.setUp
    tearDown = fixtures.IntegrationTests.tearDown
    write = fixtures.IntegrationTests.write
    native_fixture = fixtures.IntegrationTests.native_fixture

    def test_lost_acknowledgement_reuses_capture_key_and_skips_unchanged(self):
        registration, _ = self.native_fixture()
        api = PublicationAPI()
        api.lose_response = True
        with self.assertRaises(G["APIError"]):
            G["publish_registered_session"](api, registration, self.directory, self.adapter.snapshot())
        result = G["publish_registered_session"](api, registration, self.directory, self.adapter.snapshot())
        self.assertEqual(len(api.commits), 2)
        self.assertEqual(api.commits[0], api.commits[1])
        self.assertEqual(G["publish_registered_session"](api, registration, self.directory, self.adapter.snapshot()), result)
        self.assertEqual(len(api.commits), 2)
        self.assertEqual(api.registrations, 1)
        self.assertFalse(list(self.directory.glob("publications/*/pending.json")))


    def test_conflict_recaptures_without_rewinding_published_sources(self):
        registration, original = self.native_fixture()
        api = PublicationAPI()
        first = G["publish_registered_session"](api, registration, self.directory, self.adapter.snapshot())
        newer = original + b'{"type":"new-event","value":2}\n'
        newest = newer + b'{"type":"new-event","value":3}\n'
        Path(registration["transcript"]).write_bytes(newer)

        def race():
            remote = copy.deepcopy(api.artifacts[first["artifact_id"]]["revision"])
            remote["id"] = "raced-revision"
            source = next(f for f in remote["manifest"]["files"] if f["role"] == "source")
            source.update(size=len(newest), sha256=G["sha256"](newest), chunks=[{"size": len(newest), "sha256": G["sha256"](newest)}])
            api.artifacts[first["artifact_id"]]["revision"] = remote
            api.artifacts[first["artifact_id"]]["artifact"]["current_revision_id"] = remote["id"]
            Path(registration["transcript"]).write_bytes(newest)

        api.before_commit = race
        with self.assertRaises(G["PublicationConflict"]):
            G["publish_registered_session"](api, registration, self.directory, self.adapter.snapshot())
        result = G["publish_registered_session"](api, registration, self.directory, self.adapter.snapshot())
        source = next(f for f in api.artifacts[result["artifact_id"]]["revision"]["manifest"]["files"] if f["role"] == "source")
        self.assertEqual(source["sha256"], G["sha256"](newest))
        self.assertEqual(api.commits[-1]["previous_revision_id"], "raced-revision")
        self.assertNotEqual(api.commits[-1]["client_key"], api.commits[-2]["client_key"])
        self.assertEqual(api.registrations, 1)


    def test_truncation_and_divergence_never_replace_remote_history(self):
        registration, original = self.native_fixture()
        api = PublicationAPI()
        first = G["publish_registered_session"](api, registration, self.directory, self.adapter.snapshot())
        for changed in (original.splitlines(keepends=True)[0], original.replace(b"Hello", b"Other")):
            Path(registration["transcript"]).write_bytes(changed)
            with self.assertRaises(G["PublicationConflict"]):
                G["publish_registered_session"](api, registration, self.directory, self.adapter.snapshot())
            self.assertEqual(api.artifacts[first["artifact_id"]]["artifact"]["current_revision_id"], first["revision_id"])
        self.assertEqual(len(api.commits), 1)


    def test_remote_deletion_requires_explicit_recreation(self):
        registration, original = self.native_fixture()
        api = PublicationAPI()
        first = G["publish_registered_session"](api, registration, self.directory, self.adapter.snapshot())
        with self.assertRaises(G["CLIError"]):
            G["publish_registered_session"](api, registration, self.directory, self.adapter.snapshot(), recreate=True)
        del api.artifacts[first["artifact_id"]]
        Path(registration["transcript"]).write_bytes(original + b'{"type":"later"}\n')
        for _ in range(2):
            with self.assertRaises(G["PublicationDeleted"]):
                G["publish_registered_session"](api, registration, self.directory, self.adapter.snapshot())
        self.assertEqual(api.registrations, 1)
        result = G["publish_registered_session"](api, registration, self.directory, self.adapter.snapshot(), recreate=True)
        self.assertNotEqual(first["artifact_id"], result["artifact_id"])
        self.assertEqual(api.registrations, 2)
        self.assertTrue(list(self.directory.glob("publications/*/retired-*.json")))


    def test_archiving_survives_an_unavailable_native_settings_api(self):
        registration, _ = self.native_fixture()
        self.write(self.directory / "sessions/fixture.json", registration)
        self.write(self.directory / "integration.json", {"artifacts": True})
        api = PublicationAPI()
        with patch.dict(G, make_settings_adapter=lambda *_: (_ for _ in ()).throw(G["CLIError"]("native settings unavailable")), hook_log=lambda *_: None):
            G["publish_integration_pass"]("claude-code", self.project, api, self.directory)
        self.assertEqual(len(api.commits), 1)
        self.assertEqual(api.commits[0]["manifest"]["dataset"]["settings_version"], "unavailable")
