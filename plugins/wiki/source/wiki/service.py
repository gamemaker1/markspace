"""The service object that jobs, panels and actions share."""

from __future__ import annotations

import hashlib
import logging
import threading
import time
from datetime import timedelta
from typing import Any, Callable

from markspace import HostError

from .ai import ModelClient
from .importers.http import Transport
from .notes import Notes
from .ratelimit import HostGate, Slots
from .settings import Settings, coerce, merge
from .state import State
from .util import Clock, iso, utcnow
from .wikifiles import WikiFiles

log = logging.getLogger("wiki")


class SpaceNotSet(ValueError):
    """No wiki space is configured and the plugin has no single editor grant to fall back on."""


class Service:
    SETTINGS_TTL = 5.0

    def __init__(self, host_fn: Callable[[], Any], *, install_settings_fn: Callable[[], dict] | None = None,
                 clock: Clock = utcnow, gate: HostGate | None = None, transport: Transport | None = None,
                 monotonic: Callable[[], float] = time.monotonic) -> None:
        self.host_fn = host_fn
        self.install_settings_fn = install_settings_fn or (lambda: {})
        self.clock = clock
        self.monotonic = monotonic
        self.state = State(host_fn, clock)
        self.notes = Notes(host_fn, self.state)
        self.files = WikiFiles(self.notes, self.state, self.settings, clock)
        self.ai = ModelClient(host_fn, self.state)
        self.gate = gate or HostGate()
        self.slots = Slots()
        self.transport = transport
        self._settings_cache: tuple[float, Settings] | None = None
        self._compile_locks: dict[str, threading.Lock] = {}
        self._guard = threading.Lock()
        self._url_locks = [threading.Lock() for _ in range(64)]

    @property
    def host(self) -> Any:
        return self.host_fn()

    # settings --------------------------------------------------------------

    def settings(self) -> Settings:
        now = self.monotonic()
        cached = self._settings_cache
        if cached and now - cached[0] < self.SETTINGS_TTL:
            return cached[1]
        try:
            installed = self.install_settings_fn() or {}
        except ValueError:
            installed = {}
        value = merge(installed, self.state.get("settings") or {})
        self._settings_cache = (now, value)
        return value

    def save_settings(self, values: dict) -> dict[str, str]:
        """Store valid form values and return an error message per rejected field."""
        good, errors = coerce(values)
        merged = {**self.settings().to_json(), **good}
        if merged["sourcesFolder"] == merged["wikiFolder"]:
            errors.setdefault("wikiFolder", "The wiki folder must differ from the sources folder.")
            good.pop("wikiFolder", None)
            good.pop("sourcesFolder", None)
        if good:
            stored = dict(self.state.get("settings") or {})
            stored.update(good)
            self.state.put("settings", stored)
            self._settings_cache = None
            self.notes.forget_folders()
        return errors

    def space_id(self, explicit: str = "") -> str:
        if explicit:
            return explicit
        configured = self.settings().space
        if configured:
            return configured
        try:
            me = self.host.me() or {}
        except HostError:
            me = {}
        editors = [g for g in me.get("grants") or [] if isinstance(g, dict) and g.get("role") == "editor"]
        if len(editors) == 1 and editors[0].get("spaceId"):
            return str(editors[0]["spaceId"])
        raise SpaceNotSet("Choose the wiki space in the Bookmark wiki settings.")

    # jobs ------------------------------------------------------------------

    def enqueue(self, kind: str, payload: dict, *, title: str = "", space_id: str | None = None,
                delay: float | None = None) -> dict:
        """Queue a job with the plugin token, so the SDK can report it after an acting token expires."""
        run_after = iso(self.clock() + timedelta(seconds=delay)) if delay else None
        return self.host.create_job(kind, title=title or kind, payload=payload, space_id=space_id or None,
                                    run_after=run_after)

    def requeue_if_paused(self, ctx: Any, kind: str) -> dict | None:
        """Queue the job again in 15 minutes when jobs are paused. Return the result for the current attempt."""
        if not self.state.paused():
            return None
        self.enqueue(kind, ctx.payload or {}, title=getattr(ctx.job, "title", "") or kind,
                     space_id=getattr(ctx.job, "space_id", "") or None, delay=900)
        return {"paused": True}

    def compile_lock(self, space_id: str) -> threading.Lock:
        with self._guard:
            return self._compile_locks.setdefault(space_id, threading.Lock())

    def url_lock(self, key: str) -> threading.Lock:
        return self._url_locks[int(hashlib.sha1(key.encode()).hexdigest()[:8], 16) % len(self._url_locks)]

    # helpers ---------------------------------------------------------------

    def secret(self, name: str) -> str:
        """Return a secret, or empty when the admin did not fill it in."""
        try:
            return self.host.secret(name)
        except HostError as exc:
            if exc.status in (403, 404):
                return ""
            raise

    def forget_note(self, note_id: str) -> None:
        """Drop the records of a note that was deleted or trashed."""
        if self.state.source(note_id):
            self.state.drop_source(note_id)
        if self.state.page(note_id):
            self.state.drop_page(note_id)
