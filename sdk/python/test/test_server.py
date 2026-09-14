"""Request verification and dispatch in Plugin.handle and over HTTP."""

from __future__ import annotations

import json
import threading
import time
import unittest
import urllib.error
import urllib.request

import support

from markspace import Plugin, toast, ui
from markspace.signature import sign, sign_headers

MANIFEST = {
    "protocol": 1, "id": "srv-test", "name": "Server", "version": "2.0.0",
    "events": ["note.*", "file.created"],
    "commands": [{"id": "good"}, {"id": "bad"}, {"id": "boom"}],
    "panels": [{"id": "p", "where": "rail"}, {"id": "broken", "where": "page"}],
    "importers": [{"id": "txt"}],
}


class ServerTests(unittest.TestCase):
    def setUp(self):
        self.plugin = Plugin(MANIFEST, data_dir=support.tempdir(), env={})
        self.plugin.store.save(support.install_payload("srv-test", "http://127.0.0.1:9"))
        self.plugin.credentials = self.plugin.store.credentials()
        self.seen: dict[str, list] = {}
        p = self.plugin

        def recorder(name):
            def fn(batch):
                self.seen.setdefault(name, []).extend(e.type for e in batch.events)
            return fn

        p.on("note.*")(recorder("note.*"))
        p.on("*")(recorder("*"))
        p.on("file.created")(recorder("file.created"))

        @p.command("good")
        def good(ctx):
            return toast(f"{ctx.user.name} in {ctx.space_id}/{ctx.item_id} via {ctx.host.token}")

        @p.command("bad")
        def bad(ctx):
            return {"toast": {"message": ""}}

        @p.command("boom")
        def boom(ctx):
            raise RuntimeError("kaput")

        @p.panel("p")
        def panel(ctx):
            return ui.text("hello")

        @p.panel("broken")
        def broken(ctx):
            return {"ui": {"type": "iframe"}}

        @p.action("save")
        def save(ctx):
            return toast(f"{ctx.values['name']} {ctx.payload['x']}")

        @p.importer("txt")
        def importer(ctx):
            return {"job": {"id": f"imp-{ctx.file_id}"}}

    def signed(self, path, payload, now=None, delivery="d-1", plugin_id="srv-test", secret=support.SECRET):
        body = json.dumps(payload).encode()
        headers = sign_headers(secret, plugin_id, delivery, "POST", path, body, now or time.time())
        status, _, raw = self.plugin.handle("POST", path, headers, body)
        return status, (json.loads(raw) if raw else None)

    def interaction(self, **extra):
        return {"context": {"spaceId": "s1", "itemId": "n1", "kind": "note"}, "actingToken": "act",
                "user": {"id": "u1", "name": "Ann"}, **extra}

    def test_manifest_and_health_need_no_signature(self):
        status, _, raw = self.plugin.handle("GET", "/manifest.json", {}, b"")
        self.assertEqual((status, json.loads(raw)["id"]), (200, "srv-test"))
        status, _, raw = self.plugin.handle("GET", "/health", {}, b"")
        self.assertEqual((status, json.loads(raw)), (200, {"status": "ok", "version": "2.0.0"}))

    def test_signature_failures_are_401(self):
        payload = {"events": []}
        body = json.dumps(payload).encode()
        now = time.time()
        cases = {
            "malformed": {"X-Markspace-Signature": "t=1", "X-Markspace-Delivery": "a"},
            "mismatch": sign_headers(bytes(32), "srv-test", "b", "POST", "/events", body, now),
            "expired": sign_headers(support.SECRET, "srv-test", "c", "POST", "/events", body, now - 301),
            "missing_delivery": {"X-Markspace-Signature": sign(support.SECRET, int(now), "POST", "/events", "", body)},
            "wrong_plugin": sign_headers(support.SECRET, "other", "e", "POST", "/events", body, now),
        }
        for code, headers in cases.items():
            with self.subTest(code):
                status, _, raw = self.plugin.handle("POST", "/events", headers, body)
                self.assertEqual((status, json.loads(raw)["code"]), (401, code))
        self.assertEqual(self.seen, {})

    def test_replay_is_401(self):
        now = time.time()
        self.assertEqual(self.signed("/events", {"events": []}, now=now)[0], 204)
        status, resp = self.signed("/events", {"events": []}, now=now)
        self.assertEqual((status, resp["code"]), (401, "replay"))
        self.assertEqual(self.signed("/events", {"events": []}, now=now, delivery="d-2")[0], 204)
        self.assertEqual(self.signed("/events", {"events": []}, now=now + 1)[0], 204)

    def test_signature_covers_method_path_and_delivery(self):
        body = json.dumps(self.interaction()).encode()
        now = time.time()
        headers = sign_headers(support.SECRET, "srv-test", "p1", "POST", "/commands/good", body, now)
        status, _, raw = self.plugin.handle("POST", "/commands/boom", headers, body)
        self.assertEqual((status, json.loads(raw)["code"]), (401, "mismatch"))
        moved = dict(headers, **{"X-Markspace-Delivery": "p2"})
        status, _, raw = self.plugin.handle("POST", "/commands/good", moved, body)
        self.assertEqual((status, json.loads(raw)["code"]), (401, "mismatch"))
        put = sign_headers(support.SECRET, "srv-test", "p3", "PUT", "/commands/good", body, now)
        status, _, raw = self.plugin.handle("POST", "/commands/good", put, body)
        self.assertEqual((status, json.loads(raw)["code"]), (401, "mismatch"))
        with_query = sign_headers(support.SECRET, "srv-test", "p4", "POST", "/commands/good?x=1", body, now)
        self.assertEqual(self.plugin.handle("POST", "/commands/good?y=2", with_query, body)[0], 200)

    def test_headers_are_case_insensitive(self):
        body = b'{"events":[]}'
        headers = {k.lower(): v for k, v in sign_headers(support.SECRET, "srv-test", "x", "POST", "/events", body, time.time()).items()}
        self.assertEqual(self.plugin.handle("POST", "/events", headers, body)[0], 204)

    def test_events_dispatch_by_pattern_once_per_batch(self):
        events = [{"id": "1", "type": "note.updated", "itemId": "n1"}, {"id": "2", "type": "file.created"},
                  {"id": "3", "type": "space.created"}, {"id": "4", "type": "note.created"}]
        status, _ = self.signed("/events", {"events": events})
        self.assertEqual(status, 204)
        self.assertEqual(self.seen, {
            "note.*": ["note.updated", "note.created"],
            "*": ["note.updated", "file.created", "space.created", "note.created"],
            "file.created": ["file.created"],
        })

    def test_event_handler_failure_is_500(self):
        @self.plugin.on("space.*")
        def fail(batch):
            raise ValueError("nope")

        status, resp = self.signed("/events", {"events": [{"id": "1", "type": "space.updated"}]})
        self.assertEqual(status, 500)
        self.assertIn("nope", resp["error"])

    def test_command_gets_context_user_and_acting_token(self):
        status, resp = self.signed("/commands/good", self.interaction())
        self.assertEqual((status, resp), (200, {"toast": {"message": "Ann in s1/n1 via act", "kind": "info"}}))

    def test_command_needs_acting_token(self):
        payload = self.interaction()
        del payload["actingToken"]
        self.assertEqual(self.signed("/commands/good", payload)[0], 400)

    def test_invalid_command_response_is_500(self):
        status, resp = self.signed("/commands/bad", self.interaction())
        self.assertEqual((status, resp["code"], resp["field"]), (500, "invalid_field", "$.toast.message"))

    def test_command_exception_is_500(self):
        status, resp = self.signed("/commands/boom", self.interaction())
        self.assertEqual(status, 500)
        self.assertIn("kaput", resp["error"])

    def test_panel_wraps_tree(self):
        self.assertEqual(self.signed("/panels/p", self.interaction()),
                         (200, {"ui": {"type": "text", "value": "hello"}}))
        status, resp = self.signed("/panels/broken", self.interaction(actingToken="act-2"), delivery="d-3")
        self.assertEqual((status, resp["code"]), (500, "unknown_type"))

    def test_action_gets_values_and_payload(self):
        status, resp = self.signed("/actions/save", self.interaction(values={"name": "x"}, payload={"x": 1}))
        self.assertEqual((status, resp["toast"]["message"]), (200, "x 1"))

    def test_import_route(self):
        payload = {"fileId": "f9", "spaceId": "s1", "actingToken": "act", "user": {"id": "u1"}}
        self.assertEqual(self.signed("/import/txt", payload), (200, {"job": {"id": "imp-f9"}}))

    def test_unknown_routes_and_methods(self):
        self.assertEqual(self.signed("/commands/missing", self.interaction())[0], 404)
        self.assertEqual(self.plugin.handle("POST", "/nope", {}, b"{}")[0], 404)
        self.assertEqual(self.plugin.handle("GET", "/events", {}, b"")[0], 405)
        self.assertEqual(self.plugin.handle("POST", "/commands/a/b", {}, b"{}")[0], 404)

    def test_body_must_be_json_object(self):
        body = b"[1]"
        headers = sign_headers(support.SECRET, "srv-test", "z", "POST", "/events", body, time.time())
        self.assertEqual(self.plugin.handle("POST", "/events", headers, body)[0], 400)

    def test_registration_checks(self):
        with self.assertRaises(ValueError):
            self.plugin.command("undeclared")
        with self.assertRaises(ValueError):
            self.plugin.job("Bad Kind")
        with self.assertRaises(ValueError):
            self.plugin.command("good")(lambda ctx: None)
        with self.assertRaises(ValueError):
            self.plugin.on("Note.updated")
        with self.assertRaises(ValueError):
            Plugin({"protocol": 2, "id": "x", "name": "x", "version": "1"})
        with self.assertRaises(ValueError):
            Plugin({"protocol": 1, "id": "x", "name": "x", "version": "1", "panels": [{"id": "p", "where": "top"}]})

    def test_over_http(self):
        server = self.plugin.make_server("127.0.0.1", 0)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        base = f"http://127.0.0.1:{server.server_address[1]}"
        with urllib.request.urlopen(base + "/health", timeout=5) as resp:
            self.assertEqual(json.load(resp)["status"], "ok")
        body = json.dumps(self.interaction()).encode()
        req = urllib.request.Request(base + "/commands/good", data=body, method="POST",
                                     headers=sign_headers(support.SECRET, "srv-test", "h1", "POST", "/commands/good", body,
                                                          time.time()))
        with urllib.request.urlopen(req, timeout=5) as resp:
            self.assertEqual(json.load(resp)["toast"]["message"], "Ann in s1/n1 via act")
        with self.assertRaises(urllib.error.HTTPError) as cm:
            urllib.request.urlopen(urllib.request.Request(base + "/commands/good", data=body, method="POST"), timeout=5)
        self.assertEqual(cm.exception.code, 401)
        cm.exception.close()


if __name__ == "__main__":
    unittest.main()
