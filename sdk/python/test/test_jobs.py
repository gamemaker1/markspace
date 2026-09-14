"""Job calls: the 200 path, the 202 path and completion reports."""

from __future__ import annotations

import json
import threading
import time
import unittest
import uuid

import support

from markspace import JobCanceled, Plugin
from markspace.signature import sign_headers

MANIFEST = {
    "protocol": 1, "id": "jobs-test", "name": "Jobs", "version": "1.0.0",
    "jobs": [{"kind": k} for k in ("quick", "slow", "fails", "fails-late", "cancels", "late-cancel")],
}


def wait_for(predicate, timeout=5.0):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(0.01)
    raise AssertionError("condition not met in time")


class JobTests(unittest.TestCase):
    def setUp(self):
        self.fake = support.FakeHost().start()
        self.addCleanup(self.fake.stop)
        self.gate = threading.Event()
        self.addCleanup(self.gate.set)
        self.started = threading.Event()
        self.plugin = Plugin(MANIFEST, data_dir=support.tempdir(), env={}, job_wait=0.3, heartbeat_every=0.05,
                             host_options={"sleep": lambda s: None})
        self.plugin.store.save(support.install_payload("jobs-test", self.fake.url))
        self.plugin.credentials = self.plugin.store.credentials()
        p = self.plugin

        @p.job("quick")
        def quick(ctx):
            return {"n": ctx.payload["n"] * 2, "attempt": ctx.attempt, "title": ctx.job.title,
                    "spaceId": ctx.job.space_id, "user": ctx.user.name if ctx.user else None}

        @p.job("slow")
        def slow(ctx):
            self.started.set()
            self.gate.wait(5)
            ctx.log("info", "almost done")
            return {"ok": True}

        @p.job("fails")
        def fails(ctx):
            raise RuntimeError("disk on fire")

        @p.job("fails-late")
        def fails_late(ctx):
            self.gate.wait(5)
            raise RuntimeError("late failure")

        @p.job("cancels")
        def cancels(ctx):
            ctx.progress(1, 10, "first")
            ctx.check_canceled()
            return {"unreachable": True}

        @p.job("late-cancel")
        def late_cancel(ctx):
            self.gate.wait(5)
            ctx.progress(2, 10)
            ctx.check_canceled()
            return {}

    def call(self, kind, job_id, payload=None, token="jobtoken"):
        call = {"job": {"id": job_id, "kind": kind, "payload": payload or {}, "attempt": 2, "title": "Count",
                        "spaceId": "s1"}}
        if token:
            call["actingToken"] = token
            call["user"] = {"id": "u1", "name": "Ann"}
        body = json.dumps(call).encode()
        headers = sign_headers(support.SECRET, "jobs-test", uuid.uuid4().hex, "POST", f"/jobs/{kind}", body, time.time())
        status, _, raw = self.plugin.handle("POST", f"/jobs/{kind}", headers, body)
        return status, (json.loads(raw) if raw else None)

    def patches(self, job_id):
        return [c for c in self.fake.calls("PATCH", f"/api/plugin/v1/jobs/{job_id}")]

    def test_quick_job_answers_200_with_result(self):
        status, resp = self.call("quick", "j1", {"n": 21})
        self.assertEqual((status, resp), (200, {"result": {"n": 42, "attempt": 2, "title": "Count", "spaceId": "s1", "user": "Ann"}}))
        time.sleep(0.1)
        self.assertEqual(self.patches("j1"), [])

    def test_slow_job_answers_202_then_patches_done(self):
        status, resp = self.call("slow", "j2")
        self.assertEqual((status, resp), (202, {"status": "running"}))
        wait_for(lambda: self.patches("j2"))
        heartbeat = self.patches("j2")[0]
        self.assertEqual(heartbeat["json"], {"progress": 0, "total": 0})
        self.gate.set()
        done = wait_for(lambda: [c for c in self.patches("j2") if (c["json"] or {}).get("status")])
        self.assertEqual(done[0]["json"], {"status": "done", "result": {"ok": True}})
        self.assertEqual(done[0]["headers"]["Authorization"], "Bearer jobtoken")
        logs = self.fake.calls("POST", "/api/plugin/v1/jobs/j2/log")
        self.assertEqual(logs[0]["json"], {"level": "info", "message": "almost done"})
        wait_for(lambda: "j2" not in self.plugin._running)

    def test_background_failure_patches_failed(self):
        self.assertEqual(self.call("fails-late", "j3")[0], 202)
        self.gate.set()
        failed = wait_for(lambda: [c for c in self.patches("j3") if (c["json"] or {}).get("status")])
        self.assertEqual(failed[0]["json"]["status"], "failed")
        self.assertIn("late failure", failed[0]["json"]["message"])

    def test_synchronous_failure_answers_500(self):
        status, resp = self.call("fails", "j4")
        self.assertEqual(status, 500)
        self.assertIn("disk on fire", resp["error"])

    def test_cancel_seen_in_progress_response(self):
        self.fake.job_status["j5"] = "canceled"
        status, resp = self.call("cancels", "j5")
        self.assertEqual(status, 409, resp)
        self.assertEqual(self.patches("j5")[0]["json"], {"progress": 1, "total": 10, "message": "first"})

    def test_background_cancel_sends_no_report(self):
        self.fake.job_status["j6"] = "canceled"
        self.assertEqual(self.call("late-cancel", "j6")[0], 202)
        self.gate.set()
        wait_for(lambda: "j6" not in self.plugin._running)
        self.assertFalse([c for c in self.patches("j6") if (c["json"] or {}).get("status")])

    def test_repeated_call_for_a_running_job_answers_202(self):
        first = {}
        t = threading.Thread(target=lambda: first.setdefault("r", self.call("slow", "j7")))
        t.start()
        self.assertTrue(self.started.wait(2))
        started = time.monotonic()
        self.assertEqual(self.call("slow", "j7"), (202, {"status": "running"}))
        self.assertLess(time.monotonic() - started, 0.25)
        self.gate.set()
        t.join(5)
        self.assertIn(first["r"][0], (200, 202))

    def test_invalid_job_id_is_400(self):
        self.assertEqual(self.call("quick", "../j")[0], 400)

    def test_unknown_kind_is_404(self):
        self.assertEqual(self.call("nope", "j8")[0], 404)

    def test_job_without_acting_token_uses_plugin_token(self):
        self.assertEqual(self.call("slow", "j9", token="")[0], 202)
        wait_for(lambda: self.patches("j9"))
        self.assertEqual(self.patches("j9")[0]["headers"]["Authorization"], f"Bearer {support.TOKEN}")
        self.gate.set()
        wait_for(lambda: "j9" not in self.plugin._running)

    def test_raising_job_canceled_is_exported(self):
        self.assertTrue(issubclass(JobCanceled, Exception))


if __name__ == "__main__":
    unittest.main()
