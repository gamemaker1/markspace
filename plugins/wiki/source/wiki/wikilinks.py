"""Wikilinks, citations and unlinked mentions in markdown bodies."""

from __future__ import annotations

import re
from dataclasses import dataclass

WIKILINK = re.compile(r"(!?)\[\[([^\[\]\n]+?)\]\]")
_ONE = r"\[\[[^\[\]\n]+?\]\]"
CITATION = re.compile(r"\(\s*(" + _ONE + r"(?:\s*[,;]\s*" + _ONE + r")*)\s*\)")
_FENCED = re.compile(r"^(```|~~~).*?^\1[^\n]*$", re.DOTALL | re.MULTILINE)
_INLINE_CODE = re.compile(r"`[^`\n]*`")
_MD_LINK = re.compile(r"!?\[[^\]\n]*\]\([^)\n]*\)")
_URL = re.compile(r"https?://\S+")
_HEADING = re.compile(r"^#{1,6}[ \t].*$", re.MULTILINE)
_COMMENT = re.compile(r"<!--.*?-->", re.DOTALL)


@dataclass
class Link:
    """One wikilink. start and end are Python string indices of the whole [[...]]."""

    raw: str
    target: str
    label: str
    heading: str
    start: int
    end: int
    embed: bool


def parse_target(raw: str) -> tuple[str, str, str]:
    """Split the inside of [[...]] into target, heading and label."""
    target, _, label = raw.partition("|")
    target, _, heading = target.partition("#")
    return target.strip(), heading.strip(), label.strip()


def links(text: str) -> list[Link]:
    """Return every wikilink in text, in order."""
    out = []
    for m in WIKILINK.finditer(text or ""):
        target, heading, label = parse_target(m.group(2))
        if target:
            out.append(Link(m.group(2), target, label, heading, m.start(), m.end(), m.group(1) == "!"))
    return out


def targets(text: str) -> list[str]:
    """Return the distinct link targets of text, in first-seen order."""
    seen: dict[str, None] = {}
    for link in links(text):
        seen.setdefault(link.target, None)
    return list(seen)


def citations(text: str) -> list[str]:
    """Return the distinct targets cited as ([[source]]) or ([[a]], [[b]]), in first-seen order."""
    seen: dict[str, None] = {}
    for m in CITATION.finditer(text or ""):
        for link in links(m.group(1)):
            seen.setdefault(link.target, None)
    return list(seen)


def non_citation_targets(text: str) -> list[str]:
    """Return link targets that appear outside citation parentheses."""
    masked = CITATION.sub(lambda m: " " * len(m.group(0)), text or "")
    return targets(masked)


def mask(text: str) -> str:
    """Replace code, links, URLs, headings and HTML comments with spaces, keeping every index."""
    def blank(m: re.Match) -> str:
        return re.sub(r"[^\n]", " ", m.group(0))

    out = text or ""
    for rx in (_FENCED, _COMMENT, _INLINE_CODE, WIKILINK, _MD_LINK, _URL, _HEADING):
        out = rx.sub(blank, out)
    return out


def find_mention(text: str, names: list[str]) -> tuple[int, int, str] | None:
    """Return (start, end, matched text) of the first unlinked mention of any name, or None.

    Matching ignores case and needs a word boundary on both sides. Longer names
    win at the same position, so "PostgreSQL WAL" beats "PostgreSQL".
    """
    masked = mask(text)
    best: tuple[int, int, str] | None = None
    for name in sorted({n.strip() for n in names if n and n.strip()}, key=len, reverse=True):
        rx = re.compile(r"(?<!\w)" + re.escape(name) + r"(?!\w)", re.IGNORECASE)
        m = rx.search(masked)
        if m and (best is None or m.start() < best[0]):
            best = (m.start(), m.end(), text[m.start():m.end()])
    return best


def word_count(text: str) -> int:
    """Count the words of a markdown body."""
    return len(re.findall(r"\w[\w'’-]*", text or ""))
