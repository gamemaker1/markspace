"""Manifest, panels, commands, actions, the import estimate, Ask and File this answer."""

import unittest

import support
from markspace import ActionContext, CommandContext, PanelContext, User, ui, validate_command_response
from markspace.plugin import validate_manifest
from wiki import cost, frontmatter, panels
from wiki.manifest import MANIFEST, PURPOSES, route
from fakehost import SPACE
from support import Harness

ALICE = User("u-alice", "Alice")


def walk(node):
    if isinstance(node, dict):
        yield node
        for value in node.values():
            yield from walk(value)
    elif isinstance(node, list):
        for value in node:
            yield from walk(value)


def action_payload(tree, action_id):
    for node in walk(tree):
        action = node.get("action")
        if isinstance(action, dict) and action.get("id") == action_id:
            return action.get("payload") or {}
    return None


class Manifest(unittest.TestCase):
    def test_manifest_declares_everything_the_plugin_serves(self):
        validate_manifest(MANIFEST)
        self.assertEqual([p["id"] for p in MANIFEST["purposes"]], list(PURPOSES))
        self.assertEqual(route("compile"), "plugin.bookmark-wiki.compile")
        plugin = __import__("wiki.app", fromlist=["x"]).create_plugin(
            data_dir=__import__("tempfile").mkdtemp(prefix="bw-manifest-"), env={})
        self.assertEqual(set(plugin._handlers["jobs"]), {j["kind"] for j in MANIFEST["jobs"]})
        self.assertEqual(set(plugin._handlers["commands"]), {c["id"] for c in MANIFEST["commands"]})
        self.assertEqual(set(plugin._handlers["panels"]), {p["id"] for p in MANIFEST["panels"]})
        self.assertEqual(set(plugin._handlers["import"]), {i["id"] for i in MANIFEST["importers"]})
        self.assertEqual({s["job"] for s in MANIFEST["schedules"]}, {"lint", "refetch", "sync"})
        self.assertEqual(MANIFEST["commands"][0]["contexts"], ["link", "global"])


