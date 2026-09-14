// Tests for the helpers of ai/admin.js: price formatting and parsing, the
// order of route targets and the request body of a route save.
import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "../fakedom.mjs";

installDOM();
const m = await import("../../../internal/web/static/js/ai/admin.js");

const t = (value, label = value) => ({ value, label, provider: "P", price: "free", missing: false });

test("usd keeps two decimals and trims float noise", () => {
  assert.equal(m.usd(0), "$0");
  assert.equal(m.usd(2.5), "$2.50");
  assert.equal(m.usd(0.075), "$0.075");
  assert.equal(m.usd(2.4999999999999996), "$2.50");
});

test("priceLabels and chipPrice describe unknown, free and priced models", () => {
  assert.deepEqual(m.priceLabels({}), ["Unknown", "Unknown"]);
  assert.deepEqual(m.priceLabels({ effectivePrice: { inputPerMTok: 0, outputPerMTok: 0 } }), ["Free", "Free"]);
  assert.equal(m.chipPrice({ effectivePrice: { inputPerMTok: 2.5, outputPerMTok: 10 } }), "$2.50 in, $10 out per M");
  assert.equal(m.chipPrice({}), "price unknown");
});

test("sourceLabel adds the fetch date for public catalogs only", () => {
  assert.equal(m.sourceLabel({ effectiveSource: "openrouter", priceFetchedAt: "2026-09-13T08:00:00Z" }), "OpenRouter, 13 Sept");
  assert.equal(m.sourceLabel({ effectiveSource: "override", priceFetchedAt: "2026-09-13T08:00:00Z" }), "Override");
  assert.equal(m.sourceLabel({ effectiveSource: "mystery" }), "Unknown");
});

test("moveItem moves one target and leaves the input untouched", () => {
  const list = [t("a|1"), t("b|2"), t("c|3")];
  assert.deepEqual(m.moveItem(list, 0, 2).map((x) => x.value), ["b|2", "c|3", "a|1"]);
  assert.deepEqual(m.moveItem(list, 2, 1).map((x) => x.value), ["a|1", "c|3", "b|2"]);
  assert.deepEqual(m.moveItem(list, 0, 5).map((x) => x.value), ["a|1", "b|2", "c|3"]);
  assert.deepEqual(list.map((x) => x.value), ["a|1", "b|2", "c|3"]);
});

test("sameTargets compares order, not identity", () => {
  assert.equal(m.sameTargets([t("a|1"), t("b|2")], [t("a|1"), t("b|2")]), true);
  assert.equal(m.sameTargets([t("a|1"), t("b|2")], [t("b|2"), t("a|1")]), false);
  assert.equal(m.sameTargets([t("a|1")], []), false);
});

test("targetPayload splits at the first bar, so model ids keep theirs", () => {
  assert.deepEqual(m.targetPayload([t("or|openai/gpt-4o"), t("x|a|b")]), [
    { providerId: "or", model: "openai/gpt-4o" },
    { providerId: "x", model: "a|b" },
  ]);
});

test("parsePrice accepts 0 or more and refuses the rest", () => {
  assert.deepEqual(m.parsePrice(""), { value: 0 });
  assert.deepEqual(m.parsePrice(" 2.5 "), { value: 2.5 });
  assert.ok(m.parsePrice("-1").error);
  assert.ok(m.parsePrice("lots").error);
  assert.ok(m.parsePrice("Infinity").error);
});

test("modelPath encodes each segment of a model id", () => {
  assert.equal(m.modelPath("openai/gpt 4o"), "openai/gpt%204o");
});
