"""Source notes and the import, fetch, summarise, refetch and sync jobs."""

from __future__ import annotations

import base64
import json
import logging
import time
from datetime import timedelta
from typing import Any
from urllib.parse import urlsplit

from markspace import HostError

from . import frontmatter, schemas
from .importers import Bookmark, ImporterError
from .importers.karakeep import KarakeepClient
from .importers.linkding import LinkdingClient
from .jsonparse import ModelOutputError
from .notes import GENERATED_BY, Conflict, WriteResult
from .urls import InvalidURL, normalise_url, site_of, slugify, stable_id
from .util import canonical, iso, parse_iso, sha256

log = logging.getLogger("wiki.sources")

SUMMARY_MARK = "<!-- wiki:summary -->"
CAPTURED_MARK = "<!-- wiki:captured -->"
PENDING_SUMMARY = "## Summary\n\nNot summarised yet.\n"
PENDING_CAPTURED = "## Captured text\n\nNot fetched yet.\n"
MAX_CAPTURED = 200_000
PROMPT_CHARS = 12_000
DEAD_STATUSES = (404, 410)
BLOCKED_STATUSES = (401, 403, 429)
MAX_BLOCKED_TRIES = 3
MAX_TEXT_WAITS = 5
MAX_SERVER_ATTEMPTS = 4
REFETCH_BATCH = 50
MAX_TITLE = 180
QUEUE_WAIT = 30.0
sleep = time.sleep  # tests replace it


# rendering -------------------------------------------------------------------

def clean_title(title: str) -> str:
    """Make a title safe inside [[...]].

    Brackets, pipes and hashes would break the wikilink, and a slash turns a
    name wikilink into a path wikilink that no longer matches the title.
    """
    text = (title or "").replace("[", "(").replace("]", ")").replace("|", "-").replace("#", " ")
    text = text.replace("/", "-").replace("\\", "-")
    text = " ".join(text.split())
    return text[:MAX_TITLE].rstrip()


def url_title(url: str) -> str:
    """Return a placeholder title such as "sqlite.org whentouse.html" for a bookmark without a title."""
    parts = urlsplit(url)
    segments = [s for s in parts.path.split("/") if s]
    return clean_title(" ".join([site_of(url)] + segments)) or "untitled"


def split_parts(body: str) -> tuple[str, str]:
    """Return the summary and captured parts of a source note body, each without its marker."""
    s = body.find(SUMMARY_MARK)
    c = body.find(CAPTURED_MARK)
    if s == -1 or c == -1 or c < s:
        return PENDING_SUMMARY, PENDING_CAPTURED
    summary = body[s + len(SUMMARY_MARK):c].strip("\n") + "\n"
    captured = body[c + len(CAPTURED_MARK):].strip("\n") + "\n"
    return summary, captured


def render_body(summary_part: str, captured_part: str) -> str:
    return f"{SUMMARY_MARK}\n{summary_part.rstrip()}\n\n{CAPTURED_MARK}\n{captured_part.rstrip()}\n"


def has_capture(captured_part: str) -> bool:
    return captured_part.strip() not in ("", PENDING_CAPTURED.strip())


def captured_text(body: str) -> str:
    """Return the captured text of a source note body without its heading, or empty before the first fetch."""
    _, captured = split_parts(body)
    if not has_capture(captured):
        return ""
    text = captured.strip("\n")
    if text.startswith("## Captured text"):
        text = text[len("## Captured text"):]
    return text.strip("\n")


