"""Error types shared by the SDK modules."""

from __future__ import annotations


class ValidationError(ValueError):
    """A value breaks a protocol rule.

    code is one of the error codes in test/vectors/protocol.json. path
    names the offending value, such as $.children[0].title.
    """

    def __init__(self, code: str, path: str, message: str = "") -> None:
        self.code = code
        self.path = path
        self.message = message or code.replace("_", " ")
        super().__init__(f"{code} at {path}: {self.message}")


class SignatureError(Exception):
    """A signed request failed verification.

    code is malformed, mismatch, expired, missing_delivery, replay or
    wrong_plugin.
    """

    def __init__(self, code: str, message: str = "") -> None:
        self.code = code
        self.message = message or code.replace("_", " ")
        super().__init__(f"{code}: {self.message}")


class HostError(Exception):
    """A host API call failed.

    status is the HTTP status, or 0 when no response arrived. field names the
    request field the server rejected, when it named one. code is the error code
    the server sent, such as robots_disallowed.
    """

    def __init__(self, status: int, message: str, field: str | None = None, code: str | None = None) -> None:
        self.status = status
        self.message = message
        self.field = field
        self.code = code
        text = f"host API {status}: {message}"
        if field:
            text += f" (field {field})"
        super().__init__(text)


class JobCanceled(Exception):
    """The job was canceled in Markspace. Handlers may raise it to stop early."""
