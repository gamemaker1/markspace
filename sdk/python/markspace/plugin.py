"""The plugin HTTP server and handler registry."""

from __future__ import annotations

import json
import logging
import os
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Callable, Mapping
from urllib.parse import urlsplit

from . import ids, ui
from .context import (ActionContext, CommandContext, Event, EventBatch, ImportContext, JobContext, JobInfo,
                      PanelContext, User)
from .errors import JobCanceled, SignatureError, ValidationError
from .host import Host
from .install import DEFAULT_DATA_DIR, DEFAULT_INSTALL_WINDOW, ENV_DATA, ENV_INSTALL_WINDOW, ENV_TOKEN, InstallStore
from .responses import validate_command_response, validate_install
from .signature import HEADER_PLUGIN, ReplayGuard, verify_request

log = logging.getLogger("markspace")

PROTOCOL = 1
MAX_BODY = 16 << 20
MAX_INSTALL_BODY = 1 << 20
PANEL_WHERE = ("rail", "page")

Response = tuple[int, dict[str, str], bytes]


def validate_manifest(manifest: Any) -> None:
    """Raise ValueError when manifest breaks a rule the SDK depends on."""
    if not isinstance(manifest, dict):
        raise ValueError("manifest must be a JSON object")
    if manifest.get("protocol") != PROTOCOL:
        raise ValueError(f"manifest protocol must be {PROTOCOL}")
    if not ids.valid_plugin_id(manifest.get("id")):
        raise ValueError(f"manifest id {manifest.get('id')!r} does not match the plugin id rule")
    for key in ("name", "version"):
        if not isinstance(manifest.get(key), str) or not manifest[key]:
            raise ValueError(f"manifest {key} must be a non-empty string")
    for key, id_key in (("jobs", "kind"), ("commands", "id"), ("panels", "id"), ("importers", "id")):
        seen = set()
        for entry in manifest.get(key) or []:
            value = entry.get(id_key) if isinstance(entry, dict) else None
            if not ids.valid_local_id(value):
                raise ValueError(f"manifest {key} entry {value!r} does not match the local id rule")
            if value in seen:
                raise ValueError(f"manifest {key} declares {value!r} twice")
            seen.add(value)
            if key == "panels" and entry.get("where") not in PANEL_WHERE:
                raise ValueError(f"manifest panel {value!r} where must be rail or page")
    for entry in manifest.get("secrets") or []:
        name = entry.get("name") if isinstance(entry, dict) else None
        if not ids.valid_secret_name(name):
            raise ValueError(f"manifest secret {name!r} does not match the secret name rule")
    for pattern in manifest.get("events") or []:
        if not ids.valid_event_pattern(pattern):
            raise ValueError(f"manifest event {pattern!r} does not match the event pattern rule")


def _json(status: int, value: Any) -> Response:
    raw = json.dumps(value, separators=(",", ":"), ensure_ascii=False).encode("utf-8")
    return status, {"Content-Type": "application/json"}, raw


def _error(status: int, message: str, code: str | None = None, field: str | None = None) -> Response:
    body: dict[str, Any] = {"error": message}
    if code:
        body["code"] = code
    if field:
        body["field"] = field
    return _json(status, body)


class _JobRun:
    def __init__(self, ctx: JobContext) -> None:
        self.ctx = ctx
        self.lock = threading.Lock()
        self.done = threading.Event()
        self.finished = False
        self.detached = False
        self.result: Any = None
        self.exc: BaseException | None = None


def _install_window(env: Mapping[str, str]) -> float:
    """Return the install window from $PLUGIN_INSTALL_WINDOW, or the default.

    The value is a number of seconds, and 0 means no limit. Anything else fails
    at startup, so a typo does not silently keep the default.
    """
    raw = env.get(ENV_INSTALL_WINDOW, "").strip()
    if not raw:
        return DEFAULT_INSTALL_WINDOW
    try:
        value = float(raw)
    except ValueError:
        value = -1.0
    if value < 0 or value != value or value == float("inf"):
        raise ValueError(f"{ENV_INSTALL_WINDOW} must be a number of seconds, 0 for no limit, got {raw!r}")
    return value


