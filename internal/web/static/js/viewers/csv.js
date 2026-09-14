// CSV and TSV viewer: a Primer DataTable with a sticky header, the row
// count, Pagination at Primer's 20 rows (docs/primer.md 5.2, conflict row 42) and
// a notice with Download when the file has more rows than the viewer keeps.

import { h, uid, banner, announce } from "../ui.js";
import { toolButton, toolbar, errorState, loadingState, isAbort, startDownload } from "./common.js";
import { extensionOf } from "./kind.js";
import { PAGE_SIZE, pageSlice, paginationNav, rangeText } from "../fileviews.js";

/** The most data rows the viewer keeps. Download holds the rest. */
export const CSV_MAX_ROWS = 5000;

/** Data rows per page. Primer's DataTable paginates from 20 rows. */
export const CSV_PAGE_SIZE = PAGE_SIZE;

const NUMBER_RE = /^[-+(]?[$€£¥]?\s?(\d{1,3}(,\d{3})+|\d+)?(\.\d+)?(e[-+]?\d+)?\)?\s?%?$/i;

/**
 * Picks the delimiter of a CSV text from its first line. It counts commas,
 * tabs, semicolons and pipes outside quotes and takes the most frequent one.
 * @param {string} text
 * @returns {string}
 */
export function sniffDelimiter(text) {
  const counts = { ",": 0, "\t": 0, ";": 0, "|": 0 };
  let quoted = false;
  for (let i = 0; i < text.length && i < 64 * 1024; i++) {
    const c = text[i];
    if (c === '"') quoted = !quoted;
    else if (!quoted && (c === "\n" || c === "\r")) break;
    else if (!quoted && c in counts) counts[c]++;
  }
  let best = ",";
  for (const d of ["\t", ";", "|"]) if (counts[d] > counts[best]) best = d;
  return best;
}

/**
 * Parses CSV or TSV text. Quoted fields may hold delimiters, line breaks and
 * doubled quotes. CRLF and LF both end a record, and a trailing line break
 * adds no record. The first record is the header. rows holds the header and
 * at most maxRows data records. total counts every data record.
 * @param {string} text
 * @param {{maxRows?: number, delimiter?: string}} [options]
 * @returns {{rows: string[][], total: number, truncated: boolean, delimiter: string}}
 */
export function parseCsv(text, { maxRows = CSV_MAX_ROWS, delimiter = "" } = {}) {
  let s = String(text || "");
  if (s.charCodeAt(0) === 0xfeff) s = s.slice(1);
  const d = delimiter || sniffDelimiter(s);
  const rows = [];
  let records = 0;
  let row = [];
  let field = "";
  let quoted = false;
  let sawAny = false;
  const keep = () => records <= maxRows;
  const endRecord = () => {
    row.push(field);
    if (keep()) rows.push(row);
    records++;
    row = [];
    field = "";
    sawAny = false;
  };
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quoted) {
      if (c === '"') {
        if (s[i + 1] === '"') {
          field += '"';
          i++;
        } else {
          quoted = false;
        }
      } else {
        field += c;
      }
      continue;
    }
    if (c === '"' && field === "") {
      quoted = true;
      sawAny = true;
    } else if (c === d) {
      row.push(field);
      field = "";
      sawAny = true;
    } else if (c === "\n" || c === "\r") {
      if (c === "\r" && s[i + 1] === "\n") i++;
      endRecord();
    } else {
      field += c;
      sawAny = true;
    }
  }
  if (sawAny || field !== "" || row.length) endRecord();
  const total = Math.max(0, records - 1);
  return { rows, total, truncated: total > maxRows, delimiter: d };
}

/**
 * Finds the columns that hold numbers, so the DataTable aligns them to the
 * end. A column counts as numeric when at least 80% of its non-empty cells in
 * the first 200 data rows are numbers.
 * @param {string[][]} data data rows without the header
 * @param {number} width
 * @returns {boolean[]}
 */
export function numericColumns(data, width) {
  const out = [];
  const sample = data.slice(0, 200);
  for (let c = 0; c < width; c++) {
    let filled = 0;
    let numbers = 0;
    for (const r of sample) {
      const v = String(r[c] ?? "").trim();
      if (!v) continue;
      filled++;
      if (NUMBER_RE.test(v) && /\d/.test(v)) numbers++;
    }
    out.push(filled > 0 && numbers / filled >= 0.8);
  }
  return out;
}

/**
 * Formats the row count of the table, such as "1,204 rows" or
 * "Showing 5,000 of 12,000 rows".
 * @param {number} shown
 * @param {number} total
 * @returns {string}
 */
export function rowCountText(shown, total) {
  if (shown < total) return `Showing ${shown.toLocaleString("en-US")} of ${total.toLocaleString("en-US")} rows`;
  return `${total.toLocaleString("en-US")} ${total === 1 ? "row" : "rows"}`;
}

