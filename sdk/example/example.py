"""Word count, an example Markspace plugin.

It contributes a command that counts the words of a note, a rail panel with
the counts of the open note, a job that counts every note in a space, and an
event handler that stores the last count of each updated note in the plugin
key-value store. It uses only the Python standard library and the SDK.
"""

from __future__ import annotations

import logging
import os
import re
from typing import Any, Iterator

from markspace import HostError, Plugin, job, toast, ui

MANIFEST = {
    "protocol": 1,
    "id": "word-count",
    "name": "Word count",
    "version": "0.1.0",
    "description": "Counts the words in notes and spaces.",
    "permissions": ["items:read", "jobs", "storage", "ui", "events"],
    "events": ["note.updated"],
    "jobs": [{"kind": "count-space", "title": "Count words in a space", "maxAttempts": 3}],
    "commands": [{"id": "count", "title": "Count words", "icon": "hash", "contexts": ["note"]}],
    "panels": [{"id": "counts", "title": "Word count", "icon": "hash", "where": "rail", "contexts": ["note"]}],
    "importers": [{"id": "text", "title": "Count words in a text file", "accept": [".txt", ".md"]}],
}

WORD = re.compile(r"\w[\w'’-]*")
FRONTMATTER = re.compile(r"\A---\r?\n.*?\r?\n---\r?\n", re.DOTALL)
WORDS_PER_MINUTE = 230


def count_words(text: str) -> int:
    """Count the words of a markdown text, ignoring frontmatter."""
    return len(WORD.findall(FRONTMATTER.sub("", text or "", count=1)))


def plural(n: int, word: str) -> str:
    return f"{n:,} {word}" if n == 1 else f"{n:,} {word}s"


def note_of(host, item_id: str) -> dict:
    item = host.get_item(item_id)
    note = item.get("note") if isinstance(item, dict) else None
    if not isinstance(note, dict):
        raise ValueError(f"item {item_id} is not a note")
    return note


def tree_notes(tree: Any) -> Iterator[dict]:
    """Yield the notes of a space tree, depth first."""
    rows = tree.get("items", []) if isinstance(tree, dict) else tree or []
    for row in rows:
        if not isinstance(row, dict):
            continue
        if row.get("kind") == "note" and isinstance(row.get("note"), dict):
            yield row["note"]
        yield from tree_notes(row.get("children") or [])


def create_plugin(**options: Any) -> Plugin:
    """Build the plugin. Tests pass data_dir, env and timing options."""
    plugin = Plugin(MANIFEST, **options)

    @plugin.command("count")
    def count(ctx):
        if not ctx.item_id:
            return toast("Open a note to count its words.", "warn")
        note = note_of(ctx.host, ctx.item_id)
        words = count_words(note.get("content", ""))
        return toast(f"{plural(words, 'word')} in {note.get('title') or 'this note'}", "info")

    @plugin.panel("counts")
    def counts(ctx):
        if not ctx.item_id:
            return ui.empty("No note open", body="Open a note to see its word count.")
        note = note_of(ctx.host, ctx.item_id)
        content = note.get("content", "")
        words = count_words(content)
        rows = [
            {"metric": "Words", "value": words},
            {"metric": "Characters", "value": len(content)},
            {"metric": "Reading minutes", "value": max(1, round(words / WORDS_PER_MINUTE)) if words else 0},
        ]
        saved = ctx.host.kv_get(f"note:{ctx.item_id}")
        if isinstance(saved, dict) and isinstance(saved.get("words"), int):
            rows.append({"metric": "Words at last save", "value": saved["words"]})
        children = [
            ui.section("This note", ui.table([ui.column("metric", "Metric"),
                                              ui.column("value", "Value", numeric=True)], rows)),
        ]
        if ctx.space_id:
            children.append(ui.section(
                "This space",
                ui.button("Count words in this space", ui.action("count-space", {"spaceId": ctx.space_id})),
                description="Counts every note in the space as a background job.",
            ))
        return ui.stack(*children, gap="m")

    @plugin.action("count-space")
    def count_space_action(ctx):
        space_id = ctx.payload.get("spaceId") or ctx.space_id
        if not space_id:
            return toast("No space to count.", "warn")
        created = ctx.host.create_job("count-space", title="Count words", payload={"spaceId": space_id},
                                      space_id=space_id)
        return job(created["id"])

    @plugin.job("count-space")
    def count_space(ctx):
        space_id = (ctx.payload or {}).get("spaceId")
        if not space_id:
            raise ValueError("payload.spaceId is required")
        notes = list(tree_notes(ctx.host.tree(space_id).items))
        total = 0
        ctx.progress(0, len(notes), "Reading notes")
        for i, ref in enumerate(notes, start=1):
            ctx.check_canceled()
            try:
                note = note_of(ctx.host, ref["id"])
            except HostError as exc:
                if exc.status != 404:
                    raise
                ctx.log("warn", f"note {ref['id']} vanished during the count")
                continue
            total += count_words(note.get("content", ""))
            ctx.progress(i, len(notes), f"Counted {note.get('title') or ref['id']}")
        result = {"spaceId": space_id, "notes": len(notes), "words": total}
        ctx.host.kv_put(f"space:{space_id}", result)
        return result

    @plugin.on("note.updated")
    def on_note_updated(batch):
        for event in batch.events:
            if not event.item_id:
                continue
            try:
                note = note_of(batch.host, event.item_id)
            except HostError as exc:
                if exc.status == 404:
                    continue
                raise
            batch.host.kv_put(f"note:{event.item_id}", {
                "words": count_words(note.get("content", "")),
                "updatedAt": note.get("updatedAt", event.at),
            })

    @plugin.importer("text")
    def import_text(ctx):
        words = count_words(ctx.host.file_text(ctx.file_id).text)
        return toast(f"The file has {plural(words, 'word')}.", "info")

    return plugin


def main() -> None:
    logging.basicConfig(level=os.environ.get("LOG_LEVEL", "INFO"), format="%(asctime)s %(levelname)s %(message)s")
    create_plugin().serve(host=os.environ.get("HOST", "0.0.0.0"), port=int(os.environ.get("PORT", "8090")))


if __name__ == "__main__":
    main()
