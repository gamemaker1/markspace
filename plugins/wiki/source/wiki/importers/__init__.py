"""Bookmark importers.

Each importer turns its input into Bookmark values. The import job
normalises and deduplicates them, so importers keep URLs as they found them.
"""

from __future__ import annotations

from dataclasses import asdict, dataclass, field
from datetime import datetime, timezone
from typing import Any


class ImporterError(Exception):
    """An importer could not read its input."""


@dataclass
class Bookmark:
    """One bookmark from any importer. added is an ISO 8601 UTC time or empty."""

    url: str
    title: str = ""
    added: str = ""
    folder: str = ""
    tags: list[str] = field(default_factory=list)
    lists: list[str] = field(default_factory=list)
    source: str = ""

    def to_json(self) -> dict[str, Any]:
        return asdict(self)

    @classmethod
    def from_json(cls, data: dict[str, Any]) -> "Bookmark":
        return cls(url=str(data.get("url") or ""), title=str(data.get("title") or ""),
                   added=str(data.get("added") or ""), folder=str(data.get("folder") or ""),
                   tags=[str(t) for t in data.get("tags") or []], lists=[str(t) for t in data.get("lists") or []],
                   source=str(data.get("source") or ""))


def iso_from_unix(value: Any) -> str:
    """Convert a Unix time in seconds, milliseconds or microseconds to ISO 8601 UTC. Return empty on failure."""
    try:
        n = int(str(value).strip())
    except (TypeError, ValueError):
        return ""
    if n <= 0:
        return ""
    if n > 10**14:
        n //= 10**6
    elif n > 10**11:
        n //= 1000
    try:
        dt = datetime.fromtimestamp(n, timezone.utc)
    except (OverflowError, OSError, ValueError):
        return ""
    return dt.isoformat().replace("+00:00", "Z")


def clean_tags(tags: Any) -> list[str]:
    """Return distinct non-empty tags in first-seen order."""
    out: dict[str, None] = {}
    for tag in tags or []:
        text = str(tag).strip()
        if text:
            out.setdefault(text, None)
    return list(out)


def parse(importer: str, data: bytes | str) -> list[Bookmark]:
    """Parse a file for the netscape or urls importer."""
    text = data.decode("utf-8", "replace") if isinstance(data, bytes) else data
    if importer == "netscape":
        from .netscape import parse as parse_netscape

        return parse_netscape(text)
    if importer == "urls":
        from .urllist import parse as parse_urls

        return parse_urls(text)
    raise ImporterError(f"importer {importer} does not read files")
