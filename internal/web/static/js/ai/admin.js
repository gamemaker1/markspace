// Progressive enhancement for /admin/ai and /settings/ai (docs/primer.md 5.5, 5.6
// and 6.1). The models of a task wait for an explicit Save. The Enabled
// switch of a model saves at once. A price opens in a Dialog with Save.
// Provider forms open in Dialogs through admin.js. The server forms keep
// working without this script.

import { announce, formControl, h, inlineMessage, openDialog, openMenu, selectPanel, setBusy, setFieldError, toggleSwitch, watchTableScroll } from "../ui.js";

/* ---------- Helpers ---------- */

/** api sends a JSON request and resolves with the decoded body. */
export async function api(method, url, payload) {
  const init = { method, credentials: "same-origin", headers: { Accept: "application/json" } };
  if (payload !== undefined) {
    init.headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(payload);
  }
  const res = await fetch(url, init);
  const type = res.headers.get("Content-Type") || "";
  const data = type.includes("application/json") ? await res.json() : await res.text();
  if (!res.ok) {
    const err = new Error((data && data.error) || res.statusText || "The request failed.");
    err.status = res.status;
    throw err;
  }
  return data;
}

const enc = encodeURIComponent;

/** modelPath encodes a model id for a path whose last segment may hold slashes. */
export function modelPath(model) {
  return model.split("/").map(enc).join("/");
}

function items(data) {
  if (Array.isArray(data)) {
    return data;
  }
  return (data && Array.isArray(data.items) && data.items) || [];
}

/** usd renders a price in dollars with at least two decimals when it has any. */
export function usd(v) {
  let s = String(Math.round(v * 1e6) / 1e6);
  const dot = s.indexOf(".");
  if (dot >= 0 && s.length - dot - 1 < 2) {
    s += "0".repeat(2 - (s.length - dot - 1));
  }
  return "$" + s;
}

/** priceLabels renders the effective input and output price of a model. */
export function priceLabels(m) {
  const p = m.effectivePrice;
  if (!p) {
    return ["Unknown", "Unknown"];
  }
  if (!p.inputPerMTok && !p.outputPerMTok) {
    return ["Free", "Free"];
  }
  return [usd(p.inputPerMTok || 0), usd(p.outputPerMTok || 0)];
}

/** chipPrice renders the price of a model for a list row. */
export function chipPrice(m) {
  const [input, output] = priceLabels(m);
  if (input === "Unknown") {
    return "price unknown";
  }
  if (input === "Free") {
    return "free";
  }
  return `${input} in, ${output} out per M`;
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "June", "July", "Aug", "Sept", "Oct", "Nov", "Dec"];
const SOURCES = { openrouter: "OpenRouter", litellm: "LiteLLM", provider: "Provider", local: "Local", override: "Override" };

/** sourceLabel names the price source of a model, with the fetch date. */
export function sourceLabel(m) {
  const name = SOURCES[m.effectiveSource] || "Unknown";
  if ((m.effectiveSource === "openrouter" || m.effectiveSource === "litellm") && m.priceFetchedAt) {
    const d = new Date(m.priceFetchedAt);
    if (!Number.isNaN(d.getTime()) && d.getUTCFullYear() > 1) {
      return `${name}, ${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]}`;
    }
  }
  return name;
}

/** moveItem returns a copy of list with the item at from moved to to. */
export function moveItem(list, from, to) {
  const next = list.slice();
  if (from < 0 || from >= next.length || to < 0 || to >= next.length || from === to) {
    return next;
  }
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
}

/** sameTargets reports whether two target lists hold the same models in order. */
export function sameTargets(a, b) {
  return a.length === b.length && a.every((t, i) => t.value === b[i].value);
}

/** targetPayload turns targets into the body of a route request. */
export function targetPayload(list) {
  return list.map((t) => {
    const cut = t.value.indexOf("|");
    return { providerId: t.value.slice(0, cut), model: t.value.slice(cut + 1) };
  });
}

/** parsePrice reads a price field. Empty means 0. */
export function parsePrice(raw) {
  const text = String(raw ?? "").trim();
  if (text === "") {
    return { value: 0 };
  }
  const n = Number(text);
  if (!Number.isFinite(n) || n < 0) {
    return { error: "Enter a price of 0 or more" };
  }
  return { value: n };
}

