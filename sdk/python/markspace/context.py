"""Objects that plugin handlers receive."""

from __future__ import annotations

import threading
import time
from dataclasses import dataclass, field
from typing import Any

from .errors import JobCanceled
from .host import Host

LOG_LEVELS = ("debug", "info", "warn", "error")


@dataclass
class User:
    """The user who ran a command, opened a panel or posted an action."""

    id: str = ""
    name: str = ""

    @classmethod
    def from_json(cls, data: Any) -> "User":
        if not isinstance(data, dict):
            return cls()
        return cls(id=str(data.get("id") or ""), name=str(data.get("name") or ""))


@dataclass
class Event:
    """One event from Markspace. raw holds the event as received."""

    id: str
    type: str
    space_id: str = ""
    item_id: str = ""
    kind: str = ""
    actor_id: str = ""
    path: str = ""
    old_path: str = ""
    data: Any = None
    at: str = ""
    raw: dict = field(default_factory=dict, repr=False)

    @classmethod
    def from_json(cls, data: dict) -> "Event":
        return cls(
            id=str(data.get("id") or ""),
            type=str(data.get("type") or ""),
            space_id=str(data.get("spaceId") or ""),
            item_id=str(data.get("itemId") or ""),
            kind=str(data.get("kind") or ""),
            actor_id=str(data.get("actorId") or ""),
            path=str(data.get("path") or ""),
            old_path=str(data.get("oldPath") or ""),
            data=data.get("data"),
            at=str(data.get("at") or ""),
            raw=data,
        )


@dataclass
class EventBatch:
    """The events of one delivery that match the handler's pattern, in delivery order."""

    events: list[Event]
    pattern: str
    delivery_id: str
    host: Host


@dataclass
class JobInfo:
    """The job Markspace asked the plugin to run."""

    id: str
    kind: str
    payload: Any
    attempt: int
    title: str = ""
    space_id: str = ""


@dataclass
class JobContext:
    """The context of one job attempt.

    host sends the acting token when a user started the job, and the plugin
    token otherwise. user is None when no user started the job.
    """

    job: JobInfo
    host: Host
    delivery_id: str = ""
    user: User | None = None
    _canceled: threading.Event = field(default_factory=threading.Event, repr=False)
    _lock: threading.Lock = field(default_factory=threading.Lock, repr=False)
    _last: tuple[int, int, float] = field(default=(0, 0, 0.0), repr=False)

    @property
    def payload(self) -> Any:
        return self.job.payload

    @property
    def attempt(self) -> int:
        return self.job.attempt

    def progress(self, done: int, total: int | None = None, message: str | None = None) -> None:
        """Report progress. It also renews the job lease and notices a cancel."""
        resp = self.host.update_job(self.job.id, progress=done, total=total, message=message)
        with self._lock:
            self._last = (done, self._last[1] if total is None else total, time.monotonic())
        if isinstance(resp, dict) and resp.get("status") == "canceled":
            self._canceled.set()

    def log(self, level: str, message: str) -> None:
        """Append a line to the job log. level is debug, info, warn or error."""
        if level not in LOG_LEVELS:
            raise ValueError(f"log level must be one of {', '.join(LOG_LEVELS)}")
        self.host.job_log(self.job.id, level, message)

    @property
    def canceled(self) -> bool:
        """Report whether Markspace canceled the job, as seen by the last progress call."""
        return self._canceled.is_set()

    def check_canceled(self) -> None:
        """Raise JobCanceled when the job was canceled."""
        if self._canceled.is_set():
            raise JobCanceled(self.job.id)

    def cancel(self) -> None:
        """Mark the job canceled. The SDK and tests call it."""
        self._canceled.set()

    def last_progress(self) -> tuple[int, int, float]:
        """Return the last reported done, total and monotonic report time."""
        with self._lock:
            return self._last


@dataclass
class CommandContext:
    """The context of a command. host sends the acting token of this interaction."""

    id: str
    context: dict
    user: User
    host: Host
    delivery_id: str = ""

    @property
    def space_id(self) -> str:
        return str(self.context.get("spaceId") or "")

    @property
    def item_id(self) -> str:
        return str(self.context.get("itemId") or "")

    @property
    def kind(self) -> str:
        return str(self.context.get("kind") or "")


@dataclass
class PanelContext(CommandContext):
    """The context of a panel request."""


@dataclass
class ActionContext(CommandContext):
    """The context of an action. values holds the form values, payload the action payload."""

    values: dict = field(default_factory=dict)
    payload: dict = field(default_factory=dict)


@dataclass
class ImportContext:
    """The context of an import. host sends the acting token."""

    id: str
    file_id: str
    space_id: str
    user: User
    host: Host
    delivery_id: str = ""
