"""The Karakeep API importer.

GET /api/v1/bookmarks pages with limit and cursor and returns
{bookmarks, nextCursor}, newest first. Only bookmarks with link content
become bookmarks. GET /api/v1/lists and GET /api/v1/lists/{id}/bookmarks give
the list names of each bookmark.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from urllib.parse import quote, urlencode

from . import Bookmark, ImporterError, clean_tags
from .http import Deadline, Transport, urllib_transport

PAGE_SIZE = 100
MAX_PAGES = 10000


@dataclass
class Result:
    """Bookmarks newer than since, the newest createdAt seen, and whether paging finished."""

    bookmarks: list[Bookmark] = field(default_factory=list)
    newest: str = ""
    complete: bool = True


class KarakeepClient:
    def __init__(self, base_url: str, token: str, transport: Transport | None = None) -> None:
        if not base_url or not token:
            raise ImporterError("Karakeep needs the karakeep_url and karakeep_token secrets")
        self.base = base_url.rstrip("/")
        self.headers = {"Authorization": f"Bearer {token}"}
        self.transport = transport or urllib_transport

    def _get(self, path: str, query: dict) -> dict:
        data = self.transport(f"{self.base}{path}?{urlencode(query)}", self.headers)
        if not isinstance(data, dict):
            raise ImporterError(f"Karakeep {path} did not return an object")
        return data

    def _paged(self, path: str, deadline: Deadline):
        cursor = None
        for _ in range(MAX_PAGES):
            query = {"limit": PAGE_SIZE}
            if cursor:
                query["cursor"] = cursor
            data = self._get(path, query)
            yield data
            cursor = data.get("nextCursor")
            if not cursor or deadline.passed:
                return

    def list_names(self, deadline: Deadline) -> dict[str, list[str]]:
        """Map bookmark id to the names of the lists that hold it."""
        data = self._get("/api/v1/lists", {})
        names: dict[str, list[str]] = {}
        for lst in data.get("lists") or []:
            if not isinstance(lst, dict) or not lst.get("id"):
                continue
            for page in self._paged(f"/api/v1/lists/{quote(str(lst['id']), safe='')}/bookmarks", deadline):
                for b in page.get("bookmarks") or []:
                    if isinstance(b, dict) and b.get("id"):
                        names.setdefault(str(b["id"]), []).append(str(lst.get("name") or ""))
        return names

    def fetch(self, since: str = "", deadline_seconds: float | None = None) -> Result:
        """Return the link bookmarks created after since, an ISO time. Empty since returns all."""
        deadline = Deadline(deadline_seconds)
        lists = self.list_names(deadline)
        result = Result()
        for page in self._paged("/api/v1/bookmarks", deadline):
            stop = False
            for b in page.get("bookmarks") or []:
                if not isinstance(b, dict):
                    continue
                created = str(b.get("createdAt") or "")
                if created and created > result.newest:
                    result.newest = created
                if since and created and created <= since:
                    stop = True
                    continue
                content = b.get("content") if isinstance(b.get("content"), dict) else {}
                if content.get("type") != "link" or not content.get("url"):
                    continue
                tags = clean_tags(t.get("name") for t in b.get("tags") or [] if isinstance(t, dict))
                result.bookmarks.append(Bookmark(
                    url=str(content["url"]), title=str(b.get("title") or content.get("title") or ""),
                    added=created, tags=tags, lists=clean_tags(lists.get(str(b.get("id")), [])),
                    folder="", source="karakeep"))
            if stop:
                break
            if deadline.passed and page.get("nextCursor"):
                result.complete = False
                break
        return result
