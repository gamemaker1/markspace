"""Model calls with JSON answers and one repair call.

Every call sends task custom and route plugin.bookmark-wiki.<purpose>. The
answer is parsed with extract_json and checked by a validate function. When
parsing or the check fails, the client sends the repair prompt once with the
problems and the previous answer. A second failure raises ModelOutputError.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Any, Callable, TypeVar

from . import prompts
from .jsonparse import ModelOutputError, extract_json
from .manifest import PLUGIN_ID, route
from .settings import model_choice

log = logging.getLogger("wiki.ai")

T = TypeVar("T")
MAX_PREVIOUS = 24000
INSTRUCTION = "Follow the task description at the start of the text. Answer with the JSON object it asks for and nothing else."


@dataclass
class CallInfo:
    calls: int = 0
    repaired: bool = False
    model: str = ""
    input_tokens: int = 0
    output_tokens: int = 0


class ModelClient:
    def __init__(self, host_fn: Callable[[], Any], state: Any) -> None:
        self._host_fn = host_fn
        self.state = state

    def _call(self, purpose: str, system: str, user: str, space_id: str | None, model_spec: str,
              info: CallInfo) -> str:
        # Markspace keeps the first 1000 characters of a custom instruction, so the
        # prompt goes in text, which has the whole context budget.
        request: dict[str, Any] = {
            "task": "custom",
            "route": route(purpose),
            "feature": f"{PLUGIN_ID}.{purpose}",
            "instruction": INSTRUCTION,
            "text": system.rstrip("\n") + "\n\n" + user,
        }
        if space_id:
            request["spaceId"] = space_id
        choice = model_choice(model_spec)
        if choice:
            request["providerId"], request["model"] = choice
        result = self._host_fn().ai_complete(request) or {}
        info.calls += 1
        info.model = str(result.get("model") or info.model)
        pin = int(result.get("promptTokens") or 0)
        pout = int(result.get("completionTokens") or 0)
        info.input_tokens += pin
        info.output_tokens += pout
        try:
            self.state.add_usage(purpose, pin, pout)
        except Exception as exc:  # noqa: BLE001 - usage accounting must not fail the call
            log.warning("recording AI usage failed: %s", exc)
        return str(result.get("text") or "")

    def complete_json(self, purpose: str, prompt: str, values: dict[str, Any], validate: Callable[[Any], T], *,
                      space_id: str | None = None, model_spec: str = "") -> tuple[T, CallInfo]:
        """Run a prompt and return the validated answer and call information."""
        info = CallInfo()
        system, user = prompts.render(prompt, **values)
        text = self._call(purpose, system, user, space_id, model_spec, info)
        try:
            return validate(extract_json(text)), info
        except ModelOutputError as exc:
            problems = exc.problems
            log.info("%s answer failed its checks, sending one repair call: %s", purpose, "; ".join(problems))
        info.repaired = True
        r_system, r_user = prompts.render(
            "repair", schema=prompts.schema_text(prompt), problems="\n".join(f"- {p}" for p in problems),
            previous=text[:MAX_PREVIOUS], original=user)
        text = self._call(purpose, r_system, r_user, space_id, model_spec, info)
        try:
            return validate(extract_json(text)), info
        except ModelOutputError as exc:
            raise ModelOutputError(f"{purpose} answer failed after one repair call", exc.problems) from None
