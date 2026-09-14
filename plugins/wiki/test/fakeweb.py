"""Web fetch from fixture pages. No test touches the network."""

from __future__ import annotations

import base64
import hashlib
import json
from html.parser import HTMLParser
from pathlib import Path
from typing import Any

from markspace import HostError

FIXTURES = Path(__file__).resolve().parent / "fixtures"
BLOCKS = {"p": "", "h1": "# ", "h2": "## ", "h3": "### ", "li": "- ", "blockquote": "> "}
SKIP = {"script", "style", "nav", "footer", "head"}


class _Readable(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.title = ""
        self.blocks: list[str] = []
        self._skip = 0
        self._in_title = False
        self._block: list[str] | None = None
        self._prefix = ""

    def handle_starttag(self, tag, attrs):
        if tag == "title":
            self._in_title = True
        elif tag in SKIP:
            self._skip += 1
        elif tag in BLOCKS and not self._skip:
            self._block, self._prefix = [], BLOCKS[tag]

    def handle_endtag(self, tag):
        if tag == "title":
            self._in_title = False
        elif tag in SKIP:
            self._skip = max(0, self._skip - 1)
        elif tag in BLOCKS and self._block is not None:
            text = " ".join("".join(self._block).split())
            if text:
                self.blocks.append(self._prefix + text)
            self._block = None

    def handle_data(self, data):
        if self._in_title:
            self.title += data
        elif self._block is not None and not self._skip:
            self._block.append(data)


def readable(html: str) -> tuple[str, str]:
    """Return the title and markdown text of an HTML page."""
    parser = _Readable()
    parser.feed(html)
    parser.close()
    return " ".join(parser.title.split()), "\n\n".join(parser.blocks) + "\n"


class FixtureWeb:
    """Serves tests/fixtures/web/index.json. Tests can script responses per URL."""

    def __init__(self, root: Path = FIXTURES) -> None:
        self.root = root
        data = json.loads((root / "web" / "index.json").read_text(encoding="utf-8"))
        self.pages: list[dict] = data["pages"]
        self.by_url = {p["url"]: p for p in self.pages}
        self.by_id = {p["id"]: p for p in self.pages}
        self.scripts: dict[str, list[Any]] = {}
        self.calls: list[tuple[str, Any]] = []

    def script(self, url: str, *responses: Any) -> None:
        """Queue responses for url. Each is a dict merged over a 200 response, or an exception to raise."""
        self.scripts.setdefault(url, []).extend(responses)

    def add_html(self, page_id: str, url: str, html: str, **extra) -> None:
        path = self.root / "web" / "pages" / f"{page_id}.html"
        entry = {"id": page_id, "url": url, "status": 200, "inline": html, **extra}
        self.pages.append(entry)
        self.by_url[url] = entry
        self.by_id[page_id] = entry
        del path

    def bookmark(self, page_id: str) -> dict:
        entry = self.by_id[page_id]
        b = entry.get("bookmark") or {"title": entry.get("title", ""), "folder": "", "added": 0, "tags": []}
        from wiki.importers import iso_from_unix

        return {"url": entry["url"], "title": b["title"], "folder": b["folder"], "added": iso_from_unix(b["added"]),
                "tags": b["tags"], "source": "netscape"}

    def summary_for(self, url: str) -> dict | None:
        entry = self.by_url.get(url)
        for _ in range(5):
            if entry is None:
                return None
            if entry.get("summary"):
                return json.loads((self.root / entry["summary"]).read_text(encoding="utf-8"))
            if not entry.get("redirect"):
                return None
            entry = self.by_url.get(entry["redirect"])
        return None

    def fetch(self, url: str, respect_robots: Any = None) -> dict:
        self.calls.append((url, respect_robots))
        queue = self.scripts.get(url)
        if queue:
            item = queue.pop(0)
            if isinstance(item, BaseException):
                raise item
            base = {"finalUrl": url, "status": 200, "title": "", "text": "", "markdown": "", "sha256": "",
                    "fetchedAt": "2026-09-13T11:00:00Z", "contentType": "text/html"}
            base.update(item)
            if not base["sha256"]:
                base["sha256"] = hashlib.sha256((base["markdown"] or base["text"]).encode()).hexdigest()
            return base
        final = url
        entry = self.by_url.get(url)
        hops = 0
        while entry is not None and entry.get("redirect") and hops < 5:
            final = entry["redirect"]
            entry = self.by_url.get(final)
            hops += 1
        empty = {"finalUrl": final, "title": "", "text": "", "markdown": "", "sha256": "",
                 "fetchedAt": "2026-09-13T11:00:00Z"}
        if entry is None:
            return {**empty, "status": 404}
        if respect_robots and entry.get("robotsDisallowed"):
            raise HostError(403, f"robots.txt disallows {final}", "robots")
        status = int(entry.get("status") or 200)
        if status >= 400:
            return {**empty, "status": status}
        if entry.get("inline") is not None:
            raw = entry["inline"].encode()
        else:
            raw = (self.root / "web" / entry["file"]).read_bytes()
        ctype = entry.get("contentType") or "text/html"
        digest = hashlib.sha256(raw).hexdigest()
        if "pdf" in ctype:
            return {**empty, "status": 200, "contentType": ctype, "bodyBase64": base64.b64encode(raw).decode(),
                    "sha256": digest}
        title, markdown = readable(raw.decode("utf-8"))
        return {**empty, "status": 200, "contentType": ctype, "title": title, "markdown": markdown,
                "text": markdown.replace("# ", ""), "sha256": digest}
