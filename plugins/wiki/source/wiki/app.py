"""Plugin wiring. create_plugin registers every handler of the manifest on the SDK plugin."""

from __future__ import annotations

import logging
from typing import Any, Callable

from markspace import HostError, Plugin, ui
from markspace import ui_response

from . import compile as compile_job
from . import linksuggest, lint, panels, sources
from .importers.http import Transport
from .manifest import MANIFEST
from .ratelimit import HostGate
from .service import Service, SpaceNotSet
from .util import Clock, utcnow

log = logging.getLogger("wiki")

JOBS: dict[str, Callable[[Service, Any], dict]] = {
    "import": sources.job_import,
    "fetch": sources.job_fetch,
    "summarise": sources.job_summarise,
    "compile": compile_job.job_compile,
    "link-suggest": linksuggest.job_link_suggest,
    "lint": lint.job_lint,
    "refetch": sources.job_refetch,
    "sync": sources.job_sync,
}


def _space_missing(exc: SpaceNotSet) -> dict:
    return ui.empty("Choose the wiki space", body=str(exc))


def create_plugin(*, data_dir: str | None = None, env: dict | None = None, job_wait: float = 25.0,
                  install_window: float = 600.0, host_options: dict | None = None, now: Clock = utcnow,
                  gate: HostGate | None = None, transport: Transport | None = None) -> Plugin:
    plugin = Plugin(MANIFEST, data_dir=data_dir, env=env, job_wait=job_wait, install_window=install_window,
                    host_options=host_options)

    def install_settings() -> dict:
        """Return the settings an admin saved in Markspace, from GET /settings.

        When the host cannot answer, the settings of the install payload stand in.
        """
        try:
            return plugin.host().settings()
        except (HostError, ValueError, OSError) as exc:
            log.debug("GET /settings failed, using the install payload: %s", exc)
        try:
            stored = plugin.store.load() or {}
        except ValueError:
            return {}
        value = stored.get("settings")
        return value if isinstance(value, dict) else {}

    svc = Service(lambda: plugin.host(), install_settings_fn=install_settings, clock=now, gate=gate,
                  transport=transport)
    plugin.service = svc  # type: ignore[attr-defined]

    for kind, fn in JOBS.items():
        plugin.job(kind)(lambda ctx, fn=fn: fn(svc, ctx))

    @plugin.panel("home")
    def home(ctx):
        return panels.home(svc, ctx)

    @plugin.panel("wiki")
    def wiki(ctx):
        try:
            return panels.wiki(svc, ctx)
        except SpaceNotSet as exc:
            return _space_missing(exc)

    def guarded(fn: Callable[[Service, Any], dict]) -> Callable[[Any], dict]:
        def run(ctx: Any) -> dict:
            try:
                return fn(svc, ctx)
            except SpaceNotSet as exc:
                return ui_response(_space_missing(exc))
        return run

    plugin.command("add-url")(guarded(panels.command_add_url))
    plugin.command("ask")(guarded(panels.command_ask))
    for action_id, fn in panels.ACTIONS.items():
        plugin.action(action_id)(guarded(fn))

    for importer in ("netscape", "urls"):
        plugin.importer(importer)(guarded(
            lambda s, ctx, importer=importer: panels.importer_preview(s, ctx.host, importer, ctx.file_id, ctx.space_id)))

    @plugin.on("suggestion.*")
    def on_suggestion(batch):
        for event in batch.events:
            if event.type not in ("suggestion.accepted", "suggestion.discarded"):
                continue
            data = event.data if isinstance(event.data, dict) else {}
            if data.get("suggestionId"):
                svc.state.clear_suggestion(str(data["suggestionId"]))

    @plugin.on("plugin.settings.updated")
    def on_settings(batch):
        # Markspace sends this event whatever the manifest events. The next read fetches GET /settings.
        svc._settings_cache = None
        svc.notes.forget_folders()

    @plugin.on("note.*")
    def on_note(batch):
        for event in batch.events:
            if event.type in ("note.deleted", "note.trashed") and event.item_id:
                svc.forget_note(event.item_id)

    return plugin
