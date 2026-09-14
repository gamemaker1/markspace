"""URL normalisation, frontmatter, anchors and JSON parsing."""

import json
import unittest

import support
from wiki import anchors, frontmatter, wikilinks
from wiki.jsonparse import ModelOutputError, extract_json
from wiki.urls import ID_ALPHABET, InvalidURL, normalise_url, site_of, slugify, stable_id

NORMALISE = [
    ("HTTPS://WWW.Example.COM/Path/", "https://example.com/Path"),
    ("http://example.com:80/", "http://example.com/"),
    ("https://example.com:443/a", "https://example.com/a"),
    ("https://example.com:8443/a", "https://example.com:8443/a"),
    ("http://example.com:443/a", "http://example.com:443/a"),
    ("https://example.com/a#section-2", "https://example.com/a"),
    ("https://example.com", "https://example.com/"),
    ("https://example.com/", "https://example.com/"),
    ("https://example.com/a//", "https://example.com/a/"),
    ("https://example.com/?utm_source=x&b=2&a=1&fbclid=abc", "https://example.com/?a=1&b=2"),
    ("https://example.com/a/?ref=hn&ref_src=twsrc", "https://example.com/a"),
    ("https://example.com/?UTM_Medium=x&gclid=1&mc_cid=2&mc_eid=3&q=rust", "https://example.com/?q=rust"),
    ("https://example.com/?b=2&a=3&a=1", "https://example.com/?a=1&a=3&b=2"),
    ("https://example.com/?q=a%20b&utm_campaign=z", "https://example.com/?q=a%20b"),
    ("https://example.com/?reference=1", "https://example.com/?reference=1"),
    ("https://example.com/Case/Stays", "https://example.com/Case/Stays"),
    ("https://www2.example.com/", "https://www2.example.com/"),
    ("https://user:secret@Example.com/x", "https://example.com/x"),
    ("  https://example.com/a  ", "https://example.com/a"),
    ("https://[::1]:8080/x", "https://[::1]:8080/x"),
    ("https://example.com./a", "https://example.com/a"),
    ("https://example.com/?", "https://example.com/"),
    ("https://example.com/?&&x=1&", "https://example.com/?x=1"),
]

INVALID = ["", "ftp://example.com/file", "javascript:alert(1)", "not a url", "https:///nohost", "mailto:a@b.c",
           "https://exa mple.com/"]


class NormaliseTable(unittest.TestCase):
    def test_table(self):
        for raw, want in NORMALISE:
            with self.subTest(raw=raw):
                self.assertEqual(normalise_url(raw), want)

    def test_normalised_form_is_stable(self):
        for raw, want in NORMALISE:
            if want.endswith("//") or want.endswith("/") and len(want.split("/", 3)[3]) > 0:
                continue  # dropping one trailing slash is not idempotent by design
            with self.subTest(raw=raw):
                self.assertEqual(normalise_url(want), want)

    def test_invalid(self):
        for raw in INVALID:
            with self.subTest(raw=raw), self.assertRaises(InvalidURL):
                normalise_url(raw)

    def test_variants_dedupe(self):
        variants = ["https://www.sqlite.org/wal.html", "https://sqlite.org/wal.html#ckpt",
                    "HTTPS://WWW.SQLITE.ORG/wal.html?utm_source=newsletter"]
        self.assertEqual({normalise_url(v) for v in variants}, {"https://sqlite.org/wal.html"})

    def test_titles_stay_name_wikilinks(self):
        from wiki.sources import clean_title

        self.assertEqual(clean_title("asyncio: Asynchronous I/O"), "asyncio: Asynchronous I-O")
        self.assertEqual(clean_title(" [Draft] a|b #1 "), "(Draft) a-b 1")
        raw = "a/b\\c"
        self.assertEqual(wikilinks.links("[[" + clean_title(raw) + "]]")[0].target, "a-b-c")

    def test_site_slug_and_ids(self):
        self.assertEqual(site_of("https://www.sqlite.org/wal.html"), "sqlite.org")
        self.assertEqual(slugify("Café au lait: a history!"), "cafe-au-lait-a-history")
        self.assertEqual(slugify("???"), "untitled")
        a = stable_id("source", "space1", "https://sqlite.org/wal.html")
        self.assertEqual(len(a), 26)
        self.assertTrue(set(a) <= set(ID_ALPHABET))
        self.assertEqual(a, stable_id("source", "space1", "https://sqlite.org/wal.html"))
        self.assertNotEqual(a, stable_id("source", "space2", "https://sqlite.org/wal.html"))


