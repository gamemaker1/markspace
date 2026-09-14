"""Importer parsing for Netscape exports, URL lists, Karakeep and Linkding, and the scheduled sync job."""

import unittest
from urllib.parse import parse_qs, urlsplit

import support
from wiki.importers import ImporterError, netscape, urllist
from wiki.importers.karakeep import KarakeepClient
from wiki.importers.linkding import LinkdingClient
from wiki.urls import normalise_url
from support import Harness


def rows(bookmarks):
    return [(b.url, b.title, b.folder, b.added, b.tags) for b in bookmarks]


class NetscapeExports(unittest.TestCase):
    def test_chrome(self):
        got = netscape.parse(support.fixture_text("bookmarks", "chrome.html"))
        self.assertEqual(rows(got), [
            ("https://www.sqlite.org/wal.html", "Write-Ahead Logging", "Bookmarks bar", "2024-03-02T10:14:00Z", []),
            ("https://www.postgresql.org/docs/current/wal-intro.html?utm_source=chrome&utm_medium=bookmark",
             "PostgreSQL: Reliability & the Write-Ahead Log", "Bookmarks bar/Databases", "2024-03-02T10:15:00Z", []),
            ("https://sqlite.org/whentouse.html", "Appropriate Uses For SQLite", "Bookmarks bar/Databases/SQLite",
             "2024-03-02T10:16:40Z", []),
            ("https://raft.github.io/", "The Raft Consensus Algorithm", "Other bookmarks", "2024-03-02T10:20:00Z", []),
            ("https://www.sqlite.org/wal.html#ckpt", "WAL checkpoints", "Other bookmarks", "2024-03-02T10:21:40Z", []),
        ])
        self.assertEqual(normalise_url(got[0].url), normalise_url(got[4].url))

    def test_firefox(self):
        got = netscape.parse(support.fixture_text("bookmarks", "firefox.html"))
        self.assertEqual(rows(got), [
            ("https://arxiv.org/abs/1706.03762", "Attention Is All You Need", "Machine learning",
             "2024-03-02T10:14:00Z", ["transformers", "papers"]),
            ("https://gist.github.com/karpathy/llm-wiki", "LLM Wiki", "Machine learning", "2024-03-02T10:14:10Z",
             ["wiki", "llm"]),
            ("https://etcd.io/docs/v3.5/learning/why/", "Why etcd", "Bookmarks Toolbar", "2024-03-02T10:14:20Z", []),
            ("https://go.dev/blog/go1.13-errors?fbclid=IwAR0abc", "Working with Errors in Go 1.13", "Other Bookmarks",
             "2024-03-02T10:14:30Z", ["go"]),
        ])

    def test_safari(self):
        got = netscape.parse(support.fixture_text("bookmarks", "safari.html"))
        self.assertEqual(rows(got), [
            ("https://htmx.org/essays/hypermedia-driven-applications/", "Hypermedia-Driven Applications", "Favorites", "", []),
            ("https://zettelkasten.de/introduction/", "Introduction to the Zettelkasten Method", "Favorites/Notes", "", []),
            ("https://help.obsidian.md/links", "Internal links – Obsidian Help", "Favorites/Notes", "", []),
            ("https://www.theatlantic.com/magazine/archive/1945/07/as-we-may-think/303881/", "As We May Think",
             "Reading List", "", []),
        ])

    def test_e2e_export_has_30_bookmarks(self):
        got = netscape.parse(support.fixture_text("bookmarks", "e2e-30.html"))
        self.assertEqual(len(got), 30)
        self.assertEqual(len({normalise_url(b.url) for b in got}), 30)
        self.assertIn("Distributed systems/Papers", {b.folder for b in got})

    def test_not_an_export(self):
        with self.assertRaises(ImporterError):
            netscape.parse("just some text")


class URLList(unittest.TestCase):
    def test_lines_and_comments(self):
        got = urllist.parse(support.fixture_text("bookmarks", "urls.txt"))
        self.assertEqual([(b.url, b.title) for b in got], [
            ("https://www.sqlite.org/wal.html", ""),
            ("https://WWW.SQLITE.ORG/wal.html?utm_source=newsletter", ""),
            ("https://raft.github.io/", "The Raft site"),
            ("not-a-url", ""),
            ("https://kubernetes.io/docs/concepts/overview/", ""),
        ])


def karakeep_transport(calls):
    def transport(url, headers):
        calls.append((url, headers))
        parts = urlsplit(url)
        query = parse_qs(parts.query)
        files = {"/api/v1/lists": "lists.json", "/api/v1/lists/l1/bookmarks": "list-l1.json",
                 "/api/v1/lists/l2/bookmarks": "list-l2.json"}
        if parts.path == "/api/v1/bookmarks":
            name = "bookmarks-2.json" if query.get("cursor") == ["cursor-page-2"] else "bookmarks-1.json"
        else:
            name = files[parts.path]
        return support.fixture_json("api", "karakeep", name)
    return transport


