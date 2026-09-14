"""The Linkding API importer.

GET /api/bookmarks/ pages with limit and offset and returns
{count, next, previous, results}. Each result has url, title, website_title,
tag_names and date_added. The added_since parameter limits a scheduled run to
new bookmarks, and the client filters on date_added as well, because older
Linkding versions ignore the parameter.
"""

from __future__ import annotations

from urllib.parse import urlencode

from . import Bookmark, ImporterError, clean_tags
from .http import Deadline, Transport, same_origin, urllib_transport
from .karakeep import Result
from ..util import parse_iso

PAGE_SIZE = 100
MAX_PAGES = 10000


class LinkdingClient:
    def __init__(self, base_url: str, token: str, transport: Transport | None = None) -> None:
        if not base_url or not token:
            raise ImporterError("Linkding needs the linkding_url and linkding_token secrets")
        self.base = base_url.rstrip("/")
        self.headers = {"Authorization": f"Token {token}"}
        self.transport = transport or urllib_transport

    def fetch(self, since: str = "", deadline_seconds: float | None = None) -> Result:
        """Return the bookmarks added after since, an ISO time. Empty since returns all."""
        deadline = Deadline(deadline_seconds)
        since_dt = parse_iso(since)
        query = {"limit": PAGE_SIZE, "offset": 0}
        if since:
            query["added_since"] = since
        url = f"{self.base}/api/bookmarks/?{urlencode(query)}"
        result = Result()
        offset = 0
        for _ in range(MAX_PAGES):
            data = self.transport(url, self.headers)
            if not isinstance(data, dict):
                raise ImporterError("Linkding did not return an object")
            rows = data.get("results") or []
            for b in rows:
                if not isinstance(b, dict) or not b.get("url"):
                    continue
                added = str(b.get("date_added") or "")
                added_dt = parse_iso(added)
                if added_dt and (not result.newest or added_dt > parse_iso(result.newest)):
                    result.newest = added
                if since_dt and added_dt and added_dt <= since_dt:
                    continue
                result.bookmarks.append(Bookmark(
                    url=str(b["url"]), title=str(b.get("title") or b.get("website_title") or ""),
                    added=added, tags=clean_tags(b.get("tag_names") or []), source="linkding"))
            nxt = data.get("next")
            if not nxt or not rows:
                break
            if deadline.passed:
                result.complete = False
                break
            offset += len(rows)
            if isinstance(nxt, str) and same_origin(self.base, nxt):
                url = nxt
            else:
                url = f"{self.base}/api/bookmarks/?{urlencode({**query, 'offset': offset})}"
        return result
