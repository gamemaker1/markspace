/**
 * The AI rail tab as a conversation about the note. Earlier turns are listed
 * oldest first, each answer with citation chips and the actions Insert at
 * cursor, Copy and Save as note. The composer at the bottom has the @ mentions
 * and chips of the ask bar and a This note or This space scope switch. A usage
 * line at the foot shows today's and this month's tokens and budget use.
 */

import { h, announce, confirmDialog, isNarrow, button, segmentedControl, progressBar, inlineMessage, label as uiLabel, blankslate } from "/static/js/ui.js";
import { panelShell } from "/static/js/panels.js";
import { createComposer } from "/static/js/ai/composer.js";
import {
  noteChip, streamEvents, renderAnswer, sourceChips, copyToClipboard, aiIcon, formatTokens, refsFromChips,
  aiCapabilities, AI_OFFLINE_MESSAGE, shortUrl, showAiOffline,
} from "/static/js/ai/common.js";

/* ---------- Pure helpers ---------- */

/** Formats a cost in the report currency. */
export function formatCost(value, currency = "USD") {
  const v = Number(value) || 0;
  const sign = currency === "USD" ? "$" : currency + " ";
  if (v > 0 && v < 0.01) return "<" + sign + "0.01";
  return sign + v.toFixed(2);
}

/**
 * Builds the usage line from GET /api/ai/usage/summary. pct is null without a
 * budget. warn turns on at 80 percent.
 * @returns {{text: string, pct: number|null, warn: boolean, exceeded: boolean}|null}
 */
export function usageLine(summary) {
  if (!summary) return null;
  const tokens = (p) => (p ? (p.tokens !== undefined ? p.tokens : (p.inputTokens || 0) + (p.outputTokens || 0)) : 0);
  const today = summary.today || {};
  const month = summary.month || {};
  const parts = ["Today " + formatTokens(tokens(today)) + " tokens", "This month " + formatTokens(tokens(month)) + " tokens"];
  if ((Number(month.cost) || 0) > 0) parts.push(formatCost(month.cost, summary.currency));
  const b = summary.budget || {};
  const limited = (Number(b.tokenLimit) || 0) > 0 || (Number(b.costLimit) || 0) > 0;
  const pct = limited ? Math.max(0, Math.min(100, Number(b.usedPct) || 0)) : null;
  return { text: parts.join(" · "), pct, warn: limited && (pct >= 80 || !!b.warn || !!b.exceeded), exceeded: !!b.exceeded };
}

/**
 * Builds the note saved from an answer: the first line of the question as the
 * title, the answer, and its sources as a numbered list whose numbers match
 * the [n] citations.
 * @returns {{title: string, content: string}}
 */
export function noteFromAnswer({ question = "", answer = "", sources = [] }) {
  const line = String(question).split("\n").map((l) => l.trim()).find(Boolean) || "AI answer";
  const title = line.length > 60 ? line.slice(0, 59).trimEnd() + "…" : line;
  const out = [String(answer).trim()];
  const listed = (sources || []).map((s, i) => [s, i + 1]).filter(([s]) => s && !s.skipped);
  if (listed.length) {
    out.push("", "## Sources", "");
    for (const [s, n] of listed) {
      if (s.url) out.push(`${n}. [${String(s.title || shortUrl(s.url)).replace(/[[\]]/g, "")}](${s.url})`);
      else if (s.noteId) out.push(`${n}. [[${String(s.title || "Untitled").replace(/[[\]|]/g, "")}]]`);
      else out.push(`${n}. ${s.title || "Source"}`);
    }
  }
  return { title, content: out.join("\n") + "\n" };
}

/* ---------- Tab ---------- */

/**
 * Mounts the AI tab.
 * @param {HTMLElement} container
 * @param {{note: object, access: string, api: Function, store?: object, sync?: object, user?: object, isOnline?: () => boolean,
 *   navigate?: (url: string) => void, aiEnabled?: boolean, insertTracked?: Function, insertText?: Function, onClose?: Function,
 *   render?: Function, seed?: {question: string, answer: string, sources: object[]}, fetch?: Function}} opts
 * @returns {{unmount(): void}}
 */
