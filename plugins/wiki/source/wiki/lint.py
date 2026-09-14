"""The daily lint job.

It writes Wiki/Lint/<date>.md with one task-list item per finding and creates
one suggestion per note for the findings that have a mechanical fix. Missing
pages get compile jobs instead of suggestions.
"""

from __future__ import annotations

import difflib
import logging
import re
from typing import Any, Callable

from markspace import HostError

from . import frontmatter, schemas, wikilinks
from .jsonparse import ModelOutputError
from .notes import GENERATED_BY, Conflict
from .urls import stable_id
from .util import day, first_paragraph, iso, parse_iso
from .wikifiles import WikiFiles

log = logging.getLogger("wiki.lint")

LONG_PAGE_WORDS = 1500
MISSING_PAGE_SOURCES = 3
CHECKS = (("orphans", "Orphans"), ("broken", "Broken links"), ("missing", "Missing pages"),
          ("contradictions", "Contradictions"), ("drift", "Drift"), ("dead", "Dead sources"),
          ("long", "Long pages"), ("index", "Index"))
META = ("index", "log", "schema")


def job_lint(svc: Any, ctx: Any) -> dict:
    payload = ctx.payload or {}
    if svc.state.paused() and not payload.get("manual"):
        return {"paused": True}
    space = svc.space_id(str(payload.get("spaceId") or ctx.job.space_id or ""))
    return Linter(svc, ctx, space).run()


def replace_section(body: str, heading: str, section: str) -> str:
    """Replace the ## heading section of body with section, or append section when the heading is absent."""
    rx = re.compile(rf"^## {re.escape(heading)}[ \t]*\n.*?(?=^## |\Z)", re.MULTILINE | re.DOTALL)
    section = section.rstrip("\n") + "\n"
    if rx.search(body):
        return rx.sub(lambda _: section + "\n", body, count=1).rstrip("\n") + "\n"
    return body.rstrip("\n") + "\n\n" + section


def code(text: str) -> str:
    return "`" + text.replace("`", "'") + "`"


