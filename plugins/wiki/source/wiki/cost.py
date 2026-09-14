"""The cost estimate shown before an import is queued.

Fetch and summarise count 3000 input and 500 output tokens per source.
Compile counts 6000 input and 1500 output tokens per page, times the average
pages per source over the last 100 compiles, or 8 before any. Prices come from
GET /api/plugin/v1/ai/models. A setting with no model uses the first listed
model that has a price, because the route default is not visible to plugins.
"""

from __future__ import annotations

from typing import Any

from markspace import HostError

from .settings import model_choice

SUMMARISE_IN, SUMMARISE_OUT = 3000, 500
COMPILE_IN, COMPILE_OUT = 6000, 1500
DEFAULT_PAGES_PER_SOURCE = 8.0


def price_for(models: list[Any], spec: str) -> tuple[float, float, str] | None:
    """Return (input per MTok, output per MTok, label) for a model setting, or None when no price is known."""
    choice = model_choice(spec)
    for m in models:
        if choice and (m.provider_id, m.model) != choice:
            continue
        prices = m.prices if isinstance(m.prices, dict) else {}
        pin, pout = prices.get("inputPerMTok"), prices.get("outputPerMTok")
        if isinstance(pin, (int, float)) and isinstance(pout, (int, float)) and pin >= 0 and pout >= 0:
            return float(pin), float(pout), f"{m.model} ({m.provider_id})"
    return None


def estimate(svc: Any, new_urls: int, models: list[Any] | None = None) -> dict:
    avg = svc.state.average_pages_per_source(DEFAULT_PAGES_PER_SOURCE)
    s_in, s_out = new_urls * SUMMARISE_IN, new_urls * SUMMARISE_OUT
    pages = new_urls * avg
    c_in, c_out = pages * COMPILE_IN, pages * COMPILE_OUT
    if models is None:
        try:
            models = svc.host.ai_models()
        except HostError:
            models = []
    settings = svc.settings()
    sp = price_for(models, settings.summariseModel)
    cp = price_for(models, settings.compileModel)
    cost = None
    if sp and cp:
        cost = (s_in * sp[0] + s_out * sp[1] + c_in * cp[0] + c_out * cp[1]) / 1_000_000
    return {
        "urls": new_urls,
        "pagesPerSource": round(avg, 1),
        "inputTokens": int(round(s_in + c_in)),
        "outputTokens": int(round(s_out + c_out)),
        "cost": None if cost is None else round(cost, 2),
        "summariseModel": sp[2] if sp else "",
        "compileModel": cp[2] if cp else "",
    }


def describe(est: dict) -> str:
    text = (f"{est['urls']:,} new URLs. The estimate is {est['inputTokens']:,} input and "
            f"{est['outputTokens']:,} output tokens, with {est['pagesPerSource']} wiki pages per source.")
    if est["cost"] is None:
        return text + " No price is known for the models, so the cost is unknown."
    models = est["summariseModel"]
    if est["compileModel"] != est["summariseModel"]:
        models += f" and {est['compileModel']}"
    return text + f" The estimated cost is ${est['cost']:,.2f} with {models}."