export function openAITab(container, opts) {
  const api = opts.api;
  const isOnline = opts.isOnline || (() => navigator.onLine);
  const noteOf = () => opts.note;
  let thread = null;
  let abort = null;
  let scope = "note";
  let unmounted = false;
  let renderFn = opts.render || null;

  const clearBtn = button({ label: "Clear", variant: "invisible", size: "small", className: "ai-clear" });
  clearBtn.hidden = true;
  const shell = panelShell({ title: "AI", className: "ms-ai ai-tab", action: clearBtn, onClose: opts.onClose });
  container.appendChild(shell.root);
  const turns = h("ol", { class: "ai-thread", "aria-label": "Conversation" });
  let empty = blankslate({ icon: "sparkle", title: "Ask about this note", description: "Answers cite the notes they draw on.", size: "small", headingLevel: 3 });
  shell.body.append(empty, turns);

  const scopeSwitch = segmentedControl({
    label: "Scope", size: "small",
    items: [{ label: "This note", value: "note", selected: true }, { label: "This space", value: "space" }],
    onChange: (value) => {
      scope = value;
      composer.focus();
    },
  });

  // A seed from Continue in AI tab arrives as seed, or as a selection reference
  // when the shell passes the request through contextRefs.
  const seedAnswer = (opts.seed && opts.seed.answer) || ((opts.contextRefs || []).find((r) => r.kind === "selection") || {}).text || "";
  const defaultChips = () => {
    const n = noteOf();
    const chips = [noteChip({ id: n.id, title: n.title, content: n.content || "" }, "Note")];
    if (seedAnswer) {
      const a = String(seedAnswer);
      chips.push({ key: "seed", kind: "selection", badge: "Answer", label: a.replace(/\s+/g, " ").slice(0, 30) + (a.length > 30 ? "…" : ""), title: a.slice(0, 300), chars: a.length, ref: { kind: "selection", text: a } });
    }
    return chips;
  };

  const composer = createComposer({
    api, store: opts.store, note: noteOf, placeholder: seedAnswer ? "Continue the conversation" : "Ask about this note", label: "Ask about this note",
    chips: defaultChips(), capability: () => (composer && composer.chips().some((c) => c.image) ? "vision" : "text"),
    onSubmit: (text) => send(text),
    onEscape: () => { if (abort) abort.abort(); },
  });
  const usage = h("div", { class: "ai-usage" });
  const composerBox = h("div", { class: "ai-thread-composer" },
    composer.mentions, composer.chipsRow, composer.inputRow,
    h("div", { class: "ask-footer" }, scopeSwitch, h("span", { class: "ask-spacer" }), composer.modelChip));
  shell.foot.append(composerBox, usage);

  /* ----- Turns ----- */

  function messages() {
    return thread ? thread.messages || [] : [];
  }

  function draw() {
    const list = messages();
    empty.hidden = list.length > 0;
    clearBtn.hidden = !thread || !list.length;
    turns.replaceChildren(...list.map((m, i) => drawTurn(m, list[i - 1])));
    const last = turns.lastElementChild;
    if (last && last.scrollIntoView) last.scrollIntoView({ block: "nearest" });
  }

  function citeSource(s) {
    if (s.url) window.open(s.url, "_blank", "noopener,noreferrer");
    else if (s.noteId && opts.navigate) opts.navigate("/n/" + encodeURIComponent(s.noteId));
  }

  function drawTurn(m, prev) {
    if (m.role === "user") {
      return h("li", { class: "ai-turn is-user" }, h("div", { class: "ai-question", text: m.content }));
    }
    const body = h("div", { class: "answer-body md-body" + (m.streaming ? " is-streaming" : "") });
    renderAnswer(body, m.content || "", { render: renderFn ? (md) => renderFn(md, {}) : null, sources: m.sources || [], onCite: isNarrow() ? null : citeSource });
    const li = h("li", { class: "ai-turn is-answer", dataset: { id: m.id || "" } }, body);
    if (m.error) {
      const row = h("div", { class: "ai-error-row" }, inlineMessage({ variant: "critical", text: m.error }));
      if (prev) {
        row.appendChild(button({ label: "Try again", variant: "invisible", size: "small", onClick: () => { thread.messages = messages().filter((x) => x !== m && x !== prev); send(prev.content); } }));
      }
      li.appendChild(row);
      return li;
    }
    if (m.streaming || !m.content) return li;
    const chips = sourceChips(m.sources, citeSource);
    if (chips) li.appendChild(chips);
    const actions = h("div", { class: "answer-actions" });
    const add = (label, run) => {
      const b = button({ label, size: "small", className: "answer-action" });
      b.addEventListener("click", () => run(b));
      actions.appendChild(b);
    };
    if (typeof opts.insertTracked === "function" || typeof opts.insertText === "function") add("Insert at cursor", () => insertAtCursor(m));
    add("Copy", (b) => copyToClipboard(m.content, b));
    if (opts.sync && typeof opts.sync.createNote === "function" && accessAtLeastEdit(opts.access)) add("Save as note", (b) => saveAsNote(m, prev, b));
    if (m.applied) actions.appendChild(uiLabel("Applied", { variant: "success" }));
    li.appendChild(actions);
    return li;
  }

  async function insertAtCursor(m) {
    // The editor puts the answer in as a tracked insertion. The shell's rail hands
    // over insertText only, which takes the tracked option.
    if (typeof opts.insertTracked === "function") opts.insertTracked(m.content.trim(), { title: "AI tab" });
    else opts.insertText(m.content.trim(), { tracked: true, title: "AI tab" });
    m.applied = true;
    draw();
    announce("Inserted for review in the note");
    if (thread && m.id) {
      try {
        await api("PUT", `/api/ai/threads/${encodeURIComponent(thread.id)}/messages/${encodeURIComponent(m.id)}/applied`, { applied: true });
      } catch { /* the label stays local */ }
    }
  }

  /** Saves an answer as a note. The result shows as an InlineMessage beside the actions. */
  async function saveAsNote(m, prev, btn) {
    const n = noteOf();
    const { title, content } = noteFromAnswer({ question: prev ? prev.content : "", answer: m.content, sources: m.sources });
    const row = btn.parentElement;
    for (const old of row.querySelectorAll(".inline-message, .ai-open-note")) old.remove();
    try {
      const created = await opts.sync.createNote({ spaceId: n.spaceId, parentId: n.parentId || "", title, content, tags: [], icon: "" });
      row.appendChild(inlineMessage({ variant: "success", size: "small", text: "Saved as note" }));
      if (created && opts.navigate) row.appendChild(button({ label: "Open note", variant: "link", size: "small", className: "ai-open-note", onClick: () => opts.navigate("/n/" + encodeURIComponent(created.id)) }));
      announce("Saved as note " + title);
    } catch (err) {
      row.appendChild(inlineMessage({ variant: "critical", size: "small", text: "The note could not be saved: " + ((err && err.message) || "error") }));
    }
  }

  /* ----- Sending ----- */

  async function send(text) {
    if (!text) {
      composer.focus();
      return;
    }
    if (!isOnline()) {
      showAiOffline(composer.send);
      return;
    }
    if (abort) return;
    shell.setError("");
    const chips = composer.chips();
    composer.clear();
    try {
      if (!thread) thread = { ...(await api("POST", "/api/ai/threads", { noteId: noteOf().id })), messages: [] };
    } catch (err) {
      composer.setValue(text);
      shell.setError(errorText(err));
      return;
    }
    const userTurn = { role: "user", content: text };
    const answer = { role: "assistant", content: "", streaming: true, sources: [] };
    thread.messages = [...messages(), userTurn, answer];
    draw();
    abort = new AbortController();
    composer.setBusy(true);
    let frame = 0;
    const redraw = () => {
      frame = 0;
      const el = turns.lastElementChild && turns.lastElementChild.querySelector(".answer-body");
      if (el) renderAnswer(el, answer.content, { render: renderFn ? (md) => renderFn(md, {}) : null });
    };
    const body = { content: text, scope };
    const refs = refsFromChips(chips, text);
    if (refs.length) body.context = refs;
    const model = composer.model();
    if (model) {
      body.providerId = model.providerId;
      body.model = model.model;
    }
    try {
      const done = await streamEvents(`/api/ai/threads/${encodeURIComponent(thread.id)}/messages`, body, {
        fetch: opts.fetch, signal: abort.signal,
        onEvent: (name, data) => {
          if (name === "delta" && data.delta) {
            answer.content += data.delta;
            if (!frame) frame = requestAnimationFrame(redraw);
          }
        },
      });
      if (frame) cancelAnimationFrame(frame);
      if (done && done.message) Object.assign(answer, done.message, { streaming: false });
      else answer.streaming = false;
      if (done && done.userMessage) Object.assign(userTurn, done.userMessage);
      if (done && done.sources && !answer.sources.length) answer.sources = done.sources;
      if (done && done.incomplete) answer.error = "The answer ended early";
    } catch (err) {
      if (frame) cancelAnimationFrame(frame);
      answer.streaming = false;
      if (err && err.name === "AbortError") {
        if (!answer.content) answer.error = "Stopped";
      } else {
        answer.error = errorText(err);
      }
    } finally {
      abort = null;
      if (!unmounted) {
        composer.setBusy(false);
        draw();
        loadUsage();
      }
    }
  }

  clearBtn.addEventListener("click", () => {
    if (!thread) return;
    confirmDialog({
      title: "Clear this conversation?",
      body: "The questions and answers about this note will be deleted. You cannot restore them.",
      confirmLabel: "Clear conversation",
      danger: true,
      returnFocus: composer.input,
      onConfirm: async () => {
        await api("DELETE", "/api/ai/threads/" + encodeURIComponent(thread.id));
        thread = null;
        draw();
        announce("Conversation cleared");
      },
    });
  });

  /* ----- Loading ----- */

  async function loadUsage() {
    try {
      const line = usageLine(await api("GET", "/api/ai/usage/summary"));
      if (unmounted || !line) return;
      const color = line.exceeded || (line.pct !== null && line.pct >= 100) ? "danger" : line.warn ? "attention" : "accent";
      const bar = line.pct === null ? null : progressBar({ value: line.pct, color, label: "Monthly budget used", valueText: Math.round(line.pct) + "% of the monthly budget" });
      usage.classList.toggle("is-warn", line.warn);
      usage.replaceChildren(h("span", { class: "usage-text", text: line.text }), bar,
        h("a", { class: "usage-link", href: "/settings/usage", "data-full-nav": "", text: "Usage" }));
    } catch {
      usage.replaceChildren();
    }
  }

  function setOffline(off) {
    shell.setOffline(off, AI_OFFLINE_MESSAGE);
    composer.setDisabled(off, off ? AI_OFFLINE_MESSAGE : "Ask about this note");
  }

  async function load() {
    if (opts.aiEnabled === false) {
      shell.body.replaceChildren(blankslate({ icon: "sparkle", title: "AI is turned off", description: "An administrator can turn AI on for this server.", size: "small", headingLevel: 3 }));
      composerBox.hidden = true;
      return;
    }
    if (!isOnline()) {
      setOffline(true);
      return;
    }
    setOffline(false);
    if (!renderFn) {
      try { renderFn = (await import("/static/js/markdown.js")).render || null; } catch { renderFn = null; }
    }
    try {
      const caps = await aiCapabilities(api);
      if (unmounted) return;
      if (!caps.text) {
        const admin = !!(opts.user && opts.user.isAdmin);
        const connect = blankslate({
          icon: "sparkle", title: "Connect a model", description: "AI answers need a model from the AI settings.", size: "small", headingLevel: 3,
          primaryAction: { label: "Connect a model", href: admin ? "/admin/ai" : "/settings/ai" },
        });
        empty.replaceWith(connect);
        empty = connect;
        composerBox.hidden = true;
        return;
      }
      const data = await api("GET", "/api/ai/threads?noteId=" + encodeURIComponent(noteOf().id));
      if (unmounted) return;
      thread = ((data && data.items) || [])[0] || null;
      draw();
    } catch (err) {
      if (unmounted) return;
      if (err && err.status === 0) setOffline(true);
      else shell.setError(errorText(err), load);
    }
    loadUsage();
    if (seedAnswer) composer.focus();
  }

  const onOnline = () => load();
  const onOffline = () => setOffline(true);
  window.addEventListener("online", onOnline);
  window.addEventListener("offline", onOffline);
  load();

  return {
    unmount() {
      unmounted = true;
      if (abort) abort.abort();
      window.removeEventListener("online", onOnline);
      window.removeEventListener("offline", onOffline);
      composer.destroy();
      shell.root.remove();
    },
  };
}

function accessAtLeastEdit(access) {
  return access === "edit" || access === "manage";
}

function errorText(err) {
  if (!err) return "Something went wrong";
  if (err.status === 429) return "The monthly AI budget is used up";
  if (err.status === 503 || err.status === 415) return err.message || "No model can answer right now";
  return err.message || "Something went wrong";
}

export { aiIcon };
