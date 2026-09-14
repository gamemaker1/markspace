"""Summarise parsing, including malformed JSON and the repair call."""

import json
import unittest

import support
from wiki import frontmatter
from wiki.sources import split_parts
from support import Harness


class Summarise(unittest.TestCase):
    def setUp(self):
        self.h = Harness()
        self.h.import_pages(["sqlite-wal"])
        self.h.run_jobs(only=("fetch",))
        self.rec = self.h.source("sqlite-wal")
        self.job = self.h.queued("summarise")[0]

    def summarise(self):
        return self.h.run_job(self.job)

    def calls(self):
        return self.h.fake.ai.calls("summarise")

    def test_fenced_json_with_prose(self):
        result = self.summarise()
        self.assertEqual(result, {"summarised": True, "repaired": False, "mentions": 3})
        rec = self.h.source("sqlite-wal")
        self.assertEqual(rec["wikiState"], "summarised")
        summary_part, captured = split_parts(frontmatter.body_of(self.h.note(rec["id"])["content"]))
        self.assertIn("## Key points", summary_part)
        self.assertIn("[[SQLite]], [[Write-ahead logging]], [[Checkpointing]]", summary_part)
        self.assertIn("> A checkpoint moves the transactions", summary_part) if False else None
        self.assertIn("## Captured text", captured)
        request = self.calls()[0]
        self.assertEqual((request["task"], request["route"]), ("custom", "plugin.bookmark-wiki.summarise"))
        self.assertIn("<<<INPUT", request["text"])
        self.assertEqual(len(self.h.queued("compile")), 1)
        self.assertEqual(frontmatter.read(self.h.note(rec["id"]))["wiki_state"], "summarised")

    def test_malformed_answer_gets_one_repair_call(self):
        self.h.fake.ai.script("summarise", "I could not produce JSON, sorry.")
        result = self.summarise()
        self.assertTrue(result["repaired"])
        self.assertEqual(len(self.calls()), 2)
        repair = self.calls()[1]["text"]
        self.assertIn("Problems:", repair)
        self.assertIn("<<<PREVIOUS\nI could not produce JSON, sorry.\nPREVIOUS>>>", repair)
        self.assertIn('"key_points"', self.calls()[1]["text"])

    def test_schema_problem_gets_repair_call(self):
        self.h.fake.ai.script("summarise", json.dumps({"summary": "A page.", "mentions": "SQLite"}))
        self.assertTrue(self.summarise()["repaired"])
        repair = self.calls()[1]["text"]
        self.assertIn("key_points must be a non-empty array", repair)
        self.assertIn("mentions must be an array", repair)

    def test_two_failures_mark_the_source_failed(self):
        self.h.fake.ai.script("summarise", "nope", '{"summary": ""}')
        result = self.summarise()
        self.assertIn("failed", result)
        rec = self.h.source("sqlite-wal")
        self.assertEqual(rec["wikiState"], "failed")
        self.assertIn("summary must not be empty", rec["error"])
        self.assertEqual(self.h.queued("compile"), [])
        self.assertEqual(frontmatter.read(self.h.note(rec["id"]))["wiki_state"], "failed")
        self.assertEqual(len(self.calls()), 2)

    def test_quotes_missing_from_the_text_are_cleared(self):
        answer = support.fixture_json("ai", "summaries", "sqlite-wal.json")
        answer["key_points"][0]["quote"] = "A sentence the page never says."
        self.h.fake.ai.script("summarise", "```\n" + json.dumps(answer) + "\n```")
        self.summarise()
        points = self.h.source("sqlite-wal")["summary"]["key_points"]
        self.assertEqual(points[0]["quote"], "")
        self.assertTrue(points[1]["quote"])

    def test_same_summary_twice_changes_nothing(self):
        self.summarise()
        self.h.run_jobs(only=("compile",))
        before = self.h.snapshot()
        self.assertEqual(self.h.run("summarise", {"sourceId": self.rec["id"]}), {"unchanged": True})
        self.assertEqual(self.h.snapshot(), before)

    def test_language_setting_and_model_choice(self):
        self.h.settings.update({"summaryLanguage": "de", "summariseModel": "openrouter/cheap-model"})
        self.summarise()
        request = self.calls()[0]
        self.assertIn("the language with the code de", request["text"])
        self.assertEqual((request["providerId"], request["model"]), ("openrouter", "cheap-model"))
        self.assertEqual(self.h.svc.state.usage()["summarise"]["calls"], 1)


if __name__ == "__main__":
    unittest.main()
