"""The fake host over HTTP, and a job runner that calls the plugin as Markspace would.

HostServer serves the host API routes from docs/plugins.md at /api/plugin/v1 on
top of FakeMarkspace. Lists use the {items, nextCursor} envelope. A POST with
an Idempotency-Key that was seen before gets the stored response. JobRunner
posts signed /jobs/{kind} requests to the plugin, retries failed attempts and
waits for 202 jobs to report through PATCH /jobs/{id}.
"""

from __future__ import annotations

import json
import threading
import time
import urllib.error
import urllib.request
import uuid
from datetime import datetime, timezone
from email.parser import BytesParser
from email.policy import HTTP
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import parse_qs, unquote, urlsplit

from markspace import FileText, HostError, KVEntry, Page, sign_headers

from fakehost import PLUGIN_USER, USER, FakeMarkspace

PREFIX = "/api/plugin/v1"


def free_server(handler: type, ports: range) -> ThreadingHTTPServer:
    for port in ports:
        try:
            return ThreadingHTTPServer(("127.0.0.1", port), handler)
        except OSError:
            continue
    raise OSError(f"no free port in {ports.start}-{ports.stop - 1}")


def _encode(value: Any) -> Any:
    if isinstance(value, Page):
        out = {"items": value.items}
        if value.next_cursor:
            out["nextCursor"] = value.next_cursor
        return out
    if isinstance(value, FileText):
        return {"text": value.text, "truncated": value.truncated}
    if isinstance(value, KVEntry):
        return {"key": value.key, "value": value.value, "updatedAt": value.updated_at}
    return value