def source_fields(rec: dict) -> dict:
    folders = rec.get("folders") or []
    summary = rec.get("summary") or {}
    fields = {
        "title": rec["title"],
        "type": "source",
        "source_url": rec["url"],
        "canonical_url": rec.get("canonicalUrl") or "",
        "site": rec["site"],
        "author": rec.get("author") or summary.get("author") or "",
        "published": rec.get("published") or summary.get("published") or "",
        "bookmarked": rec.get("bookmarked") or "",
        "bookmark_folder": folders[0] if len(folders) == 1 else folders,
        "bookmark_tags": rec.get("tags") or [],
    }
    if rec.get("lists"):
        fields["bookmark_lists"] = rec["lists"]
    fields.update({
        "fetched": rec.get("fetched") or "",
        "sha256": rec.get("sha256") or "",
        "status": rec.get("status") or "pending",
        "wiki_state": rec.get("wikiState") or "pending",
        "language": rec.get("language") or summary.get("language") or "",
        "generated_by": GENERATED_BY,
        "tags": ["source"] + [t for t in summary.get("tags") or [] if t != "source"],
    })
    return fields


def summary_markdown(summary: dict, pages: dict[str, str] | None = None) -> str:
    """Render the summary part. With pages, mentions link to those page titles and the rest stay plain text."""
    lines = ["## Summary", "", summary["summary"], "", "## Key points", ""]
    for point in summary.get("key_points") or []:
        lines.append(f"- {point['claim']}")
        if point.get("quote"):
            lines.append(f"  > {point['quote']}")

    def mention(name: str) -> str:
        if pages is None:
            return f"[[{name}]]"
        title = pages.get(name.lower())
        if not title:
            return name
        return f"[[{title}]]" if title == name else f"[[{title}|{name}]]"

    names = ", ".join(mention(m["name"]) for m in summary.get("mentions") or [])
    lines += ["", "## Mentions", "", names or "None."]
    return "\n".join(lines) + "\n"


def source_folder(settings: Any, site: str) -> str:
    return f"{settings.sourcesFolder}/{site}"


def write_source(svc: Any, rec: dict, *, summary_part: str | None = None, captured_part: str | None = None,
                 title: str | None = None) -> WriteResult | None:
    """Write the frontmatter and parts of a source note. The captured part is written only once."""
    for _ in range(2):
        note = svc.notes.get(rec["id"])
        if note is None:
            return None
        body = frontmatter.body_of(note.get("content") or "")
        old_summary, old_captured = split_parts(body)
        new_captured = captured_part if captured_part is not None and not has_capture(old_captured) else old_captured
        content = frontmatter.dump(source_fields(rec)) + render_body(summary_part or old_summary, new_captured)
        try:
            return svc.notes.write(note, content, title=title, suggestion_title=f"Update source {rec['title']}",
                                   rationale="Bookmark wiki fetched or summarised this source again.", kind="source")
        except Conflict:
            continue
    log.warning("source note %s changed twice during a write", rec["id"])
    return None


# import ----------------------------------------------------------------------

def title_free(svc: Any, space: str, title: str, note_id: str) -> bool:
    """Report whether [[title]] would reach only note_id.

    Markspace resolves a name by slug and by case-insensitive title, so a
    source and a page whose titles differ only in case would share one link.
    """
    if svc.state.alias(title):
        return False
    try:
        items = svc.host.resolve(space, "", [title]).items
    except HostError:
        return True
    return not any(isinstance(i, dict) and i.get("id") and i.get("id") != note_id for i in items)


def _unique_title(svc: Any, space: str, title: str, site: str, note_id: str) -> str:
    candidates = [title, f"{title} ({site})"] + [f"{title} ({site}) {n}" for n in range(2, 50)]
    for cand in candidates:
        holder = svc.state.source_by_title(cand)
        if holder == note_id:
            return cand
        if holder is None and title_free(svc, space, cand, note_id) and svc.state.claim_source_title(cand, note_id):
            return cand
    fallback = f"{title} {note_id[-6:]}"
    svc.state.claim_source_title(fallback, note_id)
    return fallback


def _merge(rec: dict, b: Bookmark) -> bool:
    changed = False
    for key, values in (("tags", b.tags), ("lists", b.lists), ("folders", [b.folder] if b.folder else [])):
        merged = list(rec.get(key) or [])
        for v in values:
            if v not in merged:
                merged.append(v)
        if merged != (rec.get(key) or []):
            rec[key] = merged
            changed = True
    if b.added and (not rec.get("bookmarked") or b.added < rec["bookmarked"]):
        rec["bookmarked"] = b.added
        changed = True
    return changed


