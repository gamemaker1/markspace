"""A scripted model for tests.

Tests queue raw answers per purpose with script(). Without a script, the fake
answers from fixtures. Summaries come from tests/fixtures/ai/summaries. Pages
are built from the compile input, so they cite the source, link the other
pages in the batch and link user notes that mention the page title. The
builder merges an existing body, so the same input gives the same page.
"""

from __future__ import annotations

import json
import re
import threading
from typing import Any

from wiki.prompts import extract_input

KIND_WORDS = {"person": "a person", "organisation": "an organisation", "tool": "a tool", "project": "a project",
              "place": "a place"}


def _bullets(body: str, heading: str) -> list[str]:
    m = re.search(rf"^## {re.escape(heading)}\n\n((?:- .*\n?)*)", body or "", re.MULTILINE)
    return [line for line in (m.group(1).splitlines() if m else []) if line.startswith("- ")]


class ScriptedAI:
    def __init__(self, fake: Any) -> None:
        self.fake = fake
        self.scripts: dict[str, list[Any]] = {}
        self.requests: list[dict] = []
        self.lock = threading.Lock()

    def script(self, purpose: str, *answers: Any) -> None:
        with self.lock:
            self.scripts.setdefault(purpose, []).extend(answers)

    def calls(self, purpose: str) -> list[dict]:
        return [r for r in self.requests if r.get("route", "").endswith("." + purpose)]

    def complete(self, request: dict) -> dict:
        purpose = str(request.get("route", "")).rsplit(".", 1)[-1]
        with self.lock:
            self.requests.append(json.loads(json.dumps(request)))
            queue = self.scripts.get(purpose)
            answer = queue.pop(0) if queue else None
        if callable(answer):
            text = answer(request)
        elif isinstance(answer, str):
            text = answer
        elif isinstance(answer, (dict, list)):
            text = json.dumps(answer)
        else:
            text = self.default(purpose, request)
        prompt = str(request.get("instruction", "")) + str(request.get("text", ""))
        return {"text": text, "provider": "fake", "model": "fake-model", "route": request.get("route"), "attempts": 1,
                "promptTokens": len(prompt) // 4, "completionTokens": len(text) // 4}

    # defaults --------------------------------------------------------------

    def default(self, purpose: str, request: dict) -> str:
        user = str(request.get("text", ""))
        if "<<<PREVIOUS" in user:
            user = user.split("Original request:\n", 1)[-1]
        data = extract_input(user) or {}
        if purpose == "summarise":
            body = json.dumps(self.summary(data), indent=2)
            return f"Here is the summary.\n```json\n{body}\n```\nThe mentions are ordered by importance."
        if purpose == "compile":
            return json.dumps(self.page(data))
        if purpose == "lint":
            return json.dumps({"contested": False, "disagreements": [], "section": ""})
        if purpose == "ask":
            titles = [p["title"] for p in data.get("pages") or []][:2]
            answer = " ".join(f"{t} is covered in the wiki ([[{t}]])." for t in titles) or "The wiki does not say."
            return json.dumps({"title": f"About {data.get('question', '')[:60]}", "answer": answer, "cited": titles})
        raise AssertionError(f"no default answer for {purpose}")

    def summary(self, data: dict) -> dict:
        fixture = self.fake.web.summary_for(data.get("url", ""))
        if fixture:
            return fixture
        text = data.get("captured_text", "")
        sentences = [s.strip() for s in re.split(r"(?<=[.!?])\s+", " ".join(text.split())) if s.strip()]
        return {"title": data.get("title", ""), "author": "", "published": "", "language": "en",
                "summary": " ".join(sentences[:2]) or "No summary.",
                "key_points": [{"claim": s.rstrip("."), "quote": s} for s in sentences[:3]] or
                              [{"claim": "The page is short", "quote": ""}],
                "mentions": [], "tags": []}

    def page(self, data: dict) -> dict:
        page, source = data["page"], data["source"]
        title, cite = page["title"], source["title"]
        current = page.get("current_body") or ""
        if current:
            opening = current.split("\n\n", 1)[0].strip()
        else:
            kind = KIND_WORDS.get(page.get("entity_kind"), "a comparison" if page["type"] == "comparison" else "a concept")
            opening = f"{title} is {kind} described in {cite} ([[{cite}]])."
        details = _bullets(current, "Details")
        for claim in source.get("key_points", [])[:2]:
            line = f"- {claim} ([[{cite}]])."
            if line not in details:
                details.append(line)
        related = _bullets(current, "Related")
        for other in data.get("wiki_pages", [])[:4]:
            line = f"- [[{other}]]"
            if line not in related:
                related.append(line)
        notes = _bullets(current, "In your notes")
        for note in data.get("related_notes", []):
            line = f"- [[{note['title']}]]: mentions {title}."
            names = [title, page.get("name") or ""] + list(page.get("aliases") or [])
            if line not in notes and any(n and self.fake.note_mentions(note["title"], n) for n in names):
                notes.append(line)
        body = f"{opening}\n\n## Details\n\n" + "\n".join(details) + "\n\n## Related\n\n" + "\n".join(related) + "\n"
        if notes:
            body += "\n## In your notes\n\n" + "\n".join(notes) + "\n"
        return {
            "title": title, "type": page["type"], "entity_kind": page.get("entity_kind", ""),
            "aliases": page.get("aliases", []), "tags": [], "confidence": "medium", "contested": False,
            "definition": re.sub(r"\s*\(\[\[.*?\]\]\)", "", opening),
            "body": body,
            "related_notes": [re.match(r"- \[\[(.+?)\]\]", n).group(1) for n in notes],
            "removed_citations": [],
        }