class Panels(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.h = Harness()
        cls.user_notes = cls.h.seed_user_notes()
        cls.h.pipeline(["sqlite-wal", "pg-wal"])

    def panel(self, item_id=None):
        context = {"spaceId": SPACE, **({"itemId": item_id, "kind": "note"} if item_id else {})}
        return PanelContext(id="wiki", context=context, user=ALICE, host=self.h.fake)

    def test_home_page(self):
        self.h.fake.secrets.update({"karakeep_url": "https://keep.example.com", "karakeep_token": "t"})
        tree = panels.home(self.h.svc, self.panel())
        ui.validate(tree)
        text = str(tree)
        self.assertIn("'Connected'", text)
        self.assertIn("preview-api", text)
        self.assertIn("Run lint now", text)
        self.assertIn("AI usage this month", text)
        self.assertEqual(action_payload(tree, "save-settings"), {})
        self.assertIn("'summariseModel', 'options'", str([(f["name"], k) for f in walk(tree) if f.get("type") == "select"
                                                          for k in f]))

    def test_rail_tabs(self):
        source = self.h.source("sqlite-wal")
        tree = panels.wiki(self.h.svc, self.panel(source["id"]))
        ui.validate(tree)
        self.assertEqual(action_payload(tree, "refetch-source"), {"sourceId": source["id"]})
        self.assertIn("Pages built from it", str(tree))
        page_id = self.h.svc.state.alias("PostgreSQL")
        tree = panels.wiki(self.h.svc, self.panel(page_id))
        ui.validate(tree)
        self.assertEqual(action_payload(tree, "regenerate-page"), {"pageId": page_id})
        tree = panels.wiki(self.h.svc, self.panel(self.user_notes["Storage design notes"]))
        ui.validate(tree)
        self.assertIn("Related wiki pages", str(tree))
        self.assertIn("'SQLite'", str(tree))
        ui.validate(panels.wiki(self.h.svc, self.panel()))

    def test_regenerate_page_queues_a_restricted_compile(self):
        page_id = self.h.svc.state.alias("SQLite")
        ctx = ActionContext(id="regenerate-page", context={"spaceId": SPACE}, user=ALICE, host=self.h.fake,
                            payload={"pageId": page_id})
        resp = panels.action_regenerate_page(self.h.svc, ctx)
        validate_command_response(resp)
        job = self.h.fake.jobs[resp["job"]["id"]]
        self.assertEqual(job["payload"]["only"], [page_id])
        job["status"] = "canceled"


class ImportEstimate(unittest.TestCase):
    def test_file_preview_confirm_and_idempotent_queue(self):
        h = Harness()
        h.import_pages(["raft-site"])
        file_id = h.fake.add_file("bookmarks.html", support.fixture_text("bookmarks", "chrome.html").encode())
        resp = panels.importer_preview(h.svc, h.fake, "netscape", file_id, SPACE)
        validate_command_response(resp)
        text = str(resp)
        self.assertIn("5 bookmarks from Browser bookmarks. 3 are new, 1 are already in the wiki", text)
        self.assertIn("The estimated cost is $", text)
        payload = action_payload(resp, "confirm-import")
        ctx = ActionContext(id="confirm-import", context={"spaceId": SPACE}, user=ALICE, host=h.fake, payload=payload)
        first = panels.action_confirm_import(h.svc, ctx)
        self.assertEqual(panels.action_confirm_import(h.svc, ctx), first)
        self.assertEqual(len(h.queued("import")), 1)
        h.run_jobs(only=("import",))
        self.assertEqual(len([n for n in h.fake.all_notes() if n["path"].startswith("Sources/")]), 4)
        self.assertEqual(h.svc.state.runs()[0]["jobId"], first["job"]["id"])
        ui.validate(panels.home(h.svc, PanelContext(id="home", context={}, user=ALICE, host=h.fake)))

    def test_cost_estimate_numbers(self):
        h = Harness()
        est = cost.estimate(h.svc, 10)
        self.assertEqual((est["inputTokens"], est["outputTokens"], est["pagesPerSource"]), (510000, 125000, 8.0))
        self.assertEqual(est["cost"], 0.44)
        self.assertEqual(est["summariseModel"], "cheap-model (openrouter)")
        h.svc.state.record_pages_per_source(2)
        h.svc.state.record_pages_per_source(4)
        self.assertEqual(cost.estimate(h.svc, 1)["inputTokens"], 3000 + 3 * 6000)
        h.fake.models = []
        self.assertIsNone(cost.estimate(h.svc, 1)["cost"])
        self.assertIn("the cost is unknown", cost.describe(cost.estimate(h.svc, 1)))


class Commands(unittest.TestCase):
    def setUp(self):
        self.h = Harness()

    def command(self, context):
        return CommandContext(id="add-url", context={"spaceId": SPACE, **context}, user=ALICE, host=self.h.fake)

    def test_add_url_from_a_link_queues_fetch_through_import(self):
        resp = panels.command_add_url(self.h.svc, self.command({"link": "https://raft.github.io/"}))
        validate_command_response(resp)
        self.h.run_jobs(only=("import",))
        self.assertEqual(len(self.h.queued("fetch")), 1)

    def test_add_url_without_a_link_shows_a_form(self):
        resp = panels.command_add_url(self.h.svc, self.command({}))
        validate_command_response(resp)
        self.assertEqual(resp["ui"]["type"], "form")
        bad = panels.action_add_url(self.h.svc, ActionContext(id="add-url", context={}, user=ALICE, host=self.h.fake,
                                                              values={"url": "ftp://x"}))
        self.assertIn("That is not a web address", str(bad))

    def test_settings_form_saves_valid_values(self):
        ctx = ActionContext(id="save-settings", context={}, user=ALICE, host=self.h.fake,
                            values={"maxPagesPerSource": "abc", "suggestionsPerDay": "5", "wikiFolder": "Sources"})
        resp = panels.action_save_settings(self.h.svc, ctx)
        validate_command_response(resp)
        self.assertIn("maxPagesPerSource: Enter a whole number.", str(resp))
        self.assertIn("wikiFolder: The wiki folder must differ", str(resp))
        self.assertEqual(self.h.svc.settings().suggestionsPerDay, 5)
        self.assertEqual(self.h.svc.settings().maxPagesPerSource, 12)

    def test_pause_and_resume(self):
        ctx = ActionContext(id="pause", context={}, user=ALICE, host=self.h.fake)
        validate_command_response(panels.action_pause(self.h.svc, ctx))
        self.assertTrue(self.h.svc.state.paused())
        self.assertIn("Resume jobs", str(panels.home(self.h.svc, ctx)))
        panels.action_resume(self.h.svc, ctx)
        self.assertFalse(self.h.svc.state.paused())


class Ask(unittest.TestCase):
    def test_ask_and_file_the_answer(self):
        h = Harness()
        h.pipeline(["sqlite-wal", "pg-wal"])
        ctx = ActionContext(id="ask", context={"spaceId": SPACE}, user=ALICE, host=h.fake,
                            values={"question": "What does write-ahead logging do?"})
        resp = panels.action_ask(h.svc, ctx)
        validate_command_response(resp)
        payload = action_payload(resp, "file-answer")
        self.assertTrue(payload["cited"])
        request = h.fake.ai.calls("ask")[0]
        self.assertEqual(request["route"], "plugin.bookmark-wiki.ask")
        ctx = ActionContext(id="file-answer", context={"spaceId": SPACE}, user=ALICE, host=h.fake, payload=payload)
        filed = panels.action_file_answer(h.svc, ctx)
        validate_command_response(filed)
        answers = [n for n in h.fake.all_notes() if n["path"].startswith("Wiki/Answers/")]
        self.assertEqual(len(answers), 1)
        fm = frontmatter.read(answers[0])
        self.assertEqual(fm["type"], "answer")
        self.assertEqual(fm["question"], "What does write-ahead logging do?")
        for title in payload["cited"]:
            self.assertIn(f"- [[{title}]]", answers[0]["content"])
        self.assertIn(f"[[{answers[0]['title']}]]", h.fake.note_by_path("Wiki/index.md")["content"])
        self.assertIn("Already filed as", str(panels.action_file_answer(h.svc, ctx)))
        self.assertEqual(len([n for n in h.fake.all_notes() if n["path"].startswith("Wiki/Answers/")]), 1)

    def test_empty_question(self):
        h = Harness()
        resp = panels.action_ask(h.svc, ActionContext(id="ask", context={}, user=ALICE, host=h.fake, values={}))
        self.assertIn("Enter a question.", str(resp))


if __name__ == "__main__":
    unittest.main()
