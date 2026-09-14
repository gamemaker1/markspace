// Jobs page (docs/primer.md 5.2, 5.5, 5.6 and 5.9). Rows update live from the
// event stream. Pause, Resume and Cancel use the JSON API and update the row
// in place. Cancel asks through a ConfirmationDialog. The log opens in a
// Dialog. Every action is also a form post, so the page works without this
// script.

import { actionMenu, announce, banner, button, confirmDialog, h, iconButton, inlineMessage, label, openDialog, progressBar, relativeTime, spinner } from "/static/js/ui.js";

const page = document.querySelector("[data-jobs-page]");

const LABELS = { queued: "Queued", running: "Running", paused: "Paused", done: "Done", failed: "Failed", canceled: "Canceled" };
const TONES = { queued: "secondary", running: "accent", paused: "secondary", done: "success", failed: "danger", canceled: "secondary" };
const BARS = { done: "success", failed: "danger", paused: "neutral", canceled: "neutral" };
const FINISHED = new Set(["done", "failed", "canceled"]);
const VIEW_STATUSES = {
  active: new Set(["queued", "running", "paused"]),
  finished: FINISHED,
  all: null,
};
const NOTICES = { cancel: "Job canceled", pause: "Job paused", resume: "Job resumed" };

async function api(method, path) {
  const res = await fetch(path, { method, headers: { Accept: "application/json" }, credentials: "same-origin" });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(body.error || res.statusText || "The request failed.");
  }
  return body;
}

/** showProblem shows a failure in a critical Banner at the top of the page. */

/**
 * Splits an id such as plugin.word-count.count-space into text nodes with a
 * <wbr> after each ".", "-", "_", "/", ":" or "@", so a narrow cell wraps at
 * those points and never inside a word. The server template does the same
 * through its breakable function.
 * @param {string} text
 * @returns {Node[]}
 */
function breakableNodes(text) {
  const out = [];
  for (const part of String(text).split(/(?<=[.\-_/:@])(?=.)/)) {
    if (out.length) out.push(document.createElement("wbr"));
    out.push(document.createTextNode(part));
  }
  return out;
}

function showProblem(message) {
  banner({ variant: "critical", title: message, into: page.querySelector("[data-job-banner-host]"), focus: true });
}

/** logURL is the page address that shows the log of a job without the script. */
function logURL(id) {
  const q = new URLSearchParams(page.dataset.query || "");
  q.set("view", page.dataset.view || "active");
  q.set("open", id);
  return `/jobs?${q.toString()}#job-log`;
}

function titleOf(job) {
  return job.title || job.kind || "Job";
}

/** focusWithin reports whether focus sits inside el, or was lost to body. */
function focusWithin(el) {
  return el.contains(document.activeElement);
}

function renderStatus(row) {
  const job = row._job;
  row.dataset.status = job.status;
  row.querySelector("[data-job-status]").replaceChildren(label(LABELS[job.status] || job.status, { variant: TONES[job.status] || "secondary" }));
}

function renderProgress(row) {
  const job = row._job;
  const cell = row.querySelector("[data-job-progress]");
  const total = Number(job.total) || 0;
  const done = Math.min(Number(job.progress) || 0, total || Infinity);
  const color = BARS[job.status] || "accent";
  // Narrow viewports hide the Progress column and show its text under the
  // row header instead.
  const inline = row.querySelector("[data-job-progress-inline]");
  if (inline) {
    inline.textContent = total > 0 ? `${done} of ${total}` : "";
  }
  if (total > 0) {
    const text = `${done} of ${total}`;
    const existing = cell.querySelector(".progress-bar");
    if (existing && existing.dataset.color === color && existing.set) {
      existing.set(done, text);
      cell.querySelector(".job-progress-text").textContent = text;
      return;
    }
    const bar = progressBar({ value: done, max: total, color, label: `Progress of ${titleOf(job)}`, valueText: text });
    bar.set = bar.set.bind(bar);
    const wrap = h("div", { class: "job-progress" }, bar, h("span", { class: "job-progress-text", text }));
    // A server-rendered bar has no set method. The new one replaces it.
    cell.replaceChildren(wrap);
    return;
  }
  if (job.status === "running") {
    if (!cell.querySelector(".spinner")) {
      const spin = spinner({ size: "small", label: "" });
      spin.setAttribute("aria-hidden", "true");
      cell.replaceChildren(h("div", { class: "job-progress" }, spin, h("span", { class: "job-progress-text", text: "Running" })));
    }
    return;
  }
  cell.replaceChildren();
}

