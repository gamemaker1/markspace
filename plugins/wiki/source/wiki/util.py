"""Time, hashing and text helpers shared by the jobs."""

from __future__ import annotations

import hashlib
import json
from datetime import datetime, timezone
from typing import Any, Callable

Clock = Callable[[], datetime]


def utcnow() -> datetime:
    return datetime.now(timezone.utc)


def iso(dt: datetime) -> str:
    """Format a time as 2026-09-13T11:02:00Z."""
    return dt.astimezone(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def parse_iso(text: str) -> datetime | None:
    """Parse an ISO 8601 time. Return None for empty or invalid text."""
    if not text:
        return None
    try:
        dt = datetime.fromisoformat(text.replace("Z", "+00:00"))
    except ValueError:
        return None
    return dt if dt.tzinfo else dt.replace(tzinfo=timezone.utc)


def day(dt: datetime) -> str:
    return dt.astimezone(timezone.utc).strftime("%Y-%m-%d")


def month(dt: datetime) -> str:
    return dt.astimezone(timezone.utc).strftime("%Y-%m")


def minute(dt: datetime) -> str:
    return dt.astimezone(timezone.utc).strftime("%Y-%m-%d %H:%M")


def sha256(text: str | bytes) -> str:
    raw = text.encode("utf-8") if isinstance(text, str) else text
    return hashlib.sha256(raw).hexdigest()


def canonical(value: Any) -> str:
    """Return a stable JSON encoding for hashing."""
    return json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(",", ":"))


def cut(text: str, limit: int) -> str:
    """Cut text to limit characters at a word boundary when one is near."""
    text = " ".join((text or "").split())
    if len(text) <= limit:
        return text
    head = text[: limit - 1]
    space = head.rfind(" ")
    if space > limit * 0.6:
        head = head[:space]
    return head.rstrip(",;:. ") + "…"


def first_paragraph(body: str) -> str:
    """Return the first paragraph of a markdown body that is not a heading."""
    for block in (body or "").split("\n\n"):
        block = block.strip()
        if block and not block.startswith("#") and not block.startswith("<!--"):
            return " ".join(block.split())
    return ""
