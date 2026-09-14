"""The Netscape bookmark file format that Chrome, Firefox, Safari and Edge export.

The file nests <DL> lists. A <DT><H3> heading names the folder of the <DL>
that follows it. Each <DT><A HREF> is a bookmark with ADD_DATE in Unix
seconds and, in Firefox exports, TAGS as a comma separated list. <H1> names
the whole file and is not a folder.
"""

from __future__ import annotations

from html.parser import HTMLParser

from . import Bookmark, ImporterError, clean_tags, iso_from_unix

SKIP_SCHEMES = ("javascript:", "place:", "data:", "file:", "about:", "chrome:", "edge:", "feed:")


class _Parser(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.bookmarks: list[Bookmark] = []
        self.stack: list[str | None] = []
        self.pending_folder: str | None = None
        self.heading: list[str] | None = None
        self.anchor: dict | None = None
        self.saw_dl = False

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        a = {k.lower(): (v or "") for k, v in attrs}
        if tag == "dl":
            self.saw_dl = True
            self.stack.append(self.pending_folder)
            self.pending_folder = None
        elif tag == "h3":
            self.heading = []
        elif tag == "a":
            self.anchor = {"href": a.get("href", ""), "add_date": a.get("add_date", ""),
                           "tags": a.get("tags", ""), "text": []}

    def handle_endtag(self, tag: str) -> None:
        if tag == "dl":
            if self.stack:
                self.stack.pop()
            self.pending_folder = None
        elif tag == "h3" and self.heading is not None:
            self.pending_folder = " ".join("".join(self.heading).split())
            self.heading = None
        elif tag == "a" and self.anchor is not None:
            self._finish_anchor()

    def handle_data(self, data: str) -> None:
        if self.heading is not None:
            self.heading.append(data)
        elif self.anchor is not None:
            self.anchor["text"].append(data)

    def _finish_anchor(self) -> None:
        anchor, self.anchor = self.anchor, None
        href = (anchor["href"] or "").strip()
        if not href or href.lower().startswith(SKIP_SCHEMES):
            return
        folder = "/".join(name for name in self.stack if name)
        title = " ".join("".join(anchor["text"]).split())
        tags = clean_tags(t for t in anchor["tags"].split(",")) if anchor["tags"] else []
        self.bookmarks.append(Bookmark(url=href, title=title, added=iso_from_unix(anchor["add_date"]),
                                       folder=folder, tags=tags, source="netscape"))

    def close(self) -> None:
        super().close()
        if self.anchor is not None:
            self._finish_anchor()


def parse(text: str) -> list[Bookmark]:
    """Return the bookmarks of a Netscape bookmark file in document order."""
    parser = _Parser()
    parser.feed(text or "")
    parser.close()
    if not parser.saw_dl and not parser.bookmarks:
        raise ImporterError("the file is not a Netscape bookmark export")
    return parser.bookmarks