def _queue_fetch(svc: Any, ctx: Any, rec: dict, space: str, done: int, total: int) -> None:
    """Queue the fetch of a source.

    Markspace refuses a job with 429 while the plugin has 10,000 queued jobs.
    The import then waits for the queue to drain, reports progress on each
    wait, and stops when the job is canceled.
    """
    while True:
        try:
            svc.enqueue("fetch", {"sourceId": rec["id"]}, title=f"Fetch {rec['title']}", space_id=space)
            return
        except HostError as exc:
            if exc.status != 429:
                raise
        ctx.progress(done, total, f"The job queue is full. Imported {done:,} of {total:,}. Waiting for queued jobs.")
        ctx.check_canceled()
        sleep(QUEUE_WAIT)


def import_bookmarks(svc: Any, ctx: Any, space: str, bookmarks: list[Bookmark], importer: str) -> dict:
    """Create pending source notes for new URLs, merge known ones, and queue a fetch for each pending source."""
    settings = svc.settings()
    svc.files.ensure_all(space)
    counts = {"new": 0, "merged": 0, "known": 0, "invalid": 0, "queued": 0}
    total = len(bookmarks)
    for i, b in enumerate(bookmarks):
        if i % 10 == 0:
            ctx.progress(i, total, f"Importing {i} of {total}")
            ctx.check_canceled()
        try:
            norm = normalise_url(b.url)
        except InvalidURL:
            counts["invalid"] += 1
            continue
        with svc.url_lock(norm):
            existing = svc.state.url_note(norm)
            rec = svc.state.source(existing) if existing else None
            if rec is not None:
                if _merge(rec, b):
                    svc.state.save_source(rec)
                    write_source(svc, rec)
                    counts["merged"] += 1
                else:
                    counts["known"] += 1
            else:
                site = site_of(norm)
                note_id = stable_id("source", space, norm)
                title = clean_title(b.title) or url_title(norm)
                title = _unique_title(svc, space, title, site, note_id)
                rec = {"id": note_id, "spaceId": space, "url": b.url.strip(), "normalisedUrl": norm,
                       "title": title, "titleFromUrl": not clean_title(b.title), "site": site,
                       "status": "pending", "wikiState": "pending", "bookmarked": b.added,
                       "folders": [b.folder] if b.folder else [], "tags": list(b.tags), "lists": list(b.lists),
                       "importer": importer, "tries": 0, "fetchQueued": False}
                content = frontmatter.dump(source_fields(rec)) + render_body(PENDING_SUMMARY, PENDING_CAPTURED)
                note = svc.notes.create(space, source_folder(settings, site), title, content, client_id=note_id)
                if note["id"] != note_id:
                    svc.state.claim_source_title(title, note["id"])
                    rec["id"] = note["id"]
                svc.state.save_source(rec)
                svc.state.set_url_note(norm, rec["id"])
                counts["new"] += 1
            if rec["status"] == "pending" and not rec.get("fetchQueued"):
                _queue_fetch(svc, ctx, rec, space, i, total)
                rec["fetchQueued"] = True
                svc.state.save_source(rec)
                counts["queued"] += 1
    ctx.progress(total, total, "Import finished")
    svc.files.append_log(space, "import", importer,
                         f"new {counts['new']}, merged {counts['merged']}, known {counts['known']}")
    return counts


def load_import(svc: Any, payload: dict) -> tuple[str, list[Bookmark]] | None:
    """Return the importer and bookmarks named by an import payload, or None when a stored import is gone."""
    importer = str(payload.get("importer") or "urls")
    if payload.get("importId"):
        raw = svc.host.blob_get(f"imports/{payload['importId']}.json")
        if raw is None:
            return None
        data = json.loads(raw)
        return str(data.get("importer") or importer), [Bookmark.from_json(b) for b in data.get("bookmarks") or []]
    if payload.get("fileId"):
        from .importers import parse

        return importer, parse(importer, svc.host.file_content(payload["fileId"]))
    return importer, [Bookmark.from_json(b) for b in payload.get("bookmarks") or [] if isinstance(b, dict)]


