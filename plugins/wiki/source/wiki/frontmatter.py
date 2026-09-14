"""A small YAML subset for note frontmatter.

The plugin writes frontmatter in a fixed key order with flow lists and quoted
strings where plain YAML would be ambiguous. The parser reads that output and
the common forms a person types by hand: plain and quoted scalars, flow lists,
block lists, booleans, integers and trailing comments.
"""

from __future__ import annotations

import json
import re
from typing import Any

_PLAIN = re.compile(r"[A-Za-z0-9_][A-Za-z0-9 ._/@+:-]*")
_RESERVED = {"true", "false", "yes", "no", "on", "off", "null", "~", ""}
_INT = re.compile(r"-?[0-9]+")


def split(content: str) -> tuple[str | None, str]:
    """Split a leading frontmatter block from content. Return (yaml text or None, body).

    The rules match internal/store. The block opens with a --- line and closes
    with a --- or ... line. Without a closing line there is no block.
    """
    text = content or ""
    if text.startswith("---\r\n"):
        rest = text[5:]
    elif text.startswith("---\n"):
        rest = text[4:]
    else:
        return None, text
    pos = 0
    lines = rest.splitlines(keepends=True)
    for i, line in enumerate(lines):
        pos += len(line)
        if line.rstrip("\r\n") in ("---", "..."):
            return "".join(lines[:i]), rest[pos:]
    return None, text


def body_of(content: str) -> str:
    """Return content without its frontmatter block."""
    return split(content)[1]


def _scalar(value: Any) -> str:
    if value is True:
        return "true"
    if value is False:
        return "false"
    if value is None:
        return '""'
    if isinstance(value, (int, float)):
        return str(value)
    text = str(value)
    if (_PLAIN.fullmatch(text) and text.lower() not in _RESERVED and not _INT.fullmatch(text)
            and not text.endswith((" ", ":")) and ": " not in text and " #" not in text):
        return text
    return json.dumps(text, ensure_ascii=False)


def _flow_item(value: Any) -> str:
    text = _scalar(value)
    if "," in text and not text.startswith('"'):
        return json.dumps(str(value), ensure_ascii=False)
    return text


def dump(fields: dict[str, Any]) -> str:
    """Render fields as a frontmatter block, including both --- lines, in insertion order."""
    out = ["---"]
    for key, value in fields.items():
        if isinstance(value, (list, tuple)):
            out.append(f"{key}: [{', '.join(_flow_item(v) for v in value)}]")
        else:
            out.append(f"{key}: {_scalar(value)}")
    out.append("---")
    return "\n".join(out) + "\n"


def _strip_comment(text: str) -> str:
    in_single = in_double = False
    for i, c in enumerate(text):
        if c == '"' and not in_single and (i == 0 or text[i - 1] != "\\"):
            in_double = not in_double
        elif c == "'" and not in_double:
            in_single = not in_single
        elif c == "#" and not in_single and not in_double and (i == 0 or text[i - 1] in " \t"):
            return text[:i].rstrip()
    return text.strip()


def _split_flow(inner: str) -> list[str]:
    items, buf, depth = [], [], 0
    quote = ""
    for i, c in enumerate(inner):
        if quote:
            buf.append(c)
            if c == quote and (quote == "'" or inner[i - 1] != "\\"):
                quote = ""
            continue
        if c in "\"'":
            quote = c
            buf.append(c)
        elif c == "[":
            depth += 1
            buf.append(c)
        elif c == "]":
            depth -= 1
            buf.append(c)
        elif c == "," and depth == 0:
            items.append("".join(buf).strip())
            buf = []
        else:
            buf.append(c)
    last = "".join(buf).strip()
    if last or items:
        items.append(last)
    return [i for i in items if i != ""]


def _parse_scalar(text: str) -> Any:
    text = text.strip()
    if text.startswith('"'):
        end = text.rfind('"')
        try:
            return json.loads(text[: end + 1] if end > 0 else text)
        except ValueError:
            return text.strip('"')
    if text.startswith("'"):
        end = text.rfind("'")
        return (text[1:end] if end > 0 else text[1:]).replace("''", "'")
    text = _strip_comment(text)
    if text.startswith("[") and text.endswith("]"):
        return [_parse_scalar(i) for i in _split_flow(text[1:-1])]
    low = text.lower()
    if low in ("true", "yes", "on"):
        return True
    if low in ("false", "no", "off"):
        return False
    if low in ("null", "~"):
        return None
    if _INT.fullmatch(text):
        try:
            return int(text)
        except ValueError:
            return text
    return text


def parse(yaml_text: str | None) -> dict[str, Any]:
    """Parse a frontmatter block without its --- lines. Unknown forms become strings."""
    out: dict[str, Any] = {}
    if not yaml_text:
        return out
    key = None
    for raw in yaml_text.splitlines():
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        stripped = raw.strip()
        if raw[:1] in " \t" or stripped.startswith("- "):
            if key is not None and stripped.startswith("-"):
                if not isinstance(out.get(key), list):
                    out[key] = []
                out[key].append(_parse_scalar(stripped[1:]))
            continue
        name, sep, value = raw.partition(":")
        if not sep:
            continue
        key = name.strip()
        value = value.strip()
        out[key] = _parse_scalar(value) if value else []
    for k, v in list(out.items()):
        if v == []:
            # A key with no value and no block items is an empty string, not an empty list.
            continue
    return out


def read(note: dict) -> dict[str, Any]:
    """Return the frontmatter of a host note, or of rendered content in {"content": ...}.

    GET /items returns the frontmatter of a note as note.frontmatter, with the
    title, tags and icon and the extra keys. A note dict may also carry extra,
    its own title and tags, or a block at the top of content, and keys in that
    block win. The plugin stores a source author as source_author, because
    Markspace owns author, and this function returns it as author again.
    """
    fields: dict[str, Any] = {}
    for key in ("extra", "frontmatter"):
        value = note.get(key)
        if isinstance(value, dict):
            fields.update(value)
    if note.get("id"):
        if isinstance(note.get("title"), str) and "title" not in fields:
            fields["title"] = note["title"]
        if isinstance(note.get("tags"), list) and "tags" not in fields:
            fields["tags"] = note["tags"]
    if "source_author" in fields:
        fields.setdefault("author", fields.pop("source_author"))
    yaml_text, _ = split(note.get("content") or "")
    fields.update(parse(yaml_text))
    return fields
