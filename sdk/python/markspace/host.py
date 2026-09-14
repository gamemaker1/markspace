"""Client for the Markspace host API at /api/plugin/v1.

Every call sends the plugin token or an acting token as a bearer token. Every
POST carries an Idempotency-Key header with a fresh key, and a retry of that
POST sends the same key. A call that gets 429, 502, 503 or 504, or no response
at all, runs again up to max_tries times in total. The delay doubles from 0.5 s
up to 30 s with random jitter, and a Retry-After header replaces it. A 500 and
every other failure raise HostError without a retry.
"""

from __future__ import annotations

import copy
import email.utils
import http.client
import io
import json
import os
import random
import secrets
import time
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, BinaryIO, Callable, Iterable, Iterator
from urllib.parse import quote, urlencode

from . import ids
from .errors import HostError

API_PREFIX = "/api/plugin/v1"
RETRY_STATUSES = (429, 502, 503, 504)
MAX_RETRY_AFTER = 120.0
CHUNK = 64 * 1024
USER_AGENT = "markspace-plugin-python/0.1"
IDEMPOTENCY_HEADER = "Idempotency-Key"

_UNSET = object()


@dataclass
class StreamEvent:
    """One server-sent event from a streamed AI call. data is parsed JSON when possible."""

    event: str
    data: Any


@dataclass
class Page:
    """One page of a list response. next_cursor is None on the last page."""

    items: list
    next_cursor: str | None = None


@dataclass
class KVEntry:
    """A stored key-value entry."""

    key: str
    value: Any
    updated_at: str = ""


@dataclass
class FileText:
    """The extracted text of a file. truncated is true when extraction stopped at its limit."""

    text: str
    truncated: bool = False


@dataclass
class AIModel:
    """A model the plugin may call."""

    provider_id: str
    model: str
    vision: bool = False
    image_generation: bool = False
    image_editing: bool = False
    prices: Any = None
    raw: dict = field(default_factory=dict, repr=False)

    @classmethod
    def from_json(cls, data: dict) -> "AIModel":
        return cls(provider_id=str(data.get("providerId") or ""), model=str(data.get("model") or ""),
                   vision=bool(data.get("vision")), image_generation=bool(data.get("imageGeneration")),
                   image_editing=bool(data.get("imageEditing")), prices=data.get("prices"), raw=data)


class _Body:
    """A request body that can be sent more than once when replayable is true."""

    def __init__(self, factory: Callable[[], Iterable[bytes]], length: int | None, replayable: bool) -> None:
        self.factory = factory
        self.length = length
        self.replayable = replayable


