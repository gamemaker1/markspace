"""A text file with one URL per line.

Lines starting with # are comments. Text after the URL and a space becomes
the title.
"""

from __future__ import annotations

from . import Bookmark


def parse(text: str) -> list[Bookmark]:
    """Return one bookmark per URL line, in file order."""
    out = []
    for raw in (text or "").lstrip("﻿").splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        url, _, title = line.partition(" ")
        out.append(Bookmark(url=url.strip(), title=" ".join(title.split()), source="urls"))
    return out