def job_import(svc: Any, ctx: Any) -> dict:
    payload = ctx.payload or {}
    space = svc.space_id(str(payload.get("spaceId") or ctx.job.space_id or ""))
    try:
        loaded = load_import(svc, payload)
    except ImporterError as exc:
        ctx.log("error", str(exc))
        return {"error": str(exc)}
    if loaded is None:
        return {"skipped": "the stored import is gone"}
    importer, bookmarks = loaded
    counts = import_bookmarks(svc, ctx, space, bookmarks, importer)
    if payload.get("importId"):
        meta = svc.state.get(f"import:{payload['importId']}") or {}
        if meta:
            meta.update({"status": "done", "result": counts})
            svc.state.put(f"import:{payload['importId']}", meta)
    return counts


# fetch -----------------------------------------------------------------------

def _robots_refusal(exc: HostError) -> bool:
    if getattr(exc, "code", None) == "robots_disallowed":
        return True
    return "robots" in (exc.message or "").lower() or (exc.field or "") == "robots"


def _set_status(svc: Any, rec: dict, status: str, reason: str) -> dict:
    rec["status"] = status
    rec["error"] = reason
    rec["tries"] = 0
    rec["fetched"] = iso(svc.clock())
    svc.state.save_source(rec)
    write_source(svc, rec)
    return {"status": status, "reason": reason}


def job_fetch(svc: Any, ctx: Any) -> dict:
    payload = ctx.payload or {}
    rec = svc.state.source(str(payload.get("sourceId") or ""))
    if rec is None:
        return {"skipped": "source is gone"}
    paused = svc.requeue_if_paused(ctx, "fetch")
    if paused:
        return paused
    if svc.notes.get(rec["id"]) is None:
        svc.state.drop_source(rec["id"])
        return {"skipped": "source note is gone"}
    return fetch_source(svc, ctx, rec, refetch=bool(payload.get("refetch")))


def fetch_source(svc: Any, ctx: Any, rec: dict, refetch: bool = False) -> dict:
    settings = svc.settings()
    if rec.get("fileId") and not rec.get("capturedAt") and rec.get("status") == "pending":
        return _pdf_text(svc, ctx, rec)
    url = rec["url"]
    host_name = (urlsplit(url).hostname or "").lower()
    with svc.slots.acquire(settings.fetchConcurrency):
        svc.gate.wait(host_name)
        try:
            resp = svc.host.web_fetch(url, respect_robots=settings.respectRobots)
        except HostError as exc:
            if _robots_refusal(exc):
                return _set_status(svc, rec, "blocked", f"robots.txt disallows the page: {exc.message}")
            if exc.status in (400, 403, 422):
                return _set_status(svc, rec, "blocked", f"the fetcher refused the URL: {exc.message}")
            if exc.status in (413, 415):
                return _set_status(svc, rec, "not-text", f"the fetcher refused the content: {exc.message}")
            raise
    if not isinstance(resp, dict):
        raise HostError(200, "web fetch returned no object")
    if resp.get("robots") in ("disallowed", "blocked") or resp.get("blockedBy") == "robots":
        return _set_status(svc, rec, "blocked", "robots.txt disallows the page")
    status = int(resp.get("status") or 200)
    if status in DEAD_STATUSES:
        return _set_status(svc, rec, "dead", f"HTTP {status}")
    if status in BLOCKED_STATUSES:
        rec["tries"] = int(rec.get("tries") or 0) + 1
        if rec["tries"] >= MAX_BLOCKED_TRIES:
            return _set_status(svc, rec, "blocked", f"HTTP {status} after {MAX_BLOCKED_TRIES} tries")
        svc.state.save_source(rec)
        svc.enqueue("fetch", {"sourceId": rec["id"], "refetch": refetch}, title=f"Fetch {rec['title']}",
                    space_id=rec["spaceId"], delay=60 * rec["tries"])
        return {"retry": rec["tries"], "status": status}
    if status >= 400:
        if ctx.attempt >= MAX_SERVER_ATTEMPTS:
            rec["wikiState"] = "failed" if not rec.get("capturedAt") else rec.get("wikiState", "failed")
            rec["error"] = f"HTTP {status}"
            svc.state.save_source(rec)
            write_source(svc, rec)
            return {"failed": f"HTTP {status}"}
        raise RuntimeError(f"{url} answered HTTP {status}")
    rec["tries"] = 0
    final = str(resp.get("finalUrl") or url)
    rec["finalUrl"] = final
    rec["canonicalUrl"] = str(resp.get("canonicalUrl") or final)
    ctype = str(resp.get("contentType") or resp.get("mime") or "").lower()
    path = urlsplit(final).path.lower()
    if "pdf" in ctype or (path.endswith(".pdf") and not str(resp.get("markdown") or "").strip()):
        return _pdf(svc, ctx, rec, resp, refetch)
    text = str(resp.get("markdown") or resp.get("text") or "")
    digest = str(resp.get("sha256") or sha256(text))
    return _captured(svc, rec, text, digest, str(resp.get("title") or ""))