class Host:
    """Host API client bound to one token."""

    def __init__(
        self,
        markspace_url: str | None,
        token: str | None,
        plugin_id: str = "",
        *,
        timeout: float = 30.0,
        stream_timeout: float = 300.0,
        max_tries: int = 5,
        sleep: Callable[[float], None] = time.sleep,
        rand: Callable[[], float] = random.random,
    ) -> None:
        self.markspace_url = (markspace_url or "").rstrip("/")
        self.token = token
        self.plugin_id = plugin_id
        self.timeout = timeout
        self.stream_timeout = stream_timeout
        self.max_tries = max(1, max_tries)
        self.sleep = sleep
        self.rand = rand

    @property
    def base_url(self) -> str:
        return self.markspace_url + API_PREFIX

    def with_token(self, token: str) -> "Host":
        """Return a copy of this client that sends token."""
        h = copy.copy(self)
        h.token = token
        return h

    # transport -------------------------------------------------------------

    def delay(self, attempt: int, retry_after: str | None = None) -> float:
        """Return the wait before retry number attempt, counted from 1."""
        if retry_after:
            parsed = _parse_retry_after(retry_after)
            if parsed is not None:
                return min(parsed, MAX_RETRY_AFTER)
        base = min(30.0, 0.5 * 2 ** (attempt - 1))
        return base * (0.5 + 0.5 * self.rand())

    def open(
        self,
        method: str,
        path: str,
        *,
        json_body: Any = _UNSET,
        body: _Body | bytes | None = None,
        content_type: str | None = None,
        query: dict[str, Any] | None = None,
        accept: str = "application/json",
        timeout: float | None = None,
        idempotency_key: str | None = None,
    ) -> http.client.HTTPResponse:
        """Send a request with retries and return the open response. The caller closes it."""
        if not self.markspace_url:
            raise HostError(0, "MARKSPACE_URL is not configured")
        if not self.token:
            raise HostError(0, "no host API token is configured")
        url = self.base_url + path
        if query:
            q = {k: v for k, v in query.items() if v is not None}
            if q:
                url += "?" + urlencode(q)
        if json_body is not _UNSET:
            raw = json.dumps(json_body, separators=(",", ":")).encode("utf-8")
            body = _Body(lambda: [raw], len(raw), True)
            content_type = "application/json"
        elif isinstance(body, (bytes, bytearray)):
            raw = bytes(body)
            body = _Body(lambda: [raw], len(raw), True)
        headers = {
            "Authorization": f"Bearer {self.token}",
            "Accept": accept,
            "User-Agent": f"{USER_AGENT} ({self.plugin_id})" if self.plugin_id else USER_AGENT,
        }
        if content_type:
            headers["Content-Type"] = content_type
        if method == "POST":
            headers[IDEMPOTENCY_HEADER] = idempotency_key or secrets.token_hex(16)
        tries = self.max_tries if body is None or body.replayable else 1
        attempt = 0
        while True:
            attempt += 1
            data = None
            req_headers = dict(headers)
            if body is not None:
                data = body.factory()
                if body.length is not None:
                    req_headers["Content-Length"] = str(body.length)
            req = urllib.request.Request(url, data=data, headers=req_headers, method=method)
            try:
                return urllib.request.urlopen(req, timeout=timeout or self.timeout)
            except urllib.error.HTTPError as exc:
                status = exc.code
                try:
                    payload = exc.read()
                except OSError:
                    payload = b""
                retry_after = exc.headers.get("Retry-After") if exc.headers else None
                exc.close()
                if status in RETRY_STATUSES and attempt < tries:
                    self.sleep(self.delay(attempt, retry_after))
                    continue
                raise _error_from(status, payload) from None
            except (urllib.error.URLError, OSError, http.client.HTTPException) as exc:
                if attempt < tries:
                    self.sleep(self.delay(attempt))
                    continue
                reason = getattr(exc, "reason", exc)
                raise HostError(0, f"{method} {path}: {reason}") from None

    def call(self, method: str, path: str, json_body: Any = _UNSET, **kwargs: Any) -> Any:
        """Send a request and return the decoded JSON response, or None for an empty body."""
        with self.open(method, path, json_body=json_body, **kwargs) as resp:
            raw = resp.read()
        if not raw:
            return None
        try:
            return json.loads(raw)
        except ValueError:
            raise HostError(resp.status, f"{method} {path}: response is not JSON") from None

    def page(self, method: str, path: str, json_body: Any = _UNSET, **kwargs: Any) -> Page:
        """Send a request whose response is a list {items, nextCursor} and return one Page."""
        return _page(self.call(method, path, json_body, **kwargs), f"{method} {path}")

    def _optional(self, method: str, path: str, **kwargs: Any) -> Any:
        try:
            return self.call(method, path, **kwargs)
        except HostError as exc:
            if exc.status == 404:
                return _UNSET
            raise

    # identity and items ----------------------------------------------------

    def me(self) -> dict:
        """Return the plugin id, grants and permissions of the token."""
        return self.call("GET", "/me")

    def settings(self) -> dict:
        """Return the settings an admin saved for the plugin, from GET /settings."""
        data = self.call("GET", "/settings")
        value = data.get("settings") if isinstance(data, dict) else None
        return value if isinstance(value, dict) else {}

    def tree(self, space_id: str) -> Page:
        """Return the tree of a space. Each item is {kind, folder|note|file, children}."""
        return self.page("GET", f"/spaces/{_oid(space_id)}/tree")

    def get_item(self, item_id: str) -> dict:
        """Return {kind, folder|note|file} for an item."""
        return self.call("GET", f"/items/{_oid(item_id)}")

    def create_note(self, space_id: str, title: str, content: str = "", *, parent_id: str = "",
                    tags: list[str] | None = None, id: str | None = None, icon: str | None = None,
                    frontmatter: dict | None = None, folder_note: bool = False, folder_id: str = "") -> dict:
        """Create a note. content is the body. frontmatter sets extra frontmatter keys.

        folder_note with folder_id creates the folder note <folder>/index.md. The host answers 409 when the
        folder already has one.
        """
        body: dict[str, Any] = {"spaceId": space_id, "parentId": parent_id, "title": title, "content": content,
                                "tags": list(tags or [])}
        if folder_note:
            body["folderNote"] = True
            body["folderId"] = folder_id
        if id is not None:
            body["id"] = id
        if icon is not None:
            body["icon"] = icon
        if frontmatter is not None:
            body["frontmatter"] = frontmatter
        return self.call("POST", "/notes", body)

    def update_note(self, note_id: str, *, base_updated_at: str | datetime, title: str | None = None,
                    content: str | None = None, tags: list[str] | None = None, icon: str | None = None,
                    frontmatter: dict | None = None) -> dict:
        """Update a note. base_updated_at is the updatedAt the change was based on.

        frontmatter merges extra frontmatter keys, and a None value deletes a key.
        """
        body: dict[str, Any] = {"baseUpdatedAt": _time(base_updated_at)}
        if title is not None:
            body["title"] = title
        if content is not None:
            body["content"] = content
        if tags is not None:
            body["tags"] = list(tags)
        if icon is not None:
            body["icon"] = icon
        if frontmatter is not None:
            body["frontmatter"] = frontmatter
        return self.call("PATCH", f"/notes/{_oid(note_id)}", body)

    def create_folder(self, space_id: str, name: str, *, parent_id: str = "") -> dict:
        """Create a folder."""
        return self.call("POST", "/folders", {"spaceId": space_id, "parentId": parent_id, "name": name})

    def upload_file(self, space_id: str, file: BinaryIO | bytes, name: str, *, parent_id: str = "",
                    mime: str | None = None, id: str | None = None, on_conflict: str | None = None) -> dict:
        """Upload a file as multipart form data, streaming from a binary file object.

        A seekable file object is sent with a Content-Length and retried from
        its starting offset. Any other file object is sent once.
        """
        if isinstance(file, (bytes, bytearray)):
            file = io.BytesIO(bytes(file))
        boundary = "markspace-" + secrets.token_hex(16)
        fields = [("spaceId", space_id), ("parentId", parent_id)]
        if id is not None:
            fields.append(("id", id))
        if on_conflict is not None:
            fields.append(("onConflict", on_conflict))
        head = b"".join(
            f'--{boundary}\r\nContent-Disposition: form-data; name="{k}"\r\n\r\n'.encode() + v.encode("utf-8") + b"\r\n"
            for k, v in fields
        )
        head += (
            f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{_quote_filename(name)}"\r\n'
            f"Content-Type: {mime or 'application/octet-stream'}\r\n\r\n"
        ).encode("utf-8")
        tail = f"\r\n--{boundary}--\r\n".encode()
        start, size = _seekable_span(file)

        def parts() -> Iterator[bytes]:
            if start is not None:
                file.seek(start)
            yield head
            while True:
                chunk = file.read(CHUNK)
                if not chunk:
                    break
                yield chunk
            yield tail

        length = None if size is None else len(head) + size + len(tail)
        body = _Body(parts, length, start is not None)
        with self.open("POST", "/files", body=body, content_type=f"multipart/form-data; boundary={boundary}") as resp:
            return _decode(resp.read())

    def open_file_content(self, file_id: str) -> http.client.HTTPResponse:
        """Return the open response for a file's bytes. The caller closes it."""
        return self.open("GET", f"/files/{_oid(file_id)}/content", accept="*/*", timeout=self.stream_timeout)

    def file_content(self, file_id: str) -> bytes:
        """Return a file's bytes."""
        with self.open_file_content(file_id) as resp:
            return resp.read()

    def file_text(self, file_id: str) -> FileText:
        """Return the extracted text of a file."""
        data = self.call("GET", f"/files/{_oid(file_id)}/text")
        if not isinstance(data, dict):
            raise HostError(200, "file text response is not an object")
        return FileText(text=str(data.get("text") or ""), truncated=bool(data.get("truncated")))

    def move(self, item_id: str, parent_id: str, *, position: int | None = None) -> Any:
        """Move an item to another folder in its space."""
        body: dict[str, Any] = {"parentId": parent_id}
        if position is not None:
            body["position"] = position
        return self.call("POST", f"/items/{_oid(item_id)}/move", body)

    def copy(self, item_id: str, *, space_id: str, parent_id: str, name: str | None = None) -> Any:
        """Copy an item, possibly into another space."""
        body: dict[str, Any] = {"spaceId": space_id, "parentId": parent_id}
        if name is not None:
            body["name"] = name
        return self.call("POST", f"/items/{_oid(item_id)}/copy", body)

    def trash(self, item_id: str) -> Any:
        """Move an item to the trash."""
        return self.call("POST", f"/items/{_oid(item_id)}/trash", {})

    # links and search ------------------------------------------------------

    def outbound_links(self, note_id: str) -> Page:
        """Return the resolved outbound links of a note."""
        return self.page("GET", f"/notes/{_oid(note_id)}/links")

    def backlinks(self, item_id: str) -> Page:
        """Return the notes that link to an item."""
        return self.page("GET", f"/items/{_oid(item_id)}/backlinks")

    def resolve(self, space_id: str, from_note_id: str, targets: list[str]) -> Page:
        """Resolve link targets as seen from a note. Items are {target, kind, id, path, url}."""
        return self.page("POST", "/resolve", {"spaceId": space_id, "fromNoteId": from_note_id, "targets": list(targets)})

    def search(self, q: str, *, mode: str | None = None, space: str | None = None, limit: int | None = None,
               cursor: str | None = None) -> Page:
        """Run a text or semantic search."""
        return self.page("GET", "/search", query={"q": q, "mode": mode, "space": space, "limit": limit,
                                                  "cursor": cursor})

    # suggestions and comments ----------------------------------------------

    def create_suggestion(self, note_id: str, title: str, edits: list[dict], *, rationale: str | None = None) -> dict:
        """Propose edits to a note. Each edit is {anchor, text}."""
        body: dict[str, Any] = {"noteId": note_id, "title": title, "edits": list(edits)}
        if rationale is not None:
            body["rationale"] = rationale
        return self.call("POST", "/suggestions", body)

    def withdraw_suggestion(self, suggestion_id: str) -> None:
        """Withdraw a suggestion the plugin created."""
        self.call("DELETE", f"/suggestions/{_oid(suggestion_id)}")

    def create_comment(self, item_id: str, body: str, *, anchor: dict | None = None) -> dict:
        """Comment on an item."""
        payload: dict[str, Any] = {"itemId": item_id, "body": body}
        if anchor is not None:
            payload["anchor"] = anchor
        return self.call("POST", "/comments", payload)

    # AI and web ------------------------------------------------------------

    def ai_complete(self, request: dict) -> dict:
        """Run an AIRequest and return the result."""
        return self.call("POST", "/ai/complete", request, timeout=self.stream_timeout)

    def ai_complete_stream(self, request: dict) -> Iterator[StreamEvent]:
        """Run an AIRequest and yield its server-sent events, such as delta, done and error."""
        resp = self.open("POST", "/ai/complete", json_body=request, accept="text/event-stream",
                         timeout=self.stream_timeout)
        with resp:
            yield from parse_sse(resp)

    def ai_embed(self, texts: list[str]) -> Any:
        """Return embeddings for texts."""
        return self.call("POST", "/ai/embed", {"texts": list(texts)}, timeout=self.stream_timeout)

    def ai_image(self, prompt: str, *, size: str | None = None, file_id: str | None = None,
                 mask_file_id: str | None = None) -> Any:
        """Generate or edit an image."""
        body: dict[str, Any] = {"prompt": prompt}
        for k, v in (("size", size), ("fileId", file_id), ("maskFileId", mask_file_id)):
            if v is not None:
                body[k] = v
        return self.call("POST", "/ai/image", body, timeout=self.stream_timeout)

    def ai_models(self) -> list[AIModel]:
        """Return the models the plugin may call, with capabilities and prices."""
        return [AIModel.from_json(m) for m in self.page("GET", "/ai/models").items if isinstance(m, dict)]

    def web_fetch(self, url: str, *, respect_robots: bool | None = None) -> dict:
        """Fetch a page through the guarded fetcher."""
        body: dict[str, Any] = {"url": url}
        if respect_robots is not None:
            body["respectRobots"] = respect_robots
        return self.call("POST", "/web/fetch", body)

    # jobs ------------------------------------------------------------------

    def create_job(self, kind: str, *, title: str = "", payload: Any = None, space_id: str | None = None,
                   run_after: str | datetime | None = None, parent_id: str | None = None) -> dict:
        """Queue a job of one of the plugin's kinds and return the Job."""
        body: dict[str, Any] = {"kind": kind, "title": title, "payload": {} if payload is None else payload}
        if space_id is not None:
            body["spaceId"] = space_id
        if run_after is not None:
            body["runAfter"] = _time(run_after)
        if parent_id is not None:
            body["parentId"] = parent_id
        return self.call("POST", "/jobs", body)

    def update_job(self, job_id: str, *, progress: int | None = None, total: int | None = None,
                   message: str | None = None, status: str | None = None, result: Any = _UNSET) -> dict:
        """Report progress, or finish a job with status done or failed. Return the Job.

        A returned status of canceled means the job should stop.
        """
        body: dict[str, Any] = {}
        for k, v in (("progress", progress), ("total", total), ("message", message), ("status", status)):
            if v is not None:
                body[k] = v
        if result is not _UNSET:
            body["result"] = result
        return self.call("PATCH", f"/jobs/{_oid(job_id)}", body)

    def job_log(self, job_id: str, level: str, message: str) -> None:
        """Append a line to a job's log. level is debug, info, warn or error."""
        self.call("POST", f"/jobs/{_oid(job_id)}/log", {"level": level, "message": message})

    # storage ---------------------------------------------------------------

    def kv_entry(self, key: str) -> KVEntry | None:
        """Return the entry stored under key, or None when there is none."""
        data = self._optional("GET", f"/kv/{_key(key)}")
        if data is _UNSET:
            return None
        if not isinstance(data, dict) or "value" not in data:
            raise HostError(200, "key-value response has no value")
        return KVEntry(key=str(data.get("key") or key), value=data["value"], updated_at=str(data.get("updatedAt") or ""))

    def kv_get(self, key: str, default: Any = None) -> Any:
        """Return the JSON value stored under key, or default when there is none."""
        entry = self.kv_entry(key)
        return default if entry is None else entry.value

    def kv_put(self, key: str, value: Any) -> None:
        """Store a JSON value under key."""
        self.call("PUT", f"/kv/{_key(key)}", {"value": value})

    def kv_delete(self, key: str) -> None:
        """Delete key."""
        self.call("DELETE", f"/kv/{_key(key)}")

    def kv_list(self, prefix: str = "", *, cursor: str | None = None) -> Page:
        """Return one page of keys that start with prefix. Items are {key, size, updatedAt}."""
        return self.page("GET", "/kv", query={"prefix": prefix, "cursor": cursor})

    def kv_keys(self, prefix: str = "") -> Iterator[dict]:
        """Yield every {key, size, updatedAt} that starts with prefix, following cursors."""
        cursor = None
        while True:
            page = self.kv_list(prefix, cursor=cursor)
            yield from page.items
            if not page.next_cursor:
                return
            cursor = page.next_cursor

    def blob_put(self, key: str, data: bytes | BinaryIO, *, content_type: str = "application/octet-stream") -> None:
        """Store a blob. A binary file object is streamed."""
        if isinstance(data, (bytes, bytearray)):
            body: _Body | bytes = bytes(data)
        else:
            start, size = _seekable_span(data)

            def chunks() -> Iterator[bytes]:
                if start is not None:
                    data.seek(start)
                while True:
                    chunk = data.read(CHUNK)
                    if not chunk:
                        return
                    yield chunk

            body = _Body(chunks, size, start is not None)
        with self.open("PUT", f"/blobs/{_key(key)}", body=body, content_type=content_type):
            pass

    def blob_get(self, key: str) -> bytes | None:
        """Return a blob, or None when there is none."""
        try:
            with self.open("GET", f"/blobs/{_key(key)}", accept="*/*", timeout=self.stream_timeout) as resp:
                return resp.read()
        except HostError as exc:
            if exc.status == 404:
                return None
            raise

    def blob_delete(self, key: str) -> None:
        """Delete a blob."""
        self.call("DELETE", f"/blobs/{_key(key)}")

    def secret(self, name: str) -> str:
        """Return a secret the admin filled in."""
        if not ids.valid_secret_name(name):
            raise ValueError(f"invalid secret name {name!r}")
        data = self.call("GET", f"/secrets/{name}")
        if not isinstance(data, dict) or not isinstance(data.get("value"), str):
            raise HostError(200, "secret response has no value")
        return data["value"]

    # notifications and events ----------------------------------------------

    def notify(self, message: str, *, user_ids: list[str] | None = None, space_id: str | None = None,
               link: str | None = None) -> Any:
        """Notify users, or the members of a space."""
        body: dict[str, Any] = {"message": message}
        if user_ids is not None:
            body["userIds"] = list(user_ids)
        if space_id is not None:
            body["spaceId"] = space_id
        if link is not None:
            body["link"] = link
        return self.call("POST", "/notify", body)

    def emit(self, name: str, data: Any = None, *, space_id: str | None = None) -> Any:
        """Publish the event plugin.<id>.<name>. A name that starts with plugin. is sent as is."""
        event_type = name if name.startswith("plugin.") else f"plugin.{self.plugin_id}.{name}"
        body: dict[str, Any] = {"type": event_type, "data": {} if data is None else data}
        if space_id is not None:
            body["spaceId"] = space_id
        return self.call("POST", "/events", body)


