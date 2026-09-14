"""Folders, notes and the write rule.

The plugin writes only notes whose frontmatter has generated_by: bookmark-wiki
and that no one else edited since the plugin last wrote them. Every other
change becomes a suggestion built from a line diff of the note body.
"""

from __future__ import annotations

import difflib
import logging
import threading
from dataclasses import dataclass
from typing import Any, Callable, Iterator

from markspace import HostError

from . import anchors, frontmatter
from .util import canonical, sha256

log = logging.getLogger("wiki.notes")

GENERATED_BY = "bookmark-wiki"


class Conflict(Exception):
    """The note changed after it was read. The caller re-reads it and tries again."""


@dataclass
class WriteResult:
    action: str  # created, updated, unchanged, suggested, skipped
    note: dict | None = None
    suggestion: dict | None = None


# The plugin renders a note as a frontmatter block and a body. A host write sends
# the body as content, title and tags as note fields, and every other key in the
# frontmatter object. Markspace owns created, updated and author, so the note's
# own times stand in for created and updated, and a source author is stored as
# source_author.
NOTE_FIELDS = ("title", "tags", "icon")
HOST_TIMES = ("created", "updated")
RENAMED = {"author": "source_author"}


def split_content(content: str) -> tuple[str, dict]:
    """Return the body of rendered note content and the fields of its frontmatter block."""
    yaml_text, body = frontmatter.split(content)
    return body, frontmatter.parse(yaml_text)


def extra_of(note: dict) -> dict:
    """Return the frontmatter keys of a host note other than title, tags and icon."""
    view = note.get("frontmatter") if isinstance(note.get("frontmatter"), dict) else note.get("extra")
    if not isinstance(view, dict):
        return {}
    return {k: v for k, v in view.items() if k not in NOTE_FIELDS}


def host_frontmatter(fields: dict, current: dict | None = None) -> tuple[list | None, dict]:
    """Split rendered fields into the tags and the frontmatter object of a host write.

    A key of current that fields no longer hold is sent as None, which deletes it.
    """
    tags = fields.get("tags") if isinstance(fields.get("tags"), list) else None
    extra = {RENAMED.get(k, k): v for k, v in fields.items() if k not in NOTE_FIELDS and k not in HOST_TIMES}
    for key in current or {}:
        if key not in extra:
            extra[key] = None
    return tags, extra


def _comparable(fields: dict, ignore_keys: tuple[str, ...] = ()) -> dict:
    """Drop the keys a host write does not store as sent. Markspace may add inline hashtags to tags."""
    skip = set(ignore_keys) | set(NOTE_FIELDS) | set(HOST_TIMES)
    return {k: v for k, v in fields.items() if k not in skip}


def fingerprint_of(content: str, extra: dict | None = None) -> str:
    """Hash the body and frontmatter of note content, independent of where the host keeps frontmatter."""
    fields = frontmatter.read({"content": content, "extra": extra or {}})
    return sha256(canonical({"body": frontmatter.body_of(content), "fm": _comparable(fields)}))


def note_fingerprint(note: dict) -> str:
    body = frontmatter.body_of(note.get("content") or "")
    return sha256(canonical({"body": body, "fm": _comparable(frontmatter.read(note))}))


def same_content(note: dict, content: str, ignore_keys: tuple[str, ...] = ()) -> bool:
    """Report whether content matches the note in body and frontmatter, ignoring some keys.

    Title changes are compared by the caller. Tags match when the note holds every new tag.
    """
    if frontmatter.body_of(note.get("content") or "") != frontmatter.body_of(content):
        return False
    old = frontmatter.read(note)
    new = frontmatter.read({"content": content})
    if canonical(_comparable(old, ignore_keys)) != canonical(_comparable(new, ignore_keys)):
        return False
    if "tags" in ignore_keys:
        return True
    have = {str(t).lower() for t in old.get("tags") or []} if isinstance(old.get("tags"), list) else set()
    want = new.get("tags") if isinstance(new.get("tags"), list) else []
    return all(str(t).lower() in have for t in want)


