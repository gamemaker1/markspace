"""Builders and validation for the declarative UI trees in docs/plugins.md.

Builders return plain dicts and leave out arguments that are None. validate()
applies the rules in test/vectors/protocol.json and raises ValidationError
with the first problem it finds. The plugin server validates every tree before
it sends one, so a bad tree fails in the plugin, not in the browser.
"""

from __future__ import annotations

import json
import math
from typing import Any

from . import ids
from .errors import ValidationError

_List = list
_Dict = dict

MAX_NODES = 2000
MAX_BYTES = 1 << 20
MAX_DEPTH = 32

NODE_TYPES = ("stack", "section", "form", "list", "table", "text", "markdown", "progress", "badge", "button",
              "link", "empty", "code")
FIELD_TYPES = ("text", "textarea", "number", "url", "select", "checkbox", "file", "note", "folder", "space")
GAPS = ("s", "m", "l")
TONES = ("neutral", "good", "warn", "bad", "accent")
VARIANTS = ("primary", "secondary", "danger")

_KEYS = {
    "stack": ("children", "gap"),
    "section": ("title", "description", "count", "children"),
    "form": ("id", "title", "description", "fields", "submit", "action", "more"),
    "field": ("type", "name", "label", "hint", "value", "required", "options"),
    "option": ("value", "label"),
    "list": ("items", "empty"),
    "listItem": ("title", "subtitle", "icon", "link", "badge", "actions"),
    "listItemBadge": ("type", "label", "tone"),
    "table": ("columns", "rows", "rowActions"),
    "column": ("key", "label", "numeric"),
    "text": ("value",),
    "markdown": ("value",),
    "progress": ("value", "max", "label", "jobId"),
    "badge": ("label", "tone"),
    "button": ("label", "variant", "action", "confirm"),
    "link": ("label", "to"),
    "linkTarget": ("itemId", "url"),
    "empty": ("title", "body", "action"),
    "code": ("value", "language"),
    "action": ("id", "payload"),
}


def _clean(d: dict[str, Any]) -> dict[str, Any]:
    return {k: v for k, v in d.items() if v is not None}


# Builders ------------------------------------------------------------------

def stack(*children: dict, gap: str | None = None) -> dict:
    """A vertical stack of nodes. gap is s, m or l."""
    return _clean({"type": "stack", "children": _List(children), "gap": gap})


def section(title: str, *children: dict, description: str | None = None, count: int | None = None) -> dict:
    """A titled group of nodes."""
    return _clean({"type": "section", "title": title, "description": description, "count": count,
                   "children": _List(children) if children else None})


def action(id: str, payload: dict | None = None) -> dict:
    """An action posted back to the plugin's /actions/{id} route."""
    return _clean({"id": id, "payload": payload})


def option(value: str, label: str) -> dict:
    """One choice of a select field."""
    return {"value": value, "label": label}


def field(type: str, name: str, *, label: str | None = None, hint: str | None = None, value: Any = None,
          required: bool | None = None, options: _List[dict] | None = None) -> dict:
    """A form field. type is one of FIELD_TYPES."""
    return _clean({"type": type, "name": name, "label": label, "hint": hint, "value": value,
                   "required": required, "options": options})


def form(id: str, fields: _List[dict], action: dict, *, title: str | None = None, description: str | None = None,
         submit: str | None = None, more: _List[dict] | None = None) -> dict:
    """A form. Submitting it posts the values to action."""
    return _clean({"type": "form", "id": id, "title": title, "description": description, "fields": _List(fields),
                   "submit": submit, "action": action, "more": more})


def target(*, item_id: str | None = None, url: str | None = None) -> dict:
    """A link target, either an item id or an http or https URL."""
    return _clean({"itemId": item_id, "url": url})


