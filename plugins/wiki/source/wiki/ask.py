"""Ask the wiki and File this answer."""

from __future__ import annotations

import re
from typing import Any

from markspace import HostError

from . import frontmatter, schemas, wikilinks
from .notes import GENERATED_BY
from .urls import stable_id
from .util import canonical, cut, first_paragraph, iso, sha256

BEST_PAGES = 8
PAGE_CHARS = 4000
INDEX_CHARS = 6000
STOP_WORDS = {"what", "which", "when", "where", "does", "about", "with", "from", "that", "this", "have", "there",
              "their", "how", "why", "who", "the", "and", "for", "are", "can", "into", "between"}


def _in_scope(path: str, settings: Any) -> bool:
    wiki, sources = settings.wikiFolder + "/", settings.sourcesFolder + "/"
    if path.startswith(sources):
        return True
    if not path.startswith(wiki):
        return False
    rest = path[len(wiki):]
    return "/" in rest and not rest.startswith("Lint/")


def search_pages(svc: Any, space: str, question: str) -> list[dict]:
    """Return up to 8 note stubs from Wiki and Sources that match the question, best first."""
    settings = svc.settings()
    order: dict[str, dict] = {}
    score: dict[str, float] = {}

    def add(hits: list, weight: float) -> None:
        for rank, hit in enumerate(hits):
            note = hit.get("note") if isinstance(hit, dict) else None
            if not isinstance(note, dict) or not note.get("id"):
                continue
            if not _in_scope(str(note.get("path") or ""), settings):
                continue
            order.setdefault(note["id"], note)
            score[note["id"]] = score.get(note["id"], 0.0) + weight / (rank + 1)

    try:
        add(svc.host.search(question, mode="semantic", space=space, limit=30).items, 2.0)
    except HostError:
        pass
    if not order:
        try:
            add(svc.host.search(question, mode="text", space=space, limit=30).items, 2.0)
        except HostError:
            pass
    if not order:
        words = [w for w in re.findall(r"[\w'-]{4,}", question.lower()) if w not in STOP_WORDS][:6]
        for word in words:
            try:
                add(svc.host.search(word, mode="text", space=space, limit=15).items, 1.0)
            except HostError:
                continue
    ranked = sorted(order, key=lambda i: (-score[i], list(order).index(i)))
    return [order[i] for i in ranked[:BEST_PAGES]]


def ask(svc: Any, space: str, question: str) -> dict:
    question = " ".join((question or "").split())
    if not question:
        raise ValueError("Enter a question.")
    index = svc.files.ensure_index(space)
    pages = []
    for stub in search_pages(svc, space, question):
        note = svc.notes.get(stub["id"])
        if note is None:
            continue
        fm = frontmatter.read(note)
        pages.append({"title": note.get("title") or "", "type": fm.get("type") or "note",
                      "body": frontmatter.body_of(note.get("content") or "")[:PAGE_CHARS]})
    data = {"question": question, "index": frontmatter.body_of(index.get("content") or "")[:INDEX_CHARS],
            "pages": pages}
    result, _ = svc.ai.complete_json("ask", "ask", {"input_json": data}, schemas.answer, space_id=space)
    allowed = {p["title"].lower(): p["title"] for p in pages}

    def keep(m: re.Match) -> str:
        target, _, label = wikilinks.parse_target(m.group(2))
        return m.group(0) if target.lower() in allowed else (label or target)

    text = wikilinks.WIKILINK.sub(keep, result["answer"])
    cited = [allowed[c.lower()] for c in result["cited"] if c.lower() in allowed]
    for c in wikilinks.targets(text):
        if c.lower() in allowed and allowed[c.lower()] not in cited:
            cited.append(allowed[c.lower()])
    title = result["title"] or cut(question, 80)
    return {"question": question, "title": title, "answer": text, "cited": cited, "pages": len(pages)}


def file_answer(svc: Any, space: str, question: str, answer: str, title: str, cited: list[str]) -> tuple[dict, bool]:
    """Write Wiki/Answers/<slug>.md. Return the note and whether it was created now."""
    settings = svc.settings()
    key = sha256(canonical({"q": question, "a": answer}))
    known = svc.state.get(f"answer:{key}")
    if isinstance(known, str):
        note = svc.notes.get(known)
        if note:
            return note, False
    title = " ".join((title or cut(question, 80)).split()).replace("[", "(").replace("]", ")").replace("|", "-")
    if svc.state.alias(title) or svc.state.source_by_title(title):
        title = f"{title} ({svc.clock().strftime('%Y-%m-%d')})"
    now = iso(svc.clock())
    sources = [c for c in cited if svc.state.source_by_title(c)]
    pages = [c for c in cited if c not in sources]
    fields = {"title": title, "type": "answer", "entity_kind": "", "aliases": [], "tags": ["answer"],
              "sources": [f"[[{s}]]" for s in sources], "related_notes": [], "confidence": "medium",
              "contested": False, "question": question, "created": now, "updated": now,
              "generated_by": GENERATED_BY}
    body = f"{answer.strip()}\n\n## Question\n\n{question}\n\n## Pages cited\n\n"
    body += "".join(f"- [[{c}]]\n" for c in pages + sources) or "None.\n"
    note = svc.notes.create(space, f"{settings.wikiFolder}/Answers", title, frontmatter.dump(fields) + body,
                            client_id=stable_id("answer", space, key))
    svc.state.put(f"answer:{key}", note["id"])
    svc.state.save_page({"id": note["id"], "spaceId": space, "title": title, "type": "answer", "entityKind": "",
                         "aliases": [], "sources": [svc.state.source_by_title(s) for s in sources],
                         "inputs": {}, "created": now})
    svc.state.set_alias(title, note["id"])
    svc.files.add_to_index(space, [("answer", title, first_paragraph(wikilinks.CITATION.sub("", answer)))])
    svc.files.append_log(space, "answer", f"[[{title}]]", f"cites {len(cited)}")
    return note, True
