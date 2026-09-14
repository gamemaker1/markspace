"""Import dedupe and fetch state transitions."""

import unittest

import support
from markspace import HostError
from wiki import frontmatter
from wiki.ratelimit import HostGate
from wiki.sources import captured_text
from wiki.urls import normalise_url
from wiki.util import iso
from fakehost import SPACE
from support import Harness


class FetchStates(unittest.TestCase):
    def setUp(self):
        self.h = Harness()

    def add(self, url, title=""):
        self.h.run("import", {"importer": "urls", "bookmarks": [{"url": url, "title": title}]})
        return self.h.svc.state.source(self.h.svc.state.url_note(normalise_url(url)))

    def rec(self, rec):
        return self.h.svc.state.source(rec["id"])

    def fm(self, rec):
        return frontmatter.read(self.h.note(rec["id"]))

    def test_html_page_is_captured_once(self):
        rec = self.add("https://www.sqlite.org/wal.html", "Write-Ahead Logging")
        self.assertEqual(len(self.h.queued("fetch")), 1)
        self.h.run_jobs(only=("fetch",))
        rec = self.rec(rec)
        self.assertEqual((rec["status"], rec["wikiState"]), ("ok", "fetched"))
        note = self.h.note(rec["id"])
        self.assertTrue(note["path"].startswith("Sources/sqlite.org/"))
        self.assertIn("A checkpoint moves the transactions", captured_text(frontmatter.body_of(note["content"])))
        self.assertNotIn("window.analytics", note["content"])
        self.assertEqual(self.fm(rec)["status"], "ok")
        self.assertEqual(self.fm(rec)["sha256"], rec["sha256"])
        self.assertEqual(len(self.h.queued("summarise")), 1)
        body = frontmatter.body_of(note["content"])
        self.assertEqual(self.h.run("fetch", {"sourceId": rec["id"], "refetch": True}), {"status": "ok", "drift": False})
        self.assertEqual(frontmatter.body_of(self.h.note(rec["id"])["content"]), body)

    def test_404_and_410_are_dead(self):
        dead = self.add("https://old-blog.example.net/posts/gone")
        gone = self.add("https://gone.example.com/x")
        self.h.fake.web.script("https://gone.example.com/x", {"status": 410})
        self.h.run_jobs(only=("fetch",))
        self.assertEqual(self.rec(dead)["status"], "dead")
        self.assertEqual(self.rec(gone)["status"], "dead")
        self.assertEqual(self.fm(gone)["status"], "dead")
        self.assertEqual(self.h.queued("summarise"), [])

    def test_403_three_times_is_blocked(self):
        rec = self.add("https://paywalled.example.com/article/why-crdts")
        results = []
        for _ in range(3):
            job = self.h.queued("fetch")[0]
            results.append(self.h.run_job(job))
            if len(results) < 3:
                self.assertTrue(self.h.queued("fetch")[0]["runAfter"], "a retry waits before it runs")
        self.assertEqual(results[0], {"retry": 1, "status": 403})
        self.assertEqual(results[1], {"retry": 2, "status": 403})
        self.assertEqual(results[2]["status"], "blocked")
        self.assertEqual(self.rec(rec)["status"], "blocked")
        self.assertEqual(self.h.queued("fetch"), [])

    def test_429_then_ok(self):
        url = "https://raft.github.io/"
        rec = self.add(url)
        self.h.fake.web.script(url, {"status": 429})
        self.h.run_jobs(only=("fetch",))
        self.assertEqual(self.rec(rec)["status"], "ok")
        self.assertEqual(self.rec(rec)["tries"], 0)

    def test_robots_blocks_without_retries(self):
        rec = self.add("https://norobots.example.org/private/notes")
        self.h.run_jobs(only=("fetch",))
        self.assertEqual(self.rec(rec)["status"], "blocked")
        self.assertIn("robots", self.rec(rec)["error"])
        self.assertEqual(self.h.fake.web.calls[-1][1], True)

    def test_robots_setting_off(self):
        self.h.settings["respectRobots"] = False
        rec = self.add("https://norobots.example.org/private/notes")
        self.h.run_jobs(only=("fetch",))
        self.assertEqual(self.h.fake.web.calls[-1][1], False)
        self.assertEqual(self.rec(rec)["status"], "ok")

    def test_pdf_is_uploaded_next_to_the_note_and_read_from_file_text(self):
        rec = self.add("https://raft.github.io/raft.pdf", "In Search of an Understandable Consensus Algorithm")
        first = self.h.run_job(self.h.queued("fetch")[0])
        self.assertEqual(first, {"waiting": "file text", "waits": 1})
        rec = self.rec(rec)
        self.assertEqual(rec["status"], "pending")
        self.assertTrue(self.h.queued("fetch")[0]["runAfter"])
        self.h.run_jobs(only=("fetch",))
        rec = self.rec(rec)
        self.assertEqual((rec["status"], rec["wikiState"]), ("ok", "fetched"))
        files = [e["obj"] for e in self.h.fake.items.values() if e["kind"] == "file"]
        self.assertEqual(len(files), 1)
        note = self.h.note(rec["id"])
        self.assertEqual(files[0]["parentId"], note["parentId"])
        self.assertEqual(files[0]["mime"], "application/pdf")
        text = captured_text(frontmatter.body_of(note["content"]))
        self.assertIn("Raft is a consensus algorithm for managing a replicated log.", text)
        self.assertIn(f"[[{files[0]['name']}]]", text)
        self.assertEqual(self.h.web_fetches if hasattr(self.h, "web_fetches") else 1,
                         len([c for c in self.h.fake.web.calls if c[0].endswith("raft.pdf")]))

    def test_drift_is_recorded_without_changing_the_note(self):
        url = "https://www.sqlite.org/wal.html"
        rec = self.add(url)
        self.h.run_jobs(only=("fetch",))
        body = frontmatter.body_of(self.h.note(rec["id"])["content"])
        changed = {"markdown": "SQLite now documents WAL2 mode.\n", "title": "Write-Ahead Logging"}
        self.h.fake.web.script(url, changed, changed)
        self.assertEqual(self.h.run("fetch", {"sourceId": rec["id"], "refetch": True}), {"status": "ok", "drift": True})
        self.assertEqual(self.h.run("fetch", {"sourceId": rec["id"], "refetch": True})["drift"], True)
        rec = self.rec(rec)
        self.assertEqual(len(rec["drift"]), 1)
        self.assertEqual(frontmatter.body_of(self.h.note(rec["id"])["content"]), body)
        drift_note = self.h.note(rec["drift"][0]["noteId"])
        self.assertTrue(drift_note["path"].startswith("Wiki/Lint/Drift/"))
        self.assertIn("WAL2 mode", drift_note["content"])

    def test_server_errors_retry_then_fail(self):
        url = "https://raft.github.io/"
        rec = self.add(url)
        self.h.fake.web.script(url, *[{"status": 503}] * 4)
        self.h.run_jobs(only=("fetch",))
        self.assertEqual(len(self.h.errors), 3)
        rec = self.rec(rec)
        self.assertEqual((rec["status"], rec["wikiState"]), ("pending", "failed"))

    def test_title_comes_from_the_page_when_the_bookmark_has_none(self):
        rec = self.add("https://www.sqlite.org/whentouse.html")
        self.assertEqual(rec["title"], "sqlite.org whentouse.html")
        self.h.run_jobs(only=("fetch",))
        rec = self.rec(rec)
        self.assertEqual(rec["title"], "Appropriate Uses For SQLite")
        self.assertEqual(self.h.note(rec["id"])["title"], "Appropriate Uses For SQLite")
        self.assertEqual(self.h.svc.state.source_by_title("Appropriate Uses For SQLite"), rec["id"])

    def test_redirect_keeps_final_url(self):
        rec = self.add("http://go.example/raft-talk", "Raft talk")
        self.h.run_jobs(only=("fetch",))
        rec = self.rec(rec)
        self.assertEqual(rec["status"], "ok")
        self.assertEqual(rec["finalUrl"], "https://www.infoq.com/presentations/raft/")

    def test_paused_fetch_waits(self):
        rec = self.add("https://raft.github.io/")
        self.h.svc.state.set_paused(True)
        self.assertEqual(self.h.run_job(self.h.queued("fetch")[0]), {"paused": True})
        self.assertEqual(self.rec(rec)["status"], "pending")
        self.assertEqual(len(self.h.queued("fetch")), 1)
        self.assertTrue(self.h.queued("fetch")[0]["runAfter"])