def diff_edits(old_body: str, new_body: str) -> list[dict]:
    """Return suggestion edits that turn old_body into new_body, one per changed run of lines."""
    a = old_body.splitlines(keepends=True)
    b = new_body.splitlines(keepends=True)
    starts = [0]
    for line in a:
        starts.append(starts[-1] + len(line))
    body = anchors.Body(old_body)
    edits: list[dict] = []
    matcher = difflib.SequenceMatcher(None, a, b, autojunk=False)
    for tag, i1, i2, j1, j2 in matcher.get_opcodes():
        if tag == "equal":
            continue
        text = "".join(b[j1:j2])
        if i1 == i2:
            edits.append({"anchor": body.insertion(body.offset(starts[i1])), "text": text})
            continue
        # The server accepts exact text up to 5000 code units, so a long run is split by lines.
        groups: list[tuple[int, int]] = []
        g_start = i1
        for i in range(i1, i2):
            if starts[i + 1] - starts[g_start] > anchors.MAX_EXACT and i > g_start:
                groups.append((g_start, i))
                g_start = i
        groups.append((g_start, i2))
        for n, (s, e) in enumerate(groups):
            anchor = body.make(body.offset(starts[s]), body.offset(starts[e]))
            if anchor is None:
                continue
            edits.append({"anchor": anchor, "text": text if n == 0 else ""})
    return edits