class HostServer:
    def __init__(self, fake: FakeMarkspace, token: str, acting_tokens: dict[str, str] | None = None) -> None:
        self.fake = fake
        self.tokens = {token: PLUGIN_USER, **(acting_tokens or {})}
        self.idempotent: dict[str, tuple[int, bytes]] = {}
        self.replayed = 0
        self.posts_without_key = 0
        self.requests: list[tuple[str, str]] = []
        self.lock = threading.Lock()
        self.server: ThreadingHTTPServer | None = None

    @property
    def url(self) -> str:
        host, port = self.server.server_address[:2]
        return f"http://{host}:{port}"

    def start(self, ports: range) -> "HostServer":
        outer = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def _serve(self) -> None:
                length = int(self.headers.get("Content-Length") or 0)
                body = self.rfile.read(length) if length else b""
                status, ctype, payload = outer.handle(self.command, self.path, self.headers, body)
                self.send_response(status)
                if payload:
                    self.send_header("Content-Type", ctype)
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

            do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = _serve

            def log_message(self, *args: Any) -> None:
                pass

        self.server = free_server(Handler, ports)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        return self

    def stop(self) -> None:
        if self.server:
            self.server.shutdown()
            self.server.server_close()

    # dispatch --------------------------------------------------------------

    def handle(self, method: str, target: str, headers: Any, body: bytes) -> tuple[int, str, bytes]:
        parts = urlsplit(target)
        path = parts.path
        with self.lock:
            self.requests.append((method, path))
        auth = headers.get("Authorization") or ""
        actor = self.tokens.get(auth[len("Bearer "):] if auth.startswith("Bearer ") else "")
        if actor is None:
            return self._json(401, {"error": "unknown token"})
        if not path.startswith(PREFIX + "/"):
            return self._json(404, {"error": "not found"})
        key = headers.get("Idempotency-Key") if method == "POST" else None
        if method == "POST":
            if not key:
                self.posts_without_key += 1
            else:
                with self.lock:
                    if key in self.idempotent:
                        self.replayed += 1
                        status, payload = self.idempotent[key]
                        return status, "application/json", payload
        try:
            with self.fake.as_user(actor):
                status, value = self.route(method, path[len(PREFIX):], parse_qs(parts.query), headers, body)
        except HostError as exc:
            status, value = exc.status, {"error": exc.message, **({"field": exc.field} if exc.field else {})}
        if isinstance(value, bytes):
            return status, "application/octet-stream", value
        result = self._json(status, _encode(value)) if value is not None else (status, "application/json", b"")
        if key:
            with self.lock:
                self.idempotent[key] = (result[0], result[2])
        return result

    @staticmethod
    def _json(status: int, value: Any) -> tuple[int, str, bytes]:
        return status, "application/json", json.dumps(value).encode()

    def route(self, method: str, path: str, query: dict, headers: Any, body: bytes) -> tuple[int, Any]:
        f = self.fake
        seg = [unquote(s) for s in path.strip("/").split("/")]
        data = json.loads(body) if body and "json" in (headers.get("Content-Type") or "") else {}
        q = {k: v[0] for k, v in query.items()}
        match (method, seg):
            case ("GET", ["me"]):
                return 200, f.me()
            case ("GET", ["spaces", space, "tree"]):
                return 200, f.tree(space)
            case ("GET", ["items", item]):
                return 200, f.get_item(item)
            case ("GET", ["settings"]):
                return 200, {"settings": f.settings()}
            case ("POST", ["notes"]):
                return 201, f.create_note(data["spaceId"], data["title"], data.get("content", ""),
                                          parent_id=data.get("parentId", ""), tags=data.get("tags"), id=data.get("id"),
                                          icon=data.get("icon"), frontmatter=data.get("frontmatter"),
                                          folder_note=bool(data.get("folderNote")), folder_id=data.get("folderId", ""))
            case ("PATCH", ["notes", note]):
                return 200, f.update_note(note, base_updated_at=data.get("baseUpdatedAt"), title=data.get("title"),
                                          content=data.get("content"), tags=data.get("tags"), icon=data.get("icon"),
                                          frontmatter=data.get("frontmatter"))
            case ("POST", ["folders"]):
                return 201, f.create_folder(data["spaceId"], data["name"], parent_id=data.get("parentId", ""))
            case ("POST", ["files"]):
                fields, name, mime, raw = self.multipart(headers, body)
                return 201, f.upload_file(fields["spaceId"], raw, name, parent_id=fields.get("parentId", ""),
                                          mime=mime, id=fields.get("id"), on_conflict=fields.get("onConflict"))
            case ("GET", ["files", fid, "content"]):
                return 200, f.file_content(fid)
            case ("GET", ["files", fid, "text"]):
                return 200, f.file_text(fid)
            case ("POST", ["items", item, "trash"]):
                return 200, f.trash(item)
            case ("GET", ["notes", note, "links"]):
                return 200, f.outbound_links(note)
            case ("GET", ["items", item, "backlinks"]):
                return 200, f.backlinks(item)
            case ("POST", ["resolve"]):
                return 200, f.resolve(data["spaceId"], data.get("fromNoteId", ""), data["targets"])
            case ("GET", ["search"]):
                return 200, f.search(q.get("q", ""), mode=q.get("mode"), space=q.get("space"),
                                     limit=int(q["limit"]) if q.get("limit") else None, cursor=q.get("cursor"))
            case ("POST", ["suggestions"]):
                return 201, f.create_suggestion(data["noteId"], data["title"], data["edits"],
                                                rationale=data.get("rationale"))
            case ("DELETE", ["suggestions", sid]):
                f.withdraw_suggestion(sid)
                return 204, None
            case ("POST", ["comments"]):
                return 201, f.create_comment(data["itemId"], data["body"], anchor=data.get("anchor"))
            case ("POST", ["ai", "complete"]):
                return 200, f.ai_complete(data)
            case ("GET", ["ai", "models"]):
                return 200, {"items": f.models}
            case ("POST", ["web", "fetch"]):
                return 200, f.web_fetch(data["url"], respect_robots=data.get("respectRobots"))
            case ("POST", ["jobs"]):
                return 201, f.create_job(data["kind"], title=data.get("title", ""), payload=data.get("payload"),
                                         space_id=data.get("spaceId"), run_after=data.get("runAfter"),
                                         parent_id=data.get("parentId"))
            case ("PATCH", ["jobs", jid]):
                kwargs = {k: data[k] for k in ("progress", "total", "message", "status") if k in data}
                if "result" in data:
                    kwargs["result"] = data["result"]
                return 200, f.update_job(jid, **kwargs)
            case ("POST", ["jobs", jid, "log"]):
                f.job_log(jid, data["level"], data["message"])
                return 204, None
            case ("GET", ["kv"]):
                return 200, f.kv_list(q.get("prefix", ""), cursor=q.get("cursor"))
            case ("GET", ["kv", key]):
                entry = f.kv_entry(key)
                if entry is None:
                    raise HostError(404, "key not found")
                return 200, entry
            case ("PUT", ["kv", key]):
                f.kv_put(key, data["value"])
                return 204, None
            case ("DELETE", ["kv", key]):
                f.kv_delete(key)
                return 204, None
            case ("PUT", ["blobs", key]):
                f.blob_put(key, body)
                return 204, None
            case ("GET", ["blobs", key]):
                blob = f.blob_get(key)
                if blob is None:
                    raise HostError(404, "blob not found")
                return 200, blob
            case ("DELETE", ["blobs", key]):
                f.blob_delete(key)
                return 204, None
            case ("GET", ["secrets", name]):
                return 200, {"name": name, "value": f.secret(name)}
            case ("POST", ["notify"]):
                return 200, f.notify(data.get("message", ""))
            case ("POST", ["events"]):
                return 200, f.emit(data.get("type", ""), data.get("data"), space_id=data.get("spaceId"))
        raise HostError(404, f"no route {method} {path}")

    @staticmethod
    def multipart(headers: Any, body: bytes) -> tuple[dict[str, str], str, str, bytes]:
        raw = f"Content-Type: {headers.get('Content-Type')}\r\n\r\n".encode() + body
        message = BytesParser(policy=HTTP).parsebytes(raw)
        fields: dict[str, str] = {}
        name, mime, data = "", "application/octet-stream", b""
        for part in message.iter_parts():
            field = part.get_param("name", header="content-disposition")
            if field == "file":
                name = part.get_filename() or "file"
                mime = part.get_content_type()
                data = part.get_payload(decode=True) or b""
            else:
                fields[field] = (part.get_payload(decode=True) or b"").decode()
        return fields, name, mime, data


