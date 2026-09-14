// Browser-only settings on the server-rendered settings pages.
//
// The Offline files page shows how much of the file cache budget this browser
// uses. A new limit waits for its Save button (docs/primer.md 6.1), and clearing
// the cache asks in a ConfirmationDialog (6.2). The Profile page's Editor
// section holds ToggleSwitches for preferences stored in this browser. They
// apply at once and show no Saved message. The pages are rendered on the
// server, so this module finds the signed-in user itself.

import { createFileCache } from "./filecache.js";
import { announce, confirmDialog, formatBytes, inlineMessage } from "./ui.js";

/**
 * Returns the signed-in user id from the cached session, then from the server.
 * @returns {Promise<string>}
 */
async function signedInUserId() {
  try {
    const cached = JSON.parse(window.localStorage.getItem("markspace.user") || "null");
    if (cached && cached.id) return cached.id;
  } catch {
    // No cached session. Ask the server.
  }
  try {
    const res = await fetch("/api/auth/me", { credentials: "same-origin", headers: { Accept: "application/json" } });
    if (!res.ok) return "";
    const data = await res.json();
    return (data && data.user && data.user.id) || "";
  } catch {
    return "";
  }
}

/** Minimal store for the cache. The settings page has no local note database open. */
const emptyStore = {
  async listAllFiles() {
    return [];
  },
  async listAllFolders() {
    return [];
  },
};

/**
 * Formats the usage line.
 * @param {{used: number, count: number, pinnedCount: number, budget: number}} u
 * @returns {string}
 */
export function usageText(u) {
  const kept = u.pinnedCount === 1 ? "1 file kept offline" : `${u.pinnedCount} files kept offline`;
  return `Using ${formatBytes(u.used)} of ${formatBytes(u.budget)} in this browser, ${kept}.`;
}

/**
 * Wires the preference ToggleSwitches under root. Each switch is a button
 * with aria-pressed and data-pref-switch naming a localStorage key. On stores
 * "1", and off removes the key.
 * @param {ParentNode} root
 * @param {{getItem(key: string): string | null, setItem(key: string, value: string): void, removeItem(key: string): void} | null} storage
 */
export function wirePreferenceSwitches(root, storage) {
  for (const track of root.querySelectorAll("[data-pref-switch]")) {
    const key = track.getAttribute("data-pref-switch");
    const wrap = track.closest(".toggle-switch");
    const set = (on) => {
      track.setAttribute("aria-pressed", on ? "true" : "false");
      if (wrap) wrap.setAttribute("data-checked", on ? "true" : "false");
    };
    let on = false;
    try {
      on = !!storage && storage.getItem(key) === "1";
    } catch {
      on = false;
    }
    set(on);
    track.addEventListener("click", () => {
      const next = track.getAttribute("aria-pressed") !== "true";
      set(next);
      try {
        if (!storage) return;
        if (next) storage.setItem(key, "1");
        else storage.removeItem(key);
      } catch {
        // Storage is disabled. The switch still shows the choice for this page.
      }
    });
  }
}

/**
 * Makes sure the radio group offers the current budget and checks it. A
 * budget outside the offered sizes gets its own radio.
 * @param {Element} group
 * @param {string} budget
 * @returns {HTMLInputElement}
 */
export function checkBudget(group, budget) {
  let input = Array.from(group.querySelectorAll('input[type="radio"]')).find((r) => r.value === budget);
  if (!input) {
    const doc = group.ownerDocument || document;
    const label = doc.createElement("label");
    label.className = "check";
    input = doc.createElement("input");
    input.type = "radio";
    input.name = "budget";
    input.value = budget;
    label.appendChild(input);
    label.appendChild(doc.createTextNode(" " + formatBytes(Number(budget))));
    group.appendChild(label);
  }
  input.checked = true;
  return input;
}

/**
 * Runs the Offline files page.
 * @param {Element} section
 */
async function runOfflineFiles(section) {
  const form = section.querySelector("[data-offline-budget-form]");
  const group = section.querySelector("[data-offline-budget]");
  const usage = section.querySelector("[data-offline-usage]");
  const message = section.querySelector("[data-offline-saved]");
  const clear = section.querySelector("[data-offline-clear]");
  const unavailable = section.querySelector("[data-offline-unavailable]");

  const userId = await signedInUserId();
  if (!userId || typeof caches === "undefined") {
    // The controls cannot work here, so they go and a message explains why.
    for (const part of section.querySelectorAll("[data-offline-part]")) part.hidden = true;
    unavailable.replaceChildren(inlineMessage({ variant: "unavailable", text: "Offline files are not available in this browser." }));
    unavailable.hidden = false;
    return;
  }
  // The page reads the cache but never downloads, so the store stays empty
  // and reconcile is not called here.
  const cache = createFileCache({ userId, store: emptyStore });
  await cache.ready;

  let saved = String(cache.budget());
  checkBudget(group, saved);
  const refresh = async () => {
    usage.textContent = usageText(await cache.usage());
  };
  await refresh();

  const chosen = () => {
    const input = group.querySelector('input[type="radio"]:checked');
    return input ? input.value : saved;
  };
  // A changed limit that is not saved yet warns before the page unloads.
  window.addEventListener("beforeunload", (event) => {
    if (chosen() !== saved) event.preventDefault();
  });
  form.addEventListener("change", () => message.replaceChildren());
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const value = chosen();
    // Submitting an unchanged form does nothing and shows nothing.
    if (value === saved) return;
    try {
      await cache.setBudget(Number(value));
      saved = value;
      await refresh();
      message.replaceChildren(inlineMessage({ variant: "success", text: "Storage limit saved" }));
      announce("Storage limit saved");
    } catch {
      message.replaceChildren(inlineMessage({ variant: "critical", text: "The limit could not be saved. Try again." }));
      announce("The limit could not be saved");
    }
  });

  clear.addEventListener("click", async () => {
    const done = await confirmDialog({
      title: "Clear offline files?",
      body: "Every stored file is removed from this browser. A file downloads again the next time you open it.",
      confirmLabel: "Clear offline files",
      danger: true,
      returnFocus: clear,
      onConfirm: async () => {
        await cache.clear();
        await refresh();
      },
    });
    if (done) announce("Offline files cleared");
  });
}

function main() {
  let storage = null;
  try {
    storage = window.localStorage;
  } catch {
    storage = null;
  }
  wirePreferenceSwitches(document, storage);
  const section = document.querySelector("[data-offline-files]");
  if (section) runOfflineFiles(section);
}

// Tests import this module without a page, so it runs only on a settings page.
if (typeof window !== "undefined" && window.location && /^\/settings(\/|$)/.test(window.location.pathname || "")) {
  main();
}