class Plugin:
    """A Markspace plugin: its manifest, its handlers and its HTTP server.

    data_dir defaults to $PLUGIN_DATA, then /data. install_window is the number
    of seconds after startup during which an unsigned first install is
    accepted. It defaults to $PLUGIN_INSTALL_WINDOW, then 600, and 0 accepts an
    unsigned first install at any time. job_wait is the number of seconds a job call waits for the
    handler before it answers 202.
    """

    def __init__(
        self,
        manifest: dict,
        *,
        data_dir: str | None = None,
        install_window: float | None = None,
        job_wait: float = 25.0,
        heartbeat_every: float = 20.0,
        env: Mapping[str, str] | None = None,
        clock: Callable[[], float] = time.time,
        host_options: dict[str, Any] | None = None,
    ) -> None:
        validate_manifest(manifest)
        self.manifest = manifest
        self.id: str = manifest["id"]
        self.env = os.environ if env is None else env
        self.data_dir = data_dir or self.env.get(ENV_DATA) or DEFAULT_DATA_DIR
        self.install_window = install_window if install_window is not None else _install_window(self.env)
        self.job_wait = job_wait
        self.heartbeat_every = heartbeat_every
        self.clock = clock
        self.host_options = dict(host_options or {})
        self.store = InstallStore(self.data_dir, self.env)
        self.credentials = self.store.credentials()
        self.started_at = clock()
        self.guard = ReplayGuard()
        self._events: list[tuple[str, Callable[[EventBatch], Any]]] = []
        self._handlers: dict[str, dict[str, Callable[..., Any]]] = {
            "jobs": {}, "commands": {}, "panels": {}, "actions": {}, "import": {},
        }
        self._running: dict[str, _JobRun] = {}
        self._running_lock = threading.Lock()
        self._install_lock = threading.Lock()

    # registration ----------------------------------------------------------

    def _declared(self, key: str, id_key: str) -> set[str]:
        return {e.get(id_key) for e in self.manifest.get(key) or [] if isinstance(e, dict)}

    def _register(self, route: str, id: str, declared: set[str] | None, what: str) -> Callable:
        if not ids.valid_local_id(id):
            raise ValueError(f"{what} id {id!r} does not match the local id rule")
        if declared is not None and id not in declared:
            raise ValueError(f"{what} {id!r} is not declared in the manifest")

        def deco(fn: Callable[..., Any]) -> Callable[..., Any]:
            if id in self._handlers[route]:
                raise ValueError(f"{what} {id!r} is registered twice")
            self._handlers[route][id] = fn
            return fn

        return deco

    def on(self, pattern: str) -> Callable:
        """Register an event handler for an event type or a pattern such as note.* or *."""
        if not ids.valid_event_pattern(pattern):
            raise ValueError(f"event pattern {pattern!r} does not match the event pattern rule")

        def deco(fn: Callable[[EventBatch], Any]) -> Callable[[EventBatch], Any]:
            self._events.append((pattern, fn))
            return fn

        return deco

    def job(self, kind: str) -> Callable:
        """Register the handler of a job kind declared in the manifest."""
        return self._register("jobs", kind, self._declared("jobs", "kind"), "job")

    def command(self, id: str) -> Callable:
        """Register the handler of a command declared in the manifest."""
        return self._register("commands", id, self._declared("commands", "id"), "command")

    def panel(self, id: str) -> Callable:
        """Register the handler of a panel declared in the manifest."""
        return self._register("panels", id, self._declared("panels", "id"), "panel")

    def action(self, id: str) -> Callable:
        """Register the handler of a UI action."""
        return self._register("actions", id, None, "action")

    def importer(self, id: str) -> Callable:
        """Register the handler of an importer declared in the manifest."""
        return self._register("import", id, self._declared("importers", "id"), "importer")

    # hosts -----------------------------------------------------------------

    def host(self, token: str | None = None) -> Host:
        """Return a host API client for token, or for the plugin token."""
        c = self.credentials
        return Host(c.markspace_url, token or c.token, self.id, **self.host_options)

    # dispatch --------------------------------------------------------------

    def handle(self, method: str, target: str, headers: Mapping[str, str], body: bytes) -> Response:
        """Serve one request. The HTTP server and tests call it."""
        lower = {k.lower(): v for k, v in headers.items()}

        def get(name: str) -> str | None:
            return lower.get(name.lower())

        path = urlsplit(target).path
        if path == "/manifest.json":
            return _json(200, self.manifest) if method == "GET" else _error(405, "method not allowed")
        if path == "/health":
            if method != "GET":
                return _error(405, "method not allowed")
            return _json(200, {"status": "ok", "version": self.manifest["version"]})
        if path == "/install":
            return self._install(get, path, body) if method == "POST" else _error(405, "method not allowed")
        parts = path.strip("/").split("/")
        if parts == ["events"]:
            route, id = "events", ""
        elif len(parts) == 2 and parts[0] in self._handlers:
            route, id = parts
        else:
            return _error(404, "not found")
        if method != "POST":
            return _error(405, "method not allowed")
        if route != "events" and id not in self._handlers[route]:
            return _error(404, f"no handler for /{route}/{id}")
        if len(body) > MAX_BODY:
            return _error(413, "request body too large")
        creds = self.credentials
        if creds.signing_secret is None:
            return _error(503, "plugin is not installed")
        try:
            verify_request(creds.signing_secret, get, method, path, body, self.clock(), self.guard)
        except SignatureError as exc:
            return _error(401, f"signature rejected: {exc.message}", exc.code)
        plugin_header = get(HEADER_PLUGIN)
        if plugin_header and plugin_header != self.id:
            return _error(401, f"request is for plugin {plugin_header}", "wrong_plugin")
        try:
            data = json.loads(body)
        except ValueError:
            return _error(400, "body is not JSON")
        if not isinstance(data, dict):
            return _error(400, "body is not a JSON object")
        delivery = get("X-Markspace-Delivery") or ""
        try:
            if route == "events":
                return self._events_route(data, delivery)
            if route == "jobs":
                return self._job_route(id, data, delivery)
            return self._interaction(route, id, data, delivery)
        except Exception as exc:  # noqa: BLE001 - a handler failure becomes a 500
            log.exception("handler for %s failed", path)
            return _error(500, f"{type(exc).__name__}: {exc}")

    def _install(self, get: Callable[[str], str | None], path: str, body: bytes) -> Response:
        if len(body) > MAX_INSTALL_BODY:
            return _error(413, "request body too large")
        with self._install_lock:
            creds = self.credentials
            if creds.signing_secret is not None:
                try:
                    verify_request(creds.signing_secret, get, "POST", path, body, self.clock(), self.guard)
                except SignatureError as exc:
                    return _error(401, f"re-install needs a signature from the current secret: {exc.message}",
                                  exc.code)
            elif self.env.get(ENV_TOKEN):
                return _error(403, "install is configured from the environment", "install_closed")
            elif self.install_window > 0 and self.clock() - self.started_at > self.install_window:
                return _error(403, "install window closed. Restart the plugin to reopen it", "install_closed")
            try:
                data = json.loads(body)
            except ValueError:
                return _error(400, "body is not JSON")
            try:
                validate_install(data, self.id)
            except ValidationError as exc:
                return _error(400, exc.message, exc.code, exc.path)
            self.store.save(data)
            self.credentials = self.store.credentials()
            log.info("installed for %s", self.credentials.markspace_url)
            return _json(200, {"ok": True})

    def _events_route(self, data: dict, delivery: str) -> Response:
        raw = data.get("events")
        if not isinstance(raw, list) or not all(isinstance(e, dict) for e in raw):
            return _error(400, "events must be an array of objects")
        events = [Event.from_json(e) for e in raw]
        host = self.host()
        for pattern, fn in self._events:
            matched = [e for e in events if ids.match_event(pattern, e.type)]
            if matched:
                fn(EventBatch(events=matched, pattern=pattern, delivery_id=delivery, host=host))
        return 204, {}, b""

    def _job_route(self, kind: str, data: dict, delivery: str) -> Response:
        job = data.get("job")
        if not isinstance(job, dict) or not ids.valid_object_id(job.get("id")):
            return _error(400, "job.id is missing or invalid")
        attempt = job.get("attempt")
        info = JobInfo(
            id=job["id"], kind=kind, payload=job.get("payload"),
            attempt=attempt if isinstance(attempt, int) and not isinstance(attempt, bool) else 0,
            title=job["title"] if isinstance(job.get("title"), str) else "",
            space_id=job["spaceId"] if isinstance(job.get("spaceId"), str) else "",
        )
        acting = data.get("actingToken")
        token = acting if isinstance(acting, str) and acting else None
        user = User.from_json(data["user"]) if isinstance(data.get("user"), dict) else None
        ctx = JobContext(job=info, host=self.host(token), delivery_id=delivery, user=user)
        fn = self._handlers["jobs"][kind]
        with self._running_lock:
            if info.id in self._running:
                return _json(202, {"status": "running"})
            run = _JobRun(ctx)
            self._running[info.id] = run
        threading.Thread(target=self._work, args=(run, fn), name=f"job-{info.id}", daemon=True).start()
        run.done.wait(self.job_wait)
        with run.lock:
            if not run.finished:
                run.detached = True
        if run.detached:
            threading.Thread(target=self._heartbeat, args=(run,), name=f"heartbeat-{info.id}", daemon=True).start()
            return _json(202, {"status": "running"})
        if isinstance(run.exc, JobCanceled):
            return _error(409, "job canceled")
        if run.exc is not None:
            return _error(500, f"{type(run.exc).__name__}: {run.exc}")
        try:
            return _json(200, {"result": {} if run.result is None else run.result})
        except (TypeError, ValueError) as exc:
            return _error(500, f"job result is not JSON: {exc}")

    def _work(self, run: _JobRun, fn: Callable[[JobContext], Any]) -> None:
        result, error = None, None
        try:
            result = fn(run.ctx)
        except BaseException as exc:  # noqa: BLE001 - reported to Markspace
            error = exc
            if not isinstance(exc, JobCanceled):
                log.exception("job %s failed", run.ctx.job.id)
        with run.lock:
            run.result, run.exc, run.finished = result, error, True
            detached = run.detached
        run.done.set()
        try:
            if detached:
                self._report(run)
        finally:
            with self._running_lock:
                self._running.pop(run.ctx.job.id, None)

    def _report(self, run: _JobRun) -> None:
        job_id = run.ctx.job.id
        try:
            if isinstance(run.exc, JobCanceled):
                return
            if run.exc is not None:
                run.ctx.host.update_job(job_id, status="failed", message=f"{type(run.exc).__name__}: {run.exc}")
                return
            result = {} if run.result is None else run.result
            try:
                json.dumps(result)
            except (TypeError, ValueError) as exc:
                run.ctx.host.update_job(job_id, status="failed", message=f"job result is not JSON: {exc}")
                return
            run.ctx.host.update_job(job_id, status="done", result=result)
        except Exception:  # noqa: BLE001 - nothing else can take the report
            log.exception("reporting job %s failed", job_id)

    def _heartbeat(self, run: _JobRun) -> None:
        while not run.done.wait(self.heartbeat_every):
            done, total, at = run.ctx.last_progress()
            if time.monotonic() - at < self.heartbeat_every:
                continue
            try:
                run.ctx.progress(done, total)
            except Exception as exc:  # noqa: BLE001 - the next beat tries again
                log.warning("heartbeat for job %s failed: %s", run.ctx.job.id, exc)

    def _interaction(self, route: str, id: str, data: dict, delivery: str) -> Response:
        token = data.get("actingToken")
        if not isinstance(token, str) or not token:
            return _error(400, "actingToken is required")
        user = User.from_json(data.get("user"))
        host = self.host(token)
        fn = self._handlers[route][id]
        if route == "import":
            file_id, space_id = data.get("fileId"), data.get("spaceId")
            if not isinstance(file_id, str) or not isinstance(space_id, str):
                return _error(400, "fileId and spaceId are required")
            resp = fn(ImportContext(id=id, file_id=file_id, space_id=space_id, user=user, host=host,
                                    delivery_id=delivery))
            return self._command_response(resp, route, id)
        context = data.get("context") if isinstance(data.get("context"), dict) else {}
        if route == "panels":
            resp = fn(PanelContext(id=id, context=context, user=user, host=host, delivery_id=delivery))
            tree = resp["ui"] if isinstance(resp, dict) and set(resp) == {"ui"} else resp
            try:
                ui.validate(tree)
            except ValidationError as exc:
                log.error("panel %s returned an invalid UI tree: %s", id, exc)
                return _error(500, f"invalid UI tree: {exc.message}", exc.code, exc.path)
            return _json(200, {"ui": tree})
        if route == "actions":
            values = data.get("values") if isinstance(data.get("values"), dict) else {}
            payload = data.get("payload") if isinstance(data.get("payload"), dict) else {}
            resp = fn(ActionContext(id=id, context=context, user=user, host=host, delivery_id=delivery,
                                    values=values, payload=payload))
        else:
            resp = fn(CommandContext(id=id, context=context, user=user, host=host, delivery_id=delivery))
        return self._command_response(resp, route, id)

    def _command_response(self, resp: Any, route: str, id: str) -> Response:
        try:
            validate_command_response(resp)
        except ValidationError as exc:
            log.error("%s %s returned an invalid response: %s", route, id, exc)
            return _error(500, f"invalid command response: {exc.message}", exc.code, exc.path)
        return _json(200, resp)

    # serving ---------------------------------------------------------------

    def make_server(self, host: str = "0.0.0.0", port: int = 8090) -> ThreadingHTTPServer:
        """Return a threaded HTTP server bound to host and port. The caller runs serve_forever."""
        plugin = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"
            server_version = "markspace-plugin-python/0.1"

            def _dispatch(self) -> None:
                length_header = self.headers.get("Content-Length")
                if length_header is None:
                    if self.headers.get("Transfer-Encoding"):
                        self._send((411, {"Content-Type": "application/json"}, b'{"error":"length required"}'),
                                   close=True)
                        return
                    length = 0
                else:
                    try:
                        length = int(length_header)
                    except ValueError:
                        self._send(_error(400, "bad Content-Length"), close=True)
                        return
                if length < 0 or length > MAX_BODY:
                    self._send(_error(413, "request body too large"), close=True)
                    return
                body = self.rfile.read(length) if length else b""
                # http.server decodes the request line as Latin-1. Recover the raw bytes so the
                # signed path matches what the client sent.
                target = self.path.encode("iso-8859-1").decode("utf-8", "surrogateescape")
                self._send(plugin.handle(self.command, target, dict(self.headers.items()), body))

            def _send(self, response: Response, close: bool = False) -> None:
                status, headers, payload = response
                self.send_response(status)
                for k, v in headers.items():
                    self.send_header(k, v)
                self.send_header("Content-Length", str(len(payload)))
                if close:
                    self.send_header("Connection", "close")
                    self.close_connection = True
                self.end_headers()
                if payload and self.command != "HEAD":
                    self.wfile.write(payload)

            do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = _dispatch

            def log_message(self, format: str, *args: Any) -> None:  # noqa: A002
                log.debug("%s %s", self.address_string(), format % args)

        server = ThreadingHTTPServer((host, port), Handler)
        server.daemon_threads = True
        return server

    def serve(self, host: str = "0.0.0.0", port: int = 8090) -> None:
        """Serve until interrupted."""
        server = self.make_server(host, port)
        log.info("plugin %s %s listening on %s:%d", self.id, self.manifest["version"], host, port)
        if not self.credentials.installed:
            if self.env.get(ENV_TOKEN):
                log.warning("MARKSPACE_TOKEN is set but the URL or signing secret is missing")
            else:
                if self.install_window > 0:
                    log.info("waiting for POST /install for %.0f s", self.install_window)
                else:
                    log.info("waiting for POST /install")
        try:
            server.serve_forever()
        except KeyboardInterrupt:
            pass
        finally:
            server.server_close()
