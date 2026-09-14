"""Plugin state in the host key-value store.

Keys:

| Key | Value |
| --- | --- |
| settings | values saved from the settings form |
| url:<hash> | {url, noteId} for a normalised URL |
| source:<noteId> | the source record |
| srctitle:<hash> | {title, noteId}, so every source title is unique |
| page:<noteId> | the wiki page record |
| alias:<hash> | {name, pageId} for a lower-cased page title or alias |
| written:<noteId> | {hash} of the content the plugin last wrote |
| sugg:<noteId> | open suggestions of the plugin on a note, by kind |
| suggid:<suggestionId> | {noteId, kind} |
| suggday:<date> | the number of link suggestions created that day |
| stats:counts | source counts by status and wiki state |
| stats:pages | pages touched by each of the last 100 compiles |
| usage:<month> | AI calls and tokens by purpose |
| runs | the last 20 import and lint jobs |
| paused | true while jobs are paused |
| sync:<importer> | the last run of a scheduled API import |
| import:<id> | a parsed import waiting for confirmation |
| wikifile:<space>:<name> | the note id of index, log or schema |

Read-modify-write sequences hold a process lock. The plugin runs as one
process, so the lock is enough.
"""

from __future__ import annotations

import threading
from datetime import timedelta
from typing import Any, Callable

from .util import Clock, day, iso, month, parse_iso, sha256, utcnow

SUGGESTION_TTL = timedelta(days=14)
STATUSES = ("pending", "ok", "dead", "blocked", "not-text")
WIKI_STATES = ("pending", "fetched", "summarised", "compiled", "failed")


def _h(text: str) -> str:
    return sha256(text)[:40]


