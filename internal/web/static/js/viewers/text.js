// Text viewer: a read-only CodeMirror 6 view styled as a Primer markdown code
// block, under a toolbar with Find, Wrap lines and Copy.

import { h, announce, copyFeedback, banner } from "../ui.js";
import { toolButton, toolbar, findBar, errorState, loadingState, isAbort, startDownload } from "./common.js";
import { languageFor, TEXT_MAX_BYTES } from "./kind.js";

/** The most find matches the viewer marks. */
export const MAX_MARKS = 10000;

/**
 * Finds case-insensitive, non-overlapping occurrences of a query.
 * @param {string} text
 * @param {string} query
 * @param {number} [limit]
 * @returns {{from: number, to: number}[]}
 */
export function findRanges(text, query, limit = MAX_MARKS) {
  const q = String(query || "").toLowerCase();
  const out = [];
  if (!q) return out;
  const lower = String(text || "").toLowerCase();
  for (let at = lower.indexOf(q); at >= 0 && out.length < limit; at = lower.indexOf(q, at + q.length)) {
    out.push({ from: at, to: at + q.length });
  }
  return out;
}

/**
 * Builds the highlight style. Tokens get classes, and files.css colours them
 * with Primer's prettylights syntax tokens in both themes.
 * @param {object} cm the codemirror.js module
 * @returns {object} an extension
 */
function highlighting(cm) {
  const t = cm.tags;
  const style = cm.HighlightStyle.define([
    { tag: [t.keyword, t.controlKeyword, t.moduleKeyword, t.operatorKeyword], class: "fv-tok-keyword" },
    { tag: [t.string, t.special(t.string), t.regexp], class: "fv-tok-string" },
    { tag: [t.number, t.bool, t.atom, t.null], class: "fv-tok-constant" },
    { tag: [t.comment, t.lineComment, t.blockComment], class: "fv-tok-comment" },
    { tag: [t.definition(t.variableName), t.function(t.variableName), t.typeName, t.className], class: "fv-tok-entity" },
    { tag: [t.propertyName, t.attributeName, t.labelName], class: "fv-tok-constant" },
    { tag: [t.tagName, t.meta, t.processingInstruction], class: "fv-tok-tag" },
    { tag: t.heading, class: "fv-tok-heading" },
    { tag: t.emphasis, class: "fv-tok-em" },
    { tag: t.strong, class: "fv-tok-strong" },
    { tag: [t.link, t.url], class: "fv-tok-link" },
    { tag: t.invalid, class: "fv-tok-invalid" },
  ]);
  return cm.syntaxHighlighting(style);
}

/**
 * Mounts the text viewer.
 * @param {HTMLElement} container
 * @param {{file: object, url: string, downloadUrl: string, isNarrow: () => boolean, signal: AbortSignal}} ctx
 * @returns {Promise<{unmount(): void}>}
 */