class JobRunner:
    """Dispatches queued jobs to the plugin with up to workers in flight, as the Markspace worker pool would."""

    def __init__(self, fake: FakeMarkspace, plugin_url: str, secret: bytes, plugin_id: str, workers: int = 4) -> None:
        self.fake = fake
        self.plugin_url = plugin_url
        self.secret = secret
        self.plugin_id = plugin_id
        self.workers = workers
        self.max_attempts = {}
        self.warp = 0.0
        self.failures: list[tuple[str, str, int, str]] = []
        self.dispatched = 0

    def post(self, path: str, payload: dict) -> tuple[int, Any]:
        body = json.dumps(payload).encode()
        headers = {"Content-Type": "application/json"}
        headers.update(sign_headers(self.secret, self.plugin_id, uuid.uuid4().hex, "POST", path, body, time.time()))
        req = urllib.request.Request(self.plugin_url + path, data=body, method="POST", headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                raw, status = resp.read(), resp.status
        except urllib.error.HTTPError as exc:
            with exc:
                raw, status = exc.read(), exc.code
        return status, (json.loads(raw) if raw else None)

    def _ready(self, job: dict, now: float) -> bool:
        if not job["runAfter"]:
            return True
        at = datetime.fromisoformat(job["runAfter"].replace("Z", "+00:00")).timestamp()
        return at <= now + self.warp

    def _run(self, job_id: str) -> None:
        f = self.fake
        with f.lock:
            job = f.jobs[job_id]
            job["attempts"] += 1
            envelope = {"job": {"id": job["id"], "kind": job["kind"], "payload": job["payload"],
                                "attempt": job["attempts"], "title": job["title"], "spaceId": job["spaceId"]}}
            kind, attempts = job["kind"], job["attempts"]
        status, resp = self.post(f"/jobs/{kind}", envelope)
        if status == 202:
            deadline = time.time() + 120
            while time.time() < deadline:
                with f.lock:
                    if f.jobs[job_id]["status"] in ("done", "failed"):
                        break
                time.sleep(0.02)
            with f.lock:
                final = f.jobs[job_id]["status"]
            if final == "done":
                return
            status, resp = 500, {"error": f.jobs[job_id].get("message") or "job did not finish"}
        with f.lock:
            job = f.jobs[job_id]
            if status == 200:
                job["status"], job["result"] = "done", (resp or {}).get("result")
            elif status == 409:
                job["status"] = "canceled"
            else:
                message = (resp or {}).get("error", "") if isinstance(resp, dict) else str(resp)
                self.failures.append((job_id, kind, status, message))
                job["message"] = message
                job["status"] = "queued" if attempts < self.max_attempts.get(kind, 3) else "failed"

    def run_until_idle(self, timeout: float = 240.0) -> None:
        from wiki.manifest import MANIFEST

        self.max_attempts = {j["kind"]: j.get("maxAttempts", 3) for j in MANIFEST["jobs"]}
        deadline = time.time() + timeout
        threads: dict[str, threading.Thread] = {}
        while True:
            if time.time() > deadline:
                raise AssertionError("jobs did not settle before the timeout")
            for jid in [j for j, t in threads.items() if not t.is_alive()]:
                del threads[jid]
            now = time.time()
            with self.fake.lock:
                queued = [j for j in self.fake.jobs.values() if j["status"] == "queued" and j["id"] not in threads]
                ready = [j for j in queued if self._ready(j, now)]
                for job in ready[: max(0, self.workers - len(threads))]:
                    job["status"] = "running"
                    t = threading.Thread(target=self._run, args=(job["id"],), daemon=True)
                    threads[job["id"]] = t
                    self.dispatched += 1
                    t.start()
            if not queued and not threads:
                return
            if queued and not ready and not threads:
                earliest = min(datetime.fromisoformat(j["runAfter"].replace("Z", "+00:00")).timestamp() for j in queued)
                self.warp = max(self.warp, earliest - now + 0.001)
            time.sleep(0.01)


def utc_iso(ts: float) -> str:
    return datetime.fromtimestamp(ts, timezone.utc).isoformat().replace("+00:00", "Z")
