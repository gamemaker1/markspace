"""An in-memory Markspace host.

FakeMarkspace implements every host API method the plugin calls, with the
method names and return types of markspace.Host. It keeps folders,
notes and files, resolves wikilinks by path, slug and title, runs a simple
text search, stores suggestions and checks their anchors, queues jobs, and
answers AI and web fetch calls from fixtures. fakehttp.py serves the same
object over HTTP.
"""

from __future__ import annotations

import copy
import hashlib
import itertools
import json
import re
import threading
from contextlib import contextmanager
from datetime import datetime, timedelta, timezone
from typing import Any, Iterator

# Frontmatter keys Markspace owns. A frontmatter object that sets one answers 400.
OWNED_FRONTMATTER = ("id", "position", "created", "updated", "author", "updated_by", "deleted", "deleted_at",
                     "deleted_by", "trashed_from", "trash_path", "parent")

from markspace import AIModel, FileText, HostError, KVEntry, Page

from wiki import frontmatter, wikilinks
from wiki.anchors import Body
from wiki.manifest import MANIFEST
from wiki.urls import slugify
from fakeai import ScriptedAI
from fakeweb import FixtureWeb

SPACE = "space1"
PLUGIN_USER = "u-plugin"
USER = "u-alice"
BASE = datetime(2026, 9, 13, 11, 0, tzinfo=timezone.utc)
KV_PAGE = 50
CLIENT_ID = re.compile(r"[0-9abcdefghjkmnpqrstvwxyz]{26}")
JOB_KINDS = {j["kind"] for j in MANIFEST["jobs"]}
_UNSET = object()


def _json_copy(value: Any) -> Any:
    return json.loads(json.dumps(value))


def _common_suffix(a: list[int], b: list[int]) -> int:
    n = 0
    while n < len(a) and n < len(b) and a[-1 - n] == b[-1 - n]:
        n += 1
    return n


def _common_prefix(a: list[int], b: list[int]) -> int:
    n = 0
    while n < len(a) and n < len(b) and a[n] == b[n]:
        n += 1
    return n


def resolve_anchor(body: str, anchor: dict) -> tuple[int, int] | None:
    """Find an anchor in body as docs/comments.md describes. An empty exact is an insertion point."""
    b = list(Body(body).u)
    exact = list(Body(anchor.get("exact") or "").u)
    prefix = list(Body(anchor.get("prefix") or "").u)
    suffix = list(Body(anchor.get("suffix") or "").u)
    hint = int(anchor.get("start") or 0)
    best = None
    for i in range(len(b) - len(exact) + 1):
        if b[i:i + len(exact)] != exact:
            continue
        score = _common_suffix(prefix, b[max(0, i - len(prefix)):i]) + \
            _common_prefix(suffix, b[i + len(exact):i + len(exact) + len(suffix)])
        if not exact and score < len(prefix) + len(suffix):
            continue
        key = (-score, abs(i - hint), i)
        if best is None or key < best[0]:
            best = (key, i)
    if best is None:
        return None
    return best[1], best[1] + len(exact)