def _captured(svc: Any, rec: dict, text: str, digest: str, page_title: str, lead: str = "") -> dict:
    now = iso(svc.clock())
    if not text.strip():
        return _set_status(svc, rec, "not-text", "the page has no readable text")
    if rec.get("capturedAt"):
        rec["fetched"] = now
        rec["status"] = "ok"
        rec.pop("error", None)
        drift = bool(rec.get("sha256")) and digest != rec["sha256"]
        if drift:
            record_drift(svc, rec, text, digest)
        svc.state.save_source(rec)
        write_source(svc, rec)
        if rec.get("wikiState") in ("pending", "fetched"):
            svc.enqueue("summarise", {"sourceId": rec["id"]}, title=f"Summarise {rec['title']}", space_id=rec["spaceId"])
        return {"status": "ok", "drift": drift}
    new_title = None
    if rec.get("titleFromUrl") and clean_title(page_title):
        new_title = _unique_title(svc, rec["spaceId"], clean_title(page_title), rec["site"], rec["id"])
        rec["title"] = new_title
        rec["titleFromUrl"] = False
    rec.update({"status": "ok", "sha256": digest, "fetched": now, "capturedAt": now})
    rec.pop("error", None)
    if rec.get("wikiState") in ("pending", "failed"):
        rec["wikiState"] = "fetched"
    captured = "## Captured text\n\n" + lead + text[:MAX_CAPTURED].strip() + "\n"
    svc.state.save_source(rec)
    write_source(svc, rec, captured_part=captured, title=new_title)
    svc.enqueue("summarise", {"sourceId": rec["id"]}, title=f"Summarise {rec['title']}", space_id=rec["spaceId"])
    return {"status": "ok", "captured": len(text)}


def record_drift(svc: Any, rec: dict, text: str, digest: str) -> None:
    """Keep the new text in a drift note under Wiki/Lint/Drift and list it on the source record."""
    entries = list(rec.get("drift") or [])
    if any(e.get("newSha") == digest for e in entries):
        return
    date = svc.clock().strftime("%Y-%m-%d")
    settings = svc.settings()
    title = clean_title(f"{rec['title']} (fetched {date})")
    fields = {"title": title, "type": "drift", "source": f"[[{rec['title']}]]", "source_url": rec["url"],
              "fetched": iso(svc.clock()), "sha256": digest, "generated_by": GENERATED_BY}
    body = (f"The page at {rec['url']} changed after the first capture. The source note [[{rec['title']}]] keeps "
            f"the first capture.\n\n## Text fetched on {date}\n\n{text[:MAX_CAPTURED].strip()}\n")
    note = svc.notes.create(rec["spaceId"], f"{settings.wikiFolder}/Lint/Drift", title,
                            frontmatter.dump(fields) + body, client_id=stable_id("drift", rec["id"], digest))
    entries.append({"date": iso(svc.clock()), "oldSha": rec.get("sha256", ""), "newSha": digest,
                    "noteId": note["id"], "title": title})
    rec["drift"] = entries[-20:]


