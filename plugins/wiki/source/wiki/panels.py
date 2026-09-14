"""Panels, commands, actions and importers.

Every result is a declarative UI tree or a command response. Forms and
confirmations stay inline, as docs/ui.md requires. Toasts are not used, because
the results of these interactions show where the user acted.
"""

from __future__ import annotations

import json
import secrets
import time
from typing import Any

from markspace import HostError, ui
from markspace import job as job_response
from markspace import ui_response

from . import cost
from .ask import ask as run_ask
from .ask import file_answer
from .importers import Bookmark, ImporterError
from .importers import parse as parse_file
from .jsonparse import ModelOutputError
from .manifest import SETTINGS_FIELDS
from .service import SpaceNotSet
from .sources import api_client
from .state import STATUSES, WIKI_STATES
from .urls import InvalidURL, normalise_url
from .util import iso

PREVIEW_BUDGET = 6.0
IMPORTERS = {"netscape": "Browser bookmarks", "urls": "URL list", "karakeep": "Karakeep", "linkding": "Linkding"}


def notice(title: str, body: str = "") -> dict:
    return ui_response(ui.empty(title, body=body or None))


# forms -----------------------------------------------------------------------

def ask_form(value: str = "") -> dict:
    return ui.form("ask", [ui.field("textarea", "question", label="Question", value=value or None, required=True)],
                   ui.action("ask"), submit="Ask the wiki")


def import_form() -> dict:
    return ui.form("import-file", [
        ui.field("file", "fileId", label="Bookmark file", required=True),
        ui.field("select", "importer", label="Format", value="netscape",
                 options=[ui.option("netscape", "Browser bookmarks (.html)"), ui.option("urls", "URL list (.txt)")]),
    ], ui.action("preview-import"), submit="Estimate import",
        description="The estimate shows the new URLs and the AI cost before anything is queued.")


def settings_form(svc: Any) -> dict:
    values = svc.settings().to_json()
    try:
        models = svc.host.ai_models()
    except HostError:
        models = []
    fields = []
    for spec in SETTINGS_FIELDS:
        name = spec["name"]
        kwargs = {"label": spec.get("label"), "hint": spec.get("hint"), "required": spec.get("required")}
        value = values.get(name)
        if name in ("summariseModel", "compileModel") and models:
            options = [ui.option("", "Route default")]
            options += [ui.option(f"{m.provider_id}/{m.model}", f"{m.model} ({m.provider_id})") for m in models]
            if value and value not in {o["value"] for o in options}:
                options.append(ui.option(value, value))
            fields.append(ui.field("select", name, value=value, options=options, **kwargs))
        else:
            fields.append(ui.field(spec["type"], name, value=value, **kwargs))
    main, more = fields[:3], fields[3:]
    return ui.form("settings", main, ui.action("save-settings"), title="Settings", submit="Save settings", more=more)


def preview_tree(meta: dict, est: dict) -> dict:
    lines = (f"{meta['total']:,} bookmarks from {IMPORTERS.get(meta['importer'], meta['importer'])}. "
             f"{meta['new']:,} are new, {meta['known']:,} are already in the wiki, and "
             f"{meta['invalid']:,} are not http or https URLs.")
    if meta.get("partial"):
        lines += " The check of known URLs ran out of time, so the new count is an upper bound."
    children = [ui.markdown(lines + "\n\n" + cost.describe(est))]
    if meta["new"] or meta["known"]:
        children.append(ui.button(f"Queue {meta['unique']:,} bookmarks", ui.action("confirm-import", {"importId": meta["id"]}),
                                  variant="primary"))
    children.append(ui.button("Discard", ui.action("discard-import", {"importId": meta["id"]}), variant="secondary",
                              confirm="Confirm discard"))
    return ui.section("Import estimate", *children)


# previews --------------------------------------------------------------------