/**
 * Returns one page of data rows and its slice.
 * @param {string[][]} data data rows without the header
 * @param {number} page 1-based, clamped to the pages that exist
 * @param {number} [size]
 * @returns {{slice: {page: number, count: number, start: number, end: number}, rows: string[][]}}
 */
export function csvPage(data, page, size = CSV_PAGE_SIZE) {
  const slice = pageSlice(data.length, page, size);
  return { slice, rows: data.slice(slice.start, slice.end) };
}

/**
 * Mounts the CSV viewer.
 * @param {HTMLElement} container
 * @param {{file: object, url: string, downloadUrl: string, isNarrow: () => boolean, signal: AbortSignal}} ctx
 * @returns {Promise<{unmount(): void}>}
 */
export async function mountViewer(container, ctx) {
  const file = ctx.file || {};
  const ac = new AbortController();
  const abort = () => ac.abort();
  if (ctx.signal) ctx.signal.addEventListener("abort", abort, { once: true });
  const root = h("div", { class: "fv-csv" }, loadingState("Loading table"));
  container.appendChild(root);
  try {
    const res = await fetch(ctx.url, { credentials: "same-origin", signal: ac.signal });
    if (!res.ok) throw new Error("status " + res.status);
    const text = await res.text();
    const ext = extensionOf(file.name);
    const { rows, total, truncated } = parseCsv(text, { delimiter: ext === "tsv" || ext === "tab" ? "\t" : "" });
    const [head = [], ...data] = rows;
    const width = rows.reduce((max, r) => Math.max(max, r.length), 0);
    const numeric = numericColumns(data, width);
    const countId = uid("fv-csv-count");
    const cellClass = (i) => (numeric[i] ? "col-num" : null);
    const tbody = h("tbody");
    const table = h(
      "table",
      { class: "data-table fv-csv-table", "aria-label": file.name || "Table", "aria-describedby": countId },
      h("thead", {}, h("tr", {}, Array.from({ length: width }, (_, i) => h("th", { scope: "col", class: cellClass(i), text: head[i] ?? "" })))),
      tbody
    );
    const count = h("span", { class: "fv-toolbar-text", id: countId, text: rowCountText(data.length, total) });
    const wrap = toolButton({
      label: "Wrap cell text",
      icon: "typography",
      pressed: false,
      onClick: () => {
        const on = !root.classList.contains("is-wrapped");
        root.classList.toggle("is-wrapped", on);
        wrap.setAttribute("aria-pressed", on ? "true" : "false");
      },
    });
    const notice = truncated
      ? banner({
          variant: "info",
          title: "Some rows are not shown",
          description: `The table shows the first ${data.length.toLocaleString("en-US")} of ${total.toLocaleString("en-US")} rows. Download the file to see every row.`,
          actions: [{ label: "Download", onClick: () => startDownload(ctx.downloadUrl, file.name || "") }],
          flush: true,
        })
      : null;
    const scroll = h("div", { class: "fv-table-scroll", tabindex: "0", role: "region", "aria-label": (file.name || "CSV") + " table" }, table);
    let nav = null;
    const draw = (page, { focus = false } = {}) => {
      const { slice, rows: shown } = csvPage(data, page);
      tbody.replaceChildren(
        ...shown.map((r) =>
          h(
            "tr",
            {},
            Array.from({ length: width }, (_, i) => (i === 0 ? h("th", { scope: "row", class: cellClass(i), text: r[i] ?? "" }) : h("td", { class: cellClass(i), text: r[i] ?? "" })))
          )
        )
      );
      root.classList.toggle("is-paged", slice.count > 1);
      const next = slice.count > 1 ? paginationNav(slice, data.length, goTo, { table: true }) : null;
      if (nav) nav.replaceWith(...(next ? [next] : []));
      else if (next) root.appendChild(next);
      nav = next;
      scroll.scrollTop = 0;
      if (focus && nav) {
        const current = nav.querySelector(".pagination-item[aria-current]");
        if (current) current.focus();
      }
      return slice;
    };
    const goTo = (page) => {
      const slice = draw(page, { focus: true });
      announce(rangeText(slice.start, slice.end, data.length));
    };
    root.replaceChildren(...[toolbar("Table", [count, h("span", { class: "fv-spacer" }), wrap]), notice, scroll].filter(Boolean));
    draw(1);
  } catch (err) {
    if (!isAbort(err)) root.replaceChildren(errorState(ctx, "The table could not be loaded."));
  }
  return {
    unmount() {
      ac.abort();
      if (ctx.signal) ctx.signal.removeEventListener("abort", abort);
      root.remove();
    },
  };
}
