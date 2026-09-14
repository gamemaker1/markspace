"""Compile: page links, citations, checks with one repair call, conflicts, idempotency and serialization."""

import json
import threading
import time
import unittest

import support
from markspace import HostError
from wiki import frontmatter, wikilinks
from wiki.prompts import extract_input
from fakehost import SPACE, USER
from support import Harness

THREE = ["sqlite-wal", "pg-wal", "sqlite-when"]


def base_page(h, request):
    user = request["text"]
    if "<<<PREVIOUS" in user:
        user = user.split("Original request:\n", 1)[-1]
    return h.fake.ai.page(extract_input(user))


def resolves_to(h, note, target):
    item = h.fake.resolve(SPACE, note["id"], [target]).items[0]
    return h.fake.note(item["id"]) if item["id"] else None


class PagesAndLinks(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.h = Harness()
        cls.user_notes = cls.h.seed_user_notes()
        cls.h.pipeline(THREE)

    def pages(self):
        return [self.h.fake.note(p["id"]) for p in self.h.svc.state.pages()]

    def test_pages_are_filed_by_type(self):
        paths = {p["title"]: p["path"] for p in self.pages()}
        self.assertEqual(paths["SQLite"], "Wiki/Entities/sqlite.md")
        self.assertEqual(paths["Write-ahead logging (concept)"], "Wiki/Concepts/write-ahead-logging-concept.md")
        self.assertEqual(set(paths), {"SQLite", "Write-ahead logging (concept)", "Checkpointing", "PostgreSQL",
                                      "Embedded database"})

    def test_page_titles_never_collide_with_source_titles(self):
        page = self.h.page("Write-ahead logging")
        self.assertEqual(page["title"], "Write-ahead logging (concept)")
        self.assertIn("Write-ahead logging", frontmatter.read(page)["aliases"])
        source = self.h.note(self.h.source("sqlite-wal")["id"])
        self.assertIn("[[SQLite]], [[Write-ahead logging (concept)|Write-ahead logging]], [[Checkpointing]]",
                      source["content"])
        self.assertEqual(resolves_to(self.h, page, "Write-Ahead Logging")["id"], source["id"])
        h = Harness()
        h.pipeline(["sqlite-wal"])
        h.run("import", {"importer": "urls", "bookmarks": [{"url": "https://example.org/sqlite", "title": "SQLite"}]})
        self.assertEqual(h.svc.state.source(h.svc.state.url_note("https://example.org/sqlite"))["title"],
                         "SQLite (example.org)")

    def test_pages_cite_sources_and_link_pages_and_user_notes(self):
        linked_user_notes = set()
        for page in self.pages():
            body = frontmatter.body_of(page["content"])
            cites = wikilinks.citations(body)
            self.assertTrue(cites, page["title"])
            for cite in cites:
                target = resolves_to(self.h, page, cite)
                self.assertIsNotNone(target, cite)
                self.assertTrue(target["path"].startswith("Sources/"), cite)
            links = 0
            for target in wikilinks.non_citation_targets(body):
                note = resolves_to(self.h, page, target)
                self.assertIsNotNone(note, f"{page['title']} links {target}")
                if note["id"] in self.user_notes.values():
                    linked_user_notes.add(note["title"])
                if note["path"].startswith("Wiki/") or note["id"] in self.user_notes.values():
                    links += 1
            self.assertGreaterEqual(links, 2, page["title"])
            fm = frontmatter.read(page)
            self.assertEqual(fm["generated_by"], "bookmark-wiki")
            self.assertEqual(fm["sources"], [f"[[{c}]]" for c in cites])
        self.assertIn("Storage design notes", linked_user_notes)

    def test_postgresql_page_merges_two_sources(self):
        cites = wikilinks.citations(frontmatter.body_of(self.h.page("PostgreSQL")["content"]))
        self.assertEqual(cites, ["Reliability and the Write-Ahead Log", "Appropriate Uses For SQLite"])
        self.assertEqual(len(self.h.svc.state.page(self.h.svc.state.alias("PostgreSQL"))["sources"]), 2)
        self.assertEqual(self.h.svc.state.alias("Postgres"), self.h.svc.state.alias("PostgreSQL"))

    def test_index_log_and_states(self):
        index = self.h.fake.note_by_path("Wiki/index.md")["content"]
        for page in self.pages():
            self.assertIn(f"- [[{page['title']}]]: ", index)
        self.assertLess(index.index("## Entities"), index.index("[[PostgreSQL]]"), )
        self.assertLess(index.index("[[PostgreSQL]]"), index.index("## Concepts"))
        log = self.h.fake.note_by_path("Wiki/log.md")["content"]
        self.assertIn("· compile · [[Write-Ahead Logging]] · created 3, updated 0", log)
        self.assertIn("· compile · [[Appropriate Uses For SQLite]] · created 1, updated 2", log)
        self.assertIsNotNone(self.h.fake.note_by_path("Wiki/schema.md"))
        self.assertEqual(self.h.svc.state.counts()["wikiState"]["compiled"], 3)

    def test_link_suggest_jobs_are_queued_for_touched_pages(self):
        pages = {j["payload"]["pageId"] for j in self.h.queued("link-suggest")}
        self.assertEqual(pages, {p["id"] for p in self.h.svc.state.pages()})


class Idempotency(unittest.TestCase):
    def check(self, h):
        rec = h.source("pg-wal")
        before = h.snapshot()
        calls = len(h.fake.ai.requests)
        jobs = len(h.fake.jobs)
        self.assertEqual(h.run("compile", {"sourceId": rec["id"]}), {"unchanged": True})
        self.assertEqual(h.snapshot(), before)
        self.assertEqual(len(h.fake.ai.requests), calls)
        forced = h.run("compile", {"sourceId": rec["id"], "force": True})
        self.assertEqual((forced["created"], forced["updated"], forced["unchanged"]), (0, 0, 3))
        self.assertEqual(h.snapshot(), before)
        self.assertEqual(len(h.fake.ai.requests), calls)
        self.assertEqual(len(h.fake.jobs), jobs + 2)

    def test_compiling_the_same_source_twice_changes_nothing(self):
        h = Harness()
        h.seed_user_notes()
        h.pipeline(THREE)
        self.check(h)

    def test_same_when_the_host_keeps_frontmatter_in_extra(self):
        h = Harness(strip_frontmatter=True)
        h.seed_user_notes()
        h.pipeline(THREE)
        page = h.page("PostgreSQL")
        self.assertFalse(page["content"].startswith("---"))
        self.assertEqual(page["extra"]["generated_by"], "bookmark-wiki")
        self.assertEqual(len(h.fake.open_suggestions()), 0)
        self.check(h)


class Checks(unittest.TestCase):
    def setUp(self):
        self.h = Harness()
        self.h.seed_user_notes()

    def summarised(self, ids):
        self.h.import_pages(ids)
        self.h.run_jobs(exclude=("compile", "link-suggest"))

    def compile_calls(self):
        return self.h.fake.ai.calls("compile")

    def test_unknown_citation_gets_one_repair_call(self):
        self.summarised(["sqlite-wal"])

        def bad(request):
            page = base_page(self.h, request)
            page["body"] += "\nExtra claim ([[Made up source]]).\n"
            return json.dumps(page)

        self.h.fake.ai.script("compile", bad)
        result = self.h.run_job(self.h.queued("compile")[0])
        self.assertEqual((result["created"], result["failed"]), (3, 0))
        self.assertEqual(len(self.compile_calls()), 4)
        self.assertIn("names no source note", self.compile_calls()[1]["text"])
        self.assertNotIn("Made up source", "".join(p["content"] for p in self.h.fake.all_notes()))

    def test_page_failing_twice_is_skipped_and_logged(self):
        self.summarised(["sqlite-wal"])
        broken = json.dumps({"title": "SQLite", "type": "entity", "body": "No links at all."})
        self.h.fake.ai.script("compile", broken, broken)
        job = self.h.queued("compile")[0]
        result = self.h.run_job(job)
        self.assertEqual((result["created"], result["failed"]), (2, 1))
        rec = self.h.source("sqlite-wal")
        self.assertEqual(rec["compileFailures"], ["SQLite"])
        self.assertNotIn("compiled", rec)
        self.assertTrue(any("skipped page SQLite" in m for _, m in self.h.ctxs[-1].logs))
        self.assertIn("created 2, updated 0, failed 1", self.h.fake.note_by_path("Wiki/log.md")["content"])
        retry = self.h.run("compile", {"sourceId": rec["id"]})
        self.assertEqual((retry["created"], retry["unchanged"]), (1, 2))
        self.assertIn("compiled", self.h.source("sqlite-wal"))

    def test_dropped_citation_needs_a_listed_reason(self):
        self.h.pipeline(["sqlite-wal", "sqlite-when"])
        page_id = self.h.svc.state.alias("SQLite")
        self.assertEqual(len(wikilinks.citations(self.h.page("SQLite")["content"])), 2)

        def drop(request, reason):
            page = base_page(self.h, request)
            page["body"] = page["body"].replace("[[Write-Ahead Logging]]", "[[Appropriate Uses For SQLite]]")
            if reason:
                page["removed_citations"] = [{"source": "Write-Ahead Logging", "reason": "The other source covers it."}]
            return json.dumps(page)

        self.h.fake.ai.script("compile", lambda r: drop(r, False), lambda r: drop(r, True))
        rec = self.h.source("sqlite-when")
        result = self.h.run("compile", {"sourceId": rec["id"], "force": True, "only": [page_id]})
        self.assertEqual((result["updated"], result["failed"]), (1, 0))
        self.assertIn("drops citations without listing them in removed_citations: [[Write-Ahead Logging]]",
                      self.compile_calls()[-1]["text"])
        page = self.h.page("SQLite")
        self.assertEqual(wikilinks.citations(page["content"]), ["Appropriate Uses For SQLite"])
        self.assertEqual(frontmatter.read(page)["sources"], ["[[Appropriate Uses For SQLite]]"])

    def test_link_to_a_missing_note_is_rejected(self):
        self.summarised(["sqlite-wal"])

        def bad(request):
            page = base_page(self.h, request)
            page["body"] += "\n- [[No such note]]\n"
            return json.dumps(page)

        self.h.fake.ai.script("compile", bad)
        self.h.run_job(self.h.queued("compile")[0])
        self.assertIn("[[No such note]] does not resolve", self.compile_calls()[1]["text"])
        self.assertNotIn("No such note", "".join(p["content"] for p in self.h.fake.all_notes()))

    def test_conflict_runs_step_4_again_once(self):
        self.h.pipeline(["sqlite-wal"])
        self.summarised(["pg-wal"])
        self.h.fake.faults["update_note"] = [HostError(409, "changed", "baseUpdatedAt")]
        before = len(self.compile_calls())
        result = self.h.run_job(self.h.queued("compile")[0])
        self.assertEqual((result["created"], result["updated"], result["failed"]), (1, 2, 0))
        self.assertEqual(len(self.compile_calls()) - before, 4)
        self.assertTrue(any("changed during compile" in m for _, m in self.h.ctxs[-1].logs))

    def test_max_pages_per_source_favours_existing_pages(self):
        self.h.settings["maxPagesPerSource"] = 2
        self.h.pipeline(["sqlite-wal"])
        self.assertEqual({p["title"] for p in self.h.svc.state.pages()}, {"SQLite", "Write-ahead logging (concept)"})
        self.h.pipeline(["pg-wal"])
        self.assertEqual({p["title"] for p in self.h.svc.state.pages()},
                         {"SQLite", "Write-ahead logging (concept)", "PostgreSQL"})

    def test_user_edits_to_schema_md_reach_the_model_and_stay(self):
        self.h.svc.files.ensure_all(SPACE)
        schema = self.h.fake.note_by_path("Wiki/schema.md")
        self.h.fake.user_edit(schema["id"], "# Wiki schema\n\nWrite every page in British English.\n")
        edited = self.h.fake.note(schema["id"])["content"]
        self.h.pipeline(["sqlite-wal"])
        self.assertIn("Write every page in British English.", self.compile_calls()[0]["text"])
        self.assertEqual(self.h.fake.note(schema["id"])["content"], edited)

    def test_comparison_pages(self):
        self.h.pipeline(["btree-vs-lsm"])
        page = self.h.page("B-tree vs LSM tree")
        self.assertEqual(page["path"], "Wiki/Comparisons/b-tree-vs-lsm-tree-comparison.md")
        self.assertEqual(frontmatter.read(page)["type"], "comparison")
        index = self.h.fake.note_by_path("Wiki/index.md")["content"]
        self.assertLess(index.index("## Comparisons"), index.index("[[B-tree vs LSM tree (comparison)]]"))

    def test_compile_runs_one_at_a_time_per_space(self):
        self.summarised(["sqlite-wal", "pg-wal"])
        active, peak, lock = [0], [0], threading.Lock()

        def slow(request):
            with lock:
                active[0] += 1
                peak[0] = max(peak[0], active[0])
            time.sleep(0.02)
            with lock:
                active[0] -= 1
            return self.h.fake.ai.default("compile", request)

        self.h.fake.ai.script("compile", *[slow] * 20)
        threads = [threading.Thread(target=self.h.run_job, args=(job,)) for job in self.h.queued("compile")]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(peak[0], 1)
        self.assertEqual(self.h.errors, [])
        self.assertIsNot(self.h.svc.compile_lock("a"), self.h.svc.compile_lock("b"))
        self.assertEqual(self.h.svc.counts() if False else self.h.svc.state.counts()["wikiState"]["compiled"], 2)


if __name__ == "__main__":
    unittest.main()