class AnchorVectors(unittest.TestCase):
    vectors = json.loads((support.ROOT / "test" / "vectors" / "anchors.json").read_text(encoding="utf-8"))

    def test_context_size(self):
        self.assertEqual(self.vectors["context"], anchors.CONTEXT)

    def test_make_cases(self):
        for case in self.vectors["make"]:
            with self.subTest(case["name"]):
                self.assertEqual(anchors.make(case["body"], case["start"], case["end"]), case["want"])

    def test_fake_host_resolves_like_the_vectors(self):
        from fakehost import resolve_anchor

        for case in self.vectors["resolve"]:
            if not case["anchor"].get("exact"):
                continue  # the fake treats an empty exact as a suggestion insertion point
            with self.subTest(case["name"]):
                want = case["want"]
                got = resolve_anchor(case["body"], case["anchor"])
                self.assertEqual(got, None if want["state"] == "orphaned" else (want["start"], want["end"]))

    def test_python_indices_become_utf16_offsets(self):
        body = "x😀 SQLite and SQLite"
        start = body.index("SQLite")
        anchor = anchors.for_indices(body, start, start + 6)
        self.assertEqual((anchor["start"], anchor["end"]), (4, 10))
        self.assertEqual(anchor["prefix"], "x😀 ")
        self.assertEqual(anchor["exact"], "SQLite")

    def test_insertion_anchor(self):
        body = "a" * 40 + "😀tail"
        anchor = anchors.insertion_at_index(body, 40)
        self.assertEqual(anchor["exact"], "")
        self.assertEqual(anchor["start"], anchor["end"])
        self.assertEqual(anchor["prefix"], "a" * 32)
        self.assertEqual(anchor["suffix"], "😀tail")


class JSONParsing(unittest.TestCase):
    def test_plain_object(self):
        self.assertEqual(extract_json('{"a": 1}'), {"a": 1})

    def test_code_fence_with_prose_and_trailing_text(self):
        text = 'Sure, here it is:\n```json\n{"summary": "x {y}", "n": [1, 2]}\n```\nAnything else?'
        self.assertEqual(extract_json(text), {"summary": "x {y}", "n": [1, 2]})

    def test_unterminated_fence_and_trailing_text(self):
        self.assertEqual(extract_json('```json\n{"a": "b"}\nThat is all.'), {"a": "b"})

    def test_skips_broken_braces_before_the_object(self):
        self.assertEqual(extract_json('Use {curly} braces. {"ok": true} done'), {"ok": True})

    def test_array_wanted(self):
        self.assertEqual(extract_json("Result: [1, 2] and {\"x\": 1}", want=list), [1, 2])

    def test_malformed(self):
        for text in ["", "no json here", '{"a": 1,', "```json\n{broken\n```", None]:
            with self.subTest(text=text), self.assertRaises(ModelOutputError):
                extract_json(text)


class Frontmatter(unittest.TestCase):
    def test_round_trip(self):
        fields = {"title": 'How "SQLite" uses: write-ahead logging', "type": "source",
                  "source_url": "https://www.sqlite.org/wal.html", "fetched": "2026-09-13T11:02:00Z",
                  "bookmark_tags": ["sqlite", "storage, cache"], "sources": ["[[How SQLite uses WAL]]"],
                  "contested": False, "findings": 3, "author": "", "note": "true", "hash": "a # b"}
        text = frontmatter.dump(fields)
        self.assertIn("fetched: 2026-09-13T11:02:00Z\n", text)
        self.assertIn("source_url: https://www.sqlite.org/wal.html\n", text)
        yaml_text, body = frontmatter.split(text + "Body\n")
        self.assertEqual(body, "Body\n")
        self.assertEqual(frontmatter.parse(yaml_text), fields)

    def test_split_rules(self):
        self.assertEqual(frontmatter.split("---\ntitle: x\n---\nbody"), ("title: x\n", "body"))
        self.assertEqual(frontmatter.split("---\r\ntitle: x\r\n...\r\nbody"), ("title: x\r\n", "body"))
        self.assertEqual(frontmatter.split("---\ntitle: x\nno close"), (None, "---\ntitle: x\nno close"))
        self.assertEqual(frontmatter.split("body only"), (None, "body only"))

    def test_hand_written_forms(self):
        parsed = frontmatter.parse("tags:\n  - a\n  - 'b c'\nstatus: ok   # pending, ok\nflag: yes\nempty:\n")
        self.assertEqual(parsed, {"tags": ["a", "b c"], "status": "ok", "flag": True, "empty": []})

    def test_read_merges_extra(self):
        note = {"content": "---\ntitle: a\n---\nx", "extra": {"generated_by": "bookmark-wiki", "title": "b"}}
        self.assertEqual(frontmatter.read(note), {"generated_by": "bookmark-wiki", "title": "a"})


class Wikilinks(unittest.TestCase):
    def test_citations_and_links(self):
        body = "SQLite is small ([[A]], [[B|b]]). See [[C#Part]] and ![[pic.png]]."
        self.assertEqual(wikilinks.citations(body), ["A", "B"])
        self.assertEqual(wikilinks.non_citation_targets(body), ["C", "pic.png"])

    def test_mentions_skip_code_links_and_headings(self):
        body = "# SQLite\n\n`SQLite` [[SQLite]] https://sqlite.org/SQLite [x](SQLite)\n\nWe like sqlite."
        start, end, text = wikilinks.find_mention(body, ["SQLite"])
        self.assertEqual(text, "sqlite")
        self.assertEqual(body[start:end], "sqlite")
        self.assertIsNone(wikilinks.find_mention("SQLiteDB is different", ["SQLite"]))
        self.assertEqual(wikilinks.find_mention("PostgreSQL WAL is here", ["PostgreSQL", "PostgreSQL WAL"])[2],
                         "PostgreSQL WAL")


if __name__ == "__main__":
    unittest.main()
