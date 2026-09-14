"""Install payload storage and credential resolution.

POST /install stores the payload in $PLUGIN_DATA/install.json with mode 0600.
The environment variables MARKSPACE_URL, MARKSPACE_TOKEN and
MARKSPACE_SIGNING_SECRET override the stored values one by one.
"""

from __future__ import annotations

import json
import os
import tempfile
from dataclasses import dataclass, field
from typing import Any, Mapping

from .signature import decode_secret

DEFAULT_DATA_DIR = "/data"
INSTALL_FILE = "install.json"
ENV_URL = "MARKSPACE_URL"
ENV_TOKEN = "MARKSPACE_TOKEN"
ENV_SECRET = "MARKSPACE_SIGNING_SECRET"
ENV_DATA = "PLUGIN_DATA"


@dataclass
class Credentials:
    """The values a plugin needs to call Markspace and to check its requests."""

    markspace_url: str | None = None
    token: str | None = None
    signing_secret: bytes | None = None
    grants: list[dict[str, Any]] = field(default_factory=list)
    permissions: list[str] = field(default_factory=list)
    from_env: tuple[str, ...] = ()

    @property
    def installed(self) -> bool:
        """Report whether the URL, the token and the signing secret are all known."""
        return bool(self.markspace_url and self.token and self.signing_secret)


class InstallStore:
    """Read and write install.json in a data directory."""

    def __init__(self, data_dir: str, env: Mapping[str, str] | None = None) -> None:
        self.data_dir = data_dir
        self.env = os.environ if env is None else env

    @property
    def path(self) -> str:
        return os.path.join(self.data_dir, INSTALL_FILE)

    def load(self) -> dict[str, Any] | None:
        """Return the stored payload, or None when no install exists."""
        try:
            with open(self.path, "rb") as f:
                data = json.load(f)
        except FileNotFoundError:
            return None
        except (OSError, ValueError) as exc:
            raise ValueError(f"cannot read {self.path}: {exc}") from None
        if not isinstance(data, dict):
            raise ValueError(f"{self.path} does not hold a JSON object")
        return data

    def save(self, payload: dict[str, Any]) -> None:
        """Write payload atomically with mode 0600."""
        os.makedirs(self.data_dir, mode=0o700, exist_ok=True)
        raw = json.dumps(payload, indent=2, sort_keys=True).encode("utf-8") + b"\n"
        fd, tmp = tempfile.mkstemp(prefix=".install.json.tmp-", dir=self.data_dir)
        try:
            os.fchmod(fd, 0o600)
            with os.fdopen(fd, "wb") as f:
                f.write(raw)
                f.flush()
                os.fsync(f.fileno())
            os.replace(tmp, self.path)
        except BaseException:
            try:
                os.unlink(tmp)
            except FileNotFoundError:
                pass
            raise
        try:
            dfd = os.open(self.data_dir, os.O_RDONLY)
        except OSError:
            return
        try:
            os.fsync(dfd)
        except OSError:
            pass
        finally:
            os.close(dfd)

    def credentials(self) -> Credentials:
        """Merge install.json with the environment. Raise ValueError on a bad secret."""
        stored = self.load() or {}
        creds = Credentials(
            markspace_url=stored.get("markspaceUrl") or None,
            token=stored.get("token") or None,
            grants=list(stored.get("grants") or []),
            permissions=list(stored.get("permissions") or []),
        )
        secret_text = stored.get("signingSecret") or None
        used = []
        if self.env.get(ENV_URL):
            creds.markspace_url = self.env[ENV_URL]
            used.append(ENV_URL)
        if self.env.get(ENV_TOKEN):
            creds.token = self.env[ENV_TOKEN]
            used.append(ENV_TOKEN)
        if self.env.get(ENV_SECRET):
            secret_text = self.env[ENV_SECRET]
            used.append(ENV_SECRET)
        if secret_text:
            try:
                creds.signing_secret = decode_secret(secret_text)
            except ValueError as exc:
                source = ENV_SECRET if ENV_SECRET in used else self.path
                raise ValueError(f"{source}: {exc}") from None
        creds.from_env = tuple(used)
        return creds