class State:
    def __init__(self, host_fn: Callable[[], Any], clock: Clock = utcnow) -> None:
        self._host_fn = host_fn
        self.clock = clock
        self.lock = threading.RLock()

    @property
    def host(self) -> Any:
        return self._host_fn()

    # generic ---------------------------------------------------------------

    def get(self, key: str, default: Any = None) -> Any:
        return self.host.kv_get(key, default)

    def put(self, key: str, value: Any) -> None:
        self.host.kv_put(key, value)

    def delete(self, key: str) -> None:
        try:
            self.host.kv_delete(key)
        except Exception as exc:  # noqa: BLE001 - a missing key is fine
            if getattr(exc, "status", None) != 404:
                raise

    def keys(self, prefix: str) -> list[str]:
        return [str(e.get("key")) for e in self.host.kv_keys(prefix) if isinstance(e, dict) and e.get("key")]

    # URLs and sources ------------------------------------------------------

    def url_note(self, normalised: str) -> str | None:
        entry = self.get("url:" + _h(normalised))
        return entry.get("noteId") if isinstance(entry, dict) else None

    def set_url_note(self, normalised: str, note_id: str) -> None:
        self.put("url:" + _h(normalised), {"url": normalised, "noteId": note_id})

    def source(self, note_id: str) -> dict | None:
        rec = self.get("source:" + note_id)
        return rec if isinstance(rec, dict) else None

    def save_source(self, rec: dict) -> None:
        """Store a source record and move the state counters from its previous version."""
        with self.lock:
            old = self.source(rec["id"])
            self.put("source:" + rec["id"], rec)
            self._count(old, rec)

    def drop_source(self, note_id: str) -> None:
        with self.lock:
            old = self.source(note_id)
            if old is None:
                return
            self.delete("source:" + note_id)
            if old.get("normalisedUrl") and self.url_note(old["normalisedUrl"]) == note_id:
                self.delete("url:" + _h(old["normalisedUrl"]))
            if old.get("title") and self.source_by_title(old["title"]) == note_id:
                self.delete("srctitle:" + _h(old["title"].lower()))
            self._count(old, None)

    def source_ids(self) -> list[str]:
        return [k[len("source:"):] for k in self.keys("source:")]

    def sources(self) -> list[dict]:
        out = []
        for note_id in self.source_ids():
            rec = self.source(note_id)
            if rec:
                out.append(rec)
        return out

    def source_by_title(self, title: str) -> str | None:
        entry = self.get("srctitle:" + _h(title.lower()))
        return entry.get("noteId") if isinstance(entry, dict) else None

    def claim_source_title(self, title: str, note_id: str) -> bool:
        """Record title for note_id. Return False when another source holds it."""
        with self.lock:
            holder = self.source_by_title(title)
            if holder and holder != note_id:
                return False
            if holder != note_id:
                self.put("srctitle:" + _h(title.lower()), {"title": title, "noteId": note_id})
            return True

    def _count(self, old: dict | None, new: dict | None) -> None:
        counts = self.get("stats:counts") or {}
        status = counts.setdefault("status", {})
        wiki = counts.setdefault("wikiState", {})
        changed = False
        for rec, delta in ((old, -1), (new, 1)):
            if not rec:
                continue
            for bucket, key in ((status, "status"), (wiki, "wikiState")):
                value = rec.get(key) or "pending"
                bucket[value] = max(0, int(bucket.get(value, 0)) + delta)
                changed = True
        if old and new and old.get("status") == new.get("status") and old.get("wikiState") == new.get("wikiState"):
            return
        if changed:
            self.put("stats:counts", counts)

    def counts(self) -> dict:
        counts = self.get("stats:counts") or {}
        return {"status": {s: int((counts.get("status") or {}).get(s, 0)) for s in STATUSES},
                "wikiState": {s: int((counts.get("wikiState") or {}).get(s, 0)) for s in WIKI_STATES}}

    # pages and aliases -----------------------------------------------------

    def page(self, note_id: str) -> dict | None:
        rec = self.get("page:" + note_id)
        return rec if isinstance(rec, dict) else None

    def save_page(self, rec: dict) -> None:
        self.put("page:" + rec["id"], rec)

    def drop_page(self, note_id: str) -> None:
        rec = self.page(note_id)
        if rec is None:
            return
        for name in [rec.get("title", "")] + list(rec.get("aliases") or []):
            if name and self.alias(name) == note_id:
                self.delete("alias:" + _h(name.lower()))
        self.delete("page:" + note_id)

    def page_ids(self) -> list[str]:
        return [k[len("page:"):] for k in self.keys("page:")]

    def pages(self) -> list[dict]:
        out = []
        for note_id in self.page_ids():
            rec = self.page(note_id)
            if rec:
                out.append(rec)
        return out

    def alias(self, name: str) -> str | None:
        entry = self.get("alias:" + _h(name.strip().lower()))
        return entry.get("pageId") if isinstance(entry, dict) else None

    def set_alias(self, name: str, page_id: str) -> bool:
        """Point a name at a page unless another page already holds it. Return whether the name is the page's."""
        name = name.strip()
        if not name:
            return False
        with self.lock:
            holder = self.alias(name)
            if holder and holder != page_id:
                return False
            if holder != page_id:
                self.put("alias:" + _h(name.lower()), {"name": name.lower(), "pageId": page_id})
            return True

    # notes the plugin wrote ------------------------------------------------

    def written(self, note_id: str) -> dict | None:
        entry = self.get("written:" + note_id)
        return entry if isinstance(entry, dict) else None

    def set_written(self, note_id: str, fingerprint: str) -> None:
        self.put("written:" + note_id, {"hash": fingerprint, "at": iso(self.clock())})

    # suggestions -----------------------------------------------------------

    def open_suggestions(self, note_id: str) -> dict[str, dict]:
        """Return the plugin's open suggestions on a note by kind, dropping markers older than 14 days."""
        entry = self.get("sugg:" + note_id) or {}
        now = self.clock()
        live = {}
        for kind, marker in entry.items():
            created = parse_iso(str(marker.get("createdAt") or "")) if isinstance(marker, dict) else None
            if created and now - created < SUGGESTION_TTL:
                live[kind] = marker
        return live

    def set_open_suggestion(self, note_id: str, kind: str, suggestion_id: str, **extra: Any) -> None:
        with self.lock:
            entry = self.open_suggestions(note_id)
            entry[kind] = {"id": suggestion_id, "createdAt": iso(self.clock()), **extra}
            self.put("sugg:" + note_id, entry)
            self.put("suggid:" + suggestion_id, {"noteId": note_id, "kind": kind})

    def clear_suggestion(self, suggestion_id: str) -> None:
        with self.lock:
            ref = self.get("suggid:" + suggestion_id)
            if not isinstance(ref, dict):
                return
            note_id = ref.get("noteId", "")
            entry = self.open_suggestions(note_id)
            entry = {k: v for k, v in entry.items() if v.get("id") != suggestion_id}
            if entry:
                self.put("sugg:" + note_id, entry)
            else:
                self.delete("sugg:" + note_id)
            self.delete("suggid:" + suggestion_id)

    def suggestions_today(self) -> int:
        return int(self.get("suggday:" + day(self.clock())) or 0)

    def take_suggestion_slot(self, limit: int) -> bool:
        """Count one link suggestion for today. Return False when the daily limit is reached."""
        with self.lock:
            key = "suggday:" + day(self.clock())
            used = int(self.get(key) or 0)
            if used >= limit:
                return False
            self.put(key, used + 1)
            return True

    # statistics ------------------------------------------------------------

    def record_pages_per_source(self, n: int) -> None:
        with self.lock:
            values = list(self.get("stats:pages") or [])
            values.append(int(n))
            self.put("stats:pages", values[-100:])

    def average_pages_per_source(self, default: float = 8.0) -> float:
        values = [v for v in self.get("stats:pages") or [] if isinstance(v, (int, float))]
        return sum(values) / len(values) if values else default

    def add_usage(self, purpose: str, input_tokens: int, output_tokens: int) -> None:
        with self.lock:
            key = "usage:" + month(self.clock())
            usage = self.get(key) or {}
            row = usage.setdefault(purpose, {"calls": 0, "inputTokens": 0, "outputTokens": 0})
            row["calls"] += 1
            row["inputTokens"] += int(input_tokens)
            row["outputTokens"] += int(output_tokens)
            self.put(key, usage)

    def usage(self) -> dict:
        return self.get("usage:" + month(self.clock())) or {}

    # runs, pause and imports -----------------------------------------------

    def add_run(self, job: dict, kind: str, title: str) -> None:
        if not isinstance(job, dict) or not job.get("id"):
            return
        with self.lock:
            runs = [r for r in self.get("runs") or [] if isinstance(r, dict)]
            runs.insert(0, {"jobId": job["id"], "kind": kind, "title": title, "createdAt": iso(self.clock())})
            self.put("runs", runs[:20])

    def runs(self) -> list[dict]:
        return [r for r in self.get("runs") or [] if isinstance(r, dict)]

    def paused(self) -> bool:
        return self.get("paused") is True

    def set_paused(self, value: bool) -> None:
        self.put("paused", bool(value))
