import { test } from "node:test";
import assert from "node:assert/strict";

import { viewerKind, languageFor, extensionOf, TEXT_MAX_BYTES, CSV_MAX_BYTES } from "../../internal/web/static/js/viewers/kind.js";
import { parseCsv, sniffDelimiter } from "../../internal/web/static/js/viewers/csv.js";
import { fitScale, zoomAt, clampPan, MIN_SCALE, MAX_SCALE } from "../../internal/web/static/js/viewers/image.js";
import { findMatches, pageText, pageAt, nextZoom, fitWidthScale, spanRanges, MIN_ZOOM, MAX_ZOOM } from "../../internal/web/static/js/viewers/pdf.js";

test("viewerKind picks a viewer from the media type and the extension", () => {
  const cases = [
    [{ name: "report.pdf", mime: "application/pdf" }, "pdf"],
    [{ name: "scan.PDF", mime: "application/octet-stream" }, "pdf"],
    [{ name: "east wall.jpg", mime: "image/jpeg" }, "image"],
    [{ name: "logo.svg", mime: "image/svg+xml" }, "image"],
    [{ name: "photo.png", mime: "" }, "image"],
    [{ name: "scan.tiff", mime: "image/tiff" }, "other"],
    [{ name: "clip.mp4", mime: "video/mp4" }, "video"],
    [{ name: "clip.webm", mime: "" }, "video"],
    [{ name: "memo.m4a", mime: "audio/mp4" }, "audio"],
    [{ name: "song.mp3", mime: "" }, "audio"],
    [{ name: "data.csv", mime: "text/csv" }, "csv"],
    [{ name: "data.tsv", mime: "text/plain" }, "csv"],
    [{ name: "notes.txt", mime: "text/plain" }, "text"],
    [{ name: "config.yaml", mime: "application/octet-stream" }, "text"],
    [{ name: "package.json", mime: "application/json" }, "text"],
    [{ name: "feed", mime: "application/atom+xml" }, "text"],
    [{ name: "main.go", mime: "" }, "text"],
    [{ name: "readme.md", mime: "text/markdown" }, "text"],
    [{ name: "Makefile", mime: "" }, "text"],
    [{ name: "archive.zip", mime: "application/zip" }, "other"],
    [{ name: "big.log", mime: "text/plain", size: TEXT_MAX_BYTES + 1 }, "other"],
    [{ name: "big.csv", mime: "text/csv", size: CSV_MAX_BYTES + 1 }, "other"],
    [{}, "other"],
  ];
  for (const [file, want] of cases) assert.equal(viewerKind(file), want, JSON.stringify(file));
});

test("languageFor maps extensions and media types to editor languages", () => {
  assert.equal(languageFor("a.js"), "javascript");
  assert.equal(languageFor("a.tsx"), "typescript");
  assert.equal(languageFor("a.json"), "json");
  assert.equal(languageFor("a.py"), "python");
  assert.equal(languageFor("a.go"), "go");
  assert.equal(languageFor("a.scss"), "css");
  assert.equal(languageFor("feed.xml"), "html");
  assert.equal(languageFor("q.sql"), "sql");
  assert.equal(languageFor("run.sh"), "shell");
  assert.equal(languageFor("readme.md"), "markdown");
  assert.equal(languageFor("data", "application/ld+json"), "json");
  assert.equal(languageFor("notes.txt", "text/plain"), "");
  assert.equal(extensionOf("dir/Archive.TAR.GZ"), "gz");
  assert.equal(extensionOf("Makefile"), "makefile");
});

test("parseCsv handles quotes, doubled quotes, line breaks in fields and CRLF", () => {
  const text = '﻿name,notes,depth\r\n"North quay","Clear, dredged ""twice""",4.2\r\n"Grain\nterminal",,3.1\r\n';
  const { rows, total, truncated, delimiter } = parseCsv(text);
  assert.equal(delimiter, ",");
  assert.equal(total, 2);
  assert.equal(truncated, false);
  assert.deepEqual(rows, [
    ["name", "notes", "depth"],
    ["North quay", 'Clear, dredged "twice"', "4.2"],
    ["Grain\nterminal", "", "3.1"],
  ]);
});

test("parseCsv sniffs tabs and semicolons and keeps a last line without a line break", () => {
  assert.equal(sniffDelimiter("a\tb\tc\n1\t2\t3"), "\t");
  assert.equal(sniffDelimiter('"x;y",b;c;d'), ";");
  assert.deepEqual(parseCsv("a\tb\n1\t2").rows, [["a", "b"], ["1", "2"]]);
  assert.deepEqual(parseCsv("a;b\n1;2", { delimiter: ";" }).rows, [["a", "b"], ["1", "2"]]);
  assert.deepEqual(parseCsv("").rows, []);
  assert.equal(parseCsv("only,header\n").total, 0);
});