def item(title: str, *, subtitle: str | None = None, icon: str | None = None, link: dict | None = None,
         badge: dict | None = None, actions: _List[dict] | None = None) -> dict:
    """One row of a list. link is a target(), badge is {label, tone} or a badge(), actions are button() nodes."""
    return _clean({"title": title, "subtitle": subtitle, "icon": icon, "link": link, "badge": badge,
                   "actions": actions})


def list(items: _List[dict], *, empty: str | None = None) -> dict:  # noqa: A001 - mirrors the node name
    """A list of item() rows. empty is the text shown when there are no rows."""
    return _clean({"type": "list", "items": _List(items), "empty": empty})


def column(key: str, label: str, *, numeric: bool | None = None) -> dict:
    """A table column."""
    return _clean({"key": key, "label": label, "numeric": numeric})


def table(columns: _List[dict], rows: _List[dict], *, row_actions: _List[dict] | None = None) -> dict:
    """A table. Row values are strings, numbers, booleans or None."""
    return _clean({"type": "table", "columns": _List(columns), "rows": _List(rows), "rowActions": row_actions})


def text(value: str) -> dict:
    """Plain text."""
    return {"type": "text", "value": value}


def markdown(value: str) -> dict:
    """Markdown rendered by the note renderer with raw HTML escaped."""
    return {"type": "markdown", "value": value}


def code(value: str, language: str | None = None) -> dict:
    """A code block."""
    return _clean({"type": "code", "value": value, "language": language})


def progress(value: float, *, max: float | None = None, label: str | None = None,  # noqa: A002
             job_id: str | None = None) -> dict:
    """A progress bar. With job_id the client follows job.updated events."""
    return _clean({"type": "progress", "value": value, "max": max, "label": label, "jobId": job_id})


def badge(label: str, tone: str | None = None) -> dict:
    """A short label. tone is one of TONES."""
    return _clean({"type": "badge", "label": label, "tone": tone})


def button(label: str, action: dict, *, variant: str | None = None, confirm: str | None = None) -> dict:
    """A button that posts action. confirm is the text of a confirmation prompt."""
    return _clean({"type": "button", "label": label, "variant": variant, "action": action, "confirm": confirm})


def link(label: str, *, item_id: str | None = None, url: str | None = None) -> dict:
    """A link to an item or to an http or https URL."""
    return {"type": "link", "label": label, "to": target(item_id=item_id, url=url)}


def empty(title: str, *, body: str | None = None, action: dict | None = None) -> dict:
    """An empty state. action is a button() node."""
    return _clean({"type": "empty", "title": title, "body": body, "action": action})


# Validation ----------------------------------------------------------------