const GRABBER = "M10 13a1 1 0 1 1 0-2 1 1 0 0 1 0 2Zm0-4a1 1 0 1 1 0-2 1 1 0 0 1 0 2Zm-4 4a1 1 0 1 1 0-2 1 1 0 0 1 0 2Zm5-9a1 1 0 1 1-2 0 1 1 0 0 1 2 0ZM7 8a1 1 0 1 1-2 0 1 1 0 0 1 2 0ZM6 5a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z";

/** grabberIcon draws the Octicon grabber 16, which the icon set lacks. */
function grabberIcon() {
  const ns = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(ns, "svg");
  for (const [k, v] of Object.entries({ width: "16", height: "16", viewBox: "0 0 16 16", class: "octicon octicon-grabber", "aria-hidden": "true", fill: "currentColor", focusable: "false" })) {
    svg.setAttribute(k, v);
  }
  const path = document.createElementNS(ns, "path");
  path.setAttribute("d", GRABBER);
  svg.appendChild(path);
  return svg;
}

function tokenNode(t) {
  return h("span", { class: "token ai-token" + (t.missing ? " is-missing" : "") }, h("span", { class: "token-text", text: t.label }));
}

/* ---------- Task routes ---------- */

function readTargets(list) {
  return Array.from(list.querySelectorAll(".ai-target")).map((li) => ({
    value: li.dataset.value,
    label: li.dataset.label,
    provider: li.dataset.provider,
    price: li.dataset.price,
    missing: li.dataset.missing === "true",
  }));
}