def _pdf(svc: Any, ctx: Any, rec: dict, resp: dict, refetch: bool) -> dict:
    raw_b64 = resp.get("bodyBase64") or resp.get("contentBase64") or ""
    digest = str(resp.get("sha256") or (sha256(base64.b64decode(raw_b64)) if raw_b64 else ""))
    if rec.get("capturedAt"):
        rec["fetched"] = iso(svc.clock())
        rec["status"] = "ok"
        drift = bool(digest and rec.get("pdfSha256") and digest != rec["pdfSha256"])
        if drift:
            record_drift(svc, rec, "The PDF changed. The fetcher returned no text for the new file.", digest)
        svc.state.save_source(rec)
        write_source(svc, rec)
        return {"status": "ok", "drift": drift}
    if not rec.get("fileId"):
        if not raw_b64:
            return _set_status(svc, rec, "not-text", "the fetcher returned no bytes for the PDF")
        data = base64.b64decode(raw_b64)
        space = rec["spaceId"]
        parent = svc.notes.folder_id(space, source_folder(svc.settings(), rec["site"]))
        uploaded = svc.host.upload_file(space, data, f"{slugify(rec['title'])}.pdf", parent_id=parent,
                                        mime="application/pdf", on_conflict="rename",
                                        id=stable_id("pdf", rec["id"]))
        rec["fileId"] = uploaded["id"]
        rec["fileName"] = uploaded.get("name") or f"{slugify(rec['title'])}.pdf"
        rec["pdfSha256"] = digest or sha256(data)
        rec["textWaits"] = 0
        if rec.get("titleFromUrl") and clean_title(str(resp.get("title") or "")):
            rec["pendingTitle"] = clean_title(str(resp["title"]))
        svc.state.save_source(rec)
    return _pdf_text(svc, ctx, rec)


def _pdf_text(svc: Any, ctx: Any, rec: dict) -> dict:
    extracted = svc.host.file_text(rec["fileId"])
    if not extracted.text.strip():
        rec["textWaits"] = int(rec.get("textWaits") or 0) + 1
        if rec["textWaits"] >= MAX_TEXT_WAITS:
            return _set_status(svc, rec, "not-text", "no text could be extracted from the PDF")
        svc.state.save_source(rec)
        svc.enqueue("fetch", {"sourceId": rec["id"]}, title=f"Read text of {rec['title']}", space_id=rec["spaceId"],
                    delay=60)
        return {"waiting": "file text", "waits": rec["textWaits"]}
    lead = f"Original file: [[{rec.get('fileName')}]]\n\n" if rec.get("fileName") else ""
    if extracted.truncated:
        lead += "The extracted text stops at the extraction limit.\n\n"
    return _captured(svc, rec, extracted.text, rec.get("pdfSha256") or sha256(extracted.text),
                     rec.pop("pendingTitle", ""), lead=lead)


# summarise -------------------------------------------------------------------