def linkding_transport(calls):
    def transport(url, headers):
        calls.append((url, headers))
        parts = urlsplit(url)
        assert parts.path == "/api/bookmarks/", parts.path
        offset = int(parse_qs(parts.query).get("offset", ["0"])[0])
        return support.fixture_json("api", "linkding", "page-1.json" if offset == 0 else "page-2.json")
    return transport


class Karakeep(unittest.TestCase):
    def test_pages_through_bookmarks_with_tags_and_lists(self):
        calls = []
        result = KarakeepClient("https://keep.example.com/", "tok", karakeep_transport(calls)).fetch()
        self.assertEqual([(b.url, b.title, b.tags, b.lists) for b in result.bookmarks], [
            ("https://www.sqlite.org/wal.html", "WAL in SQLite", ["sqlite", "storage"], ["Read later"]),
            ("https://www.postgresql.org/docs/current/wal-intro.html",
             "PostgreSQL: Documentation: 30.3. Write-Ahead Logging (WAL)", ["postgresql"], ["Databases", "Read later"]),
            ("https://sqlite.org/whentouse.html?utm_campaign=karakeep", "When to use SQLite", ["sqlite"], ["Databases"]),
        ])
        self.assertEqual(result.newest, "2026-09-10T08:00:00.000Z")
        self.assertTrue(result.complete)
        self.assertTrue(all(h["Authorization"] == "Bearer tok" for _, h in calls))
        self.assertTrue(any("cursor=cursor-page-2" in url for url, _ in calls))

    def test_since_stops_paging(self):
        calls = []
        result = KarakeepClient("https://keep.example.com", "tok", karakeep_transport(calls)).fetch(
            since="2026-09-05T00:00:00.000Z")
        self.assertEqual([b.url for b in result.bookmarks],
                         ["https://www.sqlite.org/wal.html", "https://www.postgresql.org/docs/current/wal-intro.html"])
        self.assertFalse(any("cursor-page-2" in url for url, _ in calls))

    def test_needs_secrets(self):
        with self.assertRaises(ImporterError):
            KarakeepClient("", "tok")


class Linkding(unittest.TestCase):
    def test_pages_through_bookmarks(self):
        calls = []
        result = LinkdingClient("https://links.example.com", "tok", linkding_transport(calls)).fetch()
        self.assertEqual([(b.url, b.title, b.tags) for b in result.bookmarks], [
            ("https://raft.github.io/", "The Raft Consensus Algorithm", ["consensus", "raft"]),
            ("https://etcd.io/docs/v3.5/learning/why/?ref=hn", "Why etcd", ["etcd"]),
            ("https://paxos.example.org/made-simple", "Paxos Made Simple", []),
        ])
        self.assertEqual(calls[1][0], "https://links.example.com/api/bookmarks/?limit=100&offset=2")
        self.assertTrue(all(h["Authorization"] == "Token tok" for _, h in calls))
        self.assertEqual(result.newest, "2026-09-08T10:00:00.000000Z")

    def test_since_filters_and_sends_added_since(self):
        calls = []
        result = LinkdingClient("https://links.example.com", "tok", linkding_transport(calls)).fetch(
            since="2026-09-05T00:00:00Z")
        self.assertEqual([b.url for b in result.bookmarks], ["https://raft.github.io/"])
        self.assertIn("added_since=2026-09-05T00%3A00%3A00Z", calls[0][0])


class SyncJob(unittest.TestCase):
    def test_schedule_imports_new_bookmarks_every_n_hours(self):
        h = Harness()
        calls = []
        h.fake.secrets.update({"karakeep_url": "https://keep.example.com", "karakeep_token": "tok"})
        h.svc.transport = karakeep_transport(calls)
        first = h.run("sync", {})
        self.assertEqual(first["karakeep"]["new"], 3)
        self.assertEqual(first["linkding"], "not configured")
        self.assertEqual(len(h.queued("fetch")), 3)
        self.assertEqual(h.run("sync", {})["karakeep"], "not due")
        h.clock.advance(hours=7)
        again = h.run("sync", {})
        self.assertEqual(again["karakeep"]["new"], 0)
        self.assertEqual(len(h.queued("fetch")), 3)
        rec = h.svc.state.source(h.svc.state.url_note("https://postgresql.org/docs/current/wal-intro.html"))
        self.assertEqual(rec["lists"], ["Databases", "Read later"])
        self.assertEqual(rec["importer"], "karakeep")


if __name__ == "__main__":
    unittest.main()
