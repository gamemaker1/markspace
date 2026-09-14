"""Plugin settings.

Values merge in this order, later ones winning. The defaults, the settings in
the install payload, and the values saved from the settings form on the
Bookmark wiki page, which live in the key-value store.
"""

from __future__ import annotations

from dataclasses import asdict, dataclass
from typing import Any

DEFAULTS: dict[str, Any] = {
    "space": "",
    "sourcesFolder": "Sources",
    "wikiFolder": "Wiki",
    "summariseModel": "",
    "compileModel": "",
    "maxPagesPerSource": 12,
    "suggestionsPerDay": 20,
    "fetchConcurrency": 4,
    "respectRobots": True,
    "summaryLanguage": "",
    "importEveryHours": 6,
}

LIMITS = {
    "maxPagesPerSource": (1, 50),
    "suggestionsPerDay": (0, 1000),
    "fetchConcurrency": (1, 16),
    "importEveryHours": (1, 24 * 7),
}


@dataclass
class Settings:
    space: str = ""
    sourcesFolder: str = "Sources"
    wikiFolder: str = "Wiki"
    summariseModel: str = ""
    compileModel: str = ""
    maxPagesPerSource: int = 12
    suggestionsPerDay: int = 20
    fetchConcurrency: int = 4
    respectRobots: bool = True
    summaryLanguage: str = ""
    importEveryHours: int = 6

    def to_json(self) -> dict[str, Any]:
        return asdict(self)


def _folder_ok(name: str) -> bool:
    if not name or name.startswith(".") or name != name.strip() or len(name.encode()) > 255:
        return False
    return not any(c in name for c in "/\\\x00") and not any(ord(c) < 0x20 for c in name)


def coerce(values: dict[str, Any]) -> tuple[dict[str, Any], dict[str, str]]:
    """Return the valid values of a form submission and an error message per rejected field."""
    good: dict[str, Any] = {}
    errors: dict[str, str] = {}
    for key, value in (values or {}).items():
        if key not in DEFAULTS:
            continue
        default = DEFAULTS[key]
        if isinstance(default, bool):
            if isinstance(value, str):
                value = value.strip().lower() in ("1", "true", "on", "yes")
            good[key] = bool(value)
        elif isinstance(default, int):
            try:
                n = int(float(value))
            except (TypeError, ValueError):
                errors[key] = "Enter a whole number."
                continue
            lo, hi = LIMITS[key]
            if not lo <= n <= hi:
                errors[key] = f"Enter a number from {lo} to {hi}."
                continue
            good[key] = n
        else:
            text = "" if value is None else str(value).strip()
            if key in ("sourcesFolder", "wikiFolder") and not _folder_ok(text):
                errors[key] = "Enter a folder name without slashes that does not start with a dot."
                continue
            good[key] = text
    if good.get("sourcesFolder") and good.get("sourcesFolder") == good.get("wikiFolder"):
        errors["wikiFolder"] = "The wiki folder must differ from the sources folder."
        good.pop("wikiFolder")
    return good, errors


def merge(*layers: dict[str, Any] | None) -> Settings:
    """Merge layers of raw values over the defaults. Invalid values are ignored."""
    merged = dict(DEFAULTS)
    for layer in layers:
        good, _ = coerce(layer or {})
        merged.update(good)
    if merged["sourcesFolder"] == merged["wikiFolder"]:
        merged["sourcesFolder"], merged["wikiFolder"] = DEFAULTS["sourcesFolder"], DEFAULTS["wikiFolder"]
    return Settings(**merged)


def model_choice(spec: str) -> tuple[str, str] | None:
    """Split providerId/model into its parts. A model id may itself contain slashes."""
    spec = (spec or "").strip()
    if not spec:
        return None
    provider, sep, model = spec.partition("/")
    if not sep or not provider or not model:
        return None
    return provider, model
