"""A small JSON over HTTP client for the Karakeep and Linkding APIs.

Tests replace the transport with a function that serves fixture pages, so no
test touches the network.
"""

from __future__ import annotations

import json
import time
import urllib.error
import urllib.request
from typing import Any, Callable
from urllib.parse import urlsplit

from . import ImporterError

Transport = Callable[[str, dict[str, str]], Any]
USER_AGENT = "markspace-bookmark-wiki/0.1"


def urllib_transport(url: str, headers: dict[str, str], timeout: float = 20.0) -> Any:
    """GET url and decode its JSON body. Raise ImporterError on any failure."""
    req = urllib.request.Request(url, headers={"Accept": "application/json", "User-Agent": USER_AGENT, **headers})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read(32 << 20)
    except urllib.error.HTTPError as exc:
        raise ImporterError(f"GET {_redact(url)} answered {exc.code}") from None
    except (urllib.error.URLError, OSError) as exc:
        raise ImporterError(f"GET {_redact(url)} failed: {getattr(exc, 'reason', exc)}") from None
    try:
        return json.loads(raw)
    except ValueError:
        raise ImporterError(f"GET {_redact(url)} did not return JSON") from None


def _redact(url: str) -> str:
    parts = urlsplit(url)
    return f"{parts.scheme}://{parts.netloc}{parts.path}"


class Deadline:
    """A point in monotonic time after which paging stops. None never expires."""

    def __init__(self, seconds: float | None, clock: Callable[[], float] = time.monotonic) -> None:
        self.clock = clock
        self.at = None if seconds is None else clock() + seconds

    @property
    def passed(self) -> bool:
        return self.at is not None and self.clock() >= self.at


def same_origin(base: str, url: str) -> bool:
    """Report whether url has the scheme and host of base."""
    a, b = urlsplit(base), urlsplit(url)
    return (a.scheme, a.netloc) == (b.scheme, b.netloc)
