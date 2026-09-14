# Markspace Python SDK

> A Python SDK for Markspace plugins that uses only the standard library.

A Markspace plugin is an HTTP service, usually in its own Podman quadlet
container. Markspace calls the plugin with signed JSON requests, and the plugin
calls back through the host API. `docs/plugins.md` defines the protocol. The
SDK needs Python 3.12 or later.

## Usage

```python
from markspace import Plugin, toast, ui

plugin = Plugin({
    "protocol": 1, "id": "hello", "name": "Hello", "version": "0.1.0",
    "permissions": ["items:read", "ui"],
    "commands": [{"id": "greet", "title": "Say hello", "contexts": ["note"]}],
    "panels": [{"id": "info", "title": "Hello", "icon": "hand", "where": "rail", "contexts": ["note"]}],
})

@plugin.command("greet")
def greet(ctx):
    note = ctx.host.get_item(ctx.item_id)["note"]
    return toast(f"Hello from {note['title']}, {ctx.user.name}")

@plugin.panel("info")
def info(ctx):
    return ui.section("Hello", ui.text(f"Item {ctx.item_id}"))

plugin.serve(port=8090)
```

`sdk/example/example.py` is a complete plugin with the id `word-count`, with
`sdk/example/containerfile` and the quadlet unit `sdk/example/example.container`.
Build its image from the repository root:

```
podman build -f sdk/example/containerfile -t markspace-example .
```

## Handlers

Each decorator registers one handler. A job, command, panel or importer id must
appear in the manifest, or registration raises `ValueError`. Actions do not
appear in the manifest.

| Decorator | Route | Handler receives | Handler returns |
| --- | --- | --- | --- |
| `@plugin.on(pattern)` | `POST /events` | `EventBatch` | nothing |
| `@plugin.job(kind)` | `POST /jobs/{kind}` | `JobContext` | a JSON result |
| `@plugin.command(id)` | `POST /commands/{id}` | `CommandContext` | a command response |
| `@plugin.panel(id)` | `POST /panels/{id}` | `PanelContext` | a UI tree, or `{"ui": tree}` |
| `@plugin.action(id)` | `POST /actions/{id}` | `ActionContext` | a command response |
| `@plugin.importer(id)` | `POST /import/{id}` | `ImportContext` | a command response |

The SDK serves `GET /manifest.json` and `GET /health` without a handler.

An event pattern is an event type or a pattern with `*`, such as `note.*`. A
`*` matches any run of characters, dots included. The SDK calls each handler
once per delivery with the matching events, in delivery order. If a handler
raises, the SDK answers 500 and Markspace delivers the batch again later, so
event handlers must be idempotent. `Event.id` identifies an event across
deliveries.

`CommandContext`, `PanelContext` and `ActionContext` carry `context` (the dict
Markspace sent), `user`, and `host`, a host client bound to the acting token of
the interaction. The properties `space_id`, `item_id` and `kind` read the
context. `ActionContext` adds `values`, the form values, and `payload`, the
action payload. `ImportContext` carries `file_id`, `space_id`, `user` and
`host`.

The SDK validates every command, action and import response and every panel
tree before it sends them. An invalid response becomes a 500 with the error
code and path, and the SDK logs it.

## Install and Credentials

`POST /install` delivers the plugin token, the signing secret and the grants.
The SDK validates the payload and writes it to `$PLUGIN_DATA/install.json`,
default `/data/install.json`. It writes a temporary file with mode 0600, syncs
it, and renames it over the old file. A successful install answers 200 with
`{"ok": true}`.

`/install` accepts an unsigned request only when all of these hold:

- no signing secret is known yet, from `install.json` or from the environment,
- `MARKSPACE_TOKEN` is not set in the environment,
- the plugin started less than `install_window` seconds ago, 600 by default.

When a signing secret is known, `/install` accepts only a request signed with
that secret. Markspace uses this to rotate the token or the secret.

The environment variables `MARKSPACE_URL`, `MARKSPACE_TOKEN` and
`MARKSPACE_SIGNING_SECRET` override the stored values one by one. Use them when
the install call cannot reach the plugin. `MARKSPACE_SIGNING_SECRET` holds the
base64 text from the admin page.

`Plugin(manifest, data_dir=..., install_window=..., job_wait=..., env=...,
clock=...)` sets these values. Tests pass `env` and `clock`.

`sign`, `verify`, `ReplayGuard` and `sign_headers` are public, so tests and
fake hosts can sign requests the same way Markspace does. A failed signature
check answers 401 with one of the codes listed in `docs/plugins.md`.

## Jobs

The SDK starts every job handler in a worker thread and waits up to `job_wait`
seconds, 25 by default. Markspace times out a job call after 30 s.

- If the handler returns in time, the SDK answers 200 with `{"result": ...}`.
- If it raises, the SDK answers 500, and Markspace counts a failed attempt.
- If it raises `JobCanceled`, the SDK answers 409.
- If it is still running, the SDK answers 202 with a running status. When the
  handler finishes, the SDK sends `PATCH /api/plugin/v1/jobs/{id}` with the
  status `done` and the result, or the status `failed` and a message. While
  the handler runs, the SDK repeats the last progress report every 20 s if the
  handler sent none, so the job lease stays valid.

