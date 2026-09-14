"""Shared test helpers: import paths, fixtures, a job context and an in-process harness."""

from __future__ import annotations

import itertools
import json
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

TESTS = Path(__file__).resolve().parent
PLUGIN = TESTS.parent
ROOT = PLUGIN.parents[1]
FIXTURES = TESTS / "fixtures"

for path in (ROOT / "sdk" / "python", PLUGIN / "source", TESTS):
    if str(path) not in sys.path:
        sys.path.insert(0, str(path))

from markspace import JobInfo  # noqa: E402

from wiki.manifest import MANIFEST  # noqa: E402
from wiki.ratelimit import HostGate  # noqa: E402
from wiki.service import Service  # noqa: E402
from fakehost import SPACE, USER, FakeMarkspace  # noqa: E402

MAX_ATTEMPTS = {j["kind"]: j.get("maxAttempts", 3) for j in MANIFEST["jobs"]}
START = datetime(2026, 9, 13, 11, 0, tzinfo=timezone.utc)

USER_NOTES = {
    "Storage design notes": (
        "# Storage design notes\n\n"
        "We picked SQLite for the local cache. Write-ahead logging keeps readers unblocked while one writer "
        "commits.\n\nPostgreSQL stays the server database. See `SQLite` docs later.\n"),
    "Reading list 2026": (
        "Read the Raft paper again before the consensus review 😀.\n\n"
        "The Transformer architecture keeps coming up in the LLM work.\n"),
    "Homelab services": (
        "Services run as Podman quadlets under systemd.\n\n"
        "Kubernetes felt too heavy for one machine.\n"),
}


def fixture_text(*parts: str) -> str:
    return FIXTURES.joinpath(*parts).read_text(encoding="utf-8")


def fixture_json(*parts: str):
    return json.loads(fixture_text(*parts))


class FakeClock:
    """A settable clock that returns aware UTC datetimes."""

    def __init__(self, start: datetime = START) -> None:
        self.now = start

    def __call__(self) -> datetime:
        return self.now

    def advance(self, **kwargs) -> None:
        self.now += timedelta(**kwargs)


class JobCtx:
    """The JobContext members the jobs use, backed by the fake host."""

    def __init__(self, fake: FakeMarkspace, job: dict) -> None:
        self.fake = fake
        self.job = JobInfo(id=job["id"], kind=job["kind"], payload=job["payload"], attempt=job["attempts"],
                           title=job["title"], space_id=job["spaceId"])
        self.logs: list[tuple[str, str]] = []
        self.canceled = False

    @property
    def payload(self):
        return self.job.payload

    @property
    def attempt(self) -> int:
        return self.job.attempt

    def progress(self, done, total=None, message=None) -> None:
        self.fake.update_job(self.job.id, progress=done, total=total, message=message)

    def log(self, level: str, message: str) -> None:
        self.logs.append((level, message))
        self.fake.job_log(self.job.id, level, message)

    def check_canceled(self) -> None:
        pass


class Harness:
    """Runs the plugin service against the in-memory fake host, with jobs executed in process."""

    def __init__(self, *, settings: dict | None = None, **fake_options) -> None:
        from wiki.app import JOBS

        self.jobs = JOBS
        self.fake = FakeMarkspace(**fake_options)
        self.clock = FakeClock()
        self.settings = {"space": SPACE, **(settings or {})}
        ticks = itertools.count(0, 60)
        # Each settings read sees a fresh cache, so tests can change self.settings at any time.
        self.svc = Service(lambda: self.fake, install_settings_fn=lambda: self.settings, clock=self.clock,
                           gate=HostGate(0.0), monotonic=lambda: float(next(ticks)))
        self.ctxs: list[JobCtx] = []
        self.errors: list[tuple[dict, BaseException]] = []

    # jobs ------------------------------------------------------------------

    def run_job(self, job: dict):
        job["status"] = "running"
        job["attempts"] += 1
        ctx = JobCtx(self.fake, job)
        self.ctxs.append(ctx)
        try:
            result = self.jobs[job["kind"]](self.svc, ctx)
        except Exception as exc:  # noqa: BLE001 - the fake queue retries like Markspace
            self.errors.append((dict(job), exc))
            job["status"] = "queued" if job["attempts"] < MAX_ATTEMPTS[job["kind"]] else "failed"
            job["message"] = f"{type(exc).__name__}: {exc}"
            return exc
        job["status"] = "done"
        job["result"] = result
        return result

    def run(self, kind: str, payload: dict | None = None, space_id: str = SPACE):
        """Queue one job and run it now. Return its result."""
        job = self.fake.create_job(kind, title=kind, payload=payload or {}, space_id=space_id)
        return self.run_job(self.fake.jobs[job["id"]])

    def run_jobs(self, *, only: tuple[str, ...] | None = None, exclude: tuple[str, ...] = (), limit: int = 5000) -> int:
        """Run queued jobs in creation order, ignoring runAfter, until none is left. Return the count."""
        count = 0
        while count < limit:
            job = next((j for j in self.fake.jobs.values() if j["status"] == "queued"
                        and (only is None or j["kind"] in only) and j["kind"] not in exclude), None)
            if job is None:
                return count
            self.run_job(job)
            count += 1
        raise AssertionError("jobs did not settle")

    def queued(self, kind: str | None = None) -> list[dict]:
        return [j for j in self.fake.jobs.values() if j["status"] == "queued" and (kind is None or j["kind"] == kind)]

    # content ---------------------------------------------------------------

    def seed_user_notes(self, notes: dict[str, str] | None = None) -> dict[str, str]:
        ids = {}
        with self.fake.as_user(USER):
            folder = self.fake.create_folder(SPACE, "Projects")["id"]
            for title, body in (notes or USER_NOTES).items():
                ids[title] = self.fake.create_note(SPACE, title, body, parent_id=folder)["id"]
        return ids

    def bookmarks(self, ids: list[str]) -> list[dict]:
        return [self.fake.web.bookmark(i) for i in ids]

    def import_pages(self, ids: list[str]):
        return self.run("import", {"importer": "urls", "bookmarks": self.bookmarks(ids)})

    def pipeline(self, ids: list[str], exclude: tuple[str, ...] = ("link-suggest",)):
        self.import_pages(ids)
        self.run_jobs(exclude=exclude)

    def source(self, page_id: str) -> dict:
        from wiki.urls import normalise_url

        url = self.fake.web.by_id[page_id]["url"]
        note_id = self.svc.state.url_note(normalise_url(url))
        assert note_id, f"no source for {page_id}"
        return self.svc.state.source(note_id)

    def note(self, note_id: str) -> dict:
        return self.fake.note(note_id)

    def page(self, title: str) -> dict:
        page_id = self.svc.state.alias(title)
        assert page_id, f"no page {title}"
        return self.fake.note(page_id)

    def snapshot(self) -> dict[str, tuple[str, str]]:
        return {n["id"]: (n["updatedAt"], n["content"]) for n in self.fake.all_notes()}