class Linter:
    def __init__(self, svc: Any, ctx: Any, space: str) -> None:
        self.svc = svc
        self.ctx = ctx
        self.space = space
        self.settings = svc.settings()
        self.wiki = self.settings.wikiFolder
        self.findings: dict[str, list[str]] = {key: [] for key, _ in CHECKS}
        self.planned: dict[str, dict] = {}
        self.compiles = 0

    # helpers ---------------------------------------------------------------

    def is_page_path(self, path: str) -> bool:
        rest = path[len(self.wiki) + 1:] if path.startswith(self.wiki + "/") else ""
        return "/" in rest and not rest.startswith("Lint/")

    def plan_edit(self, note: dict, change: Callable[[str], str], reason: str) -> None:
        entry = self.planned.setdefault(note["id"], {
            "note": note, "body": frontmatter.body_of(note.get("content") or ""), "reasons": []})
        entry["body"] = change(entry["body"])
        entry["reasons"].append(reason)

    def load_pages(self) -> list[dict]:
        pages = []
        for stub in self.svc.notes.notes_under(self.space, self.wiki):
            if not self.is_page_path(str(stub.get("path") or "")):
                continue
            note = self.svc.notes.get(stub["id"])
            if note:
                pages.append(note)
        return pages

    # run -------------------------------------------------------------------

    def run(self) -> dict:
        svc = self.svc
        now = svc.clock()
        today = day(now)
        last = parse_iso(str(svc.state.get(f"lint:last:{self.space}") or ""))
        svc.files.ensure_all(self.space)
        pages = self.load_pages()
        self.ctx.progress(0, 8, "Checking orphans")
        index_note = svc.files.ensure_index(self.space)
        index_body = frontmatter.body_of(index_note.get("content") or "")
        index_titles = WikiFiles.index_titles(index_body)
        self.check_orphans(pages, index_titles)
        self.ctx.progress(1, 8, "Checking links")
        self.check_broken(pages)
        self.ctx.progress(2, 8, "Checking missing pages")
        self.check_missing()
        self.ctx.progress(3, 8, "Checking disagreements")
        self.check_contradictions(pages, last)
        self.ctx.progress(4, 8, "Checking sources")
        self.check_drift(last)
        self.check_dead()
        self.ctx.progress(5, 8, "Checking page length")
        self.check_long(pages)
        self.ctx.progress(6, 8, "Checking the index")
        self.check_index(pages, index_note, index_body, index_titles)
        suggestions = self.apply_planned()
        self.ctx.progress(7, 8, "Writing the report")
        total = sum(len(v) for v in self.findings.values())
        report = self.write_report(today, total)
        svc.files.append_log(self.space, "lint", f"[[{today}]]", f"{total} findings, {suggestions} suggestions")
        svc.state.put(f"lint:last:{self.space}", iso(now))
        self.ctx.progress(8, 8, "Lint finished")
        return {"findings": {k: len(v) for k, v in self.findings.items()}, "suggestions": suggestions,
                "compiles": self.compiles, "report": report}

    # checks ----------------------------------------------------------------

    def check_orphans(self, pages: list[dict], index_titles: dict[str, str]) -> None:
        meta_paths = {f"{self.wiki}/{m}.md" for m in META}
        by_id = {p["id"]: p for p in pages}
        for page in pages:
            try:
                rows = self.svc.host.backlinks(page["id"]).items
            except HostError:
                continue
            sources = []
            for row in rows:
                src = row.get("fromNote") or row.get("note") or row if isinstance(row, dict) else {}
                path = str(src.get("path") or "")
                if src.get("id") == page["id"] or path in meta_paths or path.startswith(f"{self.wiki}/Lint/"):
                    continue
                sources.append(src)
            if sources:
                continue
            title = page.get("title") or ""
            if title.lower() not in index_titles:
                self.findings["orphans"].append(f"[[{title}]] has no backlinks. The index suggestion adds it to [[index]].")
                continue
            related = self.related_page(page, by_id)
            if related is None:
                self.findings["orphans"].append(f"[[{title}]] has no backlinks and no page shares a source with it.")
                continue
            self.plan_edit(related, lambda body, t=title: self.add_see_also(body, t),
                           f"link the orphan page [[{title}]]")
            self.findings["orphans"].append(
                f"[[{title}]] has no backlinks. A suggestion adds it to [[{related.get('title')}]].")

    def related_page(self, page: dict, by_id: dict[str, dict]) -> dict | None:
        rec = self.svc.state.page(page["id"]) or {}
        mine = set(rec.get("sources") or [])
        for other_id, other in by_id.items():
            if other_id == page["id"]:
                continue
            other_rec = self.svc.state.page(other_id) or {}
            if mine & set(other_rec.get("sources") or []):
                return other
        return None

    @staticmethod
    def add_see_also(body: str, title: str) -> str:
        line = f"- [[{title}]]"
        if line in body:
            return body
        if re.search(r"^## See also[ \t]*$", body, re.MULTILINE):
            return re.sub(r"(^## See also[ \t]*\n(?:\n)?(?:- .*\n)*)", lambda m: m.group(1) + line + "\n", body,
                          count=1, flags=re.MULTILINE)
        return body.rstrip("\n") + f"\n\n## See also\n\n{line}\n"

    def check_broken(self, pages: list[dict]) -> None:
        names = {}
        for rec in self.svc.state.pages():
            for n in [rec.get("title", "")] + list(rec.get("aliases") or []):
                if n:
                    names.setdefault(n.lower(), rec.get("title") or n)
        for page in pages:
            body = frontmatter.body_of(page.get("content") or "")
            targets = list(dict.fromkeys(l.target for l in wikilinks.links(body) if not l.embed))
            if not targets:
                continue
            try:
                items = self.svc.host.resolve(self.space, page["id"], targets).items
            except HostError:
                continue
            resolved = {str(i.get("target")): bool(i.get("id")) for i in items if isinstance(i, dict)}
            for target in targets:
                if resolved.get(target, True):
                    continue
                close = difflib.get_close_matches(target.lower(), list(names), n=1, cutoff=0.8)
                replacement = names[close[0]] if close else None
                self.plan_edit(page, lambda b, t=target, r=replacement: self.fix_link(b, t, r),
                               f"fix the broken link {target}")
                fix = f"A suggestion retargets it to [[{replacement}]]." if replacement else "A suggestion removes the link."
                self.findings["broken"].append(
                    f"[[{page.get('title')}]] links to {code('[[' + target + ']]')}, which resolves to nothing. {fix}")

    @staticmethod
    def fix_link(body: str, target: str, replacement: str | None) -> str:
        def swap(m: re.Match) -> str:
            t, heading, label = wikilinks.parse_target(m.group(2))
            if t != target or m.group(1):
                return m.group(0)
            if replacement:
                inner = replacement + (f"#{heading}" if heading else "") + (f"|{label}" if label else "")
                return f"[[{inner}]]"
            return label or t
        return wikilinks.WIKILINK.sub(swap, body)

    def check_missing(self) -> None:
        mentioned: dict[str, dict] = {}
        for rec in self.svc.state.sources():
            if rec.get("spaceId") != self.space or not rec.get("summary"):
                continue
            for m in rec["summary"].get("mentions") or []:
                entry = mentioned.setdefault(m["name"].lower(), {"name": m["name"], "aliases": set(), "sources": []})
                entry["aliases"].update(m.get("aliases") or [])
                if rec["id"] not in entry["sources"]:
                    entry["sources"].append(rec["id"])
        for key in sorted(mentioned):
            entry = mentioned[key]
            if len(entry["sources"]) < MISSING_PAGE_SOURCES:
                continue
            if any(self.svc.state.alias(n) for n in [entry["name"], *entry["aliases"]]):
                continue
            titles = []
            for source_id in sorted(entry["sources"]):
                rec = self.svc.state.source(source_id) or {}
                titles.append(f"[[{rec.get('title', source_id)}]]")
                self.svc.enqueue("compile", {"sourceId": source_id, "force": True, "focus": [entry["name"]]},
                                 title=f"Compile {entry['name']}", space_id=self.space)
                self.compiles += 1
            self.findings["missing"].append(
                f"{entry['name']} is mentioned by {len(entry['sources'])} sources and has no page. "
                f"Compile runs are queued for {', '.join(titles)}.")

    def check_contradictions(self, pages: list[dict], last: Any) -> None:
        for page in pages:
            fm = frontmatter.read(page)
            if fm.get("type") == "answer":
                continue
            updated = parse_iso(str(fm.get("updated") or ""))
            if not (fm.get("contested") is True or last is None or (updated and updated > last)):
                continue
            body = frontmatter.body_of(page.get("content") or "")
            cited = []
            for title in wikilinks.citations(body):
                source_id = self.svc.state.source_by_title(title)
                rec = self.svc.state.source(source_id) if source_id else None
                if rec and rec.get("summary"):
                    cited.append({"title": rec["title"], "summary": rec["summary"]["summary"],
                                  "key_points": [p["claim"] for p in rec["summary"].get("key_points") or []]})
            if len(cited) < 2:
                continue
            titles = {c["title"].lower() for c in cited}
            data = {"page": page.get("title"), "body": body[:16000], "sources": cited}
            try:
                result, _ = self.svc.ai.complete_json("lint", "lint", {"input_json": data}, schemas.contradictions,
                                                      space_id=self.space, model_spec=self.settings.compileModel)
            except ModelOutputError as exc:
                self.ctx.log("warn", f"contradiction check of {page.get('title')} failed: {'; '.join(exc.problems)}")
                continue
            disagreements = [d for d in result["disagreements"]
                             if all(p["source"].lower() in titles for p in d["positions"])]
            if not disagreements:
                continue
            claims = "; ".join(d["claim"] for d in disagreements)
            self.plan_edit(page, lambda b, s=result["section"]: replace_section(b, "Disagreements", s),
                           "update the Disagreements section")
            self.findings["contradictions"].append(
                f"[[{page.get('title')}]] has sources that disagree on {claims}. A suggestion updates ## Disagreements.")

    def check_drift(self, last: Any) -> None:
        for rec in self.svc.state.sources():
            if rec.get("spaceId") != self.space:
                continue
            for entry in rec.get("drift") or []:
                at = parse_iso(str(entry.get("date") or ""))
                if last is not None and at is not None and at <= last:
                    continue
                self.findings["drift"].append(
                    f"[[{rec['title']}]] changed on {str(entry.get('date'))[:10]}. The note keeps the first capture. "
                    f"The new text is in [[{entry.get('title')}]].")

    def check_dead(self) -> None:
        for rec in sorted(self.svc.state.sources(), key=lambda r: r.get("title", "")):
            if rec.get("spaceId") == self.space and rec.get("status") == "dead":
                self.findings["dead"].append(f"[[{rec['title']}]] is dead ({rec.get('error') or 'gone'}). {rec['url']}")

    def check_long(self, pages: list[dict]) -> None:
        for page in pages:
            words = wikilinks.word_count(frontmatter.body_of(page.get("content") or ""))
            if words > LONG_PAGE_WORDS:
                self.findings["long"].append(
                    f"[[{page.get('title')}]] has {words} words. Split it into subpages of at most {LONG_PAGE_WORDS}.")

    def check_index(self, pages: list[dict], index_note: dict, index_body: str, index_titles: dict[str, str]) -> None:
        titles = {}
        for page in pages:
            titles[str(page.get("title") or "").lower()] = page
        missing = []
        for low, page in sorted(titles.items()):
            if low and low not in index_titles:
                fm = frontmatter.read(page)
                kind = fm.get("type") if fm.get("type") in ("entity", "concept", "comparison", "answer") else "concept"
                body = frontmatter.body_of(page.get("content") or "")
                missing.append((kind, page.get("title"), first_paragraph(wikilinks.CITATION.sub("", body))))
                self.findings["index"].append(f"[[{page.get('title')}]] is missing from the index.")
        gone = {t for t in index_titles if t not in titles}
        for t in sorted(gone):
            self.findings["index"].append(f"The index lists {code('[[' + t + ']]')}, which is not a wiki page.")
        if missing or gone:
            self.plan_edit(index_note, lambda b: WikiFiles.render_index(b, missing, gone), "update the index")

    # output ----------------------------------------------------------------

    def apply_planned(self) -> int:
        created = 0
        for entry in self.planned.values():
            note = self.svc.notes.get(entry["note"]["id"])
            if note is None:
                continue
            if frontmatter.body_of(note.get("content") or "") != frontmatter.body_of(entry["note"].get("content") or ""):
                continue
            reasons = "; ".join(dict.fromkeys(entry["reasons"]))
            if self.svc.notes.suggest(note, entry["body"], "Fix wiki lint findings", f"Lint found: {reasons}."[:1000],
                                      kind="lint"):
                created += 1
        return created

    def write_report(self, today: str, total: int) -> str:
        lines = [f"# Lint {today}", "", f"{total} findings.", ""]
        for key, label in CHECKS:
            lines += [f"## {label}", ""]
            items = self.findings[key]
            lines += [f"- [ ] {item}" for item in items] if items else ["No findings."]
            lines.append("")
        body = "\n".join(lines)
        now = iso(self.svc.clock())
        folder = f"{self.wiki}/Lint"
        stub = self.svc.notes.find_note_by_path(self.space, f"{folder}/{today}.md")
        existing = self.svc.notes.get(stub["id"]) if stub else None
        fields = {"title": today, "type": "lint", "findings": total,
                  "created": (frontmatter.read(existing).get("created") if existing else None) or now,
                  "updated": now, "generated_by": GENERATED_BY}
        content = frontmatter.dump(fields) + body
        if existing is None:
            note = self.svc.notes.create(self.space, folder, today, content,
                                         client_id=stable_id("lint", self.space, today))
            return note["id"]
        try:
            self.svc.notes.write(existing, content, ignore_keys=("updated",), kind="lint-report",
                                 suggestion_title="Update the lint report")
        except Conflict:
            self.ctx.log("warn", "the lint report changed during the write")
        return existing["id"]
