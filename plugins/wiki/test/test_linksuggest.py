"""Link suggestions in user notes, the daily cap and the one-open-per-note rule."""

import tempfile
import unittest

import support
from wiki import anchors
from fakehost import SPACE
from support import Harness

NOTES = {
    "Cache decision": "We use SQLite here.\n",
    "Mobile app": "The app stores data in sqlite files.\n",
    "Backend": "Tests run on SQLite. The write-ahead logging mode matters.\n",
    "Already linked": "See [[SQLite]] for details. SQLite again.\n",
    "Code only": "Run `SQLite` or open https://sqlite.org/SQLite.\n",
    "Emoji note": "Party 😀🎉 then SQLite rocks.\n",
    "WAL file": "The WAL file grows during bulk loads.\n",
}


class LinkSuggest(unittest.TestCase):
    def setUp(self):
        self.h = Harness(settings={"suggestionsPerDay": 20})
        self.ids = self.h.seed_user_notes(NOTES)
        self.h.pipeline(["sqlite-wal"])
        self.sqlite = self.h.svc.state.alias("SQLite")
        self.wal = self.h.svc.state.alias("Write-ahead logging")

    def suggest(self, page_id):
        return self.h.run("link-suggest", {"pageId": page_id})

    def only(self, title):
        return self.h.fake.open_suggestions(self.ids[title])

    def test_first_unlinked_mention_becomes_a_link(self):
        result = self.suggest(self.sqlite)
        self.assertEqual(result["created"], 4)
        [s] = self.only("Cache decision")
        [edit] = s["edits"]
        self.assertEqual(edit["text"], "[[SQLite]]")
        self.assertEqual(edit["anchor"], anchors.make(NOTES["Cache decision"], 7, 13))
        self.assertEqual(self.h.fake.apply_suggestion(s["id"]), "We use [[SQLite]] here.\n")
        [s] = self.only("Mobile app")
        self.assertEqual(s["edits"][0]["text"], "[[SQLite|sqlite]]")
        self.assertEqual(self.only("Already linked"), [])
        self.assertEqual(self.only("Code only"), [])
        self.assertEqual(result["skipped"][self.ids["Already linked"]], "already links the page")

    def test_offsets_count_utf16_units(self):
        self.suggest(self.sqlite)
        [s] = self.only("Emoji note")
        anchor = s["edits"][0]["anchor"]
        self.assertEqual(anchor["start"], NOTES["Emoji note"].index("SQLite") + 2)
        self.assertEqual(self.h.fake.apply_suggestion(s["id"]), "Party 😀🎉 then [[SQLite]] rocks.\n")

    def test_alias_mentions_link_with_a_label(self):
        self.suggest(self.wal)
        [s] = self.only("WAL file")
        self.assertEqual(s["edits"][0]["text"], "[[Write-ahead logging (concept)|WAL]]")

    def test_daily_cap(self):
        self.h.settings["suggestionsPerDay"] = 2
        first = self.suggest(self.sqlite)
        self.assertEqual(first["created"], 2)
        self.assertIn("daily limit reached", first["skipped"].values())
        self.assertEqual(self.suggest(self.sqlite), {"created": 0, "skipped": "daily limit reached"})
        self.assertEqual(len(self.h.fake.open_suggestions()), 2)
        self.h.clock.advance(days=1)
        self.assertEqual(self.suggest(self.sqlite)["created"], 2)
        self.assertEqual(len(self.h.fake.open_suggestions()), 4)

    def test_parallel_jobs_leave_one_suggestion_per_note(self):
        import threading
        import time

        create = self.h.fake.create_suggestion

        def slow_create(*args, **kwargs):
            time.sleep(0.05)
            return create(*args, **kwargs)

        self.h.fake.create_suggestion = slow_create
        jobs = [self.h.fake.create_job("link-suggest", payload={"pageId": p}, space_id=SPACE)
                for p in (self.sqlite, self.wal)]
        threads = [threading.Thread(target=self.h.run_job, args=(self.h.fake.jobs[j["id"]],)) for j in jobs]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(self.h.errors, [])
        self.assertEqual(len(self.only("Backend")), 1)

    def test_one_open_suggestion_per_note(self):
        self.suggest(self.sqlite)
        [backend] = self.only("Backend")
        result = self.suggest(self.wal)
        self.assertEqual(result["skipped"][self.ids["Backend"]], "an open suggestion exists")
        self.assertEqual(len(self.only("Backend")), 1)
        self.h.fake.suggestions[backend["id"]]["status"] = "discarded"
        plugin = __import__("wiki.app", fromlist=["create_plugin"]).create_plugin(
            data_dir=tempfile.mkdtemp(prefix="bw-events-"), env={})
        plugin.service.state = self.h.svc.state
        from markspace import Event, EventBatch

        [(pattern, handler)] = [e for e in plugin._events if e[0] == "suggestion.*"]
        handler(EventBatch(events=[Event.from_json({"id": "ev1", "type": "suggestion.discarded",
                                                    "data": {"suggestionId": backend["id"]}})],
                           pattern=pattern, delivery_id="d1", host=None))
        self.assertEqual(self.h.svc.state.open_suggestions(self.ids["Backend"]), {})
        self.assertEqual(self.suggest(self.wal)["created"], 1)
        [s] = self.only("Backend")
        self.assertEqual(s["edits"][0]["text"], "[[Write-ahead logging (concept)|write-ahead logging]]")


if __name__ == "__main__":
    unittest.main()