def parse_sse(lines: Iterable[bytes]) -> Iterator[StreamEvent]:
    """Parse a text/event-stream into events. A blank line ends each event."""
    event = ""
    data: list[str] = []
    for raw in lines:
        line = raw.decode("utf-8", "replace").rstrip("\r\n")
        if line == "":
            if data:
                yield StreamEvent(event or "message", _maybe_json("\n".join(data)))
            event, data = "", []
            continue
        if line.startswith(":"):
            continue
        name, _, value = line.partition(":")
        if value.startswith(" "):
            value = value[1:]
        if name == "event":
            event = value
        elif name == "data":
            data.append(value)
    if data:
        yield StreamEvent(event or "message", _maybe_json("\n".join(data)))


def _page(data: Any, what: str) -> Page:
    if isinstance(data, dict) and isinstance(data.get("items"), list):
        return Page(items=data["items"], next_cursor=data.get("nextCursor") or None)
    raise HostError(200, f"{what}: expected a list response {{items, nextCursor}}")


def _maybe_json(text: str) -> Any:
    try:
        return json.loads(text)
    except ValueError:
        return text


def _decode(raw: bytes) -> Any:
    if not raw:
        return None
    try:
        return json.loads(raw)
    except ValueError:
        raise HostError(200, "response is not JSON") from None


