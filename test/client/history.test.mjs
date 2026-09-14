// Tests for the version history helpers in panels.js: the day grouping of
// the timeline, clock and relative times, and the line change summary.
import { test } from "node:test";
import assert from "node:assert/strict";
import { register } from "node:module";

// Browser modules import each other as /static/js/...; map that to the files.
register("data:text/javascript," + encodeURIComponent(`
export async function resolve(specifier, context, next) {
  if (specifier.startsWith("/static/")) {
    return { url: new URL("../../internal/web/static/" + specifier.slice(8), ${JSON.stringify(import.meta.url)}).href, shortCircuit: true };
  }
  return next(specifier, context);
}`));

const {
  historyDayLabel, groupVersionsByDay, clockTime, versionWhen, timeSince, lineChanges, formatLineChanges, HISTORY_RAIL_ROWS, HISTORY_PAGE,
} = await import("../../internal/web/static/js/panels.js");

// Monday, Sep 14, 2026, 15:00 local time.
const NOW = new Date(2026, 8, 14, 15, 0).getTime();
const at = (month, day, hour = 12, minute = 0, year = 2026) => new Date(year, month - 1, day, hour, minute).getTime();

test("history days read Today, Yesterday, a weekday within the week, then a date", () => {
  assert.equal(historyDayLabel(at(9, 14, 9, 5), NOW), "Today");
  assert.equal(historyDayLabel(at(9, 14, 23, 59), NOW), "Today", "a later time today is still today");
  assert.equal(historyDayLabel(at(9, 13, 23, 59), NOW), "Yesterday");
  assert.equal(historyDayLabel(at(9, 13, 0, 1), NOW), "Yesterday");
  assert.equal(historyDayLabel(at(9, 11), NOW), "Friday");
  assert.equal(historyDayLabel(at(9, 8), NOW), "Tuesday");
  assert.equal(historyDayLabel(at(9, 7), NOW), "Sep 7", "seven days back is a date");
  assert.equal(historyDayLabel(at(12, 30, 12, 0, 2025), NOW), "Dec 30, 2025", "another year names the year");
  assert.equal(historyDayLabel(new Date(at(9, 12)).toISOString(), NOW), "Saturday", "ISO strings work");
  assert.equal(historyDayLabel("not a date", NOW), "Earlier");
});

test("versions group by local day and keep their newest-first order", () => {
  const entries = [
    { id: "current", createdAt: at(9, 14, 14, 32) },
    { id: "a", createdAt: at(9, 14, 9, 0) },
    { id: "b", createdAt: at(9, 13, 18, 7) },
    { id: "c", createdAt: at(9, 13, 8, 17) },
    { id: "d", createdAt: at(9, 10, 10, 0) },
    { id: "e", createdAt: at(8, 20, 10, 0) },
    { id: "f", createdAt: at(8, 20, 9, 0) },
  ];
  const groups = groupVersionsByDay(entries, NOW);
  assert.deepEqual(groups.map((g) => g.label), ["Today", "Yesterday", "Thursday", "Aug 20"]);
  assert.deepEqual(groups.map((g) => g.entries.map((e) => e.id)), [["current", "a"], ["b", "c"], ["d"], ["e", "f"]]);
  assert.deepEqual(groupVersionsByDay([], NOW), []);
});

test("each point in time shows its clock time and full date", () => {
  assert.equal(clockTime(at(9, 14, 14, 32)), "14:32");
  assert.equal(clockTime(at(9, 14, 9, 5)), "09:05");
  assert.equal(clockTime("nope"), "");
  assert.equal(versionWhen(at(9, 14, 14, 32)), "Monday, Sep 14, 2026, 14:32");
  assert.equal(timeSince(NOW - 20e3, NOW), "Just now");
  assert.equal(timeSince(NOW - 60e3, NOW), "1 minute ago");
  assert.equal(timeSince(NOW - 3 * 3600e3, NOW), "3 hours ago");
  assert.equal(timeSince(NOW - 2 * 86400e3, NOW), "2 days ago");
});

test("the change summary counts added and removed lines", () => {
  assert.deepEqual(lineChanges("a\nb\nc\n", "a\nc\nd\ne\n"), { added: 2, removed: 1 });
  assert.deepEqual(lineChanges("same\ntext", "same\ntext\n"), { added: 0, removed: 0 }, "a final newline is not a change");
  assert.deepEqual(lineChanges("", "one\ntwo"), { added: 2, removed: 0 });
  assert.deepEqual(lineChanges("one\ntwo", ""), { added: 0, removed: 2 });
  assert.deepEqual(lineChanges("x\nx\ny", "x\ny\ny"), { added: 1, removed: 1 }, "repeated lines count once each");
  assert.equal(formatLineChanges({ added: 12, removed: 3 }), "+12 −3 lines");
  assert.equal(formatLineChanges({ added: 1, removed: 0 }), "+1 −0 line");
  assert.equal(formatLineChanges({ added: 0, removed: 0 }), "No line changes");
});

test("the rail shows five entries and the Dialog adds versions a page at a time", () => {
  assert.equal(HISTORY_RAIL_ROWS, 5);
  assert.ok(HISTORY_PAGE >= 10 && HISTORY_PAGE < 40, "40 versions need more than one page");
});
