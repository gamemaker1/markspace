"""Per-host pacing and a resizable concurrency limit for fetches."""

from __future__ import annotations

import threading
import time
from contextlib import contextmanager
from typing import Callable, Iterator


class HostGate:
    """Space requests to one host at least interval seconds apart.

    wait reserves the next free slot for a host and sleeps until it arrives.
    Reservations are made under a lock, so concurrent callers queue in order.
    """

    def __init__(self, interval: float = 1.0, clock: Callable[[], float] = time.monotonic,
                 sleep: Callable[[float], None] = time.sleep) -> None:
        self.interval = interval
        self.clock = clock
        self.sleep = sleep
        self._next: dict[str, float] = {}
        self._lock = threading.Lock()

    def reserve(self, host: str) -> float:
        """Reserve a slot and return the seconds to wait before using it."""
        with self._lock:
            now = self.clock()
            at = max(now, self._next.get(host, 0.0))
            self._next[host] = at + self.interval
            if len(self._next) > 10000:
                for key in [k for k, v in self._next.items() if v < now][:5000]:
                    del self._next[key]
            return at - now

    def wait(self, host: str) -> None:
        delay = self.reserve(host)
        if delay > 0:
            self.sleep(delay)


class Slots:
    """A counting limit whose size can change between acquisitions."""

    def __init__(self) -> None:
        self._cond = threading.Condition()
        self._busy = 0

    @contextmanager
    def acquire(self, limit: int) -> Iterator[None]:
        limit = max(1, int(limit))
        with self._cond:
            while self._busy >= limit:
                self._cond.wait()
            self._busy += 1
        try:
            yield
        finally:
            with self._cond:
                self._busy -= 1
                self._cond.notify()
