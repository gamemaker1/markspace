"""Host API client: retries, idempotency, errors, uploads, streams, lists and storage."""

from __future__ import annotations

import email.utils
import io
import json
import time
import unittest

import support

from markspace import FileText, Host, HostError, KVEntry, Page


class NonSeekable(io.RawIOBase):
    def __init__(self, data: bytes) -> None:
        self._buf = io.BytesIO(data)

    def readable(self) -> bool:
        return True

    def seekable(self) -> bool:
        return False

    def read(self, n: int = -1) -> bytes:
        return self._buf.read(n)


class HostTests(unittest.TestCase):
    def setUp(self):
        self.fake = support.FakeHost().start()
        self.addCleanup(self.fake.stop)
        self.sleeps: list[float] = []
        self.host = Host(self.fake.url, support.TOKEN, "word-count", sleep=self.sleeps.append, rand=lambda: 1.0)

    def test_retries_honor_retry_after_then_back_off(self):
        self.fake.faults["GET /api/plugin/v1/me"] = [
            (429, {"Retry-After": "2", "Content-Type": "application/json"}, b'{"error":"slow down"}'),
            (503, {}, b""),
            (502, {}, b""),
        ]
        self.assertEqual(self.host.me()["pluginId"], "word-count")
        self.assertEqual(self.sleeps, [2.0, 1.0, 2.0])
        self.assertEqual(len(self.fake.calls("GET", "/api/plugin/v1/me")), 4)

    def test_retry_after_is_honored_on_502_and_504(self):
        self.fake.faults["GET /api/plugin/v1/me"] = [(502, {"Retry-After": "3"}, b""), (504, {"Retry-After": "4"}, b"")]
        self.host.me()
        self.assertEqual(self.sleeps, [3.0, 4.0])

    def test_retry_after_http_date(self):
        when = email.utils.formatdate(time.time() + 3, usegmt=True)
        self.fake.faults["GET /api/plugin/v1/me"] = [(429, {"Retry-After": when}, b"")]
        self.host.me()
        self.assertEqual(len(self.sleeps), 1)
        self.assertTrue(1.0 <= self.sleeps[0] <= 3.0, self.sleeps)

    def test_retry_after_is_capped(self):
        self.fake.faults["GET /api/plugin/v1/me"] = [(429, {"Retry-After": "86400"}, b"")]
        self.host.me()
        self.assertEqual(self.sleeps, [120.0])

    def test_jitter_stays_within_half_and_full_delay(self):
        h = Host(self.fake.url, "t", rand=lambda: 0.0)
        self.assertEqual([h.delay(n) for n in (1, 2, 3, 8)], [0.25, 0.5, 1.0, 15.0])
        h.rand = lambda: 0.999999
        self.assertAlmostEqual(h.delay(10), 30.0, places=3)

    def test_gives_up_after_five_tries(self):
        self.fake.faults["GET /api/plugin/v1/me"] = [(504, {}, b"")] * 6
        with self.assertRaises(HostError) as cm:
            self.host.me()
        self.assertEqual(cm.exception.status, 504)
        self.assertEqual(len(self.fake.calls("GET", "/api/plugin/v1/me")), 5)
        self.assertEqual(len(self.sleeps), 4)

    def test_other_errors_raise_without_retry(self):
        with self.assertRaises(HostError) as cm:
            self.host.create_note("space1", "")
        e = cm.exception
        self.assertEqual((e.status, e.message, e.field), (400, "title is required", "title"))
        self.assertEqual(self.sleeps, [])

    def test_create_note_sends_folder_note_fields_only_when_asked(self):
        with self.assertRaises(HostError):
            self.host.create_note("space1", "Wiki index", folder_note=True, folder_id="folder1")
        with self.assertRaises(HostError):
            self.host.create_note("space1", "Plain", parent_id="folder1")
        first, second = [json.loads(c["body"]) for c in self.fake.calls("POST", "/api/plugin/v1/notes")]
        self.assertEqual((first["folderNote"], first["folderId"]), (True, "folder1"))
        self.assertNotIn("folderNote", second)
        self.assertNotIn("folderId", second)

    def test_500_is_not_retried(self):
        self.fake.faults["GET /api/plugin/v1/me"] = [(500, {}, b'{"error":"boom"}')]
        with self.assertRaises(HostError) as cm:
            self.host.me()
        self.assertEqual((cm.exception.status, cm.exception.message), (500, "boom"))
        self.assertEqual(len(self.fake.calls("GET", "/api/plugin/v1/me")), 1)

    def test_connection_errors_retry_then_raise_status_0(self):
        h = Host("http://127.0.0.1:1", "t", sleep=self.sleeps.append, rand=lambda: 1.0, timeout=2)
        with self.assertRaises(HostError) as cm:
            h.me()
        self.assertEqual(cm.exception.status, 0)
        self.assertEqual(self.sleeps, [0.5, 1.0, 2.0, 4.0])

    def test_post_idempotency_key_is_reused_on_retry_and_fresh_per_call(self):
        self.fake.faults["POST /api/plugin/v1/jobs"] = [(503, {}, b"")]
        first = self.host.create_job("count-space", payload={"spaceId": "s1"})
        second = self.host.create_job("count-space", payload={"spaceId": "s1"})
        self.assertNotEqual(first["id"], second["id"])
        keys = [c["headers"].get("Idempotency-Key") for c in self.fake.calls("POST", "/api/plugin/v1/jobs")]
        self.assertEqual(len(keys), 3)
        self.assertEqual(keys[0], keys[1])
        self.assertNotEqual(keys[1], keys[2])
        self.assertTrue(all(k and len(k) == 32 for k in keys))
        self.host.me()
        self.host.kv_put("k", 1)
        self.assertNotIn("Idempotency-Key", self.fake.calls("GET", "/api/plugin/v1/me")[0]["headers"])
        self.assertNotIn("Idempotency-Key", self.fake.calls("PUT", "/api/plugin/v1/kv/k")[0]["headers"])

    def test_bearer_token_and_with_token(self):
        self.host.with_token("acting").me()
        self.host.me()
        auth = [c["headers"]["Authorization"] for c in self.fake.calls("GET", "/api/plugin/v1/me")]
        self.assertEqual(auth, ["Bearer acting", f"Bearer {support.TOKEN}"])

    def test_upload_streams_seekable_file_and_retries_from_start(self):
        data = bytes(range(256)) * 1024
        f = io.BytesIO(b"skip" + data)
        f.seek(4)
        self.fake.faults["POST /api/plugin/v1/files"] = [(503, {}, b"")]
        resp = self.host.upload_file("space1", f, 'east "wall".jpg', parent_id="folder1", mime="image/jpeg",
                                     on_conflict="rename")
        self.assertEqual(resp["id"], "file1")
        calls = self.fake.calls("POST", "/api/plugin/v1/files")
        self.assertEqual(len(calls), 2)
        self.assertIn("Content-Length", calls[1]["headers"])
        self.assertEqual(calls[0]["headers"]["Idempotency-Key"], calls[1]["headers"]["Idempotency-Key"])
        up = self.fake.uploads[0]
        self.assertEqual(up["spaceId"], "space1")
        self.assertEqual(up["parentId"], "folder1")
        self.assertEqual(up["onConflict"], "rename")
        self.assertEqual(up["file"]["content"], data)
        self.assertEqual(up["file"]["type"], "image/jpeg")
        self.assertEqual(up["file"]["filename"], "east %22wall%22.jpg")

    def test_upload_non_seekable_is_chunked_and_sent_once(self):
        data = b"x" * 100_000
        self.host.upload_file("space1", NonSeekable(data), "a.txt")
        call = self.fake.calls("POST", "/api/plugin/v1/files")[0]
        self.assertEqual(call["headers"].get("Transfer-Encoding"), "chunked")
        self.assertEqual(self.fake.uploads[0]["file"]["content"], data)

        self.fake.faults["POST /api/plugin/v1/files"] = [(503, {}, b"")]
        with self.assertRaises(HostError):
            self.host.upload_file("space1", NonSeekable(data), "b.txt")
        self.assertEqual(self.sleeps, [])

    def test_ai_stream_yields_events(self):
        self.fake.sse = (b'event: delta\ndata: {"delta":"Hel"}\n\n: ping\n\n'
                         b'event: delta\ndata: {"delta":"lo"}\n\nevent: done\ndata: {"done":true}\n\n')
        events = list(self.host.ai_complete_stream({"task": "ask", "question": "hi"}))
        self.assertEqual([(e.event, e.data) for e in events],
                         [("delta", {"delta": "Hel"}), ("delta", {"delta": "lo"}), ("done", {"done": True})])
        call = self.fake.calls("POST", "/api/plugin/v1/ai/complete")[0]
        self.assertEqual(call["headers"]["Accept"], "text/event-stream")

    def test_kv_round_trip_encodes_keys_and_wraps_values(self):
        key = "url:https://example.com/a?b=c"
        self.assertIsNone(self.host.kv_get(key))
        self.assertIsNone(self.host.kv_entry(key))
        self.assertEqual(self.host.kv_get(key, default=0), 0)
        self.host.kv_put(key, {"noteId": "n1"})
        self.assertEqual(self.host.kv_get(key), {"noteId": "n1"})
        self.assertEqual(self.host.kv_entry(key), KVEntry(key, {"noteId": "n1"}, support.UPDATED))
        put = self.fake.calls("PUT", "/api/plugin/v1/kv/")[0]
        self.assertEqual(put["path"], "/api/plugin/v1/kv/url%3Ahttps%3A%2F%2Fexample.com%2Fa%3Fb%3Dc")
        self.assertEqual(put["json"], {"value": {"noteId": "n1"}})
        self.host.kv_put(key, None)
        self.assertEqual(self.host.kv_entry(key).value, None)

    def test_kv_list_pages_with_cursor(self):
        for k in ("a1", "a2", "a3", "a4", "a5", "b1"):
            self.fake.kv[k] = 1
        page = self.host.kv_list("a")
        self.assertEqual([i["key"] for i in page.items], ["a1", "a2"])
        self.assertEqual(page.next_cursor, "2")
        self.assertEqual(self.host.kv_list("a", cursor="4").next_cursor, None)
        self.assertEqual([i["key"] for i in self.host.kv_keys("a")], ["a1", "a2", "a3", "a4", "a5"])
        self.assertEqual(set(self.host.kv_list("a").items[0]), {"key", "size", "updatedAt"})

    def test_lists_return_pages(self):
        self.fake.trees["s1"] = [{"kind": "note", "note": {"id": "n1"}}]
        self.assertEqual(self.host.tree("s1"), Page([{"kind": "note", "note": {"id": "n1"}}], None))
        self.fake.faults["GET /api/plugin/v1/items/n1/backlinks"] = [
            (200, {"Content-Type": "application/json"}, b'{"items":[{"id":"n2"}],"nextCursor":"c2"}')]
        self.assertEqual(self.host.backlinks("n1"), Page([{"id": "n2"}], "c2"))
        self.fake.faults["GET /api/plugin/v1/search"] = [(200, {"Content-Type": "application/json"}, b'[{"id":"x"}]')]
        with self.assertRaises(HostError):
            self.host.search("x", cursor="c1")
        self.assertIn("cursor=c1", self.fake.calls("GET", "/api/plugin/v1/search")[0]["query"])

    def test_secret_file_text_and_models(self):
        self.fake.secrets["karakeep_token"] = "s3cret"
        self.assertEqual(self.host.secret("karakeep_token"), "s3cret")
        self.fake.file_texts["f1"] = "hello"
        self.assertEqual(self.host.file_text("f1"), FileText("hello", False))
        self.fake.models = [{"providerId": "p1", "model": "m1", "vision": True, "imageGeneration": False,
                             "imageEditing": True, "prices": {"input": 0.5}}]
        (model,) = self.host.ai_models()
        self.assertEqual((model.provider_id, model.model, model.vision, model.image_generation, model.image_editing,
                          model.prices), ("p1", "m1", True, False, True, {"input": 0.5}))

    def test_invalid_ids_and_keys_fail_before_sending(self):
        with self.assertRaises(ValueError):
            self.host.get_item("../etc")
        with self.assertRaises(ValueError):
            self.host.kv_put("", 1)
        with self.assertRaises(ValueError):
            self.host.secret("Bad Name")
        self.assertEqual(self.fake.calls(), [])

    def test_emit_prefixes_plugin_event_type(self):
        self.fake.faults["POST /api/plugin/v1/events"] = [(204, {}, b""), (204, {}, b"")]
        self.host.emit("counted", {"words": 3}, space_id="space1")
        self.host.emit("plugin.word-count.other")
        sent = [c["json"] for c in self.fake.calls("POST", "/api/plugin/v1/events")]
        self.assertEqual(sent[0], {"type": "plugin.word-count.counted", "data": {"words": 3}, "spaceId": "space1"})
        self.assertEqual(sent[1]["type"], "plugin.word-count.other")

    def test_update_job_bodies_and_job_reply(self):
        self.fake.job_status["j1"] = "canceled"
        self.assertEqual(self.host.update_job("j1", progress=3, total=4, message="m")["status"], "canceled")
        self.host.update_job("j1", status="done", result=None)
        bodies = [c["json"] for c in self.fake.calls("PATCH", "/api/plugin/v1/jobs/j1")]
        self.assertEqual(bodies, [{"progress": 3, "total": 4, "message": "m"}, {"status": "done", "result": None}])

    def test_unconfigured_host_raises(self):
        with self.assertRaises(HostError) as cm:
            Host(None, "t").me()
        self.assertEqual(cm.exception.status, 0)


if __name__ == "__main__":
    unittest.main()