function initRouteForm(form) {
  const scope = form.dataset.scope;
  const task = form.dataset.task;
  const row = form.closest(".ai-task");
  const details = row.querySelector("[data-task-details]");
  const taskLabel = row.dataset.label;
  const list = form.querySelector("[data-targets]");
  const empty = form.querySelector("[data-targets-empty]");
  const chips = row.querySelector("[data-chips]");
  const instanceTokens = row.querySelector("[data-instance-chips] .ai-tokens");
  const resetForm = row.querySelector("[data-reset-form]");
  const message = form.querySelector("[data-route-message]");
  const saveButton = form.querySelector("[data-route-save]");
  const cancelButton = form.querySelector("[data-route-cancel]");
  const addButton = form.querySelector("[data-add-model]");
  const url = scope === "instance" ? `/api/ai/routes/instance/${enc(task)}` : `/api/ai/routes/me/${enc(task)}`;
  let saved = readTargets(list);
  let current = saved.slice();

  const clearMessage = () => message.replaceChildren();
  const showMessage = (variant, text) => message.replaceChildren(inlineMessage({ variant, text }));
  const focusMenu = (index) => {
    const li = list.children[Math.min(index, list.children.length - 1)];
    const target = (li && li.querySelector("[data-target-menu]")) || addButton;
    target.focus();
  };

  function render() {
    list.replaceChildren(...current.map(targetNode));
    empty.hidden = current.length > 0;
  }

  function targetNode(t, i) {
    const handle = h("span", { class: "ai-drag", "aria-hidden": "true", dataset: { drag: "" } });
    handle.appendChild(grabberIcon());
    const menu = h(
      "button",
      { type: "button", class: "btn btn-invisible btn-icon btn-sm row-menu-btn", "aria-label": `Actions: ${t.label}`, "aria-haspopup": "true", "aria-expanded": "false", dataset: { targetMenu: "" } },
      h("span", { class: "btn-visual", "aria-hidden": "true" }, iconNode("kebab-horizontal"))
    );
    menu.addEventListener("click", () => openTargetMenu(menu, i));
    return h(
      "li",
      { class: "ai-target" + (t.missing ? " is-missing" : ""), dataset: { value: t.value } },
      h("input", { type: "hidden", name: "target", value: t.value }),
      handle,
      h("span", { class: "counter-label ai-target-rank", "aria-hidden": "true", text: String(i + 1) }),
      h("span", { class: "ai-target-main" }, h("span", { class: "ai-target-name", text: t.label }), h("span", { class: "ai-target-sub", text: `${t.provider} · ${t.price}` })),
      h("span", { class: "ai-target-actions" }, menu)
    );
  }

  function changed(focusIndex, text) {
    render();
    clearMessage();
    // The unsaved changes warning in app.js compares the hidden target fields.
    form.dispatchEvent(new Event("change", { bubbles: true }));
    if (focusIndex !== null) {
      // The menu returns focus to its button, which the new list replaced.
      setTimeout(() => focusMenu(focusIndex), 0);
    }
    announce(text);
  }

  function openTargetMenu(anchor, i) {
    const t = current[i];
    const menuItems = [];
    if (i > 0) {
      menuItems.push({ label: "Move up", onClick: () => { current = moveItem(current, i, i - 1); changed(i - 1, `${t.label} moved to position ${i} of ${current.length}`); } });
    }
    if (i < current.length - 1) {
      menuItems.push({ label: "Move down", onClick: () => { current = moveItem(current, i, i + 1); changed(i + 1, `${t.label} moved to position ${i + 2} of ${current.length}`); } });
    }
    if (menuItems.length) {
      menuItems.push({ separator: true });
    }
    menuItems.push({ label: "Remove", danger: true, onClick: () => { current = current.filter((_, k) => k !== i); changed(i, `${t.label} removed. Save to keep the change.`); } });
    openMenu(anchor, menuItems, { placement: "bottom-end", label: `Actions: ${t.label}` });
  }

  // Pointer dragging on the handle. The ActionMenu is the keyboard route.
  list.addEventListener("pointerdown", (event) => {
    const handle = event.target.closest && event.target.closest("[data-drag]");
    if (!handle || event.button > 0) {
      return;
    }
    const li = handle.closest(".ai-target");
    const from = Array.from(list.children).indexOf(li);
    event.preventDefault();
    if (handle.setPointerCapture) {
      handle.setPointerCapture(event.pointerId);
    }
    li.classList.add("is-dragging");
    const onMove = (e) => {
      let before = null;
      for (const other of list.children) {
        if (other === li) {
          continue;
        }
        const rect = other.getBoundingClientRect();
        if (e.clientY < rect.top + rect.height / 2) {
          before = other;
          break;
        }
      }
      if (before) {
        list.insertBefore(li, before);
      } else {
        list.appendChild(li);
      }
    };
    const onUp = () => {
      handle.removeEventListener("pointermove", onMove);
      handle.removeEventListener("pointerup", onUp);
      handle.removeEventListener("pointercancel", onUp);
      li.classList.remove("is-dragging");
      const to = Array.from(list.children).indexOf(li);
      if (to !== from) {
        const t = current[from];
        current = moveItem(current, from, to);
        changed(null, `${t.label} moved to position ${to + 1} of ${current.length}`);
      } else {
        render();
      }
    };
    handle.addEventListener("pointermove", onMove);
    handle.addEventListener("pointerup", onUp);
    handle.addEventListener("pointercancel", onUp);
  });

  addButton.addEventListener("click", async () => {
    const byValue = new Map();
    const panel = selectPanel(addButton, {
      title: `Add a model to ${taskLabel}`,
      subtitle: "Prices are per million tokens",
      placeholder: "Filter models",
      filterLabel: "Filter models",
      emptyTitle: "No model to add",
      emptyBody: "Every enabled model that can run this task is in the list.",
      onSelect: (id) => {
        const t = byValue.get(id);
        if (!t) {
          return;
        }
        current = task === "embeddings" && current.length ? [t, ...current] : [...current, t];
        changed(current.indexOf(t), `${t.label} added. Save to keep the change.`);
      },
    });
    panel.setLoading(true, "Loading models");
    let rows;
    try {
      rows = items(await api("GET", `/api/ai/models?capability=${enc(task)}`));
    } catch (err) {
      panel.setError(`The models could not be loaded. ${err.message}`);
      return;
    }
    const chosen = new Set(current.map((t) => t.value));
    const cost = (m) => (m.effectivePrice ? (m.effectivePrice.inputPerMTok || 0) + (m.effectivePrice.outputPerMTok || 0) : Infinity);
    const options = rows
      .filter((m) => m.enabled && m.available && !chosen.has(`${m.providerId}|${m.model}`))
      .sort((a, b) => cost(a) - cost(b) || (a.name || a.model).localeCompare(b.name || b.model))
      .map((m) => {
        const t = { value: `${m.providerId}|${m.model}`, label: m.name || m.model, provider: m.providerName || m.providerId, price: chipPrice(m), missing: false };
        byValue.set(t.value, t);
        return { id: t.value, label: t.label, description: `${t.provider} · ${t.price}` };
      });
    panel.setLoading(false);
    panel.setItems(options);
  });

  function updateTokens() {
    if (saved.length) {
      chips.replaceChildren(...saved.map(tokenNode));
    } else if (scope === "me") {
      const copies = instanceTokens ? Array.from(instanceTokens.children).map((c) => c.cloneNode(true)) : [];
      chips.replaceChildren(h("span", { class: "ui-label ui-label-secondary", text: "Instance" }), ...copies);
    } else {
      chips.replaceChildren(h("span", { class: "ai-tokens-empty", text: task === "default" ? "No models" : "Uses Default" }));
    }
  }

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (saveButton.getAttribute("aria-busy") === "true") {
      return;
    }
    // An unchanged list saves nothing and shows nothing.
    if (sameTargets(current, saved)) {
      return;
    }
    clearMessage();
    setBusy(saveButton, true, { message: `Saving models for ${taskLabel}` });
    try {
      await api("PUT", url, { targets: targetPayload(current) });
      saved = current.slice();
      form.dispatchEvent(new CustomEvent("markspace:saved", { bubbles: true }));
      updateTokens();
      if (resetForm) {
        resetForm.hidden = saved.length === 0;
      }
      showMessage("success", "Models saved");
      announce(`Models for ${taskLabel} saved`);
    } catch (err) {
      showMessage("critical", `Not saved. ${err.message}`);
      announce(`Models for ${taskLabel} not saved. ${err.message}`);
    } finally {
      setBusy(saveButton, false);
    }
  });

  cancelButton.addEventListener("click", () => {
    current = saved.slice();
    render();
    form.dispatchEvent(new CustomEvent("markspace:saved", { bubbles: true }));
    clearMessage();
    details.open = false;
    details.querySelector("summary").focus();
  });

  if (resetForm) {
    resetForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      const button = resetForm.querySelector("button");
      setBusy(button, true, { message: `Resetting ${taskLabel}` });
      try {
        await api("DELETE", `/api/ai/routes/me/${enc(task)}`);
      } catch (err) {
        if (err.status !== 404) {
          setBusy(button, false);
          showMessage("critical", `Not reset. ${err.message}`);
          announce(`${taskLabel} not reset. ${err.message}`);
          return;
        }
      }
      setBusy(button, false);
      saved = [];
      current = [];
      render();
      updateTokens();
      clearMessage();
      addButton.focus();
      resetForm.hidden = true;
      announce(`${taskLabel} uses the instance choice`);
    });
  }

  render();
}