/** menuItems lists the actions a row offers in its current state. */
function menuItems(row) {
  const job = row._job;
  const st = job.status;
  const items = [];
  if ((st === "queued" || st === "running") && !job.pauseRequested) {
    items.push({ label: "Pause", onClick: () => runAction(row, "pause") });
  }
  if (st === "paused" || (st === "running" && job.pauseRequested)) {
    items.push({ label: "Resume", onClick: () => runAction(row, "resume") });
  }
  items.push({ label: "View log", onClick: () => openLog(row) });
  if (!FINISHED.has(st)) {
    items.push({ separator: true }, { label: "Cancel job", danger: true, onClick: () => confirmCancel(row) });
  }
  return items;
}

/** renderActions shows a Button small for one action and an ActionMenu for more. */
function renderActions(row) {
  const job = row._job;
  const cell = row.querySelector("[data-job-actions]");
  const hadFocus = focusWithin(cell) || row._wantFocus;
  row._wantFocus = false;
  if (row._menu) {
    row._menu.destroy();
    row._menu = null;
  }
  let control;
  if (FINISHED.has(job.status)) {
    control = button({
      label: "View log",
      size: "small",
      href: logURL(job.id),
      onClick: (event) => {
        event.preventDefault();
        openLog(row);
      },
    });
  } else {
    control = iconButton({ icon: "kebab-horizontal", label: `Actions: ${titleOf(job)}`, size: "small", className: "row-menu-btn" });
    row._menu = actionMenu(control, () => menuItems(row), { placement: "bottom-end", label: `Actions: ${titleOf(job)}` });
  }
  cell.replaceChildren(control);
  row._control = control;
  if (hadFocus) {
    control.focus();
  }
}

/** update merges a change into a row and renders the parts that changed. */
function update(row, patch) {
  const job = row._job;
  const before = { status: job.status, pauseRequested: job.pauseRequested };
  Object.assign(job, patch);
  renderProgress(row);
  if (before.status !== job.status || before.pauseRequested !== job.pauseRequested) {
    renderStatus(row);
    renderActions(row);
    if (!FINISHED.has(before.status) && FINISHED.has(job.status)) {
      announce(`${titleOf(job)}: ${LABELS[job.status].toLowerCase()}`);
    }
  }
  if (patch.message !== undefined) {
    const msg = row.querySelector("[data-job-message]");
    msg.textContent = patch.message || "";
    msg.hidden = !patch.message;
  }
}

/** fromView turns an API job view into the row state. */
function fromView(view) {
  return {
    id: view.id,
    title: view.title,
    kind: view.kind,
    status: view.status,
    pauseRequested: Boolean(view.pauseRequested),
    progress: view.displayProgress ?? view.progress ?? 0,
    total: view.displayTotal ?? view.total ?? 0,
    message: view.message || "",
  };
}

async function runAction(row, action) {
  row._wantFocus = true;
  try {
    const view = await api("POST", `/api/jobs/${encodeURIComponent(row._job.id)}/${action}`);
    update(row, fromView(view));
    announce(NOTICES[action]);
  } catch (err) {
    if (action === "cancel") {
      throw err;
    }
    showProblem(err.message);
  } finally {
    if (row._control && !document.querySelector("dialog[open]") && (document.activeElement === document.body || !document.activeElement)) {
      row._control.focus();
    }
  }
}

async function confirmCancel(row) {
  const title = titleOf(row._job);
  await confirmDialog({
    title: `Cancel ${title}?`,
    body: "The job stops and cannot be resumed.",
    confirmLabel: "Cancel job",
    cancelLabel: "Keep running",
    danger: true,
    returnFocus: row._control,
    onConfirm: () => runAction(row, "cancel"),
  });
  if (row._control && (document.activeElement === document.body || !row.contains(document.activeElement))) {
    row._control.focus();
  }
}

function logList(entries) {
  if (!entries.length) {
    return h("p", { class: "job-log-empty", text: "The log is empty." });
  }
  return h(
    "ol",
    { class: "job-log-list" },
    entries.map((entry) => {
      const at = new Date(entry.at);
      return h(
        "li",
        { class: "job-log-line" },
        h("time", { class: "job-log-time", datetime: entry.at, text: Number.isNaN(at.getTime()) ? "" : at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false }) }),
        h("span", { class: "job-log-level", "data-level": entry.level, text: entry.level }),
        h("span", { class: "job-log-message", text: entry.message })
      );
    })
  );
}

