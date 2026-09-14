"""Request signatures between Markspace and a plugin.

Markspace sends X-Markspace-Signature: t=<unix seconds>,v1=<hex digest>. The
digest is HMAC-SHA256 over a signed string of five parts. The decimal
timestamp, the HTTP method, the path without the query and the delivery id
each end with a newline, and the raw body follows. The key is the 32 bytes
decoded from the base64 signing secret.
"""

from __future__ import annotations

import base64
import binascii
import hashlib
import hmac
import math
import re
import threading
from collections import OrderedDict
from typing import Callable, Mapping
from urllib.parse import urlsplit

from .errors import SignatureError

HEADER_PLUGIN = "X-Markspace-Plugin"
HEADER_DELIVERY = "X-Markspace-Delivery"
HEADER_SIGNATURE = "X-Markspace-Signature"

WINDOW_SECONDS = 300
REPLAY_CAPACITY = 10000
MIN_SECRET_BYTES = 32

_HEADER = re.compile(r"t=([1-9][0-9]{0,18}),v1=([0-9a-f]{64})")
_BASE64 = re.compile(r"[A-Za-z0-9+/]*={0,2}")
_MAX_T = 2**63 - 1


def decode_secret(text: str) -> bytes:
    """Decode a base64 signing secret. Raise ValueError when it is invalid or short."""
    if not isinstance(text, str) or _BASE64.fullmatch(text) is None:
        raise ValueError("signing secret is not standard base64")
    try:
        raw = base64.b64decode(text, validate=True)
    except (binascii.Error, ValueError) as exc:
        raise ValueError(f"signing secret is not standard base64: {exc}") from None
    if len(raw) < MIN_SECRET_BYTES:
        raise ValueError(f"signing secret decodes to {len(raw)} bytes, below {MIN_SECRET_BYTES}")
    return raw


def _enc(text: str) -> bytes:
    return text.encode("utf-8", "surrogateescape")


def signed_message(t: int, method: str, path: str, delivery_id: str, body: bytes) -> bytes:
    """Return the bytes the HMAC covers."""
    return b"%d\n" % int(t) + _enc(method) + b"\n" + _enc(path) + b"\n" + _enc(delivery_id) + b"\n" + body


def _digest(secret: bytes, t: int, method: str, path: str, delivery_id: str, body: bytes) -> str:
    return hmac.new(secret, signed_message(t, method, path, delivery_id, body), hashlib.sha256).hexdigest()


def sign(secret: bytes, t: int, method: str, path: str, delivery_id: str, body: bytes) -> str:
    """Return the signature header value for a request at unix time t. path excludes the query."""
    t = int(t)
    return f"t={t},v1={_digest(secret, t, method, path, delivery_id, body)}"


def parse_signature(header: str) -> tuple[int, str]:
    """Split a header into its timestamp and hex digest. Raise SignatureError on any other form."""
    if not isinstance(header, str):
        raise SignatureError("malformed")
    m = _HEADER.fullmatch(header)
    if m is None:
        raise SignatureError("malformed")
    t = int(m.group(1))
    if t > _MAX_T:
        raise SignatureError("malformed", "timestamp out of range")
    return t, m.group(2)


def verify(secret: bytes, header: str, method: str, path: str, delivery_id: str, body: bytes, now: float,
           window: float = WINDOW_SECONDS) -> int:
    """Check a signature header. Return its timestamp or raise SignatureError.

    The checks run in the order malformed, mismatch, expired.
    """
    t, digest = parse_signature(header)
    if not hmac.compare_digest(_digest(secret, t, method, path, delivery_id, body), digest):
        raise SignatureError("mismatch")
    # Compare whole seconds, as the Go SDK does, so both accept the same requests.
    if abs(math.floor(now) - t) > window:
        raise SignatureError("expired")
    return t


class ReplayGuard:
    """Remember accepted requests within the window and reject repeats.

    A request is a replay when its delivery id was accepted before with the
    same timestamp. Markspace signs each attempt with a new timestamp, so a
    retry of a delivery passes. The cache holds at most capacity entries.
    """

    def __init__(self, capacity: int = REPLAY_CAPACITY, window: float = WINDOW_SECONDS) -> None:
        self._capacity = max(1, capacity)
        self._window = window
        self._seen: OrderedDict[tuple[str, int], int] = OrderedDict()
        self._lock = threading.Lock()

    def check(self, delivery_id: str, header: str, now: float) -> None:
        """Record a verified request, or raise SignatureError for a missing delivery id or a replay."""
        if not delivery_id:
            raise SignatureError("missing_delivery")
        t, _ = parse_signature(header)
        key = (delivery_id, t)
        with self._lock:
            self._prune(now)
            if key in self._seen:
                raise SignatureError("replay")
            self._seen[key] = t
            while len(self._seen) > self._capacity:
                self._seen.popitem(last=False)

    def _prune(self, now: float) -> None:
        while self._seen:
            key = next(iter(self._seen))
            if self._seen[key] >= math.floor(now) - self._window:
                return
            del self._seen[key]


def verify_request(
    secret: bytes,
    headers: Mapping[str, str] | Callable[[str], str | None],
    method: str,
    path: str,
    body: bytes,
    now: float,
    guard: ReplayGuard | None,
    window: float = WINDOW_SECONDS,
) -> int:
    """Verify the signature and delivery headers of a request. Return the timestamp.

    path is the request path without the query, as sent.
    """
    get = headers if callable(headers) else headers.get
    header = get(HEADER_SIGNATURE) or ""
    delivery_id = get(HEADER_DELIVERY) or ""
    t = verify(secret, header, method, path, delivery_id, body, now, window)
    if guard is not None:
        guard.check(delivery_id, header, now)
    return t


def sign_headers(secret: bytes, plugin_id: str, delivery_id: str, method: str, target: str, body: bytes,
                 now: float) -> dict[str, str]:
    """Return the three headers Markspace sends with a request. Tests and fake hosts use it.

    target may include a query, which the signature leaves out.
    """
    return {
        HEADER_PLUGIN: plugin_id,
        HEADER_DELIVERY: delivery_id,
        HEADER_SIGNATURE: sign(secret, int(now), method, urlsplit(target).path, delivery_id, body),
    }