def _error_from(status: int, payload: bytes) -> HostError:
    message = payload.decode("utf-8", "replace").strip() or http.client.responses.get(status, "error")
    field_name = None
    try:
        data = json.loads(payload)
    except ValueError:
        data = None
    if isinstance(data, dict):
        message = str(data.get("error") or message)
        field_name = data.get("field") or None
        code = data.get("code") if isinstance(data.get("code"), str) else None
        return HostError(status, message, field_name, code or None)
    return HostError(status, message, field_name)


def _parse_retry_after(value: str) -> float | None:
    value = value.strip()
    if value.isascii() and value.isdigit():
        return float(int(value))
    try:
        when = email.utils.parsedate_to_datetime(value)
    except (TypeError, ValueError):
        return None
    if when.tzinfo is None:
        when = when.replace(tzinfo=timezone.utc)
    return max(0.0, (when - datetime.now(timezone.utc)).total_seconds())


def _seekable_span(f: BinaryIO) -> tuple[int | None, int | None]:
    try:
        if not f.seekable():
            return None, None
        start = f.tell()
        end = f.seek(0, os.SEEK_END)
        f.seek(start)
        return start, end - start
    except (AttributeError, OSError, ValueError):
        return None, None


def _quote_filename(name: str) -> str:
    return name.replace("\\", "\\\\").replace('"', "%22").replace("\r", "%0D").replace("\n", "%0A")


def _time(value: str | datetime) -> str:
    if isinstance(value, datetime):
        if value.tzinfo is None:
            value = value.replace(tzinfo=timezone.utc)
        return value.isoformat().replace("+00:00", "Z")
    return value


def _oid(value: str) -> str:
    if not ids.valid_object_id(value):
        raise ValueError(f"invalid id {value!r}")
    return value


def _key(value: str) -> str:
    if not ids.valid_storage_key(value):
        raise ValueError(f"invalid storage key {value!r}")
    return quote(value, safe="")
