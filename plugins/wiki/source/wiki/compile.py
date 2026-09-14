"""The compile job, spec.md steps 1 to 8.

Idempotency works on three levels. The source fingerprint hashes the summary,
the source title, schema.md and the page limit. A compile whose fingerprint
equals the one stored after the last complete compile returns at once and
writes nothing. force skips that check. Each page record also stores the
fingerprint of every source compiled into it, and a page that already holds
this input is skipped without a model call. Only a Regenerate page run, which
names the page in only, calls the model for it again. Last, a write whose body
and frontmatter match the note, ignoring updated, is not sent, and an open
suggestion with the same edits is kept.

A new page never takes a title that already resolves to another item,
because Markspace resolves [[name]] by slug and by case-insensitive title.
Such a page gets a qualifier, as in "Write-ahead logging (concept)", and the
plain name becomes an alias.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Any

from markspace import HostError

from . import frontmatter, wikilinks
from . import schemas
from .jsonparse import ModelOutputError
from .notes import GENERATED_BY, Conflict
from .sources import summary_markdown, write_source
from .urls import stable_id
from .util import canonical, first_paragraph, iso, sha256
from .wikifiles import FOLDERS

log = logging.getLogger("wiki.compile")

FINGERPRINT_VERSION = 1
MAX_CURRENT_BODY = 16000
MAX_WIKI_TITLES = 60
MAX_PASSAGES = 8
RELATED_LIMIT = 5


@dataclass
class Plan:
    name: str
    aliases: list[str]
    title: str
    type: str
    entity_kind: str
    page_id: str | None


def page_type_for(mention: dict) -> tuple[str, str]:
    name = mention["name"].lower()
    if " vs " in f" {name} " or " versus " in f" {name} ":
        return "comparison", ""
    if mention.get("kind") == "concept":
        return "concept", ""
    return "entity", mention.get("kind") or ""


def fingerprint(rec: dict, schema: str, max_pages: int) -> str:
    return sha256(canonical({"v": FINGERPRINT_VERSION, "summary": rec.get("summary"), "title": rec.get("title"),
                             "schema": sha256(schema), "max": max_pages}))


def job_compile(svc: Any, ctx: Any) -> dict:
    payload = ctx.payload or {}
    rec = svc.state.source(str(payload.get("sourceId") or ""))
    if rec is None:
        return {"skipped": "source is gone"}
    paused = svc.requeue_if_paused(ctx, "compile")
    if paused:
        return paused
    if not rec.get("summary"):
        return {"skipped": "source is not summarised"}
    with svc.compile_lock(rec["spaceId"]):
        rec = svc.state.source(rec["id"]) or rec
        return Compiler(svc, ctx, rec, force=bool(payload.get("force")),
                        focus=[str(f) for f in payload.get("focus") or []],
                        only=[str(o) for o in payload.get("only") or []]).run()


class Compiler:
    def __init__(self, svc: Any, ctx: Any, rec: dict, *, force: bool = False, focus: list[str] | None = None,
                 only: list[str] | None = None) -> None:
        self.svc = svc
        self.ctx = ctx
        self.rec = rec
        self.force = force
        self.focus = {f.lower() for f in focus or []}
        self.only = set(only or [])
        self.settings = svc.settings()
        self.space = rec["spaceId"]
        self.wiki = self.settings.wikiFolder
        self._resolves: dict[str, bool] = {}
        self.fp = ""

    # paths -----------------------------------------------------------------

    def is_page_path(self, path: str) -> bool:
        if not path.startswith(self.wiki + "/"):
            return False
        rest = path[len(self.wiki) + 1:]
        return "/" in rest and not rest.startswith("Lint/")

    def is_user_path(self, path: str) -> bool:
        return not (path.startswith(self.settings.sourcesFolder + "/") or path.startswith(self.wiki + "/"))

    # run -------------------------------------------------------------------

    def run(self) -> dict:
        svc = self.svc
        schema = svc.files.schema_text(self.space)
        svc.files.ensure_index(self.space)
        svc.files.ensure_log(self.space)
        self.fp = fingerprint(self.rec, schema, self.settings.maxPagesPerSource)
        if not self.force and self.rec.get("compiled") == self.fp:
            return {"unchanged": True}
        plans = self.plan()
        related = self.related_notes()
        batch_titles = [p.title for p in plans]
        others = self.known_titles(exclude={t.lower() for t in batch_titles})
        results: dict[str, list[str]] = {"created": [], "updated": [], "suggested": [], "unchanged": [], "failed": []}
        new_index: list[tuple[str, str, str]] = []
        touched: list[str] = []
        page_ids: list[str] = []
        for i, plan in enumerate(plans):
            self.ctx.progress(i, len(plans), f"Compiling {plan.title}")
            self.ctx.check_canceled()
            outcome, page_id, page = self.compile_page(plan, schema, related, batch_titles, others)
            results[outcome].append(plan.title)
            if outcome == "created" and page:
                new_index.append((page["type"], plan.title, page["definition"]))
            if page_id:
                page_ids.append(page_id)
                if outcome in ("created", "updated", "suggested"):
                    touched.append(page_id)
        self.ctx.progress(len(plans), len(plans), "Updating the index and log")
        if new_index:
            svc.files.add_to_index(self.space, new_index)
        if touched or results["failed"]:
            detail = f"created {len(results['created'])}, updated {len(results['updated'])}"
            if results["suggested"]:
                detail += f", suggested {len(results['suggested'])}"
            if results["failed"]:
                detail += f", failed {len(results['failed'])}"
            svc.files.append_log(self.space, "compile", f"[[{self.rec['title']}]]", detail)
        rec = svc.state.source(self.rec["id"]) or self.rec
        rec["pages"] = sorted(set(rec.get("pages") or []) | set(page_ids))
        rec["wikiState"] = "compiled"
        rec["compileFailures"] = results["failed"]
        if not results["failed"]:
            rec["compiled"] = self.fp
        svc.state.save_source(rec)
        pages: dict[str, str] = {}
        for m in rec["summary"].get("mentions") or []:
            page = svc.state.page(svc.state.alias(m["name"]) or "") if svc.state.alias(m["name"]) else None
            if page and page.get("title"):
                pages[m["name"].lower()] = page["title"]
        write_source(svc, rec, summary_part=summary_markdown(rec["summary"], pages))
        svc.state.record_pages_per_source(len(touched))
        for page_id in touched:
            svc.enqueue("link-suggest", {"pageId": page_id}, title="Suggest links", space_id=self.space)
        return {k: len(v) for k, v in results.items()} | {"pages": results}

    # step 1 and 3 ----------------------------------------------------------

    def plan(self) -> list[Plan]:
        mentions = list(self.rec["summary"].get("mentions") or [])
        ranked: list[tuple[tuple, Plan]] = []
        seen_ids: set[str] = set()
        seen_titles: set[str] = set()
        for idx, m in enumerate(mentions):
            page_id = self.find_page(m)
            if page_id and page_id in seen_ids:
                continue
            typ, kind = page_type_for(m)
            aliases = list(m.get("aliases") or [])
            if self.only and page_id not in self.only:
                continue
            if page_id:
                pr = self.svc.state.page(page_id) or {}
                title = pr.get("title") or m["name"]
                typ = pr.get("type") or typ
                kind = pr.get("entityKind", kind)
            else:
                title = self.new_page_title(m["name"], kind or typ, seen_titles)
            if title.lower() in seen_titles:
                continue
            if title != m["name"]:
                aliases = [m["name"]] + [a for a in aliases if a.lower() != m["name"].lower()]
            seen_titles.add(title.lower())
            if page_id:
                seen_ids.add(page_id)
            rank = (0 if m["name"].lower() in self.focus else 1, idx - (4 if page_id else 0), idx)
            ranked.append((rank, Plan(m["name"], aliases, title, typ, kind, page_id)))
        ranked.sort(key=lambda r: r[0])
        return [p for _, p in ranked[: self.settings.maxPagesPerSource]]

    def new_page_title(self, name: str, label: str, taken: set[str]) -> str:
        """Return name, or name with a qualifier when name already resolves to a source, a note or a file."""
        candidates = [name, f"{name} ({label})"] + [f"{name} ({label}) {n}" for n in range(2, 20)]
        for cand in candidates:
            if cand.lower() in taken or self.svc.state.source_by_title(cand) or self.svc.state.alias(cand):
                continue
            if self.resolves(cand):
                continue
            return cand
        return f"{name} ({label}) {self.rec['id'][-6:]}"

    def find_page(self, mention: dict) -> str | None:
        """Find the page of a mention by alias index, then host link resolver, then text search in the wiki."""
        state, notes, host = self.svc.state, self.svc.notes, self.svc.host
        names = [mention["name"]] + list(mention.get("aliases") or [])
        for name in names:
            page_id = state.alias(name)
            if page_id:
                if notes.get(page_id):
                    return page_id
                state.drop_page(page_id)
        try:
            items = host.resolve(self.space, self.rec["id"], [mention["name"]]).items
        except HostError:
            items = []
        for it in items:
            if isinstance(it, dict) and it.get("id") and self.is_page_path(str(it.get("path") or "")):
                return self.adopt(str(it["id"]))
        try:
            hits = host.search(mention["name"], mode="text", space=self.space, limit=10).items
        except HostError:
            hits = []
        lower = {n.lower() for n in names}
        for hit in hits:
            note = hit.get("note") if isinstance(hit, dict) else None
            if isinstance(note, dict) and self.is_page_path(str(note.get("path") or "")):
                if str(note.get("title") or "").lower() in lower:
                    return self.adopt(str(note["id"]))
        return None

    def adopt(self, note_id: str) -> str | None:
        """Create the page record of a wiki page found outside the alias index."""
        rec = self.svc.state.page(note_id)
        if rec:
            return note_id
        note = self.svc.notes.get(note_id)
        if note is None:
            return None
        fm = frontmatter.read(note)
        aliases = [a for a in fm.get("aliases") or [] if isinstance(a, str)]
        rec = {"id": note_id, "spaceId": self.space, "title": note.get("title") or fm.get("title") or "",
               "type": fm.get("type") or "concept", "entityKind": fm.get("entity_kind") or "",
               "aliases": aliases, "sources": [], "inputs": {}}
        self.svc.state.save_page(rec)
        for name in [rec["title"]] + aliases:
            self.svc.state.set_alias(name, note_id)
        return note_id

    def known_titles(self, exclude: set[str]) -> list[str]:
        """Return titles of existing pages for other mentions of this source, for the model's link list."""
        out = []
        for m in self.rec["summary"].get("mentions") or []:
            page_id = self.svc.state.alias(m["name"])
            if not page_id:
                continue
            title = (self.svc.state.page(page_id) or {}).get("title") or m["name"]
            if title.lower() not in exclude and title not in out:
                out.append(title)
        return out[:MAX_WIKI_TITLES]

    # step 2 ----------------------------------------------------------------

    def related_notes(self) -> list[dict]:
        host = self.svc.host
        summary = self.rec["summary"]
        found: dict[str, dict] = {}
        score: dict[str, int] = {}

        def add(hits: list, weight: int) -> None:
            for hit in hits:
                note = hit.get("note") if isinstance(hit, dict) else None
                if not isinstance(note, dict) or not note.get("id"):
                    continue
                if not self.is_user_path(str(note.get("path") or "")):
                    continue
                found.setdefault(note["id"], {"id": note["id"], "title": note.get("title") or "",
                                              "excerpt": (hit.get("snippet") or note.get("excerpt") or "")[:400]})
                score[note["id"]] = score.get(note["id"], 0) + weight

        query = f"{self.rec['title']}. {summary.get('summary', '')}"[:500]
        try:
            add(host.search(query, mode="semantic", space=self.space, limit=20).items, 1)
        except HostError:
            pass
        if not found:
            for m in (summary.get("mentions") or [])[:6]:
                try:
                    add(host.search(m["name"], mode="text", space=self.space, limit=10).items, 1)
                except HostError:
                    continue
        order = sorted(found, key=lambda i: (-score[i], list(found).index(i)))
        return [found[i] for i in order[:RELATED_LIMIT]]

    # step 4 to 6 -----------------------------------------------------------

    def source_exists(self, title: str) -> bool:
        return self.svc.state.source_by_title(title) is not None

    def resolves(self, target: str) -> bool:
        key = target.lower()
        if key not in self._resolves:
            try:
                items = self.svc.host.resolve(self.space, self.rec["id"], [target]).items
            except HostError:
                items = []
            self._resolves[key] = any(isinstance(i, dict) and i.get("id") for i in items)
        return self._resolves[key]

    def passages(self, plan: Plan) -> list[str]:
        names = [plan.name.lower()] + [a.lower() for a in plan.aliases]
        quotes = [p["quote"] for p in self.rec["summary"].get("key_points") or [] if p.get("quote")]
        about = [q for q in quotes if any(n in q.lower() for n in names)]
        return (about or quotes)[:MAX_PASSAGES]

    def compile_page(self, plan: Plan, schema: str, related: list[dict], batch_titles: list[str],
                     others: list[str]) -> tuple[str, str | None, dict | None]:
        svc = self.svc
        for attempt in range(2):
            current = svc.notes.get(plan.page_id) if plan.page_id else None
            if plan.page_id and current is None:
                plan.page_id = None
            page_rec = svc.state.page(plan.page_id) if plan.page_id else None
            if (current is not None and not self.only and page_rec
                    and (page_rec.get("inputs") or {}).get(self.rec["id"]) == self.fp):
                return "unchanged", plan.page_id, None
            cur_body = frontmatter.body_of(current.get("content") or "") if current else ""
            cur_fm = frontmatter.read(current) if current else {}
            cur_cites = wikilinks.citations(cur_body)
            available = [self.rec["title"]] + [c for c in cur_cites if c != self.rec["title"] and self.source_exists(c)]
            wiki_pages = [t for t in batch_titles if t.lower() != plan.title.lower()]
            wiki_pages += [t for t in others if t.lower() != plan.title.lower() and t not in wiki_pages]
            summary = self.rec["summary"]
            data = {
                "page": {"title": plan.title, "type": plan.type, "entity_kind": plan.entity_kind,
                         "aliases": plan.aliases, "exists": current is not None,
                         "current_frontmatter": {k: cur_fm.get(k) for k in ("aliases", "tags", "confidence", "contested")
                                                 if k in cur_fm},
                         "current_body": cur_body[:MAX_CURRENT_BODY]},
                "source": {"title": self.rec["title"], "url": self.rec["url"], "summary": summary.get("summary", ""),
                           "key_points": [p["claim"] for p in summary.get("key_points") or []],
                           "supporting_passages": self.passages(plan)},
                "related_notes": [{"title": r["title"], "excerpt": r["excerpt"]} for r in related],
                "wiki_pages": wiki_pages[:MAX_WIKI_TITLES],
                "sources_available": available,
            }
            try:
                page, _ = svc.ai.complete_json(
                    "compile", "compile", {"schema": schema, "input_json": data},
                    lambda obj: self.check(obj, plan, cur_cites, batch_titles),
                    space_id=self.space, model_spec=self.settings.compileModel)
            except ModelOutputError as exc:
                self.ctx.log("warn", f"skipped page {plan.title}: {'; '.join(exc.problems)}"[:2000])
                return "failed", plan.page_id, None
            content = self.render(page, plan, cur_fm)
            if current is None:
                folder = f"{self.wiki}/{FOLDERS[page['type']]}"
                note = svc.notes.create(self.space, folder, plan.title, content,
                                        client_id=stable_id("page", self.space, plan.title.lower()))
                self.record_page(note["id"], plan, page)
                return "created", note["id"], page
            try:
                result = svc.notes.write(current, content, ignore_keys=("updated",),
                                         suggestion_title=f"Update {plan.title} from {self.rec['title']}",
                                         rationale=f"The source [[{self.rec['title']}]] adds to this page.",
                                         kind="compile")
            except Conflict:
                if attempt == 0:
                    self.ctx.log("info", f"{plan.title} changed during compile. Running it again.")
                    continue
                self.ctx.log("warn", f"skipped page {plan.title}: it changed twice during compile")
                return "failed", plan.page_id, None
            self.record_page(current["id"], plan, page)
            action = result.action if result.action in ("updated", "suggested") else "unchanged"
            return action, current["id"], page
        return "failed", plan.page_id, None

    def check(self, obj: Any, plan: Plan, cur_cites: list[str], batch_titles: list[str]) -> dict:
        """Apply the structural schema and the checks of step 5. Raise ModelOutputError with every problem."""
        page = schemas.page(obj)
        page["title"] = plan.title
        if not page["type"]:
            page["type"] = plan.type
        problems: list[str] = []
        body = page["body"]
        cites = wikilinks.citations(body)
        cite_lower = {c.lower() for c in cites}
        for c in cites:
            if not self.source_exists(c):
                problems.append(f"the citation ([[{c}]]) names no source note. Cite only titles from sources_available")
        if self.rec["title"].lower() not in cite_lower:
            problems.append(f"the page must cite the new source as ([[{self.rec['title']}]])")
        removed = {r["source"].lower() for r in page["removed_citations"]}
        dropped = [c for c in cur_cites if c.lower() not in cite_lower and c.lower() not in removed]
        if dropped:
            problems.append("the page drops citations without listing them in removed_citations: "
                            + ", ".join(f"[[{d}]]" for d in dropped))
        batch = {t.lower() for t in batch_titles}
        linked: set[str] = set()
        for target in wikilinks.non_citation_targets(body):
            low = target.lower()
            if low == plan.title.lower() or low in linked:
                continue
            if low in batch or self.svc.state.alias(target):
                linked.add(low)
            elif self.source_exists(target):
                continue
            elif self.resolves(target):
                linked.add(low)
            else:
                problems.append(f"[[{target}]] does not resolve to a wiki page or a note. "
                                "Link only titles from wiki_pages and related_notes")
        for note_title in page["related_notes"]:
            if note_title.lower() not in batch and not self.resolves(note_title):
                problems.append(f"the related note {note_title} does not resolve to a note")
        if len(linked) < 2:
            problems.append("the page must link to at least 2 other wiki pages or user notes")
        opening = body.lstrip("\n")
        if not opening or opening.startswith("#"):
            if page["definition"]:
                page["body"] = page["definition"].strip() + "\n\n" + opening
            else:
                problems.append("the page must open with one paragraph that defines the subject")
        if not page["definition"]:
            page["definition"] = first_paragraph(wikilinks.CITATION.sub("", page["body"]))
        if problems:
            raise ModelOutputError("the page failed its checks", problems)
        return page

    def render(self, page: dict, plan: Plan, cur_fm: dict) -> str:
        now = iso(self.svc.clock())
        aliases: list[str] = []
        for a in list(cur_fm.get("aliases") or []) + plan.aliases + page["aliases"]:
            if isinstance(a, str) and a.lower() != plan.title.lower() and a.lower() not in {x.lower() for x in aliases}:
                aliases.append(a)
        fields = {
            "title": plan.title,
            "type": page["type"],
            "entity_kind": page["entity_kind"],
            "aliases": aliases,
            "tags": page["tags"],
            "sources": [f"[[{c}]]" for c in wikilinks.citations(page["body"])],
            "related_notes": [f"[[{n}]]" for n in page["related_notes"]],
            "confidence": page["confidence"],
            "contested": page["contested"],
            "created": cur_fm.get("created") or now,
            "updated": now,
            "generated_by": GENERATED_BY,
        }
        return frontmatter.dump(fields) + page["body"]

    def record_page(self, page_id: str, plan: Plan, page: dict) -> None:
        state = self.svc.state
        rec = state.page(page_id) or {"id": page_id, "spaceId": self.space, "created": iso(self.svc.clock())}
        aliases = list(dict.fromkeys(list(rec.get("aliases") or []) + plan.aliases + page["aliases"]))
        rec.update({"title": plan.title, "type": page["type"], "entityKind": page["entity_kind"],
                    "aliases": aliases, "contested": page["contested"], "updatedAt": iso(self.svc.clock())})
        rec["inputs"] = {**(rec.get("inputs") or {}), self.rec["id"]: self.fp}
        rec["sources"] = sorted(set(rec.get("sources") or []) | {self.rec["id"]})
        state.save_page(rec)
        for name in [plan.title] + aliases:
            state.set_alias(name, page_id)