/** openLog loads the job log into a Dialog. A Spinner shows after 1 s. */
function openLog(row) {
  const job = row._job;
  const body = h("div", { class: "job-log-body" });
  openDialog({ title: `Log of ${titleOf(job)}`, subtitle: job.kind || "", width: "xlarge", content: body, returnFocus: row._control });
  const load = async () => {
    body.setAttribute("aria-busy", "true");
    body.replaceChildren(spinner({ size: "medium", label: "Loading the log", delay: 1000 }));
    try {
      const detail = await api("GET", `/api/jobs/${encodeURIComponent(job.id)}`);
      body.replaceChildren(logList(detail.log || []));
      update(row, fromView(detail.job));
    } catch (err) {
      body.replaceChildren(
        h(
          "div",
          { class: "job-log-error" },
          inlineMessage({ variant: "critical", text: `The log could not be loaded. ${err.message}` }),
          button({ label: "Try again", variant: "invisible", size: "small", onClick: load })
        )
      );
    } finally {
      body.removeAttribute("aria-busy");
    }
  };
  load();
}

function counts() {
  const n = page.querySelector("[data-job-list]").children.length;
  page.querySelector("[data-job-count]").textContent = String(n);
  page.querySelector("[data-job-count-text]").textContent = `(${n})`;
  page.querySelector("[data-job-table]").hidden = n === 0;
  page.querySelector("[data-job-empty]").hidden = n > 0;
}

function newRow(view) {
  const job = fromView(view);
  const title = titleOf(job);
  const row = h(
    "tr",
    { id: `job-${job.id}`, "data-job-id": job.id },
    h("th", { scope: "row" }, h("span", { class: "cell-main" }, h("span", { class: "cell-title job-title", text: title }), h("span", { class: "cell-sub" }, breakableNodes(job.kind || "")), h("span", { class: "cell-sub job-message", "data-job-message": "", hidden: !job.message, text: job.message }), h("span", { class: "cell-sub job-progress-inline", "data-job-progress-inline": "" }))),
    h("td", { class: "col-nowrap", "data-job-status": "" }),
    h("td", { class: "hide-narrow", "data-job-progress": "" }),
    h("td", { class: "col-num hide-narrow", text: view.attempts ? `${view.attempts} of ${view.maxAttempts}` : "" }),
    h("td", { class: "col-nowrap hide-narrow" }, view.createdAt ? relativeTime(view.createdAt) : null),
    h("td", { class: "col-actions", "data-job-actions": "" })
  );
  row._job = job;
  renderStatus(row);
  renderProgress(row);
  renderActions(row);
  return row;
}

const pending = new Set();

async function onJobEvent(data) {
  const row = page.querySelector(`[data-job-id="${CSS.escape(data.jobId)}"]`);
  if (row) {
    const patch = { status: data.status };
    if (data.progress !== undefined) {
      patch.progress = data.progress;
    }
    if (data.total !== undefined) {
      patch.total = data.total;
    }
    if (data.message !== undefined) {
      patch.message = data.message;
    }
    update(row, patch);
    return;
  }
  const wanted = VIEW_STATUSES[page.dataset.view];
  if ((wanted && !wanted.has(data.status)) || pending.has(data.jobId)) {
    return;
  }
  pending.add(data.jobId);
  try {
    const detail = await api("GET", `/api/jobs/${encodeURIComponent(data.jobId)}`);
    const q = new URLSearchParams(page.dataset.query || "");
    if ((q.get("module") && detail.job.module !== q.get("module")) || (q.get("kind") && detail.job.kind !== q.get("kind"))) {
      return;
    }
    if (page.querySelector(`[data-job-id="${CSS.escape(data.jobId)}"]`)) {
      return;
    }
    page.querySelector("[data-job-list]").prepend(newRow(detail.job));
    counts();
  } catch {
    // A job the person may not see, or one already deleted, stays off the page.
  } finally {
    pending.delete(data.jobId);
  }
}

if (page) {
  // Server rows become live rows. The ActionMenu of each row moves to ui.js,
  // so its items follow the state after every update.
  for (const row of page.querySelectorAll("[data-job-list] > tr[data-job-id]")) {
    row._job = {
      id: row.dataset.jobId,
      title: row.dataset.title,
      kind: row.dataset.kind,
      status: row.dataset.status,
      pauseRequested: row.hasAttribute("data-pause-requested"),
      progress: Number(row.dataset.progress) || 0,
      total: Number(row.dataset.total) || 0,
    };
    renderActions(row);
  }
  const problem = page.querySelector("[data-job-problem]");
  if (problem) {
    problem.focus();
  }
  const notice = page.querySelector("[data-job-notice]");
  if (notice) {
    announce(notice.textContent.trim());
  }
  if ("EventSource" in window) {
    const source = new EventSource("/api/events");
    source.addEventListener("job.updated", (e) => {
      try {
        const ev = JSON.parse(e.data);
        if (ev.data && ev.data.jobId) {
          onJobEvent(ev.data);
        }
      } catch {
        // A malformed frame is ignored.
      }
    });
    addEventListener("pagehide", () => source.close());
  }
}
