"""The plugin manifest served at GET /manifest.json."""

from __future__ import annotations

PLUGIN_ID = "bookmark-wiki"
VERSION = "0.1.0"

# Each purpose becomes the routing task plugin.bookmark-wiki.<purpose> from docs/models.md.
PURPOSES = ("summarise", "compile", "lint", "ask")


def route(purpose: str) -> str:
    """Return the routing task of a purpose."""
    if purpose not in PURPOSES:
        raise ValueError(f"unknown purpose {purpose!r}")
    return f"plugin.{PLUGIN_ID}.{purpose}"


SETTINGS_FIELDS = [
    {"type": "space", "name": "space", "label": "Wiki space", "required": True},
    {"type": "text", "name": "sourcesFolder", "label": "Sources folder", "value": "Sources"},
    {"type": "text", "name": "wikiFolder", "label": "Wiki folder", "value": "Wiki"},
    {"type": "text", "name": "summariseModel", "label": "Summarise model",
     "hint": "providerId/model. Empty uses the route for plugin.bookmark-wiki.summarise.", "value": ""},
    {"type": "text", "name": "compileModel", "label": "Compile model",
     "hint": "providerId/model. Empty uses the route for plugin.bookmark-wiki.compile.", "value": ""},
    {"type": "number", "name": "maxPagesPerSource", "label": "Maximum pages per source", "value": 12},
    {"type": "number", "name": "suggestionsPerDay", "label": "Suggestions per day", "value": 20},
    {"type": "number", "name": "fetchConcurrency", "label": "Fetch concurrency", "value": 4},
    {"type": "checkbox", "name": "respectRobots", "label": "Respect robots.txt", "value": True},
    {"type": "text", "name": "summaryLanguage", "label": "Summary language",
     "hint": "A language code such as en. Empty keeps the language of each source.", "value": ""},
    {"type": "number", "name": "importEveryHours", "label": "Import schedule for API importers, in hours",
     "value": 6},
]

MANIFEST: dict = {
    "protocol": 1,
    "id": PLUGIN_ID,
    "name": "Bookmark wiki",
    "version": VERSION,
    "description": "Reads bookmarks, summarises them and maintains a linked wiki.",
    "homepage": "https://example.invalid/bookmark-wiki",
    "permissions": ["items:read", "items:write", "links:read", "search", "ai:complete", "web:fetch", "jobs",
                    "storage", "ui", "suggestions", "events"],
    "events": ["suggestion.accepted", "suggestion.discarded", "note.deleted", "note.trashed"],
    "purposes": [
        {"id": "summarise", "title": "Summarise a bookmarked page", "needs": ["text"]},
        {"id": "compile", "title": "Write and update wiki pages", "needs": ["text"]},
        {"id": "lint", "title": "Check wiki pages for disagreements", "needs": ["text"]},
        {"id": "ask", "title": "Answer questions from the wiki", "needs": ["text"]},
    ],
    "jobs": [
        {"kind": "import", "title": "Import bookmarks", "maxAttempts": 3},
        {"kind": "fetch", "title": "Fetch a bookmarked page", "maxAttempts": 5},
        {"kind": "summarise", "title": "Summarise a source", "maxAttempts": 3},
        {"kind": "compile", "title": "Compile wiki pages from a source", "maxAttempts": 3},
        {"kind": "link-suggest", "title": "Suggest links to a wiki page", "maxAttempts": 3},
        {"kind": "lint", "title": "Lint the wiki", "maxAttempts": 2},
        {"kind": "refetch", "title": "Re-fetch the oldest sources", "maxAttempts": 2},
        {"kind": "sync", "title": "Import from Karakeep and Linkding", "maxAttempts": 2},
    ],
    "schedules": [
        {"job": "lint", "every": "24h"},
        {"job": "refetch", "every": "24h"},
        # The sync job runs hourly and imports when importEveryHours have passed since the last run.
        {"job": "sync", "every": "1h"},
    ],
    "commands": [
        {"id": "add-url", "title": "Add link to wiki", "icon": "bookmark", "contexts": ["link", "global"]},
        {"id": "ask", "title": "Ask the wiki", "icon": "book", "contexts": ["global"]},
    ],
    "panels": [
        {"id": "home", "title": "Bookmark wiki", "icon": "book", "where": "page", "contexts": ["global"]},
        {"id": "wiki", "title": "Wiki", "icon": "book", "where": "rail", "contexts": ["note", "space"]},
    ],
    "importers": [
        {"id": "netscape", "title": "Browser bookmarks", "accept": [".html", ".htm"]},
        {"id": "urls", "title": "URL list", "accept": [".txt"]},
    ],
    "settings": {"fields": SETTINGS_FIELDS},
    "secrets": [
        {"name": "karakeep_url", "label": "Karakeep base URL"},
        {"name": "karakeep_token", "label": "Karakeep API token"},
        {"name": "linkding_url", "label": "Linkding base URL"},
        {"name": "linkding_token", "label": "Linkding API token"},
    ],
}
