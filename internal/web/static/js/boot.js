// Loaded as a module on every page. Registers the service worker, announces
// an updated worker, and keeps the install prompt.

const markspace = (window.markspace = window.markspace || {});
let installPrompt = null;
let reloadRequested = false;
let reloading = false;

/**
 * Asks the waiting worker to take over. This tab reloads once the new
 * worker controls it.
 * @param {ServiceWorker} worker
 */
function activate(worker) {
  reloadRequested = true;
  worker.postMessage({ type: "SKIP_WAITING" });
}

/**
 * Announces a waiting worker. It dispatches markspace:update-available on
 * document with detail.reload and stores the same object on
 * window.markspace.pendingUpdate for listeners that attach later. Pages
 * without the client shell also get a small reload bar, built from text
 * nodes only.
 * @param {ServiceWorker} worker the waiting worker
 */
function offerReload(worker) {
  const reload = () => activate(worker);
  markspace.pendingUpdate = { reload };
  document.dispatchEvent(new CustomEvent("markspace:update-available", { detail: { reload } }));
  if (document.querySelector("[data-shell-root]") || document.querySelector("[data-update-prompt]")) return;
  const bar = document.createElement("div");
  bar.setAttribute("role", "status");
  bar.setAttribute("data-update-prompt", "");
  Object.assign(bar.style, {
    position: "fixed", left: "50%", bottom: "16px", transform: "translateX(-50%)", zIndex: "1000",
    display: "flex", gap: "12px", alignItems: "center", padding: "10px 14px", borderRadius: "10px",
    background: "#231f26", color: "#ffffff", font: "14px system-ui, sans-serif", boxShadow: "0 4px 16px rgba(0,0,0,.25)",
  });
  const text = document.createElement("span");
  text.textContent = "A new version is available.";
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = "Reload";
  button.addEventListener("click", reload);
  const dismiss = document.createElement("button");
  dismiss.type = "button";
  dismiss.textContent = "Later";
  dismiss.addEventListener("click", () => bar.remove());
  bar.append(text, button, dismiss);
  (document.body || document.documentElement).appendChild(bar);
}

/**
 * Watches a registration for a worker that finishes installing while this
 * page is already controlled.
 * @param {ServiceWorkerRegistration} reg
 */
function watchRegistration(reg) {
  if (reg.waiting && navigator.serviceWorker.controller) offerReload(reg.waiting);
  reg.addEventListener("updatefound", () => {
    const worker = reg.installing;
    if (!worker) return;
    worker.addEventListener("statechange", () => {
      if (worker.state === "installed" && navigator.serviceWorker.controller) offerReload(worker);
    });
  });
}

/**
 * Shows the stored install prompt.
 * @returns {Promise<string>} "accepted", "dismissed" or "unavailable"
 */
markspace.install = async function install() {
  if (!installPrompt) return "unavailable";
  const prompt = installPrompt;
  installPrompt = null;
  try {
    await prompt.prompt();
    const choice = await prompt.userChoice;
    return (choice && choice.outcome) || "dismissed";
  } catch {
    return "unavailable";
  }
};

/**
 * Reports whether an install prompt is stored.
 * @returns {boolean}
 */
markspace.canInstall = function canInstall() {
  return installPrompt !== null;
};

try {
  window.addEventListener("beforeinstallprompt", (event) => {
    event.preventDefault();
    installPrompt = event;
    document.dispatchEvent(new CustomEvent("markspace:installable"));
  });
  window.addEventListener("appinstalled", () => {
    installPrompt = null;
  });
  if ("serviceWorker" in navigator && window.isSecureContext !== false) {
    navigator.serviceWorker.addEventListener("controllerchange", () => {
      if (!reloadRequested || reloading) return;
      reloading = true;
      window.location.reload();
    });
    navigator.serviceWorker
      .register("/sw.js", { scope: "/" })
      .then(watchRegistration)
      .catch((err) => console.warn("markspace: service worker registration failed", err));
  }
} catch (err) {
  console.warn("markspace: boot failed", err);
}

// Server pages with a wide table keep the focused control in view. The shell
// installs the same handler itself.
function watchTables() {
  if (document.querySelector(".table-scroll") && !document.querySelector("[data-shell-root]")) {
    import("/static/js/ui.js").then((ui) => ui.watchTableScroll(document)).catch(() => {});
  }
}
if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", watchTables, { once: true });
else watchTables();
