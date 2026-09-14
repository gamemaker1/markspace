"""URL normalisation, slugs and deterministic ids.

normalise_url follows spec.md. It lower-cases the scheme and host, drops a
www. prefix, the fragment, default ports, user info and tracking parameters,
sorts the remaining query parameters, and drops one trailing slash from a
path longer than /.
"""

from __future__ import annotations

import hashlib
import re
import unicodedata
from urllib.parse import unquote_plus, urlsplit

DROP_PARAMS = frozenset({"fbclid", "gclid", "mc_cid", "mc_eid", "ref", "ref_src"})
DEFAULT_PORTS = {"http": 80, "https": 443}
ID_ALPHABET = "0123456789abcdefghjkmnpqrstvwxyz"


class InvalidURL(ValueError):
    """The text is not an http or https URL with a host."""


def _drop_param(raw_pair: str) -> bool:
    key = unquote_plus(raw_pair.split("=", 1)[0]).lower()
    return key.startswith("utm_") or key in DROP_PARAMS


def _sort_key(raw_pair: str) -> tuple[str, str]:
    key, _, value = raw_pair.partition("=")
    return unquote_plus(key), unquote_plus(value)


def normalise_url(raw: str) -> str:
    """Return the normalised form of an http or https URL. Raise InvalidURL for anything else."""
    text = (raw or "").strip()
    if not text:
        raise InvalidURL("empty URL")
    if any(ord(c) < 0x20 or c.isspace() for c in text):
        raise InvalidURL("URL contains whitespace")
    try:
        parts = urlsplit(text)
        host = parts.hostname
        port = parts.port
    except ValueError as exc:
        raise InvalidURL(str(exc)) from None
    scheme = parts.scheme.lower()
    if scheme not in DEFAULT_PORTS:
        raise InvalidURL(f"unsupported scheme {parts.scheme!r}")
    if not host:
        raise InvalidURL("URL has no host")
    host = host.rstrip(".")
    if host.startswith("www."):
        host = host[4:]
    netloc = f"[{host}]" if ":" in host else host
    if port is not None and port != DEFAULT_PORTS[scheme]:
        netloc += f":{port}"
    path = parts.path or "/"
    if len(path) > 1 and path.endswith("/"):
        path = path[:-1]
    pairs = [p for p in parts.query.split("&") if p and not _drop_param(p)]
    pairs.sort(key=_sort_key)
    url = f"{scheme}://{netloc}{path}"
    if pairs:
        url += "?" + "&".join(pairs)
    return url


def site_of(url: str) -> str:
    """Return the host of a URL without a www. prefix, as used for the Sources/<domain> folder."""
    try:
        host = urlsplit(url).hostname or ""
    except ValueError:
        host = ""
    host = host.rstrip(".")
    if host.startswith("www."):
        host = host[4:]
    return host or "unknown"


def slugify(text: str, max_len: int = 80) -> str:
    """Return a lower-case ASCII slug of text, or untitled when nothing is left."""
    norm = unicodedata.normalize("NFKD", text or "")
    ascii_text = "".join(c for c in norm if not unicodedata.combining(c)).encode("ascii", "ignore").decode()
    slug = re.sub(r"[^a-z0-9]+", "-", ascii_text.lower()).strip("-")
    if len(slug) > max_len:
        slug = slug[:max_len].rstrip("-")
    return slug or "untitled"


def stable_id(*parts: str) -> str:
    """Return a 26 character id in the core.NewID alphabet derived from parts.

    The plugin passes it as the client id of a created note, so a create that
    runs again after a crash returns the stored note instead of a second one.
    """
    digest = hashlib.sha256("\x1f".join(parts).encode("utf-8")).digest()
    n = int.from_bytes(digest[:17], "big")
    out = []
    for _ in range(26):
        out.append(ID_ALPHABET[n & 31])
        n >>= 5
    return "".join(reversed(out))


def url_key(normalised: str) -> str:
    """Return the key-value key of a normalised URL. Long URLs would exceed the key limit, so the key is a hash."""
    return "url:" + hashlib.sha256(normalised.encode("utf-8")).hexdigest()[:40]