def create_preview(svc: Any, space: str, importer: str, bookmarks: list[Bookmark]) -> dict:
    """Store parsed bookmarks and return the estimate tree with its confirm button."""
    seen: set[str] = set()
    unique: list[Bookmark] = []
    invalid = 0
    for b in bookmarks:
        try:
            norm = normalise_url(b.url)
        except InvalidURL:
            invalid += 1
            continue
        if norm not in seen:
            seen.add(norm)
            unique.append(b)
    new = known = 0
    partial = False
    deadline = time.monotonic() + PREVIEW_BUDGET
    for b in unique:
        if time.monotonic() > deadline:
            partial = True
            break
        if svc.state.url_note(normalise_url(b.url)):
            known += 1
        else:
            new += 1
    if partial:
        new = len(unique) - known
    import_id = secrets.token_hex(8)
    svc.host.blob_put(f"imports/{import_id}.json",
                      json.dumps({"importer": importer, "bookmarks": [b.to_json() for b in unique]}).encode(),
                      content_type="application/json")
    meta = {"id": import_id, "importer": importer, "spaceId": space, "total": len(bookmarks), "unique": len(unique),
            "new": new, "known": known, "invalid": invalid, "partial": partial, "createdAt": iso(svc.clock()),
            "status": "pending"}
    svc.state.put(f"import:{import_id}", meta)
    return preview_tree(meta, cost.estimate(svc, new))


def pending_imports(svc: Any) -> list[dict]:
    out = []
    for key in svc.state.keys("import:"):
        meta = svc.state.get(key)
        if isinstance(meta, dict) and meta.get("status") == "pending":
            out.append(meta)
    return sorted(out, key=lambda m: m.get("createdAt", ""), reverse=True)


# panels ----------------------------------------------------------------------

def home(svc: Any, ctx: Any) -> dict:
    try:
        space = svc.space_id(ctx.space_id)
    except SpaceNotSet:
        space = ""
    settings = svc.settings()
    counts = svc.state.counts()
    total = sum(counts["status"].values())
    children: list[dict] = []
    if not space:
        children.append(ui.empty("Choose the wiki space", body="Set the wiki space in the settings at the end of this page."))
    children.append(ui.section(
        "Sources",
        ui.table([ui.column("state", "Fetch status"), ui.column("n", "Sources", numeric=True)],
                 [{"state": s, "n": counts["status"][s]} for s in STATUSES]),
        ui.table([ui.column("state", "Wiki state"), ui.column("n", "Sources", numeric=True)],
                 [{"state": s, "n": counts["wikiState"][s]} for s in WIKI_STATES]),
        count=total))
    runs = svc.state.runs()[:10]
    children.append(ui.section("Jobs", *([ui.progress(0, label=f"{r['title']}, queued {r['createdAt'][:16].replace('T', ' ')}",
                                                      job_id=r["jobId"]) for r in runs]
                                         or [ui.text("No import or lint runs yet.")])))
    import_children = [preview_tree(m, cost.estimate(svc, m["new"])) for m in pending_imports(svc)[:3]]
    import_children.append(import_form())
    children.append(ui.section("Import", *import_children))
    items = []
    for importer in ("karakeep", "linkding"):
        configured = bool(svc.secret(f"{importer}_url") and svc.secret(f"{importer}_token"))
        last = svc.state.get(f"sync:{importer}") or {}
        if configured:
            subtitle = f"Imports new bookmarks every {settings.importEveryHours} hours."
            if last.get("lastRun"):
                subtitle += f" Last run {last['lastRun'][:16].replace('T', ' ')}."
            actions = [ui.button("Estimate full import", ui.action("preview-api", {"importer": importer}),
                                 variant="secondary")]
            items.append(ui.item(IMPORTERS[importer], subtitle=subtitle, badge={"label": "Connected", "tone": "good"},
                                 actions=actions))
        else:
            items.append(ui.item(IMPORTERS[importer],
                                 subtitle=f"Fill in the {importer}_url and {importer}_token secrets in Admin, Plugins."))
    children.append(ui.section("Connections", ui.list(items)))
    paused = svc.state.paused()
    children.append(ui.section(
        "Controls",
        ui.button("Resume jobs" if paused else "Pause jobs", ui.action("resume" if paused else "pause"),
                  variant="secondary"),
        ui.button("Run lint now", ui.action("run-lint"), variant="secondary"),
        description="Jobs are paused. Queued jobs wait until you resume." if paused else None))
    per_hundred = cost.estimate(svc, 100)
    children.append(ui.section("Cost estimate", ui.markdown(
        "Fetch and summarise use about 3,000 input and 500 output tokens per source. Compile uses about 6,000 input "
        "and 1,500 output tokens per wiki page. For 100 new sources: " + cost.describe(per_hundred))))
    usage = svc.state.usage()
    rows = [{"purpose": p, "calls": u.get("calls", 0), "input": u.get("inputTokens", 0), "output": u.get("outputTokens", 0)}
            for p, u in sorted(usage.items())]
    children.append(ui.section("AI usage this month", ui.table(
        [ui.column("purpose", "Purpose"), ui.column("calls", "Calls", numeric=True),
         ui.column("input", "Input tokens", numeric=True), ui.column("output", "Output tokens", numeric=True)], rows)
        if rows else ui.text("No AI calls this month.")))
    children.append(settings_form(svc))
    return ui.stack(*children, gap="l")


