"""Each lint check against a planted problem."""

import json
import unittest

import support
from wiki import frontmatter
from wiki.prompts import extract_input
from wiki.util import iso
from fakehost import SPACE
from support import Harness

DISAGREEMENT = {
    "contested": True,
    "disagreements": [{"claim": "whether WAL reduces disk writes", "positions": [
        {"source": "Reliability and the Write-Ahead Log", "says": "WAL reduces disk writes."},
        {"source": "Appropriate Uses For SQLite", "says": "It says nothing about disk writes."}]}],
    "section": "## Disagreements\n\n- The sources differ on disk writes ([[Reliability and the Write-Ahead Log]]) "
               "([[Appropriate Uses For SQLite]]).\n",
}


class Lint(unittest.TestCase):
    def setUp(self):
        self.h = Harness()
        self.h.seed_user_notes()
        self.h.pipeline(["sqlite-wal", "pg-wal", "sqlite-when"])
        self.baseline = self.h.run("lint", {})
        self.h.clock.advance(hours=1)
        self.wal_source = self.h.source("sqlite-wal")

    def lint(self):
        result = self.h.run("lint", {})
        report = self.h.fake.note_by_path(f"Wiki/Lint/{self.h.clock.now:%Y-%m-%d}.md")
        return result, report["content"]

    def rewrite(self, title, change):
        page = self.h.page(title)
        rendered = frontmatter.dump(frontmatter.read(page)) + frontmatter.body_of(page["content"])
        self.h.svc.notes.write(page, change(rendered))

    def plant_page(self, title, body, index=True):
        fields = {"title": title, "type": "concept", "entity_kind": "", "aliases": [], "tags": [],
                  "sources": ["[[Write-Ahead Logging]]"], "related_notes": [], "confidence": "medium",
                  "contested": False, "created": iso(self.h.clock()), "updated": iso(self.h.clock()),
                  "generated_by": "bookmark-wiki"}
        note = self.h.svc.notes.create(SPACE, "Wiki/Concepts", title, frontmatter.dump(fields) + body)
        self.h.svc.state.save_page({"id": note["id"], "spaceId": SPACE, "title": title, "type": "concept",
                                    "aliases": [], "sources": [self.wal_source["id"]], "inputs": {}})
        self.h.svc.state.set_alias(title, note["id"])
        if index:
            self.h.svc.files.add_to_index(SPACE, [("concept", title, "Planted.")])
        return note

    def suggestion_body(self, note_id):
        [s] = self.h.fake.open_suggestions(note_id)
        return self.h.fake.apply_suggestion(s["id"])

    def test_clean_wiki_has_no_structural_findings(self):
        for check in ("orphans", "broken", "missing", "drift", "dead", "long", "index"):
            self.assertEqual(self.baseline["findings"][check], 0, check)
        self.assertEqual(self.baseline["suggestions"], 0)

    def test_orphan_gets_a_see_also_suggestion(self):
        orphan = self.plant_page("Orphan concept", "Orphan concept is planted ([[Write-Ahead Logging]]).\n\n"
                                                   "- [[SQLite]]\n- [[PostgreSQL]]\n")
        result, report = self.lint()
        self.assertEqual(result["findings"]["orphans"], 1)
        self.assertIn("- [ ] [[Orphan concept]] has no backlinks. A suggestion adds it to", report)
        bodies = [self.h.fake.apply_suggestion(s["id"]) for s in self.h.fake.open_suggestions()]
        self.assertTrue(any("## See also\n\n- [[Orphan concept]]" in b for b in bodies))
        self.assertEqual(self.h.fake.note(orphan["id"])["updatedBy"], "u-plugin")

    def test_broken_links_are_retargeted_or_removed(self):
        self.rewrite("Write-ahead logging",
                     lambda c: c.replace("## Related\n\n", "## Related\n\n- [[Checkpointng]]\n- [[Totally missing|gone]]\n"))
        before = self.h.page("Write-ahead logging")["content"]
        result, report = self.lint()
        self.assertEqual(result["findings"]["broken"], 2)
        self.assertIn("`[[Checkpointng]]`, which resolves to nothing. A suggestion retargets it to [[Checkpointing]].", report)
        self.assertEqual(self.h.page("Write-ahead logging")["content"], before)
        body = self.suggestion_body(self.h.svc.state.alias("Write-ahead logging"))
        self.assertNotIn("Checkpointng", body)
        self.assertIn("- [[Checkpointing]]\n- gone\n", body)

    def test_missing_page_mentioned_by_three_sources_queues_compiles(self):
        for i in range(3):
            self.h.svc.state.save_source({
                "id": f"planted{i}", "spaceId": SPACE, "url": f"https://clocks{i}.example.com/", "title": f"Clock post {i}",
                "site": "example.com", "status": "ok", "wikiState": "compiled",
                "summary": {"summary": "x", "key_points": [], "tags": [],
                            "mentions": [{"name": "Vector clock", "kind": "concept", "aliases": []}]}})
        result, report = self.lint()
        self.assertEqual(result["findings"]["missing"], 1)
        self.assertIn("Vector clock is mentioned by 3 sources and has no page.", report)
        jobs = self.h.queued("compile")
        self.assertEqual(sorted(j["payload"]["sourceId"] for j in jobs), ["planted0", "planted1", "planted2"])
        self.assertTrue(all(j["payload"]["focus"] == ["Vector clock"] and j["payload"]["force"] for j in jobs))

    def test_drift_dead_and_long_pages_are_reported(self):
        rec = self.h.source("sqlite-wal")
        rec["drift"] = [{"date": iso(self.h.clock()), "oldSha": "a", "newSha": "b", "noteId": "x",
                         "title": "Write-Ahead Logging (fetched 2026-09-13)"}]
        self.h.svc.state.save_source(rec)
        self.h.run("import", {"importer": "urls", "bookmarks": [{"url": "https://old-blog.example.net/posts/gone"}]})
        self.h.run_jobs(only=("fetch",))
        self.plant_page("Long page", "Long page is planted ([[Write-Ahead Logging]]).\n\n" + "word " * 1600
                        + "\n\n- [[SQLite]]\n- [[PostgreSQL]]\n")
        result, report = self.lint()
        self.assertEqual((result["findings"]["drift"], result["findings"]["dead"], result["findings"]["long"]), (1, 1, 1))
        self.assertIn("[[Write-Ahead Logging]] changed on", report)
        self.assertIn("is dead (HTTP 404)", report)
        self.assertIn("- [ ] [[Long page]] has 16", report)
        again, _ = self.lint()
        self.assertEqual(again["findings"]["drift"], 0)

    def test_index_entries_are_checked_both_ways(self):
        index = self.h.fake.note_by_path("Wiki/index.md")
        content = "\n".join(l for l in index["content"].splitlines() if "[[PostgreSQL]]" not in l) + "\n"
        content = content.replace("## Concepts\n\n", "## Concepts\n\n- [[Ghost page]]: gone.\n")
        self.h.svc.notes.write(index, content)
        result, report = self.lint()
        self.assertEqual(result["findings"]["index"], 2)
        self.assertIn("[[PostgreSQL]] is missing from the index.", report)
        self.assertIn("The index lists `[[ghost page]]`, which is not a wiki page.", report)
        body = self.suggestion_body(index["id"])
        self.assertIn("- [[PostgreSQL]]: ", body)
        self.assertNotIn("Ghost page", body)

    def test_contested_pages_get_a_disagreements_suggestion(self):
        self.rewrite("PostgreSQL", lambda c: c.replace("contested: false", "contested: true"))

        def answer(request):
            data = extract_input(request["text"])
            return json.dumps(DISAGREEMENT if data["page"] == "PostgreSQL" else {"disagreements": [], "section": ""})

        self.h.fake.ai.script("lint", *[answer] * 20)
        before = self.h.page("PostgreSQL")["content"]
        baseline_calls = len(self.h.fake.ai.calls("lint"))
        result, report = self.lint()
        self.assertEqual(result["findings"]["contradictions"], 1)
        self.assertIn("[[PostgreSQL]] has sources that disagree on whether WAL reduces disk writes.", report)
        self.assertEqual(self.h.page("PostgreSQL")["content"], before)
        body = self.suggestion_body(self.h.svc.state.alias("PostgreSQL"))
        self.assertIn("## Disagreements\n\n- The sources differ on disk writes", body)
        calls = self.h.fake.ai.calls("lint")[baseline_calls:]
        self.assertEqual([extract_input(c["text"])["page"] for c in calls], ["PostgreSQL"])

    def test_report_is_rewritten_not_duplicated(self):
        self.lint()
        self.lint()
        reports = [n for n in self.h.fake.all_notes() if n["path"].startswith("Wiki/Lint/") and "Drift" not in n["path"]]
        self.assertEqual(len(reports), 1)
        self.assertIn("## Orphans\n\nNo findings.", reports[0]["content"])


if __name__ == "__main__":
    unittest.main()
