"""End to end over HTTP.

The plugin runs its own HTTP server on 127.0.0.1. The fake host serves the
host API over HTTP, signs every call to the plugin with the SDK helpers, and
runs jobs the way the Markspace worker pool would. The test installs the
plugin, imports 30 bookmarks from a Netscape export, runs every job to
completion, runs lint, and checks the resulting wiki.
"""

import base64
import json
import re
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request
import uuid
from collections import defaultdict

import support
from markspace import sign_headers
from wiki import frontmatter, wikilinks
from wiki.app import create_plugin
from fakehost import PLUGIN_USER, SPACE, USER, FakeMarkspace
from fakehttp import HostServer, JobRunner

PORTS = range(18190, 18200)
TOKEN = "mspl_" + "t" * 40
ACTING = "act_" + "a" * 20
SECRET = bytes(range(32))
SECRET_B64 = base64.b64encode(SECRET).decode()


class EndToEnd(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.fake = FakeMarkspace()
        cls.host = HostServer(cls.fake, TOKEN, {ACTING: USER}).start(PORTS)
        cls.plugin = create_plugin(data_dir=tempfile.mkdtemp(prefix="bookmark-wiki-e2e-"), env={}, job_wait=20.0,
                                   host_options={"max_tries": 3})
        taken = cls.host.server.server_address[1]
        cls.server = None
        for port in PORTS:
            if port == taken:
                continue
            try:
                cls.server = cls.plugin.make_server("127.0.0.1", port)
                break
            except OSError:
                continue
        if cls.server is None:
            cls.host.stop()
            raise unittest.SkipTest("no free port in 18190-18199")
        cls.url = f"http://127.0.0.1:{cls.server.server_address[1]}"
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()
        cls.runner = JobRunner(cls.fake, cls.url, SECRET, "bookmark-wiki")
        try:
            cls.build()
        except BaseException:
            cls.tearDownClass()
            raise

    @classmethod
    def build(cls):
        status, resp = cls.post("/install", {
            "pluginId": "bookmark-wiki", "markspaceUrl": cls.host.url, "token": TOKEN, "signingSecret": SECRET_B64,
            "grants": [{"spaceId": SPACE, "role": "editor"}], "permissions": ["items:read"],
            "settings": {"space": SPACE}}, signed=False)
        assert status == 200, (status, resp)
        with cls.fake.as_user(USER):
            folder = cls.fake.create_folder(SPACE, "Projects")["id"]
            cls.seeded = {cls.fake.create_note(SPACE, title, body, parent_id=folder)["id"]: body
                          for title, body in support.USER_NOTES.items()}
        cls.seeded_notes = {i: cls.fake.note(i) for i in cls.seeded}
        file_id = cls.fake.add_file("bookmarks.html", support.fixture_text("bookmarks", "e2e-30.html").encode())
        interaction = {"actingToken": ACTING, "user": {"id": USER, "name": "Alice"}}
        status, resp = cls.post("/import/netscape", {"fileId": file_id, "spaceId": SPACE, **interaction})
        assert status == 200, (status, resp)
        cls.preview = resp
        payload = next(n["action"]["payload"] for n in cls.walk(resp)
                       if isinstance(n.get("action"), dict) and n["action"].get("id") == "confirm-import")
        status, resp = cls.post("/actions/confirm-import", {"context": {"spaceId": SPACE}, "values": {},
                                                             "payload": payload, **interaction})
        assert status == 200 and "job" in resp, (status, resp)
        started = time.time()
        cls.runner.run_until_idle(timeout=300)
        status, resp = cls.post("/actions/run-lint", {"context": {"spaceId": SPACE}, "values": {}, "payload": {},
                                                       **interaction})
        assert status == 200 and "job" in resp, (status, resp)
        cls.runner.run_until_idle(timeout=120)
        cls.elapsed = time.time() - started

    @classmethod
    def tearDownClass(cls):
        if cls.server is not None:
            cls.server.shutdown()
            cls.server.server_close()
        cls.host.stop()

    @classmethod
    def walk(cls, node):
        if isinstance(node, dict):
            yield node
            for v in node.values():
                yield from cls.walk(v)
        elif isinstance(node, list):
            for v in node:
                yield from cls.walk(v)

    @classmethod
    def post(cls, path, payload, signed=True):
        body = json.dumps(payload).encode()
        headers = {"Content-Type": "application/json"}
        if signed:
            headers.update(sign_headers(SECRET, "bookmark-wiki", uuid.uuid4().hex, "POST", path, body, time.time()))
        req = urllib.request.Request(cls.url + path, data=body, method="POST", headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                raw, status = r.read(), r.status
        except urllib.error.HTTPError as exc:
            with exc:
                raw, status = exc.read(), exc.code
        return status, (json.loads(raw) if raw else None)

    # helpers ---------------------------------------------------------------

    def notes_under(self, prefix):
        return [n for n in self.fake.all_notes() if n["path"].startswith(prefix)]

    def wiki_pages(self):
        return [n for n in self.fake.all_notes()
                if re.match(r"Wiki/(Entities|Concepts|Comparisons)/", n["path"])]

    def resolve(self, note, target):
        item = self.fake.resolve(SPACE, note["id"], [target]).items[0]
        return self.fake.note(item["id"]) if item["id"] and item["kind"] == "note" else None

    # checks ----------------------------------------------------------------

    def test_preview_estimate(self):
        text = json.dumps(self.preview)
        self.assertIn("30 bookmarks from Browser bookmarks. 30 are new", text)
        self.assertIn("The estimated cost is $", text)

    def test_jobs_all_finished(self):
        statuses = defaultdict(int)
        for job in self.fake.jobs.values():
            statuses[job["status"]] += 1
        self.assertEqual(set(statuses), {"done"}, dict(statuses))
        self.assertEqual(self.runner.failures, [])
        kinds = {j["kind"] for j in self.fake.jobs.values()}
        self.assertEqual(kinds, {"import", "fetch", "summarise", "compile", "link-suggest", "lint"})

    def test_sources_and_states(self):
        sources = [n for n in self.notes_under("Sources/") if n["path"].endswith(".md")]
        self.assertEqual(len(sources), 30)
        states = defaultdict(int)
        for note in sources:
            states[frontmatter.read(note)["status"]] += 1
        self.assertEqual(dict(states), {"ok": 27, "dead": 1, "blocked": 2})
        wiki_states = {frontmatter.read(n)["wiki_state"] for n in sources if frontmatter.read(n)["status"] == "ok"}
        self.assertEqual(wiki_states, {"compiled"})
        pdf = next(n for n in sources if frontmatter.read(n)["source_url"].endswith("raft.pdf"))
        files = [e["obj"] for e in self.fake.items.values() if e["kind"] == "file" and e["obj"]["mime"] == "application/pdf"]
        self.assertEqual(len(files), 1)
        self.assertEqual(files[0]["parentId"], pdf["parentId"])
        self.assertIn("Raft is a consensus algorithm for managing a replicated log.", pdf["content"])
        redirect = next(n for n in sources if frontmatter.read(n)["source_url"] == "http://go.example/raft-talk")
        self.assertEqual(frontmatter.read(redirect)["canonical_url"], "https://www.infoq.com/presentations/raft/")

    def test_wiki_structure(self):
        for name in ("index", "log", "schema"):
            self.assertIsNotNone(self.fake.note_by_path(f"Wiki/{name}.md"), name)
        pages = self.wiki_pages()
        self.assertGreaterEqual(len(pages), 40)
        index = self.fake.note_by_path("Wiki/index.md")["content"]
        for page in pages:
            self.assertIn(f"- [[{page['title']}]]", index)
        self.assertIsNotNone(self.fake.note_by_path("Wiki/Comparisons/b-tree-vs-lsm-tree-comparison.md"))
        log = self.fake.note_by_path("Wiki/log.md")["content"]
        self.assertEqual(log.count("· compile ·"), 27)
        self.assertIn("· import · netscape · new 30", log)
        self.assertIn("· lint ·", log)
        self.assertEqual(len(self.notes_under("Wiki/Lint/")), 1)

    def test_every_page_links_twice_and_every_citation_resolves(self):
        for page in self.wiki_pages():
            body = frontmatter.body_of(page["content"])
            cites = wikilinks.citations(body)
            self.assertTrue(cites, page["title"])
            for cite in cites:
                target = self.resolve(page, cite)
                self.assertIsNotNone(target, f"{page['title']} cites {cite}")
                self.assertTrue(target["path"].startswith("Sources/"), cite)
            linked = set()
            for target in wikilinks.non_citation_targets(body):
                note = self.resolve(page, target)
                self.assertIsNotNone(note, f"{page['title']} links {target}")
                if note["id"] != page["id"] and (note["path"].startswith("Wiki/") or note["id"] in self.seeded):
                    linked.add(note["id"])
            self.assertGreaterEqual(len(linked), 2, page["title"])

    def test_pages_link_to_every_seeded_note(self):
        linked = set()
        for page in self.wiki_pages():
            for target in wikilinks.non_citation_targets(frontmatter.body_of(page["content"])):
                note = self.resolve(page, target)
                if note and note["id"] in self.seeded:
                    linked.add(note["id"])
        self.assertEqual(linked, set(self.seeded))

    def test_seeded_notes_unchanged_and_suggested(self):
        for note_id, note in self.seeded_notes.items():
            self.assertEqual(self.fake.note(note_id), note)
        self.assertFalse([w for w in self.fake.writes if w[0] in self.seeded and w[2] == PLUGIN_USER])
        for note_id in self.seeded:
            suggestions = self.fake.open_suggestions(note_id)
            self.assertEqual(len(suggestions), 1, self.seeded_notes[note_id]["title"])
            self.assertEqual(suggestions[0]["authorKind"], "plugin")
            self.assertTrue(suggestions[0]["edits"][0]["text"].startswith("[["))

    def test_plugin_writes_only_generated_notes(self):
        for note_id, action, actor in self.fake.writes:
            if actor != PLUGIN_USER:
                continue
            note = self.fake.note(note_id)
            self.assertEqual(frontmatter.read(note).get("generated_by"), "bookmark-wiki", note["path"])

    def test_host_api_protocol(self):
        self.assertEqual(self.host.posts_without_key, 0)
        self.assertGreater(len(self.host.requests), 100)

    def test_fetches_are_paced_per_host(self):
        by_host = defaultdict(list)
        for host, _, at in self.fake.fetch_times:
            by_host[host].append(at)
        self.assertTrue(any(len(v) > 1 for v in by_host.values()))
        for host, times in by_host.items():
            times.sort()
            for a, b in zip(times, times[1:]):
                self.assertGreaterEqual(b - a, 0.95, host)

    def test_unsigned_job_call_is_refused(self):
        status, resp = self.post("/jobs/compile", {"job": {"id": "x1", "kind": "compile", "payload": {}}}, signed=False)
        self.assertEqual((status, resp["code"]), (401, "malformed"))

    def test_z_recompiling_changes_nothing(self):
        before = {n["id"]: (n["updatedAt"], n["content"]) for n in self.fake.all_notes()}
        source = next(n for n in self.notes_under("Sources/") if frontmatter.read(n).get("wiki_state") == "compiled")
        self.fake.create_job("compile", title="again", payload={"sourceId": source["id"]}, space_id=SPACE)
        self.runner.run_until_idle(timeout=60)
        after = {n["id"]: (n["updatedAt"], n["content"]) for n in self.fake.all_notes()}
        self.assertEqual(after, before)


if __name__ == "__main__":
    unittest.main()