export async function mountViewer(container, ctx) {
  const file = ctx.file || {};
  const ac = new AbortController();
  const abort = () => ac.abort();
  if (ctx.signal) ctx.signal.addEventListener("abort", abort, { once: true });
  const root = h("div", { class: "fv-text" }, loadingState("Loading text"));
  container.appendChild(root);
  let view = null;
  try {
    const [cm, res] = await Promise.all([import("../../vendor/codemirror.js"), fetch(ctx.url, { credentials: "same-origin", signal: ac.signal })]);
    if (!res.ok) throw new Error("status " + res.status);
    let text = await res.text();
    let cut = false;
    if (text.length > TEXT_MAX_BYTES) {
      text = text.slice(0, TEXT_MAX_BYTES);
      cut = true;
    }
    if (ac.signal.aborted) return { unmount() {} };
    const lang = languageFor(file.name, file.mime);

    // Find marks live in a state field, so they follow the document view.
    const setMarks = cm.StateEffect.define();
    const marks = cm.StateField.define({
      create: () => cm.Decoration.none,
      update(value, tr) {
        let next = value.map(tr.changes);
        for (const effect of tr.effects) if (effect.is(setMarks)) next = effect.value;
        return next;
      },
      provide: (field) => cm.EditorView.decorations.from(field),
    });
    const wrapping = new cm.Compartment();
    let wrapped = !lang || lang === "markdown";
    const extensions = [
      cm.EditorState.readOnly.of(true),
      cm.EditorView.editable.of(false),
      cm.EditorView.contentAttributes.of({ tabindex: "0", "aria-label": "Contents of " + (file.name || "the file") }),
      highlighting(cm),
      marks,
      wrapping.of(wrapped ? cm.EditorView.lineWrapping : []),
    ];
    if (lang === "markdown") {
      extensions.push(cm.markdownSupport());
    } else if (lang) {
      const desc = cm.LanguageDescription.matchLanguageName(cm.codeLanguages, lang, true);
      if (desc && desc.support) extensions.push(desc.support);
    }
    const host = h("div", { class: "fv-text-host" });

    const state = { query: "", ranges: [], current: -1 };
    const hit = cm.Decoration.mark({ class: "fv-hit" });
    const currentHit = cm.Decoration.mark({ class: "fv-hit fv-hit-current" });
    const draw = (scroll) => {
      const deco = cm.Decoration.set(state.ranges.map((r, i) => (i === state.current ? currentHit : hit).range(r.from, r.to)), true);
      const effects = [setMarks.of(deco)];
      if (scroll && state.current >= 0) effects.push(cm.EditorView.scrollIntoView(state.ranges[state.current].from, { y: "center" }));
      view.dispatch({ effects });
      const n = state.ranges.length;
      const more = n >= MAX_MARKS ? "+" : "";
      find.setCount(state.query ? (n ? `${state.current + 1} of ${n}${more}` : "No matches") : "", n);
    };
    const run = (query) => {
      state.query = query;
      state.ranges = findRanges(text, query);
      state.current = state.ranges.length ? 0 : -1;
      draw(true);
    };
    const step = (dir) => {
      if (!state.ranges.length) return;
      state.current = (state.current + dir + state.ranges.length) % state.ranges.length;
      draw(true);
    };
    const closeFind = (focusTrigger) => {
      find.close();
      state.query = "";
      state.ranges = [];
      state.current = -1;
      draw(false);
      findButton.setAttribute("aria-expanded", "false");
      if (focusTrigger) findButton.focus();
    };
    const find = findBar({
      label: "Find in file",
      onQuery: (query, { step: dir }) => {
        if (dir && query && query === state.query) step(dir);
        else run(query);
      },
      onStep: step,
      onClose: closeFind,
    });
    const findButton = toolButton({
      label: "Find in file",
      icon: "search",
      attrs: { "aria-expanded": "false" },
      onClick: () => {
        if (find.element.hidden) {
          find.open();
          findButton.setAttribute("aria-expanded", "true");
        } else {
          closeFind(true);
        }
      },
    });
    const wrapButton = toolButton({
      label: "Wrap lines",
      icon: "typography",
      pressed: wrapped,
      onClick: () => {
        wrapped = !wrapped;
        view.dispatch({ effects: wrapping.reconfigure(wrapped ? cm.EditorView.lineWrapping : []) });
        wrapButton.setAttribute("aria-pressed", wrapped ? "true" : "false");
      },
    });
    const copyButton = toolButton({
      label: "Copy contents",
      icon: "copy",
      onClick: async () => {
        try {
          await navigator.clipboard.writeText(text);
          copyFeedback(copyButton);
        } catch {
          announce("The contents could not be copied");
        }
      },
    });
    const lines = text ? text.split("\n").length - (text.endsWith("\n") ? 1 : 0) : 0;
    const bar = toolbar("Text", [h("span", { class: "fv-toolbar-text", text: `${lines.toLocaleString("en-US")} ${lines === 1 ? "line" : "lines"}` }), h("span", { class: "fv-spacer" }), findButton, wrapButton, copyButton]);
    const notice = cut
      ? banner({
          variant: "info",
          title: "Only part of this file is shown",
          description: "The viewer shows the first 5 MB. Download the file to see all of it.",
          actions: [{ label: "Download", onClick: () => startDownload(ctx.downloadUrl, file.name || "") }],
          flush: true,
        })
      : null;
    root.replaceChildren(...[bar, find.element, notice, host].filter(Boolean));
    view = new cm.EditorView({ state: cm.EditorState.create({ doc: text, extensions }), parent: host });
  } catch (err) {
    if (!isAbort(err)) root.replaceChildren(errorState(ctx, "The text could not be loaded."));
  }
  return {
    unmount() {
      ac.abort();
      if (ctx.signal) ctx.signal.removeEventListener("abort", abort);
      if (view) view.destroy();
      root.remove();
    },
  };
}
