"""Python SDK for Markspace plugins.

A plugin is an HTTP service. Markspace signs every call to it, and the plugin
calls back through the host API. docs/plugins.md defines the protocol, and
test/vectors/protocol.json holds the conformance vectors.
"""

from . import ids, ui
from .context import (ActionContext, CommandContext, Event, EventBatch, ImportContext, JobContext, JobInfo,
                      PanelContext, User)
from .errors import HostError, JobCanceled, SignatureError, ValidationError
from .host import AIModel, FileText, Host, KVEntry, Page, StreamEvent, parse_sse
from .install import Credentials, InstallStore
from .plugin import Plugin, validate_manifest
from .responses import job, navigate, open_panel, toast, ui_response, validate_command_response, validate_install
from .signature import (ReplayGuard, decode_secret, parse_signature, sign, sign_headers, signed_message, verify,
                        verify_request)

__version__ = "0.1.0"

__all__ = [
    "AIModel", "ActionContext", "CommandContext", "Credentials", "Event", "EventBatch", "FileText", "Host", "HostError", "ImportContext",
    "InstallStore", "JobCanceled", "JobContext", "JobInfo", "KVEntry", "Page", "PanelContext", "Plugin", "ReplayGuard",
    "SignatureError", "StreamEvent", "User", "ValidationError", "decode_secret", "ids", "job", "navigate",
    "open_panel", "parse_signature", "parse_sse", "sign", "sign_headers", "signed_message", "toast", "ui", "ui_response",
    "validate_command_response", "validate_install", "validate_manifest", "verify", "verify_request",
]