class ImportDedupe(unittest.TestCase):
    def test_known_urls_merge_tags_and_folders(self):
        h = Harness()
        chrome = [b.to_json() for b in __import__("wiki.importers", fromlist=["parse"]).parse(
            "netscape", support.fixture_text("bookmarks", "chrome.html"))]
        first = h.run("import", {"importer": "netscape", "bookmarks": chrome})
        self.assertEqual((first["new"], first["merged"], first["queued"]), (4, 1, 4))
        again = h.run("import", {"importer": "netscape", "bookmarks": chrome})
        self.assertEqual((again["new"], again["merged"], again["queued"]), (0, 0, 0))
        extra = h.run("import", {"importer": "urls", "bookmarks": [
            {"url": "https://sqlite.org/wal.html?utm_source=x", "tags": ["cache"], "folder": "Reading"}]})
        self.assertEqual(extra["merged"], 1)
        rec = h.svc.state.source(h.svc.state.url_note("https://sqlite.org/wal.html"))
        self.assertEqual(rec["folders"], ["Bookmarks bar", "Other bookmarks", "Reading"])
        self.assertEqual(rec["tags"], ["cache"])
        fm = frontmatter.read(h.note(rec["id"]))
        self.assertEqual(fm["bookmark_folder"], ["Bookmarks bar", "Other bookmarks", "Reading"])
        self.assertEqual(fm["bookmarked"], "2024-03-02T10:14:00Z")
        self.assertEqual(len(h.queued("fetch")), 4)
        self.assertEqual(len([n for n in h.fake.all_notes() if n["path"].startswith("Sources/")]), 4)

    def test_import_after_a_crash_reuses_the_note(self):
        h = Harness()
        url = "https://raft.github.io/"
        h.fake.faults["kv_put"] = [HostError(503, "down")]
        with self.assertRaises(HostError):
            from wiki.sources import import_bookmarks
            from wiki.importers import Bookmark
            h.svc.files.ensure_all(SPACE)
            import_bookmarks(h.svc, support.JobCtx(h.fake, h.fake.create_job("import", payload={})), SPACE,
                             [Bookmark(url=url)], "urls")
        h.run("import", {"importer": "urls", "bookmarks": [{"url": url}]})
        self.assertEqual(len([n for n in h.fake.all_notes() if n["path"].startswith("Sources/")]), 1)