/** iconNode renders an Octicon through ui.js without importing icon twice. */
function iconNode(name) {
  return iconFn(name, 16);
}
let iconFn = () => document.createElement("span");

/* ---------- Model table ---------- */

function initModels(root) {
  const filter = root.querySelector("[data-model-filter]");
  const tableWrap = root.querySelector("[data-models-table]");
  const empty = root.querySelector("[data-models-empty]");
  if (filter) {
    const apply = () => {
      const data = new FormData(filter);
      const cap = data.get("capability") || "";
      const provider = data.get("provider") || "";
      const q = String(data.get("q") || "").trim().toLowerCase();
      let shown = 0;
      root.querySelectorAll("[data-model-row]").forEach((row) => {
        const match = (!cap || ` ${row.dataset.caps} `.includes(` ${cap} `)) && (!provider || row.dataset.providerId === provider) && (!q || row.dataset.search.includes(q));
        row.hidden = !match;
        if (match) {
          shown++;
        }
      });
      const count = root.querySelector("[data-model-count]");
      if (count) {
        count.textContent = String(shown);
      }
      const countText = root.querySelector("[data-model-count-text]");
      if (countText) {
        countText.textContent = `(${shown})`;
      }
      if (tableWrap) {
        tableWrap.hidden = shown === 0;
      }
      if (empty) {
        empty.hidden = shown > 0;
      }
      const params = new URLSearchParams(window.location.search);
      for (const [k, v] of [["capability", cap], ["provider", provider], ["q", q]]) {
        if (v) {
          params.set(k, v);
        } else {
          params.delete(k);
        }
      }
      for (const k of ["flash", "error", "field", "price", "saved", "task", "edit", "add"]) {
        params.delete(k);
      }
      const qs = params.toString();
      history.replaceState(null, "", `${window.location.pathname}${qs ? `?${qs}` : ""}#models`);
      return shown;
    };
    let timer = 0;
    filter.addEventListener("submit", (e) => {
      e.preventDefault();
      apply();
    });
    filter.addEventListener("change", apply);
    filter.addEventListener("input", () => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        const shown = apply();
        announce(shown === 1 ? "1 model" : `${shown} models`);
      }, 300);
    });
  }

  const patch = (row, body) => api("PATCH", `/api/ai/models/${enc(row.dataset.providerId)}/${modelPath(row.dataset.model)}`, body);

  // Focus inside the models table scrolls its region to show the control (ui.js).
  watchTableScroll(document);

  root.querySelectorAll("[data-toggle-form]").forEach((form) => {
    const row = form.closest("[data-model-row]");
    const box = form.querySelector("[data-model-toggle]");
    const label = row.dataset.label;
    const control = toggleSwitch({
      checked: box.checked,
      size: "small",
      label: `${label} enabled`,
      onChange: (on) =>
        patch(row, { enabled: on }).then(
          () => {
            box.checked = on;
          },
          (err) => {
            announce(`${label} not saved. ${err.message}`);
            throw err;
          }
        ),
    });
    form.appendChild(control);
    form.addEventListener("submit", (e) => e.preventDefault());
  });

  root.addEventListener("click", (event) => {
    const edit = event.target.closest && event.target.closest("[data-price-edit]");
    if (!edit) {
      return;
    }
    event.preventDefault();
    openPriceDialog(edit.closest("[data-model-row]"), edit, patch);
  });
}

