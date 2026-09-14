"""Identifier rules from test/vectors/protocol.json."""

from __future__ import annotations

import re

_PLUGIN_ID = re.compile(r"[a-z0-9][a-z0-9-]{0,62}")
_LOCAL_ID = re.compile(r"[a-z0-9][a-z0-9._-]{0,62}")
_QUALIFIED_ID = re.compile(r"[a-z0-9][a-z0-9-]{0,62}\.[a-z0-9][a-z0-9._-]{0,62}")
_OBJECT_ID = re.compile(r"[A-Za-z0-9][A-Za-z0-9_-]{0,127}")
_SECRET_NAME = re.compile(r"[a-z0-9][a-z0-9_-]{0,62}")
_EVENT_PATTERN = re.compile(r"[a-z0-9*][a-z0-9._*-]{0,127}")

MAX_STORAGE_KEY_BYTES = 512


def _full(rx: re.Pattern[str], value: object) -> bool:
    return isinstance(value, str) and rx.fullmatch(value) is not None


def valid_plugin_id(value: object) -> bool:
    """Report whether value is a plugin id, such as bookmark-wiki."""
    return _full(_PLUGIN_ID, value)


def valid_local_id(value: object) -> bool:
    """Report whether value is a command, panel, action, importer or form id or a job kind."""
    return _full(_LOCAL_ID, value)


def valid_qualified_id(value: object) -> bool:
    """Report whether value is <pluginId>.<localId>, as in openPanel.id."""
    return _full(_QUALIFIED_ID, value)


def valid_object_id(value: object) -> bool:
    """Report whether value is a job, item, space, user or delivery id."""
    return _full(_OBJECT_ID, value)


def valid_secret_name(value: object) -> bool:
    """Report whether value is a secret name from the manifest."""
    return _full(_SECRET_NAME, value)


def valid_storage_key(value: object) -> bool:
    """Report whether value is a key-value or blob key.

    A key is 1 to 512 bytes of UTF-8 with no control characters.
    """
    if not isinstance(value, str):
        return False
    try:
        raw = value.encode("utf-8")
    except UnicodeEncodeError:
        return False
    if not 1 <= len(raw) <= MAX_STORAGE_KEY_BYTES:
        return False
    return not any(b < 0x20 or b == 0x7F for b in raw)


def valid_event_pattern(value: object) -> bool:
    """Report whether value is an event type or a pattern with * wildcards."""
    return _full(_EVENT_PATTERN, value)


def match_event(pattern: str, event_type: str) -> bool:
    """Report whether event_type matches pattern.

    A * matches any run of characters, dots and the empty run included. No
    other character is special.
    """
    parts = pattern.split("*")
    rx = ".*".join(re.escape(p) for p in parts)
    return re.fullmatch(rx, event_type, flags=re.DOTALL) is not None


RULES = {
    "pluginId": valid_plugin_id,
    "localId": valid_local_id,
    "qualifiedId": valid_qualified_id,
    "objectId": valid_object_id,
    "secretName": valid_secret_name,
    "storageKey": valid_storage_key,
    "eventPattern": valid_event_pattern,
}
