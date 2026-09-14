"""Behaviours of the real Markspace host that the plugin relies on: frontmatter, settings, robots and the job cap."""

import tempfile
import unittest

import support
from markspace import HostError
from wiki import sources
from wiki.app import create_plugin
from wiki.urls import normalise_url
from fakehost import SPACE, FakeMarkspace
from support import Harness


class Frontmatter(unittest.TestCase):
    def test_source_frontmatter_goes_in_the_frontmatter_object(self):
        h = Harness()
        h.import_pages(["sqlite-wal"])
        url = h.fake.web.by_id["sqlite-wal"]["url"]
        note = h.fake.note(h.svc.state.url_note(normalise_url(url)))
        self.assertFalse(note["content"].startswith("---"))
        self.assertEqual(note["extra"]["type"], "source")
        self.assertEqual(note["extra"]["generated_by"], "bookmark-wiki")
        self.assertIn("source_author", note["extra"])
        for owned in ("author", "created", "updated", "title", "tags"):
            self.assertNotIn(owned, note["extra"])
        self.assertTrue(h.svc.notes.writable(note))


class FolderNotes(unittest.TestCase):
    def test_wiki_index_is_the_folder_note_of_the_wiki_folder(self):
        h = Harness()
        h.svc.files.ensure_all(SPACE)
        index = h.fake.note_by_path("Wiki/index.md")
        self.assertIsNotNone(index)
        self.assertIsNone(h.fake.note_by_path("Wiki/index-note.md"))
        self.assertEqual(h.svc.files.ensure_index(SPACE)["id"], index["id"])

    def test_folder_note_rules(self):
        fake = FakeMarkspace()
        folder = fake.create_folder(SPACE, "Docs")
        plain = fake.create_note(SPACE, "index", parent_id=folder["id"])
        self.assertEqual(plain["path"], "Docs/index-note.md")
        note = fake.create_note(SPACE, "Docs", folder_note=True, folder_id=folder["id"])
        self.assertEqual(note["path"], "Docs/index.md")
        with self.assertRaises(HostError) as cm:
            fake.create_note(SPACE, "Again", folder_note=True, folder_id=folder["id"])
        self.assertEqual(cm.exception.status, 409)
        with self.assertRaises(HostError) as cm:
            fake.create_note(SPACE, "No folder", folder_note=True)
        self.assertEqual(cm.exception.status, 400)


class QueueCap(unittest.TestCase):
    def test_import_waits_while_the_job_queue_is_full(self):
        h = Harness()
        waits = []
        real_sleep, real_enqueue = sources.sleep, h.svc.enqueue
        refusals = {"left": 2}

        def enqueue(kind, payload, **kwargs):
            if kind == "fetch" and refusals["left"]:
                refusals["left"] -= 1
                raise HostError(429, "index: module has 10000 queued jobs: job queue full")
            return real_enqueue(kind, payload, **kwargs)

        sources.sleep = waits.append
        h.svc.enqueue = enqueue
        try:
            h.import_pages(["sqlite-wal"])
        finally:
            sources.sleep = real_sleep
        self.assertEqual(waits, [sources.QUEUE_WAIT, sources.QUEUE_WAIT])
        self.assertEqual(len(h.queued("fetch")), 1)


class Settings(unittest.TestCase):
    def test_settings_come_from_get_settings_and_refresh_on_the_event(self):
        fake = FakeMarkspace()
        plugin = create_plugin(data_dir=tempfile.mkdtemp(prefix="bookmark-wiki-settings-"), env={})
        plugin.host = lambda token=None: fake
        fake.admin_settings = {"space": SPACE, "wikiFolder": "Knowledge"}
        svc = plugin.service
        self.assertEqual(svc.settings().wikiFolder, "Knowledge")
        fake.admin_settings = {"space": SPACE, "wikiFolder": "Garden"}
        handler = next(fn for pattern, fn in plugin._events if pattern == "plugin.settings.updated")
        handler(None)
        self.assertEqual(svc.settings().wikiFolder, "Garden")


class Robots(unittest.TestCase):
    def test_refusal_is_recognised_by_its_code(self):
        self.assertTrue(sources._robots_refusal(HostError(403, "robots.txt disallows this URL", None, "robots_disallowed")))
        self.assertTrue(sources._robots_refusal(HostError(403, "Forbidden", None, "robots_disallowed")))
        self.assertFalse(sources._robots_refusal(HostError(403, "Forbidden")))


if __name__ == "__main__":
    unittest.main()
