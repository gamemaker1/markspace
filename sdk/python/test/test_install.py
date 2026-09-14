"""Install handshake, persistence and credential precedence."""

from __future__ import annotations

import base64
import json
import os
import stat
import unittest

import support

from markspace import Plugin
from markspace.signature import sign_headers

MANIFEST = {"protocol": 1, "id": "word-count", "name": "Word count", "version": "0.1.0"}
T0 = 1789300000.0
SECRET2 = bytes([7] * 32)
SECRET2_B64 = base64.b64encode(SECRET2).decode()


class InstallTests(unittest.TestCase):
    def setUp(self):
        self.now = T0
        self.dir = support.tempdir()

    def make(self, env=None, **kw):
        return Plugin(MANIFEST, data_dir=self.dir, env=env or {}, clock=lambda: self.now, **kw)

    def post(self, plugin, payload, headers=None):
        body = json.dumps(payload).encode()
        status, _, raw = plugin.handle("POST", "/install", headers or {}, body)
        return status, (json.loads(raw) if raw else None), body

    def test_unsigned_first_install_is_persisted_with_mode_0600(self):
        p = self.make()
        payload = support.install_payload("word-count", "http://markspace:8080")
        status, resp, _ = self.post(p, payload)
        self.assertEqual(status, 200, resp)
        self.assertEqual(resp, {"ok": True})
        path = os.path.join(self.dir, "install.json")
        self.assertEqual(stat.S_IMODE(os.stat(path).st_mode), 0o600)
        with open(path) as f:
            self.assertEqual(json.load(f), payload)
        self.assertEqual(os.listdir(self.dir), ["install.json"])
        self.assertTrue(p.credentials.installed)
        self.assertEqual(p.credentials.signing_secret, support.SECRET)

    def test_install_survives_restart(self):
        self.post(self.make(), support.install_payload("word-count", "http://markspace:8080"))
        again = self.make()
        self.assertTrue(again.credentials.installed)
        self.assertEqual(again.credentials.token, support.TOKEN)

    def test_window_closes(self):
        p = self.make(install_window=60)
        self.now += 61
        status, resp, _ = self.post(p, support.install_payload("word-count", "http://markspace:8080"))
        self.assertEqual(status, 403, resp)
        self.assertFalse(os.path.exists(os.path.join(self.dir, "install.json")))

    def test_window_from_environment(self):
        p = self.make(env={"PLUGIN_INSTALL_WINDOW": "30"})
        self.assertEqual(p.install_window, 30)
        self.now += 31
        status, resp, _ = self.post(p, support.install_payload("word-count", "http://markspace:8080"))
        self.assertEqual(status, 403, resp)

    def test_zero_window_never_closes(self):
        p = self.make(env={"PLUGIN_INSTALL_WINDOW": "0"})
        self.now += 30 * 24 * 3600
        status, resp, _ = self.post(p, support.install_payload("word-count", "http://markspace:8080"))
        self.assertEqual(status, 200, resp)

    def test_argument_overrides_environment_window(self):
        p = self.make(env={"PLUGIN_INSTALL_WINDOW": "0"}, install_window=60)
        self.now += 61
        status, resp, _ = self.post(p, support.install_payload("word-count", "http://markspace:8080"))
        self.assertEqual(status, 403, resp)

    def test_invalid_window_fails_at_startup(self):
        for raw in ("-1", "ten", "inf", "nan"):
            with self.assertRaises(ValueError, msg=raw):
                self.make(env={"PLUGIN_INSTALL_WINDOW": raw})

    def test_env_token_disables_unsigned_install(self):
        p = self.make(env={"MARKSPACE_TOKEN": "mspl_env"})
        status, resp, _ = self.post(p, support.install_payload("word-count", "http://markspace:8080"))
        self.assertEqual(status, 403, resp)

    def test_reinstall_needs_current_secret(self):
        p = self.make()
        self.post(p, support.install_payload("word-count", "http://markspace:8080"))
        second = support.install_payload("word-count", "http://markspace:8080", SECRET2_B64, "mspl_second")
        status, resp, _ = self.post(p, second)
        self.assertEqual(status, 401, resp)
        self.assertEqual(resp["code"], "malformed")

        body = json.dumps(second).encode()
        wrong = sign_headers(SECRET2, "word-count", "d1", "POST", "/install", body, self.now)
        status, resp, _ = self.post(p, second, wrong)
        self.assertEqual((status, resp["code"]), (401, "mismatch"))

        good = sign_headers(support.SECRET, "word-count", "d2", "POST", "/install", body, self.now)
        status, resp, _ = self.post(p, second, good)
        self.assertEqual(status, 200, resp)
        self.assertEqual(p.credentials.token, "mspl_second")
        self.assertEqual(p.credentials.signing_secret, SECRET2)

        status, resp, _ = self.post(p, second, good)
        self.assertEqual((status, resp["code"]), (401, "mismatch"), "the old secret stops working")

        rotated = sign_headers(SECRET2, "word-count", "d4", "POST", "/install", body, self.now)
        self.assertEqual(self.post(p, second, rotated)[0], 200)
        status, resp, _ = self.post(p, second, rotated)
        self.assertEqual((status, resp["code"]), (401, "replay"))

    def test_reinstall_is_allowed_after_the_window(self):
        p = self.make(install_window=1)
        self.post(p, support.install_payload("word-count", "http://markspace:8080"))
        self.now += 3600
        payload = support.install_payload("word-count", "http://markspace:9090")
        body = json.dumps(payload).encode()
        status, resp, _ = self.post(p, payload, sign_headers(support.SECRET, "word-count", "d3", "POST", "/install", body, self.now))
        self.assertEqual(status, 200, resp)
        self.assertEqual(p.credentials.markspace_url, "http://markspace:9090")

    def test_invalid_payload_is_rejected_and_not_stored(self):
        p = self.make()
        status, resp, _ = self.post(p, support.install_payload("bookmark-wiki", "http://markspace:8080"))
        self.assertEqual(status, 400)
        self.assertEqual((resp["code"], resp["field"]), ("wrong_plugin", "$.pluginId"))
        self.assertFalse(os.path.exists(os.path.join(self.dir, "install.json")))

    def test_environment_overrides_each_value(self):
        self.post(self.make(), support.install_payload("word-count", "http://markspace:8080"))
        env = {"MARKSPACE_URL": "http://other:8081", "MARKSPACE_SIGNING_SECRET": SECRET2_B64}
        p = self.make(env=env)
        c = p.credentials
        self.assertEqual(c.markspace_url, "http://other:8081")
        self.assertEqual(c.token, support.TOKEN)
        self.assertEqual(c.signing_secret, SECRET2)
        self.assertEqual(set(c.from_env), {"MARKSPACE_URL", "MARKSPACE_SIGNING_SECRET"})

    def test_environment_alone_installs(self):
        env = {"MARKSPACE_URL": "http://m:8080", "MARKSPACE_TOKEN": "mspl_env", "MARKSPACE_SIGNING_SECRET": SECRET2_B64}
        self.assertTrue(self.make(env=env).credentials.installed)

    def test_bad_environment_secret_fails_at_startup(self):
        with self.assertRaises(ValueError):
            self.make(env={"MARKSPACE_SIGNING_SECRET": "short"})

    def test_plugin_data_env_sets_the_directory(self):
        p = Plugin(MANIFEST, env={"PLUGIN_DATA": self.dir})
        self.assertEqual(p.data_dir, self.dir)

    def test_signed_routes_need_an_install(self):
        status, _, _ = self.make().handle("POST", "/events", {}, b'{"events":[]}')
        self.assertEqual(status, 503)

    def test_install_rejects_get(self):
        status, _, _ = self.make().handle("GET", "/install", {}, b"")
        self.assertEqual(status, 405)


if __name__ == "__main__":
    unittest.main()
