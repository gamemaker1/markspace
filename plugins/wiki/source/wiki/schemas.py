"""Structural checks of model JSON.

Each function takes the decoded JSON, returns a cleaned copy, and raises
ModelOutputError listing every problem it found. The prompt templates document
the same schemas for the model.
"""

from __future__ import annotations

import re
from typing import Any

from .jsonparse import ModelOutputError

ENTITY_KINDS = ("person", "organisation", "tool", "project", "place")
MENTION_KINDS = ENTITY_KINDS + ("concept",)
PAGE_TYPES = ("entity", "concept", "comparison")
CONFIDENCE = ("low", "medium", "high")
KIND_ALIASES = {"organization": "organisation", "org": "organisation", "company": "organisation",
                "software": "tool", "library": "tool", "product": "tool", "idea": "concept", "method": "concept",
                "term": "concept", "location": "place", "city": "place", "country": "place"}
MAX_QUOTE = 300


def _strings(value: Any, path: str, problems: list[str], lower: bool = False) -> list[str]:
    if value is None:
        return []
    if not isinstance(value, list):
        problems.append(f"{path} must be an array of strings")
        return []
    out: dict[str, None] = {}
    for i, v in enumerate(value):
        if not isinstance(v, str):
            problems.append(f"{path}[{i}] must be a string")
            continue
        text = " ".join(v.split())
        if text:
            out.setdefault(text.lower() if lower else text, None)
    return list(out)


def _text(obj: dict, key: str, problems: list[str], required: bool = False) -> str:
    value = obj.get(key)
    if value is None:
        if required:
            problems.append(f"{key} is required")
        return ""
    if not isinstance(value, str):
        problems.append(f"{key} must be a string")
        return ""
    if required and not value.strip():
        problems.append(f"{key} must not be empty")
    return value.strip()


def _squash(text: str) -> str:
    return " ".join((text or "").split()).lower()


def summary(obj: Any, captured: str = "") -> dict:
    """Check a summarise answer. Quotes not found in the captured text are cleared, not rejected."""
    if not isinstance(obj, dict):
        raise ModelOutputError("the answer must be a JSON object")
    problems: list[str] = []
    out = {
        "title": _text(obj, "title", problems),
        "author": _text(obj, "author", problems),
        "published": _text(obj, "published", problems),
        "language": _text(obj, "language", problems).lower()[:16],
        "summary": _text(obj, "summary", problems, required=True),
        "key_points": [],
        "mentions": [],
        "tags": _strings(obj.get("tags"), "tags", problems, lower=True)[:12],
    }
    points = obj.get("key_points")
    if not isinstance(points, list) or not points:
        problems.append("key_points must be a non-empty array")
        points = []
    haystack = _squash(captured)
    for i, p in enumerate(points):
        if not isinstance(p, dict):
            problems.append(f"key_points[{i}] must be an object with claim and quote")
            continue
        claim = p.get("claim")
        if not isinstance(claim, str) or not claim.strip():
            problems.append(f"key_points[{i}].claim must be a non-empty string")
            continue
        quote = p.get("quote") if isinstance(p.get("quote"), str) else ""
        quote = " ".join(quote.split())[:MAX_QUOTE]
        if quote and haystack and _squash(quote) not in haystack:
            quote = ""
        out["key_points"].append({"claim": " ".join(claim.split()), "quote": quote})
    mentions = obj.get("mentions")
    if mentions is None:
        mentions = []
    if not isinstance(mentions, list):
        problems.append("mentions must be an array")
        mentions = []
    seen: set[str] = set()
    for i, m in enumerate(mentions):
        if not isinstance(m, dict):
            problems.append(f"mentions[{i}] must be an object with name, kind and aliases")
            continue
        name = m.get("name")
        if not isinstance(name, str) or not name.strip():
            problems.append(f"mentions[{i}].name must be a non-empty string")
            continue
        name = " ".join(name.split())
        if "[" in name or "]" in name or "|" in name or "#" in name:
            problems.append(f"mentions[{i}].name must not contain [, ], | or #")
            continue
        if name.lower() in seen:
            continue
        seen.add(name.lower())
        kind = str(m.get("kind") or "concept").strip().lower()
        kind = KIND_ALIASES.get(kind, kind)
        if kind not in MENTION_KINDS:
            kind = "concept"
        aliases = [a for a in _strings(m.get("aliases"), f"mentions[{i}].aliases", problems)
                   if a.lower() != name.lower() and not re.search(r"[\[\]|#]", a)]
        out["mentions"].append({"name": name, "kind": kind, "aliases": aliases})
    out["mentions"] = out["mentions"][:15]
    if problems:
        raise ModelOutputError("the summary breaks its schema", problems)
    return out


