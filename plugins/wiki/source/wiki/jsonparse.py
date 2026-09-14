"""Tolerant parsing of JSON in model output.

Models wrap JSON in code fences, put prose before it or add text after it.
extract_json tries every fenced block, then the whole text, and decodes the
first object that starts at a brace. Trailing text after the object is ignored.
"""

from __future__ import annotations

import json
import re
from typing import Any

_FENCE = re.compile(r"```[A-Za-z0-9_-]*[ \t]*\r?\n(.*?)```", re.DOTALL)
MAX_STARTS = 64


class ModelOutputError(ValueError):
    """Model output holds no usable JSON, or the JSON breaks its schema."""

    def __init__(self, message: str, problems: list[str] | None = None) -> None:
        self.problems = problems or [message]
        super().__init__(message)


def _candidates(text: str) -> list[str]:
    out = [m.group(1) for m in _FENCE.finditer(text)]
    out.append(text)
    return out


def extract_json(text: Any, want: type = dict) -> Any:
    """Return the first JSON value of type want found in text. Raise ModelOutputError otherwise."""
    if not isinstance(text, str) or not text.strip():
        raise ModelOutputError("the model returned no text")
    decoder = json.JSONDecoder()
    opener = "{" if want is dict else "["
    last_error = "no JSON object found"
    for cand in _candidates(text):
        start = cand.find(opener)
        tries = 0
        while start != -1 and tries < MAX_STARTS:
            tries += 1
            try:
                value, _ = decoder.raw_decode(cand, start)
            except ValueError as exc:
                last_error = f"invalid JSON: {exc}"
            else:
                if isinstance(value, want):
                    return value
            start = cand.find(opener, start + 1)
    raise ModelOutputError(last_error)