def job_summarise(svc: Any, ctx: Any) -> dict:
    payload = ctx.payload or {}
    rec = svc.state.source(str(payload.get("sourceId") or ""))
    if rec is None:
        return {"skipped": "source is gone"}
    paused = svc.requeue_if_paused(ctx, "summarise")
    if paused:
        return paused
    if rec.get("status") != "ok":
        return {"skipped": f"source status is {rec.get('status')}"}
    note = svc.notes.get(rec["id"])
    if note is None:
        svc.state.drop_source(rec["id"])
        return {"skipped": "source note is gone"}
    text = captured_text(frontmatter.body_of(note.get("content") or ""))
    if not text:
        return {"skipped": "source has no captured text"}
    settings = svc.settings()
    language = (f"the language with the code {settings.summaryLanguage}" if settings.summaryLanguage
                else "the language of the captured text")
    data = {"title": rec["title"], "url": rec["url"], "site": rec["site"], "captured_text": text[:PROMPT_CHARS]}
    try:
        summary, info = svc.ai.complete_json(
            "summarise", "summarise", {"input_json": data, "language_rule": language},
            lambda obj: schemas.summary(obj, text), space_id=rec["spaceId"], model_spec=settings.summariseModel)
    except ModelOutputError as exc:
        rec["wikiState"] = "failed"
        rec["error"] = "summary failed: " + "; ".join(exc.problems)[:500]
        svc.state.save_source(rec)
        write_source(svc, rec)
        ctx.log("warn", rec["error"])
        return {"failed": rec["error"]}
    digest = sha256(canonical(summary))
    force = bool(payload.get("force"))
    if rec.get("summaryHash") == digest and rec.get("wikiState") in ("summarised", "compiled") and not force:
        return {"unchanged": True}
    rec["summary"] = summary
    rec["summaryHash"] = digest
    for key in ("author", "published", "language"):
        if summary.get(key):
            rec[key] = summary[key]
    rec["wikiState"] = "summarised"
    rec.pop("error", None)
    svc.state.save_source(rec)
    write_source(svc, rec, summary_part=summary_markdown(summary))
    svc.enqueue("compile", {"sourceId": rec["id"], "force": force}, title=f"Compile {rec['title']}",
                space_id=rec["spaceId"])
    return {"summarised": True, "repaired": info.repaired, "mentions": len(summary["mentions"])}


# refetch and sync ------------------------------------------------------------

def job_refetch(svc: Any, ctx: Any) -> dict:
    if svc.state.paused():
        return {"paused": True}
    space = svc.space_id(str((ctx.payload or {}).get("spaceId") or ctx.job.space_id or ""))
    recs = [r for r in svc.state.sources()
            if r.get("spaceId") == space and (r.get("capturedAt") or r.get("status") == "dead")]
    recs.sort(key=lambda r: r.get("fetched") or "")
    for r in recs[:REFETCH_BATCH]:
        svc.enqueue("fetch", {"sourceId": r["id"], "refetch": True}, title=f"Re-fetch {r['title']}", space_id=space)
    return {"queued": min(len(recs), REFETCH_BATCH)}


def api_client(svc: Any, importer: str) -> KarakeepClient | LinkdingClient | None:
    url = svc.secret(f"{importer}_url")
    token = svc.secret(f"{importer}_token")
    if not url or not token:
        return None
    cls = KarakeepClient if importer == "karakeep" else LinkdingClient
    return cls(url, token, transport=svc.transport)


def job_sync(svc: Any, ctx: Any) -> dict:
    payload = ctx.payload or {}
    if svc.state.paused() and not payload.get("force"):
        return {"paused": True}
    space = svc.space_id(str(payload.get("spaceId") or ctx.job.space_id or ""))
    settings = svc.settings()
    now = svc.clock()
    results: dict[str, Any] = {}
    for importer in [payload["importer"]] if payload.get("importer") else ["karakeep", "linkding"]:
        client = api_client(svc, importer)
        if client is None:
            results[importer] = "not configured"
            continue
        key = f"sync:{importer}"
        last = svc.state.get(key) or {}
        last_run = parse_iso(str(last.get("lastRun") or ""))
        if not payload.get("force") and last_run and now - last_run < timedelta(hours=settings.importEveryHours):
            results[importer] = "not due"
            continue
        try:
            fetched = client.fetch(since=str(last.get("newest") or ""))
        except ImporterError as exc:
            ctx.log("warn", f"{importer}: {exc}")
            results[importer] = f"failed: {exc}"
            continue
        counts = import_bookmarks(svc, ctx, space, fetched.bookmarks, importer)
        newest = max(str(last.get("newest") or ""), fetched.newest) if fetched.complete else str(last.get("newest") or "")
        svc.state.put(key, {"lastRun": iso(now), "newest": newest, "result": counts})
        results[importer] = counts
    return results