def wiki(svc: Any, ctx: Any) -> dict:
    item_id = ctx.item_id
    if not item_id:
        return ui.section("Ask the wiki", ask_form())
    rec = svc.state.source(item_id)
    if rec:
        rows = [ui.item("Fetch status", subtitle=rec.get("error") or None,
                        badge={"label": rec.get("status", "pending"),
                               "tone": {"ok": "good", "dead": "bad", "blocked": "warn"}.get(rec.get("status"), "neutral")}),
                ui.item("Wiki state", badge={"label": rec.get("wikiState", "pending"), "tone": "neutral"}),
                ui.item("Fetched", subtitle=(rec.get("fetched") or "not yet")[:16].replace("T", " "))]
        pages = []
        for page_id in rec.get("pages") or []:
            page = svc.state.page(page_id)
            if page:
                pages.append(ui.item(page.get("title") or page_id, subtitle=page.get("type"), link=ui.target(item_id=page_id)))
        return ui.stack(
            ui.section("Source", ui.list(rows),
                       ui.button("Re-fetch", ui.action("refetch-source", {"sourceId": item_id}), variant="secondary"),
                       ui.button("Re-summarise", ui.action("resummarise-source", {"sourceId": item_id}), variant="secondary")),
            ui.section("Pages built from it", ui.list(pages, empty="No pages yet."), count=len(pages)), gap="m")
    page = svc.state.page(item_id)
    if page:
        items = []
        for source_id in page.get("sources") or []:
            src = svc.state.source(source_id) if source_id else None
            if src:
                items.append(ui.item(src["title"], subtitle=src.get("site"), link=ui.target(item_id=source_id)))
        children = [ui.section("Sources", ui.list(items, empty="No sources."), count=len(items))]
        if page.get("contested"):
            children.append(ui.badge("Contested", "warn"))
        if page.get("type") != "answer":
            children.append(ui.button("Regenerate page", ui.action("regenerate-page", {"pageId": item_id}),
                                      variant="secondary"))
        return ui.stack(*children, gap="m")
    related = []
    try:
        note = ctx.host.get_item(item_id).get("note") or {}
        settings = svc.settings()
        hits = svc.host.search(note.get("title") or "", mode="text", space=note.get("spaceId") or None, limit=20).items
        for hit in hits:
            n = hit.get("note") if isinstance(hit, dict) else None
            if isinstance(n, dict) and str(n.get("path", "")).startswith(settings.wikiFolder + "/") and svc.state.page(n["id"]):
                related.append(ui.item(n.get("title") or n["id"], link=ui.target(item_id=n["id"])))
    except HostError:
        pass
    return ui.stack(ui.section("Related wiki pages", ui.list(related[:8], empty="No related wiki pages.")),
                    ui.section("Ask the wiki", ask_form()), gap="m")


# commands --------------------------------------------------------------------

def command_add_url(svc: Any, ctx: Any) -> dict:
    link = ctx.context.get("link")
    url = link.get("url") if isinstance(link, dict) else link
    if not url:
        return ui_response(ui.form("add-url", [ui.field("url", "url", label="URL", required=True)],
                                   ui.action("add-url"), submit="Add to wiki"))
    return queue_url(svc, ctx, str(url))


def command_ask(svc: Any, ctx: Any) -> dict:
    return ui_response(ask_form())


