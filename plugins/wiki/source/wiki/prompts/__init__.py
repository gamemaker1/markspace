"""Prompt templates.

Each template is a text file with a system part and a user part separated by
a line holding "=== USER ===". Placeholders use string.Template syntax, such
as $input_json. The user part puts the structured input between the lines
<<<INPUT and INPUT>>>, so the model and test fakes can find it.
"""

from __future__ import annotations

import json
from functools import lru_cache
from pathlib import Path
from string import Template
from typing import Any

SEPARATOR = "=== USER ==="
DIR = Path(__file__).resolve().parent
NAMES = ("summarise", "compile", "repair", "lint", "ask")


@lru_cache(maxsize=None)
def _load(name: str) -> tuple[Template, Template]:
    if name not in NAMES:
        raise ValueError(f"unknown prompt {name!r}")
    text = (DIR / f"{name}.txt").read_text(encoding="utf-8")
    system, sep, user = text.partition(SEPARATOR + "\n")
    if not sep:
        raise ValueError(f"prompt {name} has no {SEPARATOR} line")
    return Template(system.strip() + "\n"), Template(user)


def render(name: str, **values: Any) -> tuple[str, str]:
    """Return the (system, user) text of a prompt with values filled in."""
    system, user = _load(name)
    filled = {k: v if isinstance(v, str) else json.dumps(v, ensure_ascii=False, indent=1)
              for k, v in values.items()}
    return system.safe_substitute(filled), user.safe_substitute(filled)


def schema_text(name: str) -> str:
    """Return the JSON schema block of a prompt, the text between 'Schema:' and the next blank line after it."""
    system, _ = _load(name)
    text = system.template
    start = text.find("Schema:\n")
    if start == -1:
        return ""
    body = text[start + len("Schema:\n"):]
    end = body.find("\n\n")
    return body if end == -1 else body[:end]


def extract_input(user_text: str) -> Any:
    """Return the JSON between <<<INPUT and INPUT>>> in a rendered user part, or None."""
    start = user_text.find("<<<INPUT\n")
    end = user_text.find("\nINPUT>>>", start)
    if start == -1 or end == -1:
        return None
    try:
        return json.loads(user_text[start + len("<<<INPUT\n"):end])
    except ValueError:
        return None
