"""The write rule. Notes edited by the user, and notes the plugin did not generate, only receive suggestions."""

import json
import unittest

import support
from wiki import frontmatter
from fakehost import PLUGIN_USER, SPACE, USER
from support import Harness


class UserEdits(unittest.TestCase):
    def setUp(self):
        self.h = Harness()
        self.user_notes = self.h.seed_user_notes()

    def test_user_edited_page_gets_a_suggestion_not_a_write(self):
        self.h.pipeline(["sqlite-wal"])
        page = self.h.page("SQLite")
        body = frontmatter.body_of(page["content"])
        edited_body = body.replace("\n\n## Related", "\n- My own note: we ship SQLite in the desktop app.\n\n## Related")
        self.h.fake.user_edit(page["id"], edited_body)
        edited = self.h.fake.note(page["id"])
        self.h.pipeline(["sqlite-when"])
        self.assertEqual(self.h.fake.note(page["id"]), edited)
        suggestions = self.h.fake.open_suggestions(page["id"])
        self.assertEqual(len(suggestions), 1)
        self.assertEqual(suggestions[0]["title"], "Update SQLite from Appropriate Uses For SQLite")
        self.assertEqual(suggestions[0]["authorId"], PLUGIN_USER)
        new_body = self.h.fake.apply_suggestion(suggestions[0]["id"])
        self.assertIn("We ship SQLite in the desktop app".lower(), new_body.lower())
        self.assertIn("([[Appropriate Uses For SQLite]])", new_body)
        log = self.h.fake.note_by_path("Wiki/log.md")["content"]
        self.assertIn("[[Appropriate Uses For SQLite]] · created 2, updated 0, suggested 1", log)

    def test_compiling_again_replaces_the_open_suggestion(self):
        self.h.pipeline(["sqlite-wal"])
        page = self.h.page("SQLite")
        self.h.fake.user_edit(page["id"], frontmatter.body_of(page["content"]) + "\nUser line.\n")
        self.h.pipeline(["sqlite-when"])
        first = self.h.fake.open_suggestions(page["id"])
        rec = self.h.source("sqlite-when")
        self.assertEqual(self.h.run("compile", {"sourceId": rec["id"]}), {"unchanged": True})
        self.assertEqual(self.h.fake.open_suggestions(page["id"]), first)
        self.h.run("compile", {"sourceId": rec["id"], "force": True, "only": [page["id"]]})
        self.assertEqual(self.h.fake.open_suggestions(page["id"]), first)

    def test_index_edited_by_the_user_gets_a_suggestion(self):
        self.h.pipeline(["sqlite-wal"])
        index = self.h.fake.note_by_path("Wiki/index.md")
        self.h.fake.user_edit(index["id"], "Pages I care about.\n\n" + frontmatter.body_of(index["content"]))
        edited = self.h.fake.note(index["id"])
        self.h.pipeline(["pg-wal"])
        self.assertEqual(self.h.fake.note(index["id"]), edited)
        [suggestion] = self.h.fake.open_suggestions(index["id"])
        body = self.h.fake.apply_suggestion(suggestion["id"])
        self.assertTrue(body.startswith("Pages I care about."))
        self.assertIn("- [[PostgreSQL]]: ", body)

    def test_log_edited_by_the_user_collects_lines_in_one_suggestion(self):
        self.h.pipeline(["sqlite-wal"])
        log = self.h.fake.note_by_path("Wiki/log.md")
        self.h.fake.user_edit(log["id"], "# My log\n")
        self.h.pipeline(["pg-wal", "sqlite-when"])
        [suggestion] = self.h.fake.open_suggestions(log["id"])
        body = self.h.fake.apply_suggestion(suggestion["id"])
        self.assertIn("compile · [[Reliability and the Write-Ahead Log]]", body)
        self.assertIn("compile · [[Appropriate Uses For SQLite]]", body)

    def test_seeded_user_notes_are_never_written(self):
        before = {i: self.h.fake.note(i) for i in self.user_notes.values()}
        self.h.pipeline(["sqlite-wal", "pg-wal", "raft-site", "attention", "quadlet"], exclude=())
        for note_id, note in before.items():
            self.assertEqual(self.h.fake.note(note_id), note)
        self.assertFalse([w for w in self.h.fake.writes if w[0] in before and w[2] == PLUGIN_USER])
        suggested = {s["noteId"] for s in self.h.fake.open_suggestions()}
        self.assertEqual(suggested & set(before), set(before))

    def test_source_note_edited_by_the_user(self):
        self.h.pipeline(["sqlite-wal"])
        rec = self.h.source("sqlite-wal")
        note = self.h.note(rec["id"])
        self.h.fake.user_edit(rec["id"], frontmatter.body_of(note["content"]) + "\nMy highlight.\n")
        edited = self.h.note(rec["id"])
        answer = support.fixture_json("ai", "summaries", "sqlite-wal.json")
        answer["summary"] = "A new summary of the WAL page."
        self.h.fake.ai.script("summarise", json.dumps(answer))
        self.h.run("summarise", {"sourceId": rec["id"], "force": True})
        self.assertEqual(self.h.note(rec["id"]), edited)
        [suggestion] = self.h.fake.open_suggestions(rec["id"])
        self.assertIn("A new summary of the WAL page.", json.dumps(suggestion["edits"]))

    def test_wiki_note_without_generated_by_only_gets_suggestions(self):
        with self.h.fake.as_user(USER):
            wiki = self.h.fake.create_folder(SPACE, "Wiki")["id"]
            concepts = self.h.fake.create_folder(SPACE, "Concepts", parent_id=wiki)["id"]
            mine = self.h.fake.create_note(SPACE, "Checkpointing", "Checkpointing is my topic.\n", parent_id=concepts)
        self.h.pipeline(["sqlite-wal"])
        self.assertEqual(self.h.fake.note(mine["id"])["content"], "Checkpointing is my topic.\n")
        self.assertEqual(self.h.svc.state.alias("Checkpointing"), mine["id"])
        [suggestion] = self.h.fake.open_suggestions(mine["id"])
        self.assertIn("([[Write-Ahead Logging]])", self.h.fake.apply_suggestion(suggestion["id"]))


if __name__ == "__main__":
    unittest.main()