def page(obj: Any) -> dict:
    """Check the structure of a compile answer. compile.py checks links and citations."""
    if not isinstance(obj, dict):
        raise ModelOutputError("the answer must be a JSON object")
    problems: list[str] = []
    out = {
        "title": _text(obj, "title", problems, required=True),
        "type": _text(obj, "type", problems, required=True).lower(),
        "entity_kind": _text(obj, "entity_kind", problems).lower(),
        "aliases": _strings(obj.get("aliases"), "aliases", problems),
        "tags": _strings(obj.get("tags"), "tags", problems, lower=True),
        "confidence": _text(obj, "confidence", problems).lower() or "medium",
        "contested": obj.get("contested") is True,
        "definition": _text(obj, "definition", problems),
        "body": obj.get("body") if isinstance(obj.get("body"), str) else "",
        "related_notes": _strings(obj.get("related_notes"), "related_notes", problems),
        "removed_citations": [],
    }
    if not out["body"].strip():
        problems.append("body must be a non-empty string")
    if out["type"] and out["type"] not in PAGE_TYPES:
        problems.append(f"type must be one of {', '.join(PAGE_TYPES)}")
    if out["confidence"] not in CONFIDENCE:
        problems.append(f"confidence must be one of {', '.join(CONFIDENCE)}")
    if out["type"] == "entity" and out["entity_kind"] and out["entity_kind"] not in ENTITY_KINDS:
        out["entity_kind"] = KIND_ALIASES.get(out["entity_kind"], "")
    if out["type"] != "entity":
        out["entity_kind"] = ""
    removed = obj.get("removed_citations") or []
    if not isinstance(removed, list):
        problems.append("removed_citations must be an array")
        removed = []
    for i, r in enumerate(removed):
        if not isinstance(r, dict) or not isinstance(r.get("source"), str) or not r["source"].strip():
            problems.append(f"removed_citations[{i}] needs a source title")
            continue
        if not isinstance(r.get("reason"), str) or not r["reason"].strip():
            problems.append(f"removed_citations[{i}] needs a reason")
            continue
        out["removed_citations"].append({"source": r["source"].strip(), "reason": r["reason"].strip()})
    body = out["body"].strip("\n")
    if body.startswith("# "):
        # A top-level heading repeats the title. Drop it rather than reject the page.
        body = body.split("\n", 1)[1].lstrip("\n") if "\n" in body else ""
    out["body"] = body + "\n" if body else ""
    if problems:
        raise ModelOutputError("the page breaks its schema", problems)
    return out


def contradictions(obj: Any) -> dict:
    """Check a lint answer."""
    if not isinstance(obj, dict):
        raise ModelOutputError("the answer must be a JSON object")
    problems: list[str] = []
    items = obj.get("disagreements") or []
    if not isinstance(items, list):
        problems.append("disagreements must be an array")
        items = []
    clean = []
    for i, d in enumerate(items):
        if not isinstance(d, dict) or not isinstance(d.get("claim"), str):
            problems.append(f"disagreements[{i}] needs a claim")
            continue
        positions = [p for p in d.get("positions") or [] if isinstance(p, dict) and isinstance(p.get("source"), str)]
        if len(positions) < 2:
            continue
        clean.append({"claim": d["claim"].strip(),
                      "positions": [{"source": p["source"].strip(), "says": str(p.get("says") or "").strip()}
                                    for p in positions]})
    section = obj.get("section") if isinstance(obj.get("section"), str) else ""
    if clean and not section.strip():
        problems.append("section must hold the ## Disagreements markdown when disagreements is not empty")
    if problems:
        raise ModelOutputError("the lint answer breaks its schema", problems)
    section = section.strip()
    if section and not section.startswith("## Disagreements"):
        section = "## Disagreements\n\n" + section
    return {"contested": bool(clean), "disagreements": clean, "section": section + "\n" if section else ""}


def answer(obj: Any) -> dict:
    """Check an ask answer."""
    if not isinstance(obj, dict):
        raise ModelOutputError("the answer must be a JSON object")
    problems: list[str] = []
    out = {
        "title": _text(obj, "title", problems)[:80],
        "answer": _text(obj, "answer", problems, required=True),
        "cited": _strings(obj.get("cited"), "cited", problems),
    }
    if problems:
        raise ModelOutputError("the answer breaks its schema", problems)
    return out