def encoded_size(tree: Any) -> int:
    """Return the size of the compact UTF-8 JSON encoding of tree."""
    return len(json.dumps(tree, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode("utf-8"))


def validate(tree: Any, root: str = "$") -> None:
    """Raise ValidationError when tree breaks a UI rule. root prefixes error paths."""
    try:
        size = encoded_size(tree)
    except (TypeError, ValueError, UnicodeEncodeError) as exc:
        raise ValidationError("invalid_field", root, f"not encodable as JSON: {exc}") from None
    if size > MAX_BYTES:
        raise ValidationError("too_large", root, f"{size} bytes exceeds {MAX_BYTES}")
    check_limits(tree, root)
    _Walker().node(tree, root)


def check_limits(tree: Any, root: str = "$") -> None:
    """Raise ValidationError when tree holds more than MAX_NODES objects or nests them deeper than MAX_DEPTH.

    Every JSON object counts. The walk is pre-order with keys in ascending order.
    """
    count = 0
    stack: _List[tuple[Any, str, int]] = [(tree, root, 0)]
    while stack:
        value, path, level = stack.pop()
        if isinstance(value, _Dict):
            level += 1
            if level > MAX_DEPTH:
                raise ValidationError("too_deep", path, f"objects nest deeper than {MAX_DEPTH}")
            count += 1
            if count > MAX_NODES:
                raise ValidationError("too_many_nodes", path, f"more than {MAX_NODES} objects")
            for key in sorted(value, reverse=True):
                stack.append((value[key], f"{path}.{key}", level))
        elif isinstance(value, _List):
            for i in range(len(value) - 1, -1, -1):
                stack.append((value[i], f"{path}[{i}]", level))


def _is_number(v: Any) -> bool:
    return isinstance(v, (int, float)) and not isinstance(v, bool) and math.isfinite(v)


def _is_scalar(v: Any) -> bool:
    return v is None or isinstance(v, (str, bool)) or _is_number(v)


def valid_url(value: str) -> bool:
    """Report whether value is an http or https URL with a host and no whitespace."""
    from urllib.parse import urlsplit

    if any(ord(c) <= 0x20 or ord(c) == 0x7F or c.isspace() for c in value):
        return False
    try:
        parts = urlsplit(value)
        host = parts.hostname
    except ValueError:
        return False
    return parts.scheme.lower() in ("http", "https") and bool(host)


class _Walker:
    """The structural pass. check_limits has already bounded the size and the object count."""

    @staticmethod
    def _record(v: Any, path: str) -> None:
        if not isinstance(v, _Dict):
            raise ValidationError("not_object", path)

    def node(self, v: Any, path: str, only: str | None = None) -> None:
        self._record(v, path)
        t = v.get("type")
        if not isinstance(t, str) or t not in NODE_TYPES:
            raise ValidationError("unknown_type", path, f"unknown node type {t!r}")
        if only is not None and t != only:
            raise ValidationError("invalid_field", path, f"expected a {only} node, got {t}")
        _unknown(v, _KEYS[t] + ("type",), path)
        getattr(self, "_" + t)(v, path)

    def _nodes(self, v: dict, key: str, path: str, only: str | None = None) -> None:
        children = v[key]
        if not isinstance(children, _List):
            raise ValidationError("invalid_field", f"{path}.{key}", "expected an array")
        for i, child in enumerate(children):
            self.node(child, f"{path}.{key}[{i}]", only)

    # node types

    def _stack(self, v: dict, path: str) -> None:
        _require(v, "children", path)
        self._nodes(v, "children", path)
        _enum(v, "gap", GAPS, path)

    def _section(self, v: dict, path: str) -> None:
        _string(v, "title", path, required=True)
        _string(v, "description", path)
        if "count" in v:
            c = v["count"]
            if not _is_number(c) or c < 0 or c != math.floor(c):
                raise ValidationError("invalid_field", f"{path}.count", "expected an integer of at least 0")
        if "children" in v:
            self._nodes(v, "children", path)

    def _form(self, v: dict, path: str) -> None:
        _local_id(v, "id", path, required=True)
        _string(v, "title", path)
        _string(v, "description", path)
        names: set[str] = set()
        _require(v, "fields", path)
        self._fields(v, "fields", path, names)
        _string(v, "submit", path)
        _require(v, "action", path)
        _action(v["action"], f"{path}.action")
        if "more" in v:
            self._fields(v, "more", path, names)

    def _fields(self, v: dict, key: str, path: str, names: set[str]) -> None:
        fields = v[key]
        if not isinstance(fields, _List):
            raise ValidationError("invalid_field", f"{path}.{key}", "expected an array")
        for i, f in enumerate(fields):
            self._field(f, f"{path}.{key}[{i}]", names)

    def _field(self, f: Any, path: str, names: set[str]) -> None:
        self._record(f, path)
        _unknown(f, _KEYS["field"], path)
        _require(f, "type", path)
        if f["type"] not in FIELD_TYPES or not isinstance(f["type"], str):
            raise ValidationError("invalid_field", f"{path}.type", f"unknown field type {f['type']!r}")
        _require(f, "name", path)
        name = f["name"]
        if not isinstance(name, str) or name == "":
            raise ValidationError("invalid_field", f"{path}.name", "expected a non-empty string")
        if name in names:
            raise ValidationError("duplicate_name", f"{path}.name", f"field name {name!r} repeats")
        names.add(name)
        _string(f, "label", path)
        _string(f, "hint", path)
        if "value" in f and not _is_scalar(f["value"]):
            raise ValidationError("invalid_field", f"{path}.value", "expected a string, number, boolean or null")
        _bool(f, "required", path)
        if f["type"] == "select":
            _require(f, "options", path)
        if "options" in f:
            if f["type"] != "select":
                raise ValidationError("invalid_field", f"{path}.options", "options are only allowed on select fields")
            opts = f["options"]
            if not isinstance(opts, _List):
                raise ValidationError("invalid_field", f"{path}.options", "expected an array")
            for i, o in enumerate(opts):
                p = f"{path}.options[{i}]"
                if not isinstance(o, _Dict):
                    raise ValidationError("invalid_field", p, "expected an object")
                _unknown(o, _KEYS["option"], p)
                _string(o, "value", p, required=True)
                _string(o, "label", p, required=True)

    def _list(self, v: dict, path: str) -> None:
        _require(v, "items", path)
        items = v["items"]
        if not isinstance(items, _List):
            raise ValidationError("invalid_field", f"{path}.items", "expected an array")
        for i, it in enumerate(items):
            p = f"{path}.items[{i}]"
            self._record(it, p)
            _unknown(it, _KEYS["listItem"], p)
            _string(it, "title", p, required=True)
            _string(it, "subtitle", p)
            _string(it, "icon", p)
            if "link" in it:
                _target(it["link"], f"{p}.link")
            if "badge" in it:
                _item_badge(it["badge"], f"{p}.badge")
            if "actions" in it:
                self._nodes(it, "actions", p, "button")
        _string(v, "empty", path)

    def _table(self, v: dict, path: str) -> None:
        _require(v, "columns", path)
        cols = v["columns"]
        if not isinstance(cols, _List):
            raise ValidationError("invalid_field", f"{path}.columns", "expected an array")
        for i, c in enumerate(cols):
            p = f"{path}.columns[{i}]"
            if not isinstance(c, _Dict):
                raise ValidationError("invalid_field", p, "expected an object")
            _unknown(c, _KEYS["column"], p)
            _string(c, "key", p, required=True)
            _string(c, "label", p, required=True)
            _bool(c, "numeric", p)
        _require(v, "rows", path)
        rows = v["rows"]
        if not isinstance(rows, _List):
            raise ValidationError("invalid_field", f"{path}.rows", "expected an array")
        for i, row in enumerate(rows):
            p = f"{path}.rows[{i}]"
            self._record(row, p)
            for k in sorted(row):
                if not _is_scalar(row[k]):
                    raise ValidationError("invalid_field", f"{p}.{k}", "expected a string, number, boolean or null")
        if "rowActions" in v:
            self._nodes(v, "rowActions", path, "button")

    def _text(self, v: dict, path: str) -> None:
        _string(v, "value", path, required=True)

    _markdown = _text

    def _code(self, v: dict, path: str) -> None:
        _string(v, "value", path, required=True)
        _string(v, "language", path)

    def _progress(self, v: dict, path: str) -> None:
        _require(v, "value", path)
        value = v["value"]
        if not _is_number(value) or value < 0:
            raise ValidationError("invalid_field", f"{path}.value", "expected a number of at least 0")
        if "max" in v:
            mx = v["max"]
            if not _is_number(mx) or mx <= 0:
                raise ValidationError("invalid_field", f"{path}.max", "expected a number above 0")
            if value > mx:
                raise ValidationError("invalid_field", f"{path}.value", "value exceeds max")
        _string(v, "label", path)
        if "jobId" in v:
            _object_id(v, "jobId", path)

    def _badge(self, v: dict, path: str) -> None:
        _string(v, "label", path, required=True)
        _enum(v, "tone", TONES, path)

    def _button(self, v: dict, path: str) -> None:
        _string(v, "label", path, required=True)
        _enum(v, "variant", VARIANTS, path)
        _require(v, "action", path)
        _action(v["action"], f"{path}.action")
        _string(v, "confirm", path)

    def _link(self, v: dict, path: str) -> None:
        _string(v, "label", path, required=True)
        _require(v, "to", path)
        _target(v["to"], f"{path}.to")

    def _empty(self, v: dict, path: str) -> None:
        _string(v, "title", path, required=True)
        _string(v, "body", path)
        if "action" in v:
            self.node(v["action"], f"{path}.action", "button")


def _unknown(v: dict, allowed: tuple[str, ...], path: str) -> None:
    extra = sorted(k for k in v if k not in allowed)
    if extra:
        raise ValidationError("unknown_field", f"{path}.{extra[0]}", f"unknown field {extra[0]!r}")


def _require(v: dict, key: str, path: str) -> None:
    if key not in v:
        raise ValidationError("missing_field", f"{path}.{key}", f"{key} is required")


def _string(v: dict, key: str, path: str, required: bool = False) -> None:
    if key not in v:
        if required:
            raise ValidationError("missing_field", f"{path}.{key}", f"{key} is required")
        return
    if not isinstance(v[key], str):
        raise ValidationError("invalid_field", f"{path}.{key}", "expected a string")


def _bool(v: dict, key: str, path: str) -> None:
    if key in v and not isinstance(v[key], bool):
        raise ValidationError("invalid_field", f"{path}.{key}", "expected a boolean")


def _enum(v: dict, key: str, allowed: tuple[str, ...], path: str) -> None:
    if key in v and (not isinstance(v[key], str) or v[key] not in allowed):
        raise ValidationError("invalid_field", f"{path}.{key}", f"expected one of {', '.join(allowed)}")


def _local_id(v: dict, key: str, path: str, required: bool = False) -> None:
    _string(v, key, path, required)
    if key in v and not ids.valid_local_id(v[key]):
        raise ValidationError("invalid_id", f"{path}.{key}", f"{v[key]!r} is not a valid id")


def _object_id(v: dict, key: str, path: str) -> None:
    _string(v, key, path)
    if not ids.valid_object_id(v[key]):
        raise ValidationError("invalid_id", f"{path}.{key}", f"{v[key]!r} is not a valid id")


def _item_badge(b: Any, path: str) -> None:
    if not isinstance(b, _Dict):
        raise ValidationError("invalid_field", path, "expected a badge object {label, tone}")
    _unknown(b, _KEYS["listItemBadge"], path)
    if "type" in b and b["type"] != "badge":
        raise ValidationError("invalid_field", f"{path}.type", "a list item badge may only have type badge")
    _string(b, "label", path, required=True)
    _enum(b, "tone", TONES, path)


def _action(a: Any, path: str) -> None:
    if not isinstance(a, _Dict):
        raise ValidationError("invalid_field", path, "expected an action object {id, payload}")
    _unknown(a, _KEYS["action"], path)
    _local_id(a, "id", path, required=True)
    if "payload" in a and not isinstance(a["payload"], _Dict):
        raise ValidationError("invalid_field", f"{path}.payload", "expected an object")


def _target(t: Any, path: str) -> None:
    if not isinstance(t, _Dict):
        raise ValidationError("invalid_field", path, "expected a link target {itemId} or {url}")
    _unknown(t, _KEYS["linkTarget"], path)
    if ("itemId" in t) == ("url" in t):
        raise ValidationError("invalid_field", path, "expected exactly one of itemId and url")
    if "itemId" in t:
        _object_id(t, "itemId", path)
    else:
        _string(t, "url", path)
        if not valid_url(t["url"]):
            raise ValidationError("invalid_url", f"{path}.url", "expected an http or https URL with a host")
