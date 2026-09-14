/**
 * The AI composer shared by the ask bar and the AI rail tab. It holds a
 * borderless text field that grows to 6 lines behind a leading sparkle, an @
 * mention list under the field, context chips as large Tokens with a token
 * estimate, a model chip that opens a SelectPanel, and the Ask button. It
 * returns its parts, so each surface arranges them.
 */

import { h, icon, button, uid } from "/static/js/ui.js";
import {
  parseMention, mentionMatches, chipElement, promptEstimate, formatTokens, urlChip, urlsInText, specialChip,
  openModelPanel, modelChipLabel, DEFAULT_CONTEXT_CHARS,
} from "/static/js/ai/common.js";

const LINE_PX = 20;

/**
 * Creates a composer.
 * @param {{
 *   api: Function, store?: object, note: () => object, placeholder?: string, label?: string,
 *   chips?: object[], model?: object|null, capability?: () => string, budget?: number, maxRows?: number,
 *   onSubmit: (text: string) => void, onEscape?: () => boolean, onTab?: () => boolean, onInput?: (text: string) => void,
 *   onChipsChange?: (chips: object[]) => void, sendLabel?: string
 * }} opts
 */
export function createComposer(opts) {
  const {
    api, store, note, placeholder = "Ask AI", onSubmit, onEscape, onTab, onInput, onChipsChange,
    capability = () => "text", budget = DEFAULT_CONTEXT_CHARS, maxRows = 6, sendLabel = "Ask",
  } = opts;
  let chips = [...(opts.chips || [])];
  let model = opts.model || null;
  let busy = false;
  let matches = [];
  let active = 0;
  let mention = null;
  let sources = null;
  let destroyed = false;
  let modelHandle = null;

  const inputId = uid("ask-input");
  const listId = uid("ask-mentions");
  const input = h("textarea", {
    id: inputId, class: "ask-input", rows: "1", placeholder,
    enterkeyhint: "send", autocomplete: "off", spellcheck: "true",
    role: "combobox", "aria-autocomplete": "list", "aria-expanded": "false", "aria-controls": listId,
  });
  const fieldLabel = h("label", { class: "sr-only", for: inputId, text: opts.label || "Ask AI" });
  const send = button({ label: sendLabel, variant: "primary", size: "small", className: "ask-send" });
  const inputRow = h("div", { class: "ask-input-row" },
    h("span", { class: "ask-input-visual", "aria-hidden": "true" }, icon("sparkle", 16)), fieldLabel, input, send);
  const mentions = h("ul", { class: "action-list ask-mentions", id: listId, role: "listbox", "aria-label": "Mentions", hidden: true });
  const chipList = h("ul", { class: "ask-chips", "aria-label": "Context" });
  const estimate = h("span", { class: "ask-estimate" });
  const chipsRow = h("div", { class: "ask-chips-row" }, chipList, estimate);
  const modelChip = button({
    label: modelChipLabel(model), variant: "default", size: "small", trailingAction: "triangle-down",
    className: "model-chip", attrs: { "aria-haspopup": "dialog", "aria-expanded": "false" },
  });

  /* ----- Text field ----- */

  function grow() {
    input.style.height = "auto";
    const max = LINE_PX * maxRows + 12;
    input.style.height = Math.min(max, Math.max(LINE_PX + 12, input.scrollHeight)) + "px";
    input.style.overflowY = input.scrollHeight > max ? "auto" : "hidden";
  }

  function drawChips() {
    chipList.replaceChildren(...chips.map((c) => h("li", { class: "ask-chip-item" }, chipElement(c, { onRemove: (x) => removeChip(x.key) }))));
    const est = promptEstimate(chips, input.value, budget);
    estimate.textContent = est.chars ? "About " + formatTokens(est.tokens) + " tokens" : "";
    estimate.classList.toggle("is-warn", est.warn);
  }

  function addChip(chip) {
    if (!chip || chips.some((c) => c.key === chip.key)) return;
    chips = [...chips, chip];
    drawChips();
    if (onChipsChange) onChipsChange(chips);
  }

  function removeChip(key) {
    chips = chips.filter((c) => c.key !== key);
    drawChips();
    if (onChipsChange) onChipsChange(chips);
    input.focus();
  }

  /* ----- Mentions ----- */

  async function loadSources() {
    if (sources) return sources;
    const n = note();
    const out = { notes: [], files: [] };
    try {
      if (store && store.listItems) {
        const items = await store.listItems(n.spaceId);
        out.notes = ((items && items.notes) || []).filter((x) => !x.deleted && x.id !== n.id);
        out.files = ((items && items.files) || []).filter((f) => !f.deleted);
      } else if (store && store.listNotes) {
        out.notes = ((await store.listNotes(n.spaceId)) || []).filter((x) => !x.deleted && x.id !== n.id);
      }
    } catch { /* no local items */ }
    if (!out.files.length && api) {
      try {
        const data = await api("GET", "/api/spaces/" + encodeURIComponent(n.spaceId) + "/files");
        out.files = ((data && data.items) || []).filter((f) => !f.deleted);
      } catch { /* files stay empty */ }
    }
    sources = out;
    return out;
  }

  function hideMentions() {
    mention = null;
    matches = [];
    mentions.hidden = true;
    mentions.replaceChildren();
    input.setAttribute("aria-expanded", "false");
    input.removeAttribute("aria-activedescendant");
  }

  function drawMentions() {
    mentions.replaceChildren();
    if (!matches.length) {
      mentions.hidden = !mention;
      input.setAttribute("aria-expanded", String(!!mention));
      if (mention) mentions.appendChild(h("li", { class: "ask-mentions-empty", role: "presentation", text: mention.heading !== null ? "No matching heading" : "No matching note or file" }));
      return;
    }
    matches.forEach((m, i) => {
      const id = listId + "-" + i;
      const opt = h("li", {
        class: "action-list-item ask-mention", role: "option", id, "aria-selected": String(i === active),
        dataset: { type: m.type },
      },
      h("span", { class: "action-list-leading", "aria-hidden": "true" }, icon(m.type === "section" ? "hash" : m.type === "file" ? (m.sub === "Image" ? "image" : "file") : m.type === "special" ? "link" : "note", 16)),
      h("span", { class: "action-list-text" }, h("span", { class: "action-list-label", text: m.label }), h("span", { class: "action-list-description is-inline", text: m.sub })));
      opt.addEventListener("mousedown", (ev) => ev.preventDefault());
      opt.addEventListener("click", () => pick(i));
      mentions.appendChild(opt);
    });
    mentions.hidden = false;
    input.setAttribute("aria-expanded", "true");
    input.setAttribute("aria-activedescendant", listId + "-" + active);
  }

  async function updateMentions() {
    const m = parseMention(input.value, input.selectionStart);
    if (!m) {
      hideMentions();
      return;
    }
    mention = m;
    const src = await loadSources();
    if (destroyed || mention !== m) return;
    matches = mentionMatches(m, { notes: src.notes, files: src.files.map((f) => ({ ...f, src: f.thumb || "" })) });
    active = 0;
    drawMentions();
  }

  function pick(i) {
    const m = matches[i];
    if (!m || !mention) return;
    const before = input.value.slice(0, mention.start);
    const after = input.value.slice(mention.end);
    input.value = (before + after).replace(/ {2,}/g, " ");
    input.setSelectionRange(before.length, before.length);
    const chip = m.type === "special" ? specialChip(m.chip.kind, note().id) : m.chip;
    hideMentions();
    addChip(chip);
    grow();
    input.focus();
  }

  /* ----- Model ----- */

  function openModels() {
    modelChip.setAttribute("aria-expanded", "true");
    modelHandle = openModelPanel(modelChip, {
      api, capability: capability(), current: model,
      onPick: (choice) => setModel(choice),
      onClose: () => {
        modelChip.setAttribute("aria-expanded", "false");
        modelHandle = null;
      },
    });
    if (!modelHandle || !modelHandle.element) {
      modelChip.setAttribute("aria-expanded", "false");
      modelHandle = null;
    }
  }

  function setModel(choice) {
    model = choice || null;
    const label = modelChip.querySelector(".btn-label");
    if (label) label.textContent = modelChipLabel(model);
    modelChip.setAttribute("aria-label", "Model: " + (model ? model.label || model.model : "Automatic"));
    modelChip.classList.toggle("is-chosen", !!model);
  }
  setModel(model);

  /* ----- Events ----- */

  input.addEventListener("input", () => {
    grow();
    drawChips();
    updateMentions();
    if (onInput) onInput(input.value);
  });
  input.addEventListener("click", () => updateMentions());
  input.addEventListener("paste", (ev) => {
    const text = ev.clipboardData ? ev.clipboardData.getData("text/plain") : "";
    const urls = urlsInText(text).slice(0, 5);
    if (!urls.length) return;
    for (const url of urls) addChip(urlChip(url));
    if (text.trim() === urls.join(" ") || urls.includes(text.trim())) ev.preventDefault();
  });
  input.addEventListener("keydown", (ev) => {
    if (ev.isComposing || ev.keyCode === 229) return;
    if (!mentions.hidden && matches.length) {
      if (ev.key === "ArrowDown" || ev.key === "ArrowUp") {
        ev.preventDefault();
        active = (active + (ev.key === "ArrowDown" ? 1 : matches.length - 1)) % matches.length;
        drawMentions();
        return;
      }
      if (ev.key === "Enter" || ev.key === "Tab") {
        ev.preventDefault();
        ev.stopPropagation();
        pick(active);
        return;
      }
    }
    if (ev.key === "Escape") {
      ev.preventDefault();
      ev.stopPropagation();
      if (!mentions.hidden) { hideMentions(); return; }
      if (onEscape) onEscape();
      return;
    }
    // Tab in an empty field accepts a result under review, as it does in the note.
    if (ev.key === "Tab" && !ev.shiftKey && !input.value.trim() && onTab && onTab()) {
      ev.preventDefault();
      ev.stopPropagation();
      return;
    }
    if (ev.key === "Enter" && !ev.shiftKey) {
      ev.preventDefault();
      ev.stopPropagation();
      submit();
      return;
    }
    // Keys typed here belong to the field, not to the editor around it.
    ev.stopPropagation();
  });
  send.addEventListener("mousedown", (ev) => ev.preventDefault());
  send.addEventListener("click", () => submit());
  modelChip.addEventListener("click", () => openModels());

  function submit() {
    if (send.getAttribute("aria-disabled") === "true" && !busy) return;
    if (busy) {
      if (onEscape) onEscape();
      return;
    }
    hideMentions();
    onSubmit(input.value.trim());
  }

  drawChips();

  /** The default arrangement. */
  const root = h("div", { class: "ask-composer" }, mentions, chipsRow, inputRow, h("div", { class: "ask-footer" }, modelChip));

  return {
    root, input, send, inputRow, mentions, chipsRow, modelChip,
    value: () => input.value,
    setValue(text) { input.value = text; grow(); drawChips(); },
    clear() { input.value = ""; grow(); hideMentions(); drawChips(); },
    focus() { input.focus(); },
    chips: () => chips,
    setChips(list) { chips = [...list]; drawChips(); },
    addChip, removeChip,
    model: () => model,
    setModel,
    setPlaceholder(text) { input.placeholder = text; },
    setBusy(on) {
      busy = !!on;
      const label = send.querySelector(".btn-label");
      if (label) label.textContent = busy ? "Stop" : sendLabel;
      send.classList.toggle("btn-primary", !busy);
      send.classList.toggle("is-stop", busy);
    },
    setDisabled(off, reason = "") {
      input.readOnly = !!off;
      if (off) send.setAttribute("aria-disabled", "true");
      else send.removeAttribute("aria-disabled");
      if (reason) input.placeholder = reason;
    },
    refreshEstimate: drawChips,
    closeLists() {
      hideMentions();
      if (modelHandle) modelHandle.close();
    },
    destroy() {
      destroyed = true;
      if (modelHandle) modelHandle.close();
    },
  };
}
