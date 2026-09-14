"""Round trip of the Word count example plugin over HTTP.

The plugin listens on 127.0.0.1 at a port from 18140 to 18149. A fake host
stands in for Markspace, and the test signs each call as Markspace would.
"""

from __future__ import annotations

import json
import random
import threading
import time
import unittest
import urllib.error
import urllib.request
import uuid

import support

from markspace import ui
from markspace.signature import sign_headers

import example

NOTE1 = {"id": "n1", "spaceId": "space1", "title": "Hello", "content": "---\ntitle: Hello\n---\nHello brave new world\n",
         "updatedAt": "2026-09-13T10:00:00Z"}
NOTE2 = {"id": "n2", "spaceId": "space1", "title": "Two", "content": "one two", "updatedAt": "2026-09-13T10:00:00Z"}


class ExampleRoundTrip(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.fake = support.FakeHost().start()
        cls.fake.notes = {"n1": NOTE1, "n2": NOTE2}
        cls.fake.trees["space1"] = [
            {"kind": "folder", "folder": {"id": "f1", "name": "Docs"}, "children": [{"kind": "note", "note": {"id": "n1"}}]},
            {"kind": "note", "note": {"id": "n2"}},
            {"kind": "file", "file": {"id": "file1"}},
        ]
        cls.fake.file_texts["txt1"] = "one two three"
        cls.data_dir = support.tempdir()
        cls.plugin = example.create_plugin(data_dir=cls.data_dir, env={}, job_wait=10.0)
        cls.server = None
        for port in random.sample(range(18140, 18150), 10):
            try:
                cls.server = cls.plugin.make_server("127.0.0.1", port)
                break
            except OSError:
                continue
        if cls.server is None:
            raise unittest.SkipTest("no free port in 18140-18149")
        cls.port = cls.server.server_address[1]
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        status, resp = cls.request("POST", "/install", support.install_payload("word-count", cls.fake.url))
        if status != 200:
            raise AssertionError(f"install failed: {status} {resp}")

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.fake.stop()

    @classmethod
    def request(cls, method, path, payload=None, signed=False):
        body = None if payload is None else json.dumps(payload).encode()
        headers = {"Content-Type": "application/json"} if body is not None else {}
        if signed:
            headers.update(sign_headers(support.SECRET, "word-count", uuid.uuid4().hex, method, path, body, time.time()))
        req = urllib.request.Request(f"http://127.0.0.1:{cls.port}{path}", data=body, method=method, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=10) as resp:
                raw, status = resp.read(), resp.status
        except urllib.error.HTTPError as exc:
            with exc:
                raw, status = exc.read(), exc.code
        return status, (json.loads(raw) if raw else None)

    def interaction(self, token="act1", **extra):
        return {"context": {"spaceId": "space1", "itemId": "n1", "kind": "note"}, "actingToken": token,
                "user": {"id": "u1", "name": "Ann"}, **extra}

    def test_port_is_in_range(self):
        self.assertTrue(18140 <= self.port <= 18149)

    def test_manifest_and_health(self):
        self.assertEqual(self.request("GET", "/manifest.json"), (200, example.MANIFEST))
        self.assertEqual(self.request("GET", "/health"), (200, {"status": "ok", "version": "0.1.0"}))

    def test_second_unsigned_install_is_refused(self):
        status, resp = self.request("POST", "/install", support.install_payload("word-count", self.fake.url))
        self.assertEqual(status, 401, resp)

    def test_unsigned_command_is_refused(self):
        self.assertEqual(self.request("POST", "/commands/count", self.interaction())[0], 401)

    def test_command_count(self):
        status, resp = self.request("POST", "/commands/count", self.interaction("act-cmd"), signed=True)
        self.assertEqual((status, resp), (200, {"toast": {"message": "4 words in Hello", "kind": "info"}}))
        calls = [c for c in self.fake.calls("GET", "/api/plugin/v1/items/n1")
                 if c["headers"]["Authorization"] == "Bearer act-cmd"]
        self.assertEqual(len(calls), 1)

    def test_panel_counts(self):
        status, resp = self.request("POST", "/panels/counts", self.interaction(), signed=True)
        self.assertEqual(status, 200, resp)
        ui.validate(resp["ui"])
        table = resp["ui"]["children"][0]["children"][0]
        self.assertEqual(table["rows"][0], {"metric": "Words", "value": 4})
        button = resp["ui"]["children"][1]["children"][0]
        self.assertEqual(button["action"], {"id": "count-space", "payload": {"spaceId": "space1"}})

    def test_action_queues_job(self):
        status, resp = self.request("POST", "/actions/count-space",
                                    self.interaction(values={}, payload={"spaceId": "space1"}), signed=True)
        self.assertEqual(status, 200, resp)
        self.assertTrue(resp["job"]["id"].startswith("job"))
        created = self.fake.calls("POST", "/api/plugin/v1/jobs")[-1]["json"]
        self.assertEqual(created["kind"], "count-space")
        self.assertEqual(created["payload"], {"spaceId": "space1"})

    def test_job_counts_space_with_progress(self):
        payload = {"job": {"id": "jcount", "kind": "count-space", "payload": {"spaceId": "space1"}, "attempt": 1,
                           "title": "Count words", "spaceId": "space1"},
                   "actingToken": "jobtok", "user": {"id": "u1", "name": "Ann"}}
        status, resp = self.request("POST", "/jobs/count-space", payload, signed=True)
        self.assertEqual((status, resp), (200, {"result": {"spaceId": "space1", "notes": 2, "words": 6}}))
        patches = [c["json"] for c in self.fake.calls("PATCH", "/api/plugin/v1/jobs/jcount")]
        self.assertEqual([(p["progress"], p["total"]) for p in patches], [(0, 2), (1, 2), (2, 2)])
        auth = {c["headers"]["Authorization"] for c in self.fake.calls("PATCH", "/api/plugin/v1/jobs/jcount")}
        self.assertEqual(auth, {"Bearer jobtok"})
        self.assertEqual(self.fake.kv["space:space1"], {"spaceId": "space1", "notes": 2, "words": 6})

    def test_event_stores_last_count(self):
        events = [{"id": "e1", "type": "note.updated", "spaceId": "space1", "itemId": "n2", "at": "2026-09-13T11:00:00Z"},
                  {"id": "e2", "type": "note.created", "spaceId": "space1", "itemId": "n1"},
                  {"id": "e3", "type": "note.updated", "spaceId": "space1", "itemId": "gone"}]
        status, _ = self.request("POST", "/events", {"events": events}, signed=True)
        self.assertEqual(status, 204)
        self.assertEqual(self.fake.kv["note:n2"], {"words": 2, "updatedAt": "2026-09-13T10:00:00Z"})
        self.assertNotIn("note:n1", self.fake.kv)
        self.assertNotIn("note:gone", self.fake.kv)

    def test_import_text(self):
        payload = {"fileId": "txt1", "spaceId": "space1", "actingToken": "act-imp", "user": {"id": "u1", "name": "Ann"}}
        status, resp = self.request("POST", "/import/text", payload, signed=True)
        self.assertEqual((status, resp), (200, {"toast": {"message": "The file has 3 words.", "kind": "info"}}))

    def test_count_words(self):
        self.assertEqual(example.count_words("---\na: b\n---\nIt's a well-known fact."), 4)
        self.assertEqual(example.count_words(""), 0)


if __name__ == "__main__":
    unittest.main()