test("parseCsv keeps at most maxRows data rows and counts them all", () => {
  const lines = ["h1,h2"];
  for (let i = 0; i < 12; i++) lines.push(`${i},x`);
  const { rows, total, truncated } = parseCsv(lines.join("\n"), { maxRows: 5 });
  assert.equal(rows.length, 6);
  assert.equal(total, 12);
  assert.equal(truncated, true);
  assert.deepEqual(rows[5], ["4", "x"]);
});

test("fitScale fits without enlarging and zoomAt keeps the point under the pointer", () => {
  assert.equal(fitScale({ width: 2000, height: 1000 }, { width: 1000, height: 800 }), 0.5);
  assert.equal(fitScale({ width: 400, height: 300 }, { width: 1000, height: 800 }), 1);
  assert.equal(fitScale({ width: 1000, height: 4000 }, { width: 500, height: 1000 }), 0.25);
  assert.equal(fitScale({ width: 0, height: 0 }, { width: 500, height: 500 }), 1);

  const state = { scale: 0.5, x: 10, y: 20 };
  const point = { x: 210, y: 120 };
  const imageX = (point.x - state.x) / state.scale;
  const imageY = (point.y - state.y) / state.scale;
  const next = zoomAt(state, 2, point);
  assert.equal(next.scale, 1);
  assert.equal((point.x - next.x) / next.scale, imageX);
  assert.equal((point.y - next.y) / next.scale, imageY);
  assert.equal(zoomAt(state, 1e9, point).scale, MAX_SCALE);
  assert.equal(zoomAt(state, 1e-9, point).scale, MIN_SCALE);
  assert.equal(zoomAt(state, 10, point, { max: 2 }).scale, 2);
});

test("clampPan centers a small image and keeps a large one against the edges", () => {
  const natural = { width: 1000, height: 500 };
  const box = { width: 800, height: 600 };
  assert.deepEqual(clampPan({ scale: 0.5, x: -300, y: 400 }, natural, box), { scale: 0.5, x: 150, y: 175 });
  assert.deepEqual(clampPan({ scale: 2, x: 50, y: -2000 }, natural, box), { scale: 2, x: 0, y: -400 });
  assert.deepEqual(clampPan({ scale: 2, x: -500, y: -100 }, natural, box), { scale: 2, x: -500, y: -100 });
});

test("pdf findMatches is case-insensitive and reports 1-based pages", () => {
  const texts = ["Harbour survey\nThe harbour wall", "", "No match here", "HARBOURharbour"];
  assert.deepEqual(findMatches(texts, "harbour"), [
    { page: 1, index: 0, length: 7 },
    { page: 1, index: 19, length: 7 },
    { page: 4, index: 0, length: 7 },
    { page: 4, index: 7, length: 7 },
  ]);
  assert.deepEqual(findMatches(texts, ""), []);
  assert.deepEqual(findMatches(["aaaa"], "aa"), [{ page: 1, index: 0, length: 2 }, { page: 1, index: 2, length: 2 }]);
});

test("pdf pageText, spanRanges and pageAt map text to spans and offsets to pages", () => {
  const { text, starts } = pageText([{ str: "Harbour ", hasEOL: false }, { type: "beginMarkedContent" }, { str: "survey", hasEOL: true }, { str: "wall" }]);
  assert.equal(text, "Harbour survey\nwall");
  assert.deepEqual(starts, [0, 8, 15]);
  const strs = ["Harbour ", "survey", "wall"];
  const ranges = spanRanges(starts, strs, [{ index: 4, length: 8, current: true }]);
  assert.deepEqual([...ranges.entries()], [
    [0, [{ start: 4, end: 8, current: true }]],
    [1, [{ start: 0, end: 4, current: true }]],
  ]);
  const pages = [{ top: 0, height: 100 }, { top: 110, height: 100 }, { top: 220, height: 100 }];
  assert.equal(pageAt(pages, 0), 1);
  assert.equal(pageAt(pages, 109), 1);
  assert.equal(pageAt(pages, 110), 2);
  assert.equal(pageAt(pages, 5000), 3);
  assert.equal(pageAt([], 10), 0);
});