def queue_url(svc: Any, ctx: Any, url: str) -> dict:
    try:
        normalise_url(url)
    except InvalidURL as exc:
        return ui_response(ui.stack(ui.text(f"That is not a web address: {exc}."),
                                    ui.form("add-url", [ui.field("url", "url", label="URL", value=url, required=True)],
                                            ui.action("add-url"), submit="Add to wiki")))
    space = svc.space_id(ctx.space_id)
    created = svc.enqueue("import", {"importer": "urls", "spaceId": space, "bookmarks": [{"url": url, "source": "urls"}]},
                          title="Add link to wiki", space_id=space)
    return job_response(created["id"])


# actions ---------------------------------------------------------------------

def action_add_url(svc: Any, ctx: Any) -> dict:
    return queue_url(svc, ctx, str(ctx.values.get("url") or ctx.payload.get("url") or ""))


def action_ask(svc: Any, ctx: Any) -> dict:
    question = str(ctx.values.get("question") or ctx.payload.get("question") or "")
    if not question.strip():
        return ui_response(ui.stack(ui.text("Enter a question."), ask_form()))
    space = svc.space_id(ctx.space_id)
    try:
        result = run_ask(svc, space, question)
    except ModelOutputError as exc:
        return ui_response(ui.stack(ui.text(f"The model answer could not be used: {exc}."), ask_form(question)))
    links = []
    for title in result["cited"]:
        target = svc.state.alias(title) or svc.state.source_by_title(title)
        if target:
            links.append(ui.item(title, link=ui.target(item_id=target)))
    payload = {k: result[k] for k in ("question", "answer", "title", "cited")}
    children = [ui.markdown(result["answer"])]
    if links:
        children.append(ui.section("Cited", ui.list(links), count=len(links)))
    children.append(ui.button("File this answer", ui.action("file-answer", payload), variant="primary"))
    return ui_response(ui.stack(*children, gap="m"))


def action_file_answer(svc: Any, ctx: Any) -> dict:
    p = ctx.payload
    if not p.get("question") or not p.get("answer"):
        return notice("Nothing to file", "Ask a question first.")
    space = svc.space_id(ctx.space_id)
    note, created = file_answer(svc, space, str(p["question"]), str(p["answer"]), str(p.get("title") or ""),
                                [str(c) for c in p.get("cited") or []])
    label = "Filed as" if created else "Already filed as"
    return ui_response(ui.stack(ui.text(label), ui.link(note.get("title") or "the answer", item_id=note["id"]), gap="s"))


def action_preview_import(svc: Any, ctx: Any) -> dict:
    file_id = str(ctx.values.get("fileId") or ctx.values.get("file") or "")
    importer = str(ctx.values.get("importer") or "netscape")
    if not file_id or importer not in ("netscape", "urls"):
        return ui_response(ui.stack(ui.text("Choose a bookmark file and its format."), import_form()))
    return importer_preview(svc, ctx.host, importer, file_id, ctx.space_id)


def importer_preview(svc: Any, host: Any, importer: str, file_id: str, space_hint: str) -> dict:
    space = svc.space_id(space_hint if space_hint and not svc.settings().space else "")
    try:
        bookmarks = parse_file(importer, host.file_content(file_id))
    except ImporterError as exc:
        return ui_response(ui.stack(ui.text(str(exc)), import_form()))
    return ui_response(create_preview(svc, space, importer, bookmarks))


def action_confirm_import(svc: Any, ctx: Any) -> dict:
    import_id = str(ctx.payload.get("importId") or "")
    meta = svc.state.get(f"import:{import_id}") if import_id else None
    if not isinstance(meta, dict):
        return notice("This import is gone", "Upload the file again.")
    if meta.get("status") != "pending" and meta.get("jobId"):
        return job_response(meta["jobId"])
    title = f"Import {meta['unique']:,} bookmarks from {IMPORTERS.get(meta['importer'], meta['importer'])}"
    created = svc.enqueue("import", {"importer": meta["importer"], "importId": import_id, "spaceId": meta["spaceId"]},
                          title=title, space_id=meta["spaceId"])
    meta.update({"status": "queued", "jobId": created["id"]})
    svc.state.put(f"import:{import_id}", meta)
    svc.state.add_run(created, "import", title)
    return job_response(created["id"])