class Refetch(unittest.TestCase):
    def test_queues_the_50_oldest(self):
        h = Harness()
        for i in range(55):
            h.svc.state.save_source({"id": f"s{i:03d}", "spaceId": SPACE, "url": f"https://e{i}.example.com/",
                                     "title": f"S{i}", "site": "example.com", "status": "ok", "wikiState": "compiled",
                                     "capturedAt": "2026-01-01T00:00:00Z",
                                     "fetched": iso(h.clock.now.replace(day=1 + (54 - i) % 28, hour=i % 24))})
        result = h.run("refetch", {})
        self.assertEqual(result, {"queued": 50})
        jobs = h.queued("fetch")
        self.assertEqual(len(jobs), 50)
        self.assertTrue(all(j["payload"]["refetch"] for j in jobs))
        fetched = [h.svc.state.source(j["payload"]["sourceId"])["fetched"] for j in jobs]
        self.assertEqual(fetched, sorted(fetched))


class Gate(unittest.TestCase):
    def test_one_request_per_second_per_host(self):
        now = [100.0]
        slept = []
        gate = HostGate(1.0, clock=lambda: now[0], sleep=slept.append)
        self.assertEqual(gate.reserve("a.example"), 0.0)
        self.assertEqual(gate.reserve("a.example"), 1.0)
        self.assertEqual(gate.reserve("b.example"), 0.0)
        now[0] = 100.5
        self.assertEqual(gate.reserve("a.example"), 1.5)
        gate.wait("b.example")
        self.assertEqual(slept, [0.5])


if __name__ == "__main__":
    unittest.main()