A second call for a job id that is still running answers 202 and starts
nothing.

| `JobContext` member | Effect |
| --- | --- |
| `job`, `payload`, `attempt` | the job id, kind, title, space id, payload and attempt number |
| `user` | the user who started the job, or None |
| `host` | a host client bound to the acting token when a user started the job, else the plugin token |
| `progress(done, total, message)` | sends `PATCH /jobs/{id}` and renews the lease |
| `log(level, message)` | appends to the job log. Levels are debug, info, warn and error |
| `canceled`, `check_canceled()` | report or raise `JobCanceled` after a progress response with status `canceled` |

## Host Client

`Host(markspace_url, token, plugin_id)` calls `/api/plugin/v1`. The plugin
object builds one with `plugin.host()`, and every context carries one.
`host.with_token(token)` returns a copy that sends another token.

A call that gets 429, 502, 503 or 504, or no response, runs again, up to 5
tries in total. A 500 never runs again. The delay before retry n is
`min(30, 0.5 * 2^(n-1))` seconds times a random factor from 0.5 to 1. A
`Retry-After` header replaces the delay, up to 120 s. Other failures raise
`HostError(status, message, field)`. `status` is 0 when no response arrived.

Every POST carries a fresh `Idempotency-Key` header, and each retry of that
POST sends the same key. The SDK sends an upload from a file object that cannot
seek only once, because it cannot rewind the file.

| Group | Methods |
| --- | --- |
| identity | `me()` |
| items | `tree`, `get_item`, `create_note`, `update_note(base_updated_at=...)`, `create_folder`, `upload_file`, `file_content`, `open_file_content`, `file_text`, `move`, `copy`, `trash` |
| links and search | `outbound_links`, `backlinks`, `resolve`, `search` |
| suggestions and comments | `create_suggestion`, `withdraw_suggestion`, `create_comment` |
| AI and web | `ai_complete`, `ai_complete_stream`, `ai_embed`, `ai_image`, `ai_models`, `web_fetch` |
| jobs | `create_job`, `update_job`, `job_log` |
| storage | `kv_get`, `kv_entry`, `kv_put`, `kv_delete`, `kv_list`, `kv_keys`, `blob_put`, `blob_get`, `blob_delete`, `secret` |
| notifications and events | `notify`, `emit` |

The list routes `tree`, `outbound_links`, `backlinks`, `resolve`, `search` and
`kv_list` return `Page(items, next_cursor)`. `kv_keys` follows the cursor
through every page. `kv_entry` returns `KVEntry(key, value, updated_at)`,
`file_text` returns `FileText(text, truncated)`, and `ai_models` returns
`AIModel` values. `update_job` returns the job, and a job with status
`canceled` tells the handler to stop. `upload_file` streams multipart form data
from a binary file object. `ai_complete_stream` yields `StreamEvent(event,
data)` for each `delta`, `done` and `error` frame. `kv_get` and `blob_get`
return a default value on 404. `emit("counted", data)` publishes
`plugin.<id>.counted`. The client checks ids and keys against the identifier
rules in `docs/plugins.md` before it sends a request, and raises `ValueError`
for an invalid one.

## UI Builders

`markspace.ui` builds the declarative UI nodes from `docs/plugins.md` as plain
dicts. It leaves out optional arguments that are `None`.

```python
from markspace import ui

tree = ui.stack(
    ui.section("Counts", ui.table([ui.column("metric", "Metric"), ui.column("value", "Value", numeric=True)],
                                  [{"metric": "Words", "value": 412}])),
    ui.button("Count this space", ui.action("count-space", {"spaceId": space_id}), variant="primary"),
    gap="m",
)
ui.validate(tree)
```

The builders are `stack`, `section`, `form`, `field`, `option`, `action`,
`list`, `item`, `target`, `table`, `column`, `text`, `markdown`, `code`,
`progress`, `badge`, `button`, `link` and `empty`.

`ui.validate(tree)` raises `ValidationError(code, path, message)` for the first
problem it finds, such as `unknown_type at $.children[2]`. It enforces the
limits of 2000 nodes, 1 MiB of JSON and a depth of 32.

| Helper | Response |
| --- | --- |
| `toast(message, kind="info")` | `{"toast": {"message", "kind"}}`. kind is info, success, warn or error |
| `open_panel(panel_id, item_id=None)` | `{"openPanel": {"id", "itemId"}}`. panel_id is qualified, such as `word-count.counts` |
| `navigate(path)` | `{"navigate": path}`. path starts with `/` |
| `job(job_id)` | `{"job": {"id"}}` |
| `ui_response(tree)` | `{"ui": tree}` |

`validate_command_response(resp)` applies the same rules as the server.

## Development

Run the tests from the repository root:

```
python3 -m unittest discover -s sdk/python/test
```

`sdk/python/test/test_vectors.py` runs every case in
`test/vectors/protocol.json`, and `sdk/vectors_test.go` runs the same file for
the Go SDK.

## License

ISC
