"""Shared test helpers: import paths, vectors and a fake Markspace host."""

from __future__ import annotations

import base64
import json
import logging
import os
import sys
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, unquote, urlsplit

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "sdk" / "python"))
sys.path.insert(0, str(ROOT / "sdk" / "example"))

VECTORS_PATH = ROOT / "test" / "vectors" / "protocol.json"
SECRET = bytes(range(32))
SECRET_B64 = base64.b64encode(SECRET).decode()
TOKEN = "mspl_" + "t" * 40
UPDATED = "2026-09-13T12:00:00Z"


def vectors() -> dict:
    with open(VECTORS_PATH, encoding="utf-8") as f:
        return json.load(f)


def tempdir() -> str:
    return tempfile.mkdtemp(prefix="msplugin-")


def install_payload(plugin_id: str, url: str, secret_b64: str = SECRET_B64, token: str = TOKEN) -> dict:
    return {"pluginId": plugin_id, "markspaceUrl": url, "token": token, "signingSecret": secret_b64,
            "grants": [{"spaceId": "space1", "role": "editor"}], "permissions": ["items:read"]}


class FakeHost:
    """An in-memory stand-in for the Markspace host API.

    faults maps "METHOD /path" to a list of (status, headers, body) replies
    that are served, one per request, before normal handling resumes.
    """

    KV_PAGE = 2

    def __init__(self) -> None:
        self.requests: list[dict] = []
        self.notes: dict[str, dict] = {}
        self.trees: dict[str, list] = {}
        self.kv: dict[str, object] = {}
        self.secrets: dict[str, str] = {}
        self.file_texts: dict[str, str] = {}
        self.models: list[dict] = []
        self.job_status: dict[str, str] = {}
        self.faults: dict[str, list[tuple[int, dict, bytes]]] = {}
        self.uploads: list[dict] = []
        self.sse: bytes = b""
        self.next_job = 0
        self.lock = threading.Lock()
        self.server: ThreadingHTTPServer | None = None

    @property
    def url(self) -> str:
        assert self.server is not None
        host, port = self.server.server_address[:2]
        return f"http://{host}:{port}"

    def start(self) -> "FakeHost":
        fake = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def handle_one(self) -> None:
                length = int(self.headers.get("Content-Length") or 0)
                if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
                    body = self._read_chunked()
                else:
                    body = self.rfile.read(length) if length else b""
                status, headers, payload = fake.respond(self.command, self.path, dict(self.headers.items()), body)
                self.send_response(status)
                for k, v in headers.items():
                    self.send_header(k, v)
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

            def _read_chunked(self) -> bytes:
                out = b""
                while True:
                    size = int(self.rfile.readline().strip().split(b";")[0], 16)
                    if size == 0:
                        self.rfile.readline()
                        return out
                    out += self.rfile.read(size)
                    self.rfile.readline()

            do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = handle_one

            def log_message(self, *args) -> None:
                pass

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = True
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        return self

    def stop(self) -> None:
        if self.server:
            self.server.shutdown()
            self.server.server_close()

    def calls(self, method: str | None = None, prefix: str = "") -> list[dict]:
        with self.lock:
            return [r for r in self.requests if (method is None or r["method"] == method) and r["path"].startswith(prefix)]

    def respond(self, method: str, target: str, headers: dict, body: bytes) -> tuple[int, dict, bytes]:
        split = urlsplit(target)
        path, query = split.path, parse_qs(split.query)
        record = {"method": method, "path": path, "query": split.query, "headers": headers, "body": body}
        try:
            record["json"] = json.loads(body) if body and "json" in headers.get("Content-Type", "") else None
        except ValueError:
            record["json"] = None
        with self.lock:
            self.requests.append(record)
            queue = self.faults.get(f"{method} {path}")
            if queue:
                return queue.pop(0)
        prefix = "/api/plugin/v1"
        if not path.startswith(prefix):
            return self._json(404, {"error": "not found"})
        route = path[len(prefix):]
        parts = route.strip("/").split("/")
        if route == "/me":
            return self._json(200, {"pluginId": "word-count", "grants": [], "permissions": []})
        if parts[0] == "items" and len(parts) == 2 and method == "GET":
            note = self.notes.get(parts[1])
            if note is None:
                return self._json(404, {"error": "item not found"})
            return self._json(200, {"kind": "note", "note": note})
        if parts[0] == "spaces" and len(parts) == 3 and parts[2] == "tree":
            return self._json(200, {"items": self.trees.get(parts[1], [])})
        if route == "/jobs" and method == "POST":
            with self.lock:
                self.next_job += 1
                job_id = f"job{self.next_job}"
            return self._json(200, {"id": job_id, "kind": record["json"]["kind"], "status": "queued"})
        if parts[0] == "jobs" and len(parts) == 2 and method == "PATCH":
            return self._json(200, {"id": parts[1], "status": self.job_status.get(parts[1], "running")})
        if parts[0] == "jobs" and len(parts) == 3 and parts[2] == "log":
            return 204, {}, b""
        if route == "/kv" and method == "GET":
            prefix_q = query.get("prefix", [""])[0]
            start = int(query.get("cursor", ["0"])[0])
            keys = sorted(k for k in self.kv if k.startswith(prefix_q))
            chunk = keys[start:start + self.KV_PAGE]
            page = {"items": [{"key": k, "size": len(json.dumps(self.kv[k])), "updatedAt": UPDATED} for k in chunk]}
            if start + self.KV_PAGE < len(keys):
                page["nextCursor"] = str(start + self.KV_PAGE)
            return self._json(200, page)
        if parts[0] == "kv" and len(parts) == 2:
            key = unquote(parts[1])
            if method == "PUT":
                data = json.loads(body)
                if not isinstance(data, dict) or "value" not in data:
                    return self._json(400, {"error": "value is required", "field": "value"})
                self.kv[key] = data["value"]
                return 204, {}, b""
            if method == "GET":
                if key not in self.kv:
                    return self._json(404, {"error": "no such key"})
                return self._json(200, {"key": key, "value": self.kv[key], "updatedAt": UPDATED})
            if method == "DELETE":
                self.kv.pop(key, None)
                return 204, {}, b""
        if parts[0] == "secrets" and len(parts) == 2:
            if parts[1] not in self.secrets:
                return self._json(404, {"error": "no such secret"})
            return self._json(200, {"name": parts[1], "value": self.secrets[parts[1]]})
        if parts[0] == "files" and len(parts) == 3 and parts[2] == "text":
            return self._json(200, {"text": self.file_texts.get(parts[1], ""), "truncated": False})
        if route == "/ai/models":
            return self._json(200, {"items": self.models})
        if route == "/files" and method == "POST":
            upload = parse_multipart(headers.get("Content-Type", ""), body)
            self.uploads.append(upload)
            return self._json(200, {"id": "file1", "name": upload["file"]["filename"]})
        if route == "/ai/complete" and method == "POST":
            return 200, {"Content-Type": "text/event-stream"}, self.sse
        if route == "/notes" and method == "POST":
            return self._json(400, {"error": "title is required", "field": "title"})
        return self._json(404, {"error": "not found"})

    @staticmethod
    def _json(status: int, value: object) -> tuple[int, dict, bytes]:
        return status, {"Content-Type": "application/json"}, json.dumps(value).encode()


def parse_multipart(content_type: str, body: bytes) -> dict:
    """Parse a multipart/form-data body into {name: value} with the file as a dict."""
    boundary = content_type.split("boundary=", 1)[1].encode()
    out: dict = {}
    for part in body.split(b"--" + boundary):
        if part in (b"", b"--\r\n", b"\r\n") or part.startswith(b"--"):
            continue
        head, _, content = part.lstrip(b"\r\n").partition(b"\r\n\r\n")
        content = content[:-2] if content.endswith(b"\r\n") else content
        disp = {}
        ctype = None
        for line in head.decode().split("\r\n"):
            name, _, value = line.partition(":")
            if name.lower() == "content-disposition":
                for item in value.split(";")[1:]:
                    k, _, v = item.strip().partition("=")
                    disp[k] = v.strip('"')
            elif name.lower() == "content-type":
                ctype = value.strip()
        if "filename" in disp:
            out[disp["name"]] = {"filename": disp["filename"], "content": content, "type": ctype}
        else:
            out[disp["name"]] = content.decode()
    return out


if not os.environ.get("MSPLUGIN_TEST_LOG"):
    _log = logging.getLogger("markspace")
    _log.addHandler(logging.NullHandler())
    _log.propagate = False