function applyModel(row, m) {
  const [input, output] = priceLabels(m);
  row.querySelector("[data-price-in]").textContent = input;
  row.querySelector("[data-price-out]").textContent = output;
  row.querySelector("[data-price-source]").textContent = sourceLabel(m);
  const shown = m.priceOverride || m.effectivePrice;
  row.dataset.override = m.priceOverride ? "true" : "";
  row.dataset.overrideIn = shown ? String(shown.inputPerMTok || 0) : "";
  row.dataset.overrideOut = shown ? String(shown.outputPerMTok || 0) : "";
}

function openPriceDialog(row, trigger, patch) {
  const label = row.dataset.label;
  const input = formControl({ label: "Input price", name: "inputPerMTok", type: "number", value: row.dataset.overrideIn, caption: "Per million input tokens", min: "0", step: "any", inputmode: "decimal" });
  const output = formControl({ label: "Output price", name: "outputPerMTok", type: "number", value: row.dataset.overrideOut, caption: "Per million output tokens", min: "0", step: "any", inputmode: "decimal" });
  const field = (wrap) => wrap.querySelector("input");
  const form = h("form", { class: "ai-price-form", novalidate: true }, input, output);
  const save = async (value) => {
    const m = await patch(row, { priceOverride: value });
    applyModel(row, m);
  };
  const footer = [{ label: "Cancel" }];
  if (row.dataset.override === "true") {
    footer.push({
      label: "Use fetched price",
      onClick: async () => {
        await save(null);
        announce(`${label} uses the fetched price`);
      },
    });
  }
  footer.push({
    label: "Save",
    variant: "primary",
    onClick: async () => {
      const a = parsePrice(field(input).value);
      const b = parsePrice(field(output).value);
      setFieldError(input, a.error || "");
      setFieldError(output, b.error || "");
      if (a.error || b.error) {
        field(a.error ? input : output).focus();
        return false;
      }
      await save({ inputPerMTok: a.value, outputPerMTok: b.value });
      announce(`Price of ${label} saved`);
      return undefined;
    },
  });
  const handle = openDialog({ title: `Price of ${label}`, subtitle: "US dollars per million tokens", width: "medium", content: form, footer, returnFocus: trigger, initialFocus: field(input) });
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    handle.buttons[handle.buttons.length - 1].click();
  });
}

/* ---------- Provider forms ---------- */

function initPresetForm(form) {
  const radios = Array.from(form.querySelectorAll("input[name=preset]"));
  const apply = () => {
    const chosen = radios.find((r) => r.checked);
    const fields = chosen ? new Set(chosen.dataset.fields.split(" ")) : null;
    form.querySelectorAll("[data-field]").forEach((node) => {
      node.hidden = fields !== null && !fields.has(node.dataset.field);
    });
  };
  radios.forEach((r) => r.addEventListener("change", apply));
  apply();
}

/* ---------- Start ---------- */

const page = typeof document !== "undefined" ? document.querySelector("[data-ai-page]") : null;
if (page) {
  const ui = await import("../ui.js");
  iconFn = ui.icon;
  page.querySelectorAll("[data-nojs]").forEach((el) => {
    el.hidden = true;
  });
  page.querySelectorAll("[data-js]").forEach((el) => {
    el.hidden = false;
  });
  page.querySelectorAll("[data-route-form]").forEach(initRouteForm);
  initModels(page);
  document.querySelectorAll("[data-provider-form]").forEach(initPresetForm);
  // A refused form shows its message in its own Dialog or row, so the page
  // Banner would repeat it.
  if (page.hasAttribute("data-error-claimed")) {
    document.querySelectorAll('[data-flash="error"]').forEach((el) => el.remove());
  }
}