def action_discard_import(svc: Any, ctx: Any) -> dict:
    import_id = str(ctx.payload.get("importId") or "")
    if import_id:
        svc.state.delete(f"import:{import_id}")
        try:
            svc.host.blob_delete(f"imports/{import_id}.json")
        except HostError as exc:
            if exc.status != 404:
                raise
    return ui_response(import_form())


def action_preview_api(svc: Any, ctx: Any) -> dict:
    importer = str(ctx.payload.get("importer") or "")
    if importer not in ("karakeep", "linkding"):
        return notice("Unknown importer")
    client = api_client(svc, importer)
    if client is None:
        return notice(f"{IMPORTERS[importer]} is not connected",
                      f"Fill in the {importer}_url and {importer}_token secrets in Admin, Plugins.")
    space = svc.space_id(ctx.space_id)
    try:
        result = client.fetch(deadline_seconds=PREVIEW_BUDGET)
    except ImporterError as exc:
        return notice(f"{IMPORTERS[importer]} did not answer", str(exc))
    tree = create_preview(svc, space, importer, result.bookmarks)
    if not result.complete:
        tree = ui.stack(ui.text("Paging stopped at the time limit. The scheduled import brings in the rest."), tree)
    return ui_response(tree)


def action_pause(svc: Any, ctx: Any) -> dict:
    svc.state.set_paused(True)
    return ui_response(home(svc, ctx))


def action_resume(svc: Any, ctx: Any) -> dict:
    svc.state.set_paused(False)
    return ui_response(home(svc, ctx))


def action_run_lint(svc: Any, ctx: Any) -> dict:
    space = svc.space_id(ctx.space_id)
    created = svc.enqueue("lint", {"spaceId": space, "manual": True}, title="Lint the wiki", space_id=space)
    svc.state.add_run(created, "lint", "Lint the wiki")
    return job_response(created["id"])


def action_save_settings(svc: Any, ctx: Any) -> dict:
    errors = svc.save_settings(ctx.values)
    if errors:
        text = " ".join(f"{name}: {message}" for name, message in errors.items())
        return ui_response(ui.stack(ui.text(text), settings_form(svc)))
    return ui_response(settings_form(svc))


def action_refetch_source(svc: Any, ctx: Any) -> dict:
    rec = svc.state.source(str(ctx.payload.get("sourceId") or ""))
    if not rec:
        return notice("This note is not a source")
    created = svc.enqueue("fetch", {"sourceId": rec["id"], "refetch": bool(rec.get("capturedAt"))},
                          title=f"Re-fetch {rec['title']}", space_id=rec["spaceId"])
    return job_response(created["id"])


def action_resummarise_source(svc: Any, ctx: Any) -> dict:
    rec = svc.state.source(str(ctx.payload.get("sourceId") or ""))
    if not rec or rec.get("status") != "ok":
        return notice("Nothing to summarise", "The source has no captured text yet.")
    created = svc.enqueue("summarise", {"sourceId": rec["id"], "force": True}, title=f"Summarise {rec['title']}",
                          space_id=rec["spaceId"])
    return job_response(created["id"])


def action_regenerate_page(svc: Any, ctx: Any) -> dict:
    page = svc.state.page(str(ctx.payload.get("pageId") or ""))
    if not page or not page.get("sources"):
        return notice("This page has no sources to regenerate from")
    first = None
    for source_id in page["sources"]:
        rec = svc.state.source(source_id) if source_id else None
        if not rec or not rec.get("summary"):
            continue
        created = svc.enqueue("compile", {"sourceId": source_id, "force": True, "only": [page["id"]]},
                              title=f"Regenerate {page.get('title')}", space_id=rec["spaceId"])
        first = first or created
    if first is None:
        return notice("No summarised source is left for this page")
    return job_response(first["id"])


ACTIONS = {
    "add-url": action_add_url,
    "ask": action_ask,
    "file-answer": action_file_answer,
    "preview-import": action_preview_import,
    "confirm-import": action_confirm_import,
    "discard-import": action_discard_import,
    "preview-api": action_preview_api,
    "pause": action_pause,
    "resume": action_resume,
    "run-lint": action_run_lint,
    "save-settings": action_save_settings,
    "refetch-source": action_refetch_source,
    "resummarise-source": action_resummarise_source,
    "regenerate-page": action_regenerate_page,
}
