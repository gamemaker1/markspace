"""The link-suggest job.

It searches the user's notes for the title and aliases of a wiki page. A note
that mentions the subject without linking to it gets one suggestion that
turns the first mention into a wikilink. The plugin keeps at most one open
suggestion per note and creates at most suggestionsPerDay link suggestions a
day.
"""

from __future__ import annotations

from typing import Any

from markspace import HostError

from . import anchors, frontmatter, wikilinks

SEARCH_LIMIT = 25


def job_link_suggest(svc: Any, ctx: Any) -> dict:
    payload = ctx.payload or {}
    page_id = str(payload.get("pageId") or "")
    page_rec = svc.state.page(page_id)
    page = svc.notes.get(page_id) if page_rec else None
    if page_rec is None or page is None:
        return {"skipped": "page is gone"}
    paused = svc.requeue_if_paused(ctx, "link-suggest")
    if paused:
        return paused
    settings = svc.settings()
    limit = settings.suggestionsPerDay
    if svc.state.suggestions_today() >= limit:
        return {"created": 0, "skipped": "daily limit reached"}
    space = page_rec.get("spaceId") or page.get("spaceId")
    title = page_rec.get("title") or page.get("title") or ""
    names = [title] + [a for a in page_rec.get("aliases") or [] if isinstance(a, str)]
    lower = {n.lower() for n in names if n}
    skip_prefixes = (settings.sourcesFolder + "/", settings.wikiFolder + "/")

    candidates: dict[str, None] = {}
    for name in names:
        try:
            hits = svc.host.search(name, mode="text", space=space, limit=SEARCH_LIMIT).items
        except HostError:
            continue
        for hit in hits:
            note = hit.get("note") if isinstance(hit, dict) else None
            if isinstance(note, dict) and note.get("id") and not str(note.get("path") or "").startswith(skip_prefixes):
                candidates.setdefault(str(note["id"]), None)

    created, reasons = 0, {}
    for note_id in candidates:
        if note_id == page_id:
            continue
        # Link-suggest jobs for different pages run in parallel. The lock makes the open-suggestion
        # check and the create one step, so a note never gets two suggestions from two jobs.
        with svc.url_lock(f"note:{note_id}"):
            outcome = _suggest_in_note(svc, note_id, page_id, title, names, lower, limit)
        if outcome == "created":
            created += 1
        elif outcome:
            reasons[note_id] = outcome
            if outcome == "daily limit reached":
                break
    return {"created": created, "candidates": len(candidates), "skipped": reasons}


def _suggest_in_note(svc: Any, note_id: str, page_id: str, title: str, names: list[str], lower: set[str],
                     limit: int) -> str:
    """Create one link suggestion in a note. Return created, a reason for skipping, or empty."""
    if svc.state.open_suggestions(note_id):
        return "an open suggestion exists"
    note = svc.notes.get(note_id)
    if note is None or svc.notes.generated(note):
        return ""
    body = frontmatter.body_of(note.get("content") or "")
    if any(t.lower() in lower for t in wikilinks.targets(body)):
        return "already links the page"
    mention = wikilinks.find_mention(body, names)
    if mention is None:
        return ""
    start, end, text = mention
    anchor = anchors.for_indices(body, start, end)
    if anchor is None:
        return ""
    if not svc.state.take_suggestion_slot(limit):
        return "daily limit reached"
    replacement = f"[[{title}]]" if text == title else f"[[{title}|{text}]]"
    suggestion = svc.host.create_suggestion(
        note_id, f"Link {title}", [{"anchor": anchor, "text": replacement}],
        rationale=f"{text} is the subject of the wiki page [[{title}]].")
    if isinstance(suggestion, dict) and suggestion.get("id"):
        svc.state.set_open_suggestion(note_id, "link", suggestion["id"], pageId=page_id)
    return "created"