class FakeMarkspace:
    def __init__(self, *, strip_frontmatter: bool = False, embeddings: bool = False, pdf_text_delay: int = 1,
                 web: FixtureWeb | None = None) -> None:
        self.lock = threading.RLock()
        self.strip_frontmatter = strip_frontmatter
        self.admin_settings: dict = {}
        self.embeddings = embeddings
        self.pdf_text_delay = pdf_text_delay
        self.items: dict[str, dict] = {}
        self.file_bytes: dict[str, bytes] = {}
        self.file_reads: dict[str, int] = {}
        self.versions: dict[str, list[dict]] = {}
        self.kv: dict[str, Any] = {}
        self.blobs: dict[str, bytes] = {}
        self.secrets: dict[str, str] = {}
        self.suggestions: dict[str, dict] = {}
        self.jobs: dict[str, dict] = {}
        self.job_logs: dict[str, list[tuple[str, str]]] = {}
        self.notifications: list[dict] = []
        self.emitted: list[dict] = []
        self.writes: list[tuple[str, str, str]] = []
        self.fetch_times: list[tuple[str, str, float]] = []
        self.faults: dict[str, list[BaseException]] = {}
        self.models = [
            {"providerId": "openrouter", "model": "cheap-model", "vision": False,
             "prices": {"inputPerMTok": 0.5, "outputPerMTok": 1.5}},
            {"providerId": "local", "model": "llama", "vision": False, "prices": {}},
        ]
        self.web = web or FixtureWeb()
        self.ai = ScriptedAI(self)
        self._seq = itertools.count(1)
        self._tick = 0
        self._local = threading.local()

    # identity --------------------------------------------------------------

    @property
    def actor(self) -> str:
        return getattr(self._local, "actor", PLUGIN_USER)

    @contextmanager
    def as_user(self, user: str) -> Iterator[None]:
        previous = self.actor
        self._local.actor = user
        try:
            yield
        finally:
            self._local.actor = previous

    def _id(self, prefix: str) -> str:
        return f"{prefix}{next(self._seq):05d}"

    def _now(self) -> str:
        with self.lock:
            self._tick += 1
            return (BASE + timedelta(seconds=self._tick)).isoformat().replace("+00:00", "Z")

    def _fault(self, name: str) -> None:
        queue = self.faults.get(name)
        if queue:
            raise queue.pop(0)

    def me(self) -> dict:
        return {"pluginId": MANIFEST["id"], "userId": PLUGIN_USER, "grants": [{"spaceId": SPACE, "role": "editor"}],
                "permissions": list(MANIFEST["permissions"])}

    # items -----------------------------------------------------------------

    def _obj(self, item_id: str, kind: str | None = None) -> dict:
        entry = self.items.get(item_id)
        if entry is None or entry["obj"].get("deleted") or (kind and entry["kind"] != kind):
            raise HostError(404, f"{kind or 'item'} {item_id} not found")
        return entry["obj"]

    def _parent_path(self, parent_id: str) -> str:
        return self._obj(parent_id, "folder")["path"] if parent_id else ""

    def _siblings(self, space_id: str, parent_id: str) -> list[dict]:
        return [e["obj"] for e in self.items.values()
                if e["obj"]["spaceId"] == space_id and (e["obj"].get("parentId") or "") == parent_id
                and not e["obj"].get("deleted")]

    def _base_name(self, obj: dict) -> str:
        return obj["path"].rsplit("/", 1)[-1]

    def _store_content(self, content: str) -> tuple[str, dict]:
        # The plugin sends frontmatter in its own field. A block in content stays in the body, as in Markspace.
        if not self.strip_frontmatter:
            return content, {}
        yaml_text, body = frontmatter.split(content)
        return body, frontmatter.parse(yaml_text)

    def _note_path(self, space_id: str, parent_id: str, title: str, note_id: str | None = None) -> str:
        base = self._parent_path(parent_id)
        # Markspace reserves index.md for folder notes, so an ordinary note titled index becomes index-note.md.
        slug = slugify(title)
        if slug == "index":
            slug = "index-note"
        taken = {self._base_name(o).lower() for o in self._siblings(space_id, parent_id) if o["id"] != note_id}
        name, n = f"{slug}.md", 2
        while name.lower() in taken:
            name, n = f"{slug}-{n}.md", n + 1
        return f"{base}/{name}" if base else name

    def note(self, note_id: str) -> dict:
        """Return the stored note with its content as the host would return it."""
        with self.lock:
            return copy.deepcopy(self._obj(note_id, "note"))

    def all_notes(self) -> list[dict]:
        with self.lock:
            return [copy.deepcopy(e["obj"]) for e in self.items.values()
                    if e["kind"] == "note" and not e["obj"].get("deleted")]

    def note_by_path(self, path: str) -> dict | None:
        return next((n for n in self.all_notes() if n["path"] == path), None)

    def body(self, note: dict) -> str:
        return frontmatter.body_of(note.get("content") or "")

    def note_mentions(self, title: str, name: str) -> bool:
        note = next((n for n in self.all_notes() if n["title"] == title), None)
        if note is None:
            return False
        return re.search(r"(?<!\w)" + re.escape(name) + r"(?!\w)", self.body(note), re.IGNORECASE) is not None

    def _stub(self, obj: dict) -> dict:
        stub = {k: v for k, v in obj.items() if k not in ("content", "extra")}
        if "content" in obj:
            stub["excerpt"] = " ".join(self.body(obj).split())[:160]
        return copy.deepcopy(stub)

    def tree(self, space_id: str) -> Page:
        self._fault("tree")
        order = {"folder": 0, "note": 1, "file": 2}
        with self.lock:
            def build(parent: str) -> list[dict]:
                rows = []
                kids = [e for e in self.items.values() if e["obj"]["spaceId"] == space_id
                        and (e["obj"].get("parentId") or "") == parent and not e["obj"].get("deleted")]
                for e in sorted(kids, key=lambda e: (order[e["kind"]], e["obj"]["path"].lower())):
                    row = {"kind": e["kind"], e["kind"]: self._stub(e["obj"])}
                    if e["kind"] == "folder":
                        row["children"] = build(e["obj"]["id"])
                    rows.append(row)
                return rows
            return Page(items=build(""))

    def get_item(self, item_id: str) -> dict:
        self._fault("get_item")
        with self.lock:
            entry = self.items.get(item_id)
            if entry is None or entry["obj"].get("deleted"):
                raise HostError(404, f"item {item_id} not found")
            return {"kind": entry["kind"], entry["kind"]: copy.deepcopy(entry["obj"])}

    def create_folder(self, space_id: str, name: str, *, parent_id: str = "") -> dict:
        self._fault("create_folder")
        with self.lock:
            if not name or "/" in name or name.startswith("."):
                raise HostError(400, "invalid folder name", "name")
            if any(self._base_name(o).lower() == name.lower() for o in self._siblings(space_id, parent_id)):
                raise HostError(409, f"{name} exists", "name")
            base = self._parent_path(parent_id)
            fid = self._id("f")
            obj = {"id": fid, "spaceId": space_id, "parentId": parent_id, "name": name,
                   "path": f"{base}/{name}" if base else name, "createdAt": self._now(), "createdBy": self.actor}
            self.items[fid] = {"kind": "folder", "obj": obj}
            return copy.deepcopy(obj)

    @staticmethod
    def _apply_frontmatter(obj: dict, fm: dict | None) -> None:
        """Merge a frontmatter object into extra the way Markspace does. Owned keys answer 400."""
        if not fm:
            return
        extra = dict(obj.get("extra") or {})
        for key, value in fm.items():
            if key.lower() in OWNED_FRONTMATTER:
                raise HostError(400, f"frontmatter.{key} is set by Markspace", f"frontmatter.{key}")
            if value is None:
                extra.pop(key, None)
            else:
                extra[key] = copy.deepcopy(value)
        if extra:
            obj["extra"] = extra
        else:
            obj.pop("extra", None)

    def settings(self) -> dict:
        return copy.deepcopy(self.admin_settings)

    def create_note(self, space_id: str, title: str, content: str = "", *, parent_id: str = "",
                    tags: list[str] | None = None, id: str | None = None, icon: str | None = None,
                    frontmatter: dict | None = None, folder_note: bool = False, folder_id: str = "") -> dict:
        self._fault("create_note")
        for key in frontmatter or {}:
            if key.lower() in OWNED_FRONTMATTER:
                raise HostError(400, f"frontmatter.{key} is set by Markspace", f"frontmatter.{key}")
        if folder_id and not folder_note:
            raise HostError(400, "folderId is used only with folderNote", "folderId")
        if folder_note:
            if not folder_id:
                raise HostError(400, "a folder note needs folderId", "folderId")
            if parent_id and parent_id != folder_id:
                raise HostError(400, "parentId must be empty or equal to folderId", "parentId")
            parent_id = folder_id
        with self.lock:
            if id is not None:
                if not CLIENT_ID.fullmatch(id):
                    raise HostError(400, "the client id is not in the NewID shape", "id")
                if id in self.items:
                    obj = self.items[id]["obj"]
                    if (self.items[id]["kind"] == "note" and obj["spaceId"] == space_id
                            and obj.get("createdBy") == self.actor and not obj.get("deleted")):
                        return copy.deepcopy(obj)
                    raise HostError(409, "the id belongs to another item", "id")
            if parent_id:
                self._obj(parent_id, "folder")
            if folder_note:
                base = self._parent_path(parent_id)
                if any(self._base_name(o).lower() == "index.md" for o in self._siblings(space_id, parent_id)):
                    raise HostError(409, f"folder {base!r} already has a folder note")
                path = f"{base}/index.md"
            else:
                path = self._note_path(space_id, parent_id, title)
            nid = id or self._id("n")
            now = self._now()
            stored, extra = self._store_content(content)
            obj = {"id": nid, "spaceId": space_id, "parentId": parent_id, "title": title,
                   "path": path, "tags": list(tags or []),
                   "content": stored, "createdAt": now, "updatedAt": now, "createdBy": self.actor,
                   "updatedBy": self.actor, "deleted": False}
            if extra:
                obj["extra"] = extra
            self._apply_frontmatter(obj, frontmatter)
            self.items[nid] = {"kind": "note", "obj": obj}
            self.writes.append((nid, "create", self.actor))
            return copy.deepcopy(obj)

    def update_note(self, note_id: str, *, base_updated_at: Any, title: str | None = None,
                    content: str | None = None, tags: list[str] | None = None, icon: str | None = None,
                    frontmatter: dict | None = None) -> dict:
        self._fault("update_note")
        with self.lock:
            obj = self._obj(note_id, "note")
            if base_updated_at and str(base_updated_at) < obj["updatedAt"]:
                raise HostError(409, "the note changed since baseUpdatedAt", "baseUpdatedAt")
            for key in frontmatter or {}:
                if key.lower() in OWNED_FRONTMATTER:
                    raise HostError(400, f"frontmatter.{key} is set by Markspace", f"frontmatter.{key}")
            self.versions.setdefault(note_id, []).append(copy.deepcopy(obj))
            self._apply_frontmatter(obj, frontmatter)
            if content is not None:
                stored, extra = self._store_content(content)
                obj["content"] = stored
                if self.strip_frontmatter and extra:
                    obj["extra"] = extra
            if title is not None and title != obj["title"]:
                obj["title"] = title
                obj["path"] = self._note_path(obj["spaceId"], obj.get("parentId") or "", title, note_id)
            if tags is not None:
                obj["tags"] = list(tags)
            obj["updatedAt"] = self._now()
            obj["updatedBy"] = self.actor
            self.writes.append((note_id, "update", self.actor))
            return copy.deepcopy(obj)

    def user_edit(self, note_id: str, body: str, user: str = USER) -> dict:
        """Replace the body of a note as a person, keeping its frontmatter."""
        with self.lock:
            obj = self._obj(note_id, "note")
            yaml_text, _ = frontmatter.split(obj["content"])
            content = (f"---\n{yaml_text}---\n" if yaml_text is not None else "") + body
            with self.as_user(user):
                return self.update_note(note_id, base_updated_at=obj["updatedAt"], content=content)

    def trash(self, item_id: str) -> dict:
        with self.lock:
            obj = self._obj(item_id)
            obj["deleted"] = True
            return {"ok": True}

    def upload_file(self, space_id: str, file: Any, name: str, *, parent_id: str = "", mime: str | None = None,
                    id: str | None = None, on_conflict: str | None = None) -> dict:
        self._fault("upload_file")
        data = file if isinstance(file, (bytes, bytearray)) else file.read()
        with self.lock:
            if id is not None and id in self.items:
                return copy.deepcopy(self.items[id]["obj"])
            if name.endswith(".md"):
                raise HostError(400, "a .md file is a note", "file")
            taken = {self._base_name(o).lower() for o in self._siblings(space_id, parent_id)}
            if name.lower() in taken:
                if on_conflict != "rename":
                    raise HostError(409, f"{name} exists", "file")
                stem, dot, ext = name.rpartition(".")
                n = 2
                while f"{stem} ({n}).{ext}".lower() in taken:
                    n += 1
                name = f"{stem} ({n}).{ext}"
            base = self._parent_path(parent_id)
            fid = id or self._id("x")
            obj = {"id": fid, "spaceId": space_id, "parentId": parent_id, "name": name,
                   "path": f"{base}/{name}" if base else name, "mime": mime or "application/octet-stream",
                   "size": len(data), "sha256": hashlib.sha256(data).hexdigest(), "createdBy": self.actor}
            self.items[fid] = {"kind": "file", "obj": obj}
            self.file_bytes[fid] = bytes(data)
            self.file_reads[fid] = 0
            return copy.deepcopy(obj)

    def add_file(self, name: str, data: bytes, mime: str = "text/html", parent_id: str = "") -> str:
        with self.as_user(USER):
            return self.upload_file(SPACE, data, name, parent_id=parent_id, mime=mime)["id"]

    def file_content(self, file_id: str) -> bytes:
        with self.lock:
            self._obj(file_id, "file")
            return self.file_bytes[file_id]

    def file_text(self, file_id: str) -> FileText:
        with self.lock:
            obj = self._obj(file_id, "file")
            self.file_reads[file_id] += 1
            data = self.file_bytes[file_id]
            if "pdf" in obj["mime"]:
                if self.file_reads[file_id] <= self.pdf_text_delay:
                    return FileText(text="", truncated=False)
                parts = re.findall(rb"\(((?:\\.|[^\\)])*)\)\s*Tj", data)
                text = "\n".join(re.sub(rb"\\(.)", rb"\1", p).decode("latin-1") for p in parts)
                return FileText(text=text, truncated=False)
            return FileText(text=data.decode("utf-8", "replace"), truncated=False)

    # links and search ------------------------------------------------------

    def _resolve_one(self, space_id: str, from_note_id: str, raw: str) -> dict | None:
        target, _, _ = wikilinks.parse_target(raw)
        entries = [e for e in self.items.values() if e["obj"]["spaceId"] == space_id and not e["obj"].get("deleted")
                   and e["kind"] in ("note", "file")]
        from_parent = ""
        if from_note_id in self.items:
            from_parent = self.items[from_note_id]["obj"].get("parentId") or ""
        groups: list[list[dict]] = []
        if "/" in target:
            groups.append([e for e in entries if e["obj"]["path"] in (target, target + ".md")])
            groups.append([e for e in entries if e["obj"]["path"].endswith(("/" + target, "/" + target + ".md"))])
        else:
            groups.append([e for e in entries if e["kind"] == "file" and e["obj"]["name"] == target])
            groups.append([e for e in entries if e["kind"] == "note"
                           and self._base_name(e["obj"])[:-3] == slugify(target)])
            groups.append([e for e in entries if e["kind"] == "note" and e["obj"]["title"].lower() == target.lower()])
        for group in groups:
            if group:
                group.sort(key=lambda e: ((e["obj"].get("parentId") or "") != from_parent, len(e["obj"]["path"]),
                                          e["obj"]["path"]))
                return group[0]
        return None

    def resolve(self, space_id: str, from_note_id: str, targets: list[str]) -> Page:
        self._fault("resolve")
        with self.lock:
            items = []
            for t in targets:
                e = self._resolve_one(space_id, from_note_id, t)
                items.append({"target": t, "kind": e["kind"] if e else "", "id": e["obj"]["id"] if e else "",
                              "path": e["obj"]["path"] if e else "", "url": ""})
            return Page(items=items)

    def outbound_links(self, note_id: str) -> Page:
        with self.lock:
            obj = self._obj(note_id, "note")
            return self.resolve(obj["spaceId"], note_id, wikilinks.targets(self.body(obj)))

    def backlinks(self, item_id: str) -> Page:
        self._fault("backlinks")
        with self.lock:
            target = self._obj(item_id)
            rows = []
            for e in list(self.items.values()):
                obj = e["obj"]
                if e["kind"] != "note" or obj.get("deleted") or obj["spaceId"] != target["spaceId"]:
                    continue
                for link in wikilinks.links(self.body(obj)):
                    hit = self._resolve_one(obj["spaceId"], obj["id"], link.raw)
                    if hit and hit["obj"]["id"] == item_id:
                        rows.append({"fromNote": self._stub(obj), "context": link.raw})
                        break
            return Page(items=rows)

    def search(self, q: str, *, mode: str | None = None, space: str | None = None, limit: int | None = None,
               cursor: str | None = None) -> Page:
        self._fault("search")
        if mode == "semantic" and not self.embeddings:
            raise HostError(400, "semantic search needs an embedding provider", "mode")
        tokens = re.findall(r"\w+", (q or "").lower())
        if not tokens:
            return Page(items=[])
        with self.lock:
            hits = []
            for e in self.items.values():
                obj = e["obj"]
                if e["kind"] != "note" or obj.get("deleted") or (space and obj["spaceId"] != space):
                    continue
                hay = (obj["title"] + "\n" + self.body(obj)).lower()
                found = [t for t in tokens if t in hay]
                if (mode == "semantic" and not found) or (mode != "semantic" and len(found) < len(tokens)):
                    continue
                score = sum(hay.count(t) for t in found) + (5 if q.lower() in obj["title"].lower() else 0)
                at = hay.find(found[0])
                snippet = " ".join((obj["title"] + "\n" + self.body(obj))[max(0, at - 60):at + 100].split())
                hits.append({"kind": "note", "note": self._stub(obj), "snippet": snippet, "score": float(score)})
            hits.sort(key=lambda h: (-h["score"], h["note"]["path"]))
            return Page(items=hits[: limit or 20])

    # suggestions -----------------------------------------------------------

    def create_suggestion(self, note_id: str, title: str, edits: list[dict], *, rationale: str | None = None) -> dict:
        self._fault("create_suggestion")
        with self.lock:
            note = self._obj(note_id, "note")
            if not title or not edits:
                raise HostError(400, "a suggestion needs a title and edits", "edits")
            body = self.body(note)
            stored = []
            for i, edit in enumerate(edits):
                anchor = edit.get("anchor")
                if not isinstance(anchor, dict) or set(anchor) - {"exact", "prefix", "suffix", "start", "end"}:
                    raise HostError(400, f"edit {i} has no valid anchor", "edits")
                if len(Body(anchor.get("exact", "")).u) > 5000 or anchor.get("start", 0) < 0 or anchor.get("end", 0) < 0:
                    raise HostError(400, f"edit {i} anchor is out of range", "edits")
                if not isinstance(edit.get("text"), str):
                    raise HostError(400, f"edit {i} has no text", "edits")
                if resolve_anchor(body, anchor) is None:
                    raise HostError(400, f"edit {i} anchor does not resolve", "edits")
                stored.append({"id": self._id("e"), "anchor": _json_copy(anchor), "text": edit["text"], "status": "open"})
            sid = self._id("s")
            self.suggestions[sid] = {"id": sid, "spaceId": note["spaceId"], "noteId": note_id,
                                     "authorKind": "plugin" if self.actor == PLUGIN_USER else "user",
                                     "authorId": self.actor, "title": title, "rationale": rationale or "",
                                     "edits": stored, "baseUpdatedAt": note["updatedAt"], "status": "open"}
            return copy.deepcopy(self.suggestions[sid])

    def withdraw_suggestion(self, suggestion_id: str) -> None:
        with self.lock:
            s = self.suggestions.get(suggestion_id)
            if s is None or s["status"] != "open":
                raise HostError(404, "suggestion not found")
            s["status"] = "withdrawn"

    def open_suggestions(self, note_id: str | None = None) -> list[dict]:
        return [copy.deepcopy(s) for s in self.suggestions.values()
                if s["status"] == "open" and (note_id is None or s["noteId"] == note_id)]

    def apply_suggestion(self, suggestion_id: str, user: str = USER) -> str:
        """Accept every open edit of a suggestion as a person. Return the new body."""
        with self.lock:
            s = self.suggestions[suggestion_id]
            note = self._obj(s["noteId"], "note")
            body = self.body(note)
            units = Body(body)
            spans = []
            for edit in s["edits"]:
                pos = resolve_anchor(body, edit["anchor"])
                if pos is None:
                    raise AssertionError(f"edit {edit['id']} is orphaned")
                spans.append((pos, edit["text"]))
            out = body
            for (start, end), text in sorted(spans, key=lambda x: x[0], reverse=True):
                out = units.slice(0, start) + text + units.slice(end, len(units)) if out is body else \
                    Body(out).slice(0, start) + text + Body(out).slice(end, len(Body(out)))
            s["status"] = "accepted"
            self.user_edit(s["noteId"], out, user)
            return out

    def create_comment(self, item_id: str, body: str, *, anchor: dict | None = None) -> dict:
        return {"id": self._id("c"), "itemId": item_id, "body": body}

    # AI and web ------------------------------------------------------------

    def ai_complete(self, request: dict) -> dict:
        self._fault("ai_complete")
        return self.ai.complete(request)

    def ai_models(self) -> list[AIModel]:
        return [AIModel.from_json(m) for m in self.models]

    def web_fetch(self, url: str, *, respect_robots: bool | None = None) -> dict:
        self._fault("web_fetch")
        import time
        from urllib.parse import urlsplit

        with self.lock:
            self.fetch_times.append(((urlsplit(url).hostname or ""), url, time.monotonic()))
        return self.web.fetch(url, respect_robots)

    # jobs ------------------------------------------------------------------

    def create_job(self, kind: str, *, title: str = "", payload: Any = None, space_id: str | None = None,
                   run_after: Any = None, parent_id: str | None = None) -> dict:
        self._fault("create_job")
        if kind not in JOB_KINDS:
            raise HostError(400, f"unknown job kind {kind}", "kind")
        with self.lock:
            jid = self._id("job")
            self.jobs[jid] = {"id": jid, "kind": kind, "title": title, "payload": _json_copy(payload or {}),
                              "spaceId": space_id or "", "status": "queued", "progress": 0, "total": 0,
                              "message": "", "runAfter": str(run_after or ""), "attempts": 0, "result": None,
                              "parentId": parent_id or "", "createdBy": f"plugin:{MANIFEST['id']}"}
            return copy.deepcopy(self.jobs[jid])

    def update_job(self, job_id: str, *, progress: int | None = None, total: int | None = None,
                   message: str | None = None, status: str | None = None, result: Any = _UNSET) -> dict:
        with self.lock:
            job = self.jobs.get(job_id)
            if job is None:
                raise HostError(404, "job not found")
            for key, value in (("progress", progress), ("total", total), ("message", message)):
                if value is not None:
                    job[key] = value
            if status is not None:
                job["status"] = status
            if result is not _UNSET:
                job["result"] = result
            return copy.deepcopy(job)

    def job_log(self, job_id: str, level: str, message: str) -> None:
        with self.lock:
            self.job_logs.setdefault(job_id, []).append((level, message))

    # storage ---------------------------------------------------------------

    def kv_entry(self, key: str) -> KVEntry | None:
        with self.lock:
            if key not in self.kv:
                return None
            return KVEntry(key=key, value=_json_copy(self.kv[key]), updated_at="2026-09-13T11:00:00Z")

    def kv_get(self, key: str, default: Any = None) -> Any:
        entry = self.kv_entry(key)
        return default if entry is None else entry.value

    def kv_put(self, key: str, value: Any) -> None:
        self._fault("kv_put")
        raw = json.dumps(value)
        if len(raw.encode()) > 1 << 20:
            raise HostError(413, "value exceeds 1 MiB", "value")
        with self.lock:
            self.kv[key] = json.loads(raw)

    def kv_delete(self, key: str) -> None:
        with self.lock:
            if key not in self.kv:
                raise HostError(404, "key not found")
            del self.kv[key]

    def kv_list(self, prefix: str = "", *, cursor: str | None = None) -> Page:
        with self.lock:
            keys = sorted(k for k in self.kv if k.startswith(prefix) and (not cursor or k > cursor))
            page = keys[:KV_PAGE]
            items = [{"key": k, "size": len(json.dumps(self.kv[k])), "updatedAt": "2026-09-13T11:00:00Z"} for k in page]
            return Page(items=items, next_cursor=page[-1] if len(keys) > KV_PAGE else None)

    def kv_keys(self, prefix: str = "") -> Iterator[dict]:
        cursor = None
        while True:
            page = self.kv_list(prefix, cursor=cursor)
            yield from page.items
            if not page.next_cursor:
                return
            cursor = page.next_cursor

    def blob_put(self, key: str, data: Any, *, content_type: str = "application/octet-stream") -> None:
        with self.lock:
            self.blobs[key] = bytes(data if isinstance(data, (bytes, bytearray)) else data.read())

    def blob_get(self, key: str) -> bytes | None:
        with self.lock:
            return self.blobs.get(key)

    def blob_delete(self, key: str) -> None:
        with self.lock:
            if key not in self.blobs:
                raise HostError(404, "blob not found")
            del self.blobs[key]

    def secret(self, name: str) -> str:
        if name not in self.secrets:
            raise HostError(404, f"secret {name} is not set")
        return self.secrets[name]

    def notify(self, message: str, **kwargs: Any) -> dict:
        self.notifications.append({"message": message, **kwargs})
        return {"ok": True}

    def emit(self, name: str, data: Any = None, *, space_id: str | None = None) -> dict:
        self.emitted.append({"type": name, "data": data, "spaceId": space_id})
        return {"ok": True}
