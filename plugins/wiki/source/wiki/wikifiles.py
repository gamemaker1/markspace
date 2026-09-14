"""Wiki/schema.md, Wiki/index.md and Wiki/log.md.

schema.md holds the conventions the model follows. The plugin creates it once
and reads it on every compile, so the user can edit it. index.md lists every
page under its type. log.md gets one line per run. When the user edited index
or log, the pending entries collect in the key-value store and one suggestion
carries all of them.
"""

from __future__ import annotations

import logging
import re
import threading
from typing import Any, Callable

from . import frontmatter
from .notes import GENERATED_BY, Conflict, Notes
from .util import Clock, cut, iso, minute, utcnow

log = logging.getLogger("wiki.wikifiles")

SECTIONS = (("entity", "Entities"), ("concept", "Concepts"), ("comparison", "Comparisons"), ("answer", "Answers"))
FOLDERS = {"entity": "Entities", "concept": "Concepts", "comparison": "Comparisons", "answer": "Answers"}
_ENTRY = re.compile(r"^\s*[-*]\s+\[\[([^\[\]|#\n]+)(?:[#|][^\]\n]*)?\]\]")

SCHEMA_BODY = """# Wiki schema

This page tells the model how to write the wiki. Edit it to change the conventions. The plugin reads it before every compile.

## Pages

- Entities are people, organisations, tools, projects and places. They live in {wiki}/Entities.
- Concepts are ideas, methods and terms. They live in {wiki}/Concepts.
- Comparisons compare two or more entities or concepts. They live in {wiki}/Comparisons.
- Answers are questions answered from the wiki and filed by the user. They live in {wiki}/Answers.

## Rules

1. The page opens with one paragraph that defines the subject.
2. Every factual sentence ends with at least one citation to a source note, as ([[source title]]).
3. The page links to at least 2 other wiki pages or user notes.
4. A claim that two sources disagree on goes under ## Disagreements, with both citations, and sets contested to true.
5. A page longer than 1500 words gets split into subpages. The lint report lists it until that happens.
6. User notes appear under ## In your notes, as wikilinks with one line each on how they relate.

## Style

- Write short declarative sentences with concrete facts.
- Use the subject's common name as the title. Put other names in aliases.
- Use ## headings for sections. Do not repeat the title as a heading.
- Keep the text of existing pages unless a new source corrects or extends it.
"""


