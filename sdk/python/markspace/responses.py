"""Command, action and import responses."""

from __future__ import annotations

from typing import Any
from urllib.parse import urlsplit

from . import ids, ui
from .errors import ValidationError

KINDS = ("toast", "openPanel", "navigate", "job", "ui")
TOAST_KINDS = ("info", "success", "warn", "error")
MAX_NAVIGATE_BYTES = 2048
ROLES = ("viewer", "commenter", "editor")


def toast(message: str, kind: str = "info") -> dict:
    """Show a short message. kind is info, success, warn or error."""
    return {"toast": {"message": message, "kind": kind}}


def open_panel(panel_id: str, item_id: str | None = None) -> dict:
    """Open a panel. panel_id is qualified, such as word-count.counts."""
    body: dict[str, Any] = {"id": panel_id}
    if item_id is not None:
        body["itemId"] = item_id
    return {"openPanel": body}


def navigate(path: str) -> dict:
    """Send the browser to a path inside Markspace, such as /s/docs/n/wiki/index."""
    return {"navigate": path}


def job(job_id: str) -> dict:
    """Point the client at a job so it can show progress."""
    return {"job": {"id": job_id}}


def ui_response(tree: dict) -> dict:
    """Show a UI tree as the result."""
    return {"ui": tree}


def validate_command_response(resp: Any) -> None:
    """Raise ValidationError when resp is not a valid command response."""
    if not isinstance(resp, dict):
        raise ValidationError("not_object", "$", "a command response is an object")
    extra = sorted(k for k in resp if k not in KINDS)
    if extra:
        raise ValidationError("unknown_field", f"$.{extra[0]}", f"unknown response kind {extra[0]!r}")
    present = [k for k in KINDS if k in resp]
    if not present:
        raise ValidationError("empty_response", "$", f"expected one of {', '.join(KINDS)}")
    if len(present) > 1:
        raise ValidationError("multiple_kinds", "$", f"found {', '.join(present)}")
    kind = present[0]
    value = resp[kind]
    path = f"$.{kind}"
    if kind == "ui":
        ui.validate(value, path)
    elif kind == "navigate":
        if not isinstance(value, str):
            raise ValidationError("invalid_field", path, "expected a string")
        if not valid_navigate(value):
            raise ValidationError("invalid_navigate", path, "expected a path inside Markspace")
    else:
        if not isinstance(value, dict):
            raise ValidationError("invalid_field", path, "expected an object")
        if kind == "toast":
            _unknown(value, ("message", "kind"), path)
            if "message" not in value:
                raise ValidationError("missing_field", f"{path}.message")
            if not isinstance(value["message"], str) or not value["message"]:
                raise ValidationError("invalid_field", f"{path}.message", "expected a non-empty string")
            if "kind" in value and value["kind"] not in TOAST_KINDS:
                raise ValidationError("invalid_field", f"{path}.kind", f"expected one of {', '.join(TOAST_KINDS)}")
        elif kind == "openPanel":
            _unknown(value, ("id", "itemId"), path)
            _id(value, "id", path, ids.valid_qualified_id, required=True)
            _id(value, "itemId", path, ids.valid_object_id)
        else:
            _unknown(value, ("id",), path)
            _id(value, "id", path, ids.valid_object_id, required=True)


def valid_navigate(path: str) -> bool:
    """Report whether path is a navigate target."""
    raw = path.encode("utf-8", "surrogatepass")
    if not 1 <= len(raw) <= MAX_NAVIGATE_BYTES:
        return False
    if not path.startswith("/") or path.startswith("//") or path.startswith("/\\"):
        return False
    return not any(b < 0x20 or b == 0x7F for b in raw)


def validate_install(body: Any, manifest_id: str) -> None:
    """Raise ValidationError when body is not a valid install payload for manifest_id."""
    from .signature import decode_secret

    if not isinstance(body, dict):
        raise ValidationError("not_object", "$", "the install payload is an object")
    if "pluginId" not in body:
        raise ValidationError("missing_field", "$.pluginId")
    if body["pluginId"] != manifest_id:
        raise ValidationError("wrong_plugin", "$.pluginId", f"this plugin is {manifest_id}")
    if "markspaceUrl" not in body:
        raise ValidationError("missing_field", "$.markspaceUrl")
    if not isinstance(body["markspaceUrl"], str) or not _valid_base_url(body["markspaceUrl"]):
        raise ValidationError("invalid_url", "$.markspaceUrl", "expected an http or https URL with a host")
    if "token" not in body:
        raise ValidationError("missing_field", "$.token")
    token = body["token"]
    if not isinstance(token, str) or not token or any(ord(c) <= 0x20 or ord(c) == 0x7F or c.isspace() for c in token):
        raise ValidationError("invalid_field", "$.token", "expected a non-empty token without whitespace")
    if "signingSecret" not in body:
        raise ValidationError("missing_field", "$.signingSecret")
    try:
        decode_secret(body["signingSecret"])
    except ValueError as exc:
        raise ValidationError("invalid_secret", "$.signingSecret", str(exc)) from None
    if "grants" in body:
        grants = body["grants"]
        if not isinstance(grants, list):
            raise ValidationError("invalid_field", "$.grants", "expected an array")
        for i, g in enumerate(grants):
            p = f"$.grants[{i}]"
            if not isinstance(g, dict):
                raise ValidationError("invalid_field", p, "expected an object")
            if "spaceId" not in g:
                raise ValidationError("missing_field", f"{p}.spaceId")
            if not isinstance(g["spaceId"], str) or not g["spaceId"]:
                raise ValidationError("invalid_field", f"{p}.spaceId", "expected a non-empty string")
            if "role" not in g:
                raise ValidationError("missing_field", f"{p}.role")
            if g["role"] not in ROLES:
                raise ValidationError("invalid_field", f"{p}.role", f"expected one of {', '.join(ROLES)}")
    if "permissions" in body:
        perms = body["permissions"]
        if not isinstance(perms, list):
            raise ValidationError("invalid_field", "$.permissions", "expected an array")
        for i, p in enumerate(perms):
            if not isinstance(p, str):
                raise ValidationError("invalid_field", f"$.permissions[{i}]", "expected a string")


def _valid_base_url(value: str) -> bool:
    if not ui.valid_url(value):
        return False
    parts = urlsplit(value)
    return not parts.query and not parts.fragment and "?" not in value and "#" not in value


def _unknown(v: dict, allowed: tuple[str, ...], path: str) -> None:
    extra = sorted(k for k in v if k not in allowed)
    if extra:
        raise ValidationError("unknown_field", f"{path}.{extra[0]}", f"unknown field {extra[0]!r}")


def _id(v: dict, key: str, path: str, rule, required: bool = False) -> None:
    if key not in v:
        if required:
            raise ValidationError("missing_field", f"{path}.{key}")
        return
    if not isinstance(v[key], str):
        raise ValidationError("invalid_field", f"{path}.{key}", "expected a string")
    if not rule(v[key]):
        raise ValidationError("invalid_id", f"{path}.{key}", f"{v[key]!r} is not a valid id")
