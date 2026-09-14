"""Suggestion anchors in the core.CommentAnchor shape.

Offsets count UTF-16 code units of the note body without frontmatter, as
docs/comments.md specifies. make follows internal/anchor.Make and reproduces the
make cases in test/vectors/anchors.json.
"""

from __future__ import annotations

import sys
from array import array

CONTEXT = 32
MAX_EXACT = 5000


def _is_high(u: int) -> bool:
    return 0xD800 <= u <= 0xDBFF


def _is_low(u: int) -> bool:
    return 0xDC00 <= u <= 0xDFFF


class Body:
    """A body converted to UTF-16 code units once."""

    def __init__(self, text: str) -> None:
        self.text = text
        units = array("H")
        units.frombytes(text.encode("utf-16-le", "surrogatepass"))
        if sys.byteorder == "big":
            units.byteswap()
        self.u = units

    def __len__(self) -> int:
        return len(self.u)

    def slice(self, start: int, end: int) -> str:
        """Return the text between two code unit offsets. A lone surrogate becomes U+FFFD."""
        part = array("H", self.u[start:end])
        if sys.byteorder == "big":
            part.byteswap()
        return part.tobytes().decode("utf-16-le", "replace")

    def splits_pair(self, i: int) -> bool:
        return 0 < i < len(self.u) and _is_low(self.u[i]) and _is_high(self.u[i - 1])

    def offset(self, index: int) -> int:
        """Convert a Python string index into a code unit offset."""
        return len(self.text[:index].encode("utf-16-le", "surrogatepass")) // 2

    def make(self, start: int, end: int) -> dict | None:
        """Build an anchor for [start, end). Return None when the selection is empty."""
        n = len(self.u)
        start = min(max(start, 0), n)
        end = min(max(end, 0), n)
        if start > end:
            start, end = end, start
        if self.splits_pair(start):
            start -= 1
        if self.splits_pair(end):
            end += 1
        if start == end:
            return None
        return self._anchor(start, end)

    def insertion(self, at: int) -> dict:
        """Build the anchor of an insertion point. exact is empty and start equals end."""
        n = len(self.u)
        at = min(max(at, 0), n)
        if self.splits_pair(at):
            at -= 1
        return self._anchor(at, at)

    def _anchor(self, start: int, end: int) -> dict:
        n = len(self.u)
        ps = max(0, start - CONTEXT)
        if self.splits_pair(ps):
            ps += 1
        se = min(n, end + CONTEXT)
        if self.splits_pair(se):
            se -= 1
        return {
            "exact": self.slice(start, end),
            "prefix": self.slice(ps, start),
            "suffix": self.slice(end, se),
            "start": start,
            "end": end,
        }


def make(body: str, start: int, end: int) -> dict | None:
    """Build an anchor for code unit offsets [start, end) of body."""
    return Body(body).make(start, end)


def for_indices(body: str, start: int, end: int) -> dict | None:
    """Build an anchor for Python string indices [start, end) of body."""
    b = Body(body)
    return b.make(b.offset(start), b.offset(end))


def insertion_at_index(body: str, index: int) -> dict:
    """Build an insertion anchor at a Python string index of body."""
    b = Body(body)
    return b.insertion(b.offset(index))


def unit_length(text: str) -> int:
    """Return the length of text in UTF-16 code units."""
    return len(text.encode("utf-16-le", "surrogatepass")) // 2