class Notes:
    def __init__(self, host_fn: Callable[[], Any], state: Any) -> None:
        self._host_fn = host_fn
        self.state = state
        self._folders: dict[tuple[str, str], str] = {}
        self._lock = threading.RLock()
        self._user_id: str | None = None

    @property
    def host(self) -> Any:
        return self._host_fn()

    def plugin_user_id(self) -> str:
        """Return the user id of the plugin from GET /me, or empty when the host does not say."""
        if self._user_id is None:
            try:
                me = self.host.me() or {}
            except HostError:
                return ""
            user = me.get("user") if isinstance(me.get("user"), dict) else {}
            self._user_id = str(me.get("userId") or user.get("id") or "")
        return self._user_id

    # tree ------------------------------------------------------------------

    def walk(self, space_id: str) -> Iterator[tuple[str, dict]]:
        """Yield (kind, item) for every folder, note and file of a space, depth first."""
        page = self.host.tree(space_id)
        stack = list(reversed(page.items))
        while stack:
            row = stack.pop()
            if not isinstance(row, dict):
                continue
            kind = row.get("kind")
            obj = row.get(kind) if kind in ("folder", "note", "file") else None
            if isinstance(obj, dict):
                yield kind, obj
            stack.extend(reversed(row.get("children") or []))

    def notes_under(self, space_id: str, prefix: str) -> list[dict]:
        """Return tree note stubs whose path starts with prefix/."""
        want = prefix.rstrip("/") + "/"
        return [n for kind, n in self.walk(space_id) if kind == "note" and str(n.get("path", "")).startswith(want)]

    def find_note_by_path(self, space_id: str, path: str) -> dict | None:
        for kind, obj in self.walk(space_id):
            if kind == "note" and obj.get("path") == path:
                return obj
        return None

    def folder_id(self, space_id: str, path: str) -> str:
        """Return the id of a folder path such as Wiki/Entities, creating missing folders. The root is empty."""
        path = path.strip("/")
        if not path:
            return ""
        with self._lock:
            if (space_id, path) in self._folders:
                return self._folders[(space_id, path)]
            for attempt in range(2):
                known = {obj.get("path"): obj.get("id") for kind, obj in self.walk(space_id) if kind == "folder"}
                parent = ""
                built = []
                try:
                    for name in path.split("/"):
                        built.append(name)
                        sub = "/".join(built)
                        if known.get(sub):
                            parent = known[sub]
                        else:
                            parent = self.host.create_folder(space_id, name, parent_id=parent)["id"]
                        self._folders[(space_id, sub)] = parent
                    return parent
                except HostError as exc:
                    if exc.status != 409 or attempt:
                        raise
                    # Another job created the folder between the tree read and the create.
            raise AssertionError("unreachable")

    def forget_folders(self) -> None:
        with self._lock:
            self._folders.clear()

    # notes -----------------------------------------------------------------

    def get(self, note_id: str) -> dict | None:
        """Return a note with content, or None when it is gone or trashed."""
        try:
            item = self.host.get_item(note_id)
        except HostError as exc:
            if exc.status in (403, 404, 410):
                return None
            raise
        note = item.get("note") if isinstance(item, dict) else None
        if not isinstance(note, dict) or note.get("deleted"):
            return None
        return note

    @staticmethod
    def generated(note: dict) -> bool:
        return frontmatter.read(note).get("generated_by") == GENERATED_BY

    def user_edited(self, note: dict) -> bool:
        """Report whether someone other than the plugin changed the note since the plugin wrote it."""
        uid = self.plugin_user_id()
        by = str(note.get("updatedBy") or "")
        if uid and by and by != uid:
            return True
        written = self.state.written(note["id"])
        return bool(written and written.get("hash") != note_fingerprint(note))

    def writable(self, note: dict) -> bool:
        return self.generated(note) and not self.user_edited(note)

    def create(self, space_id: str, folder: str, title: str, content: str, *, tags: list[str] | None = None,
               client_id: str | None = None, folder_note: bool = False) -> dict:
        """Create a note in folder. folder_note creates the folder's own index.md instead."""
        parent = self.folder_id(space_id, folder)
        body, fields = split_content(content)
        fm_tags, extra = host_frontmatter(fields)
        tags = tags if tags is not None else fm_tags
        where: dict[str, Any] = {"parent_id": parent}
        if folder_note:
            where = {"folder_note": True, "folder_id": parent}
        try:
            note = self.host.create_note(space_id, title, body, tags=tags, id=client_id,
                                         frontmatter=extra or None, **where)
        except HostError as exc:
            if not client_id or exc.status not in (400, 409):
                raise
            # The client id is taken by a trashed note or refused. Create without it.
            note = self.host.create_note(space_id, title, body, tags=tags,
                                         frontmatter=extra or None, **where)
        if not note.get("content"):
            note = {**note, "content": body}
        self.state.set_written(note["id"], fingerprint_of(content))
        return note

    def write(self, note: dict, content: str, *, title: str | None = None, tags: list[str] | None = None,
              ignore_keys: tuple[str, ...] = (), suggestion_title: str = "Update from Bookmark wiki",
              rationale: str = "", kind: str = "update") -> WriteResult:
        """Write content to a generated note the user did not edit, or suggest the body change otherwise.

        A write that loses a race with another edit raises Conflict.
        """
        title_changes = title is not None and title != note.get("title")
        if same_content(note, content, ignore_keys) and not title_changes:
            return WriteResult("unchanged", note)
        if not self.writable(note):
            suggestion = self.suggest(note, frontmatter.body_of(content), suggestion_title, rationale, kind)
            return WriteResult("suggested" if suggestion else "skipped", note, suggestion)
        body, fields = split_content(content)
        fm_tags, extra = host_frontmatter(fields, extra_of(note))
        try:
            updated = self.host.update_note(note["id"], base_updated_at=note["updatedAt"], content=body,
                                            title=title if title_changes else None,
                                            tags=tags if tags is not None else fm_tags, frontmatter=extra or None)
        except HostError as exc:
            if exc.status == 409:
                raise Conflict(note["id"]) from None
            raise
        if not isinstance(updated, dict) or not updated.get("id"):
            updated = {**note, "content": body}
        if not updated.get("content"):
            updated = {**updated, "content": body}
        self.state.set_written(note["id"], fingerprint_of(content))
        return WriteResult("updated", updated)

    def suggest(self, note: dict, new_body: str, title: str, rationale: str = "", kind: str = "update",
                edits: list[dict] | None = None) -> dict | None:
        """Create a suggestion that turns the note body into new_body.

        An open suggestion of the same kind from the plugin is withdrawn first,
        so the note never collects stale copies of one proposal.
        """
        if edits is None:
            edits = diff_edits(frontmatter.body_of(note.get("content") or ""), new_body)
        if not edits:
            return None
        digest = sha256(canonical({"title": title, "edits": edits}))
        previous = self.state.open_suggestions(note["id"]).get(kind)
        if previous and previous.get("hash") == digest:
            # The same proposal is still open. Creating it again would only churn the note's suggestions.
            return {"id": previous["id"], "reused": True}
        if previous:
            try:
                self.host.withdraw_suggestion(previous["id"])
            except HostError as exc:
                if exc.status not in (404, 409, 410):
                    raise
            self.state.clear_suggestion(previous["id"])
        suggestion = self.host.create_suggestion(note["id"], title, edits, rationale=rationale or None)
        if isinstance(suggestion, dict) and suggestion.get("id"):
            self.state.set_open_suggestion(note["id"], kind, suggestion["id"], hash=digest)
        return suggestion