test("pdf zoom steps and fit width stay within the limits", () => {
  assert.equal(nextZoom(1, 1), 1.1);
  assert.equal(nextZoom(1, -1), 0.9);
  assert.equal(nextZoom(1.05, 1), 1.1);
  assert.equal(nextZoom(MAX_ZOOM, 1), MAX_ZOOM);
  assert.equal(nextZoom(MIN_ZOOM, -1), MIN_ZOOM);
  assert.equal(fitWidthScale(612, 374), 374 / 612);
  assert.equal(fitWidthScale(612, 10), MIN_ZOOM);
  assert.equal(fitWidthScale(10, 100000), MAX_ZOOM);
  assert.equal(fitWidthScale(0, 100), 1);
});

test("csv numericColumns and rowCountText describe the DataTable", async () => {
  const { numericColumns, rowCountText } = await import("../../internal/web/static/js/viewers/csv.js");
  const data = [["a", "1,200", "3.5%", ""], ["b", "-4", "n/a", ""], ["c", "7e3", "12%", "x"]];
  assert.deepEqual(numericColumns(data, 4), [false, true, false, false]);
  assert.deepEqual(numericColumns([["", "$1.50"], ["", "(2)"]], 2), [false, true]);
  assert.equal(rowCountText(1, 1), "1 row");
  assert.equal(rowCountText(1204, 1204), "1,204 rows");
  assert.equal(rowCountText(5000, 12000), "Showing 5,000 of 12,000 rows");
});

test("csvPage pages data rows at Primer's 20 rows and clamps the page", async () => {
  const { csvPage, CSV_PAGE_SIZE, CSV_MAX_ROWS } = await import("../../internal/web/static/js/viewers/csv.js");
  assert.equal(CSV_PAGE_SIZE, 20);
  assert.equal(CSV_MAX_ROWS, 5000);
  const data = Array.from({ length: 60 }, (_, i) => [`Pier ${i + 1}`, String(i)]);
  const first = csvPage(data, 1);
  assert.deepEqual(first.slice, { page: 1, count: 3, start: 0, end: 20 });
  assert.equal(first.rows.length, 20);
  assert.equal(first.rows[0][0], "Pier 1");
  const second = csvPage(data, 2);
  assert.equal(second.rows[0][0], "Pier 21");
  assert.equal(second.rows[19][0], "Pier 40");
  const past = csvPage(data, 99);
  assert.equal(past.slice.page, 3);
  assert.equal(past.rows.length, 20);
  assert.equal(csvPage(data.slice(0, 7), 1).slice.count, 1);
  assert.deepEqual(csvPage([], 1).rows, []);
});

test("image zoomSegment marks Fit, 100% or no preset", async () => {
  const { zoomSegment } = await import("../../internal/web/static/js/viewers/image.js");
  assert.equal(zoomSegment("fit", 0.4), "fit");
  assert.equal(zoomSegment("custom", 1), "actual");
  assert.equal(zoomSegment("custom", 1.25), null);
});

test("pdf parsePageInput clamps page numbers and rejects text without digits", async () => {
  const { parsePageInput } = await import("../../internal/web/static/js/viewers/pdf.js");
  assert.equal(parsePageInput("3", 12), 3);
  assert.equal(parsePageInput(" 40 ", 12), 12);
  assert.equal(parsePageInput("0", 12), 1);
  assert.equal(parsePageInput("p. 7", 12), 7);
  assert.equal(parsePageInput("abc", 12), null);
  assert.equal(parsePageInput("2", 0), null);
});

test("text findRanges finds case-insensitive matches without overlap and stops at the limit", async () => {
  const { findRanges } = await import("../../internal/web/static/js/viewers/text.js");
  assert.deepEqual(findRanges("Aaa aA", "aa"), [{ from: 0, to: 2 }, { from: 4, to: 6 }]);
  assert.deepEqual(findRanges("abc", ""), []);
  assert.equal(findRanges("x".repeat(50), "x", 10).length, 10);
});

test("media speed labels and the no-preview facts", async () => {
  const { speedLabel, SPEEDS } = await import("../../internal/web/static/js/viewers/media.js");
  assert.equal(speedLabel(1.5), "1.5×");
  assert.ok(SPEEDS.includes(1));
  const { fileFacts } = await import("../../internal/web/static/js/viewers/common.js");
  const facts = fileFacts({ size: 2048, mime: "application/x-thing", createdAt: "" });
  assert.deepEqual(facts.map(([k]) => k), ["Size", "Type"]);
  assert.equal(facts[0][1], "2.0 KB");
});