class WikiFiles:
    def __init__(self, notes: Notes, state: Any, settings_fn: Callable[[], Any], clock: Clock = utcnow) -> None:
        self.notes = notes
        self.state = state
        self.settings_fn = settings_fn
        self.clock = clock
        self._lock = threading.RLock()

    @property
    def wiki(self) -> str:
        return self.settings_fn().wikiFolder

    def _fm(self, title: str, kind: str) -> dict:
        now = iso(self.clock())
        return {"title": title, "type": kind, "created": now, "updated": now, "generated_by": GENERATED_BY}

    def _locate(self, space_id: str, name: str) -> dict | None:
        key = f"wikifile:{space_id}:{name}"
        note_id = self.state.get(key)
        if isinstance(note_id, str):
            note = self.notes.get(note_id)
            if note:
                return note
        stub = self.notes.find_note_by_path(space_id, f"{self.wiki}/{name}.md")
        if stub:
            note = self.notes.get(stub["id"])
            if note:
                self.state.put(key, note["id"])
                return note
        return None

    def _ensure(self, space_id: str, name: str, body: str, kind: str) -> dict:
        with self._lock:
            note = self._locate(space_id, name)
            if note:
                return note
            from .urls import stable_id

            content = frontmatter.dump(self._fm(name, kind)) + body
            # index.md is the folder note of the wiki folder. Markspace names an ordinary note titled index
            # index-note.md, so the index asks for the folder note explicitly.
            note = self.notes.create(space_id, self.wiki, name, content, client_id=stable_id("wikifile", space_id, name),
                                     folder_note=name == "index")
            self.state.put(f"wikifile:{space_id}:{name}", note["id"])
            return note

    def ensure_all(self, space_id: str) -> None:
        self.ensure_schema(space_id)
        self.ensure_index(space_id)
        self.ensure_log(space_id)

    def ensure_schema(self, space_id: str) -> dict:
        return self._ensure(space_id, "schema", SCHEMA_BODY.format(wiki=self.wiki), "schema")

    def ensure_index(self, space_id: str) -> dict:
        body = "# Wiki index\n\n" + "".join(f"## {label}\n\n" for _, label in SECTIONS)
        return self._ensure(space_id, "index", body, "index")

    def ensure_log(self, space_id: str) -> dict:
        return self._ensure(space_id, "log", "# Wiki log\n\n", "log")

    def schema_text(self, space_id: str) -> str:
        note = self.ensure_schema(space_id)
        return frontmatter.body_of(note.get("content") or "")

    # index -----------------------------------------------------------------

    @staticmethod
    def index_titles(body: str) -> dict[str, str]:
        """Map the lower-cased title of each index entry to its section type."""
        out: dict[str, str] = {}
        current = ""
        labels = {label.lower(): kind for kind, label in SECTIONS}
        for line in body.splitlines():
            if line.startswith("## "):
                current = labels.get(line[3:].strip().lower(), "")
                continue
            m = _ENTRY.match(line)
            if m:
                out[m.group(1).strip().lower()] = current
        return out

    @staticmethod
    def render_index(body: str, entries: list[tuple[str, str, str]], remove: set[str] | None = None) -> str:
        """Add entries of (type, title, definition) under their sections and drop titles in remove.

        Entries already listed stay as they are. New entries go in title order
        among the plugin's lines of each section.
        """
        remove = {r.lower() for r in remove or set()}
        lines = body.splitlines()
        if remove:
            lines = [ln for ln in lines if not ((m := _ENTRY.match(ln)) and m.group(1).strip().lower() in remove)]
        present = WikiFiles.index_titles("\n".join(lines))
        for kind, label in SECTIONS:
            new = sorted({t: d for k, t, d in entries if k == kind and t.lower() not in present}.items(),
                         key=lambda kv: kv[0].lower())
            if not new:
                continue
            heading = next((i for i, ln in enumerate(lines) if ln.strip().lower() == f"## {label.lower()}"), None)
            if heading is None:
                if lines and lines[-1].strip():
                    lines.append("")
                lines.extend([f"## {label}", ""])
                heading = len(lines) - 2
            end = heading + 1
            while end < len(lines) and not lines[end].startswith("## "):
                end += 1
            section = lines[heading + 1:end]
            items = [ln for ln in section if _ENTRY.match(ln)]
            others = [ln for ln in section if not _ENTRY.match(ln) and ln.strip()]
            for title, definition in new:
                items.append(f"- [[{title}]]: {cut(definition, 160)}" if definition else f"- [[{title}]]")
            items.sort(key=lambda ln: _ENTRY.match(ln).group(1).strip().lower())
            block = [""] + others + ([""] if others else []) + items + [""]
            lines[heading + 1:end] = block
            for t, _ in new:
                present[t.lower()] = kind
        text = "\n".join(lines).rstrip("\n") + "\n"
        return re.sub(r"\n{3,}", "\n\n", text)

    def add_to_index(self, space_id: str, entries: list[tuple[str, str, str]], remove: set[str] | None = None) -> str:
        """Add pages to index.md. Return the write action."""
        if not entries and not remove:
            return "unchanged"
        with self._lock:
            for _ in range(3):
                note = self.ensure_index(space_id)
                fields = frontmatter.read(note)
                body = frontmatter.body_of(note.get("content") or "")
                pending_key = f"pending:index:{space_id}"
                pending = [tuple(e) for e in self.state.get(pending_key) or []]
                if not self.notes.writable(note):
                    merged = pending + [e for e in entries if e not in pending]
                    self.state.put(pending_key, [list(e) for e in merged])
                    new_body = self.render_index(body, merged, remove)
                    s = self.notes.suggest(note, new_body, "Add pages to the wiki index",
                                           "New wiki pages are missing from the index.", kind="index")
                    return "suggested" if s else "unchanged"
                new_body = self.render_index(body, entries, remove)
                if new_body == body:
                    return "unchanged"
                fields["updated"] = iso(self.clock())
                try:
                    return self.notes.write(note, frontmatter.dump(fields) + new_body, ignore_keys=("updated",),
                                            kind="index").action
                except Conflict:
                    continue
            log.warning("index.md changed three times during one update")
            return "skipped"

    # log -------------------------------------------------------------------

    @staticmethod
    def _append_lines(body: str, lines: list[str]) -> str:
        head = body.rstrip("\n")
        last = head.splitlines()[-1] if head else ""
        sep = "\n" if last.startswith("- ") else "\n\n"
        return (head + sep if head else "") + "\n".join(lines) + "\n"

    def append_log(self, space_id: str, operation: str, subject: str, detail: str = "") -> str:
        """Append one line such as 2026-09-13 11:05 · compile · [[Title]] · created 2, updated 5."""
        parts = [minute(self.clock()), operation]
        if subject:
            parts.append(subject)
        if detail:
            parts.append(detail)
        line = "- " + " · ".join(parts)
        with self._lock:
            for _ in range(3):
                note = self.ensure_log(space_id)
                fields = frontmatter.read(note)
                body = frontmatter.body_of(note.get("content") or "")
                if not self.notes.writable(note):
                    key = f"pending:log:{space_id}"
                    pending = list(self.state.get(key) or []) + [line]
                    self.state.put(key, pending[-200:])
                    new_body = self._append_lines(body, pending)
                    s = self.notes.suggest(note, new_body, "Add wiki log entries",
                                           "Runs of the Bookmark wiki plugin since the log was edited.", kind="log")
                    return "suggested" if s else "unchanged"
                new_body = self._append_lines(body, [line])
                fields["updated"] = iso(self.clock())
                try:
                    return self.notes.write(note, frontmatter.dump(fields) + new_body, kind="log").action
                except Conflict:
                    continue
            log.warning("log.md changed three times during one append")
            return "skipped"
