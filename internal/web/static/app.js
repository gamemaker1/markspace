/*
 * Markspace page enhancements for the server pages. Every feature here is
 * optional: the pages work with scripting off through plain links and forms.
 * Components follow Primer (docs/primer.md): ActionMenu keyboard handling for row
 * menus, ConfirmationDialog for data-confirm, Banners with Undo for
 * data-undo-*, Tooltips on icon buttons, busy buttons and RelativeTime.
 */
(function () {
  "use strict";

  var root = document.documentElement;
  var body = document.body;
  root.classList.add("js");

  var THEME_KEY = "markspace.theme";
  var TREE_KEY = "markspace.tree.";
  var SVG_NS = "http://www.w3.org/2000/svg";

  /* ---------- Small helpers ---------- */

  function storageGet(key) {
    try {
      return window.localStorage.getItem(key);
    } catch (err) {
      return null;
    }
  }

  function storageSet(key, value) {
    try {
      if (value === null) {
        window.localStorage.removeItem(key);
      } else {
        window.localStorage.setItem(key, value);
      }
    } catch (err) {
      /* storage is full or disabled */
    }
  }

  function el(tag, className, text) {
    var node = document.createElement(tag);
    if (className) {
      node.className = className;
    }
    if (text !== undefined && text !== null) {
      node.textContent = String(text);
    }
    return node;
  }

  function debounce(fn, wait) {
    var timer = null;
    return function () {
      var args = arguments;
      var self = this;
      clearTimeout(timer);
      timer = setTimeout(function () {
        fn.apply(self, args);
      }, wait);
    };
  }

  /* ui loads the shared component kit on first use. */
  var uiModule = null;
  function ui() {
    if (!uiModule) {
      uiModule = import("/static/js/ui.js");
    }
    return uiModule;
  }

  /* The container that page Banners go to. */
  function bannerHost() {
    return document.querySelector("[data-banner-host]") || document.querySelector("#main .page") || document.getElementById("main") || body;
  }

  /* announce sets the text of the status region, as ui.announce does. */
  function announce(message) {
    var region = document.querySelector("[data-status-region]");
    if (!region) {
      region = el("div", "sr-only");
      region.setAttribute("role", "status");
      region.setAttribute("aria-live", "polite");
      region.setAttribute("data-status-region", "");
      body.appendChild(region);
    }
    region.textContent = region.textContent === message ? message + " " : message;
  }

  /* status reports a result. Errors, and messages with an action, show as a
   * Banner at the top of the page. Everything is announced. options are
   * kind ("info", "success", "warn", "error") and action {label, onClick}. */
  function status(message, options) {
    var opts = options || {};
    if (opts.kind !== "error" && !opts.action) {
      announce(message);
      return Promise.resolve(null);
    }
    return ui()
      .then(function (mod) {
        return mod.banner({
          variant: { error: "critical", warn: "warning", success: "success" }[opts.kind] || "info",
          title: message,
          dismissible: opts.kind !== "error",
          into: bannerHost(),
          actions: opts.action ? [{ label: opts.action.label, onClick: opts.action.onClick }] : []
        });
      })
      .catch(function () {
        flash(message, opts.kind === "error" ? "error" : "ok");
      });
  }

  /* notify is the older name of status, with the kind as the second argument. */
  function notify(message, kind) {
    return status(message, { kind: kind || "info" });
  }

  /* flash shows a plain Banner without the component kit. */
  function flash(message, kind) {
    var host = bannerHost();
    var banner = el("section", "banner " + (kind === "error" ? "banner-critical" : "banner-success"));
    banner.setAttribute("aria-label", kind === "error" ? "Critical" : "Success");
    banner.appendChild(el("span", "banner-text", message));
    host.insertBefore(banner, host.firstChild);
    announce(message);
  }

  /* spinnerSVG draws Primer's small Spinner. */
  function spinnerSVG() {
    var svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("class", "spinner-svg");
    svg.setAttribute("viewBox", "0 0 16 16");
    svg.setAttribute("width", "16");
    svg.setAttribute("height", "16");
    svg.setAttribute("fill", "none");
    svg.setAttribute("aria-hidden", "true");
    svg.setAttribute("focusable", "false");
    var track = document.createElementNS(SVG_NS, "circle");
    [["cx", "8"], ["cy", "8"], ["r", "7"], ["stroke", "currentColor"], ["stroke-opacity", "0.25"], ["stroke-width", "2"], ["vector-effect", "non-scaling-stroke"]].forEach(function (a) {
      track.setAttribute(a[0], a[1]);
    });
    var arc = document.createElementNS(SVG_NS, "path");
    [["d", "M15 8a7.002 7.002 0 00-7-7"], ["stroke", "currentColor"], ["stroke-width", "2"], ["stroke-linecap", "round"], ["vector-effect", "non-scaling-stroke"]].forEach(function (a) {
      arc.setAttribute(a[0], a[1]);
    });
    svg.appendChild(track);
    svg.appendChild(arc);
    var wrap = el("span", "spinner spinner-small btn-spinner");
    wrap.appendChild(svg);
    return wrap;
  }

  /* setBusy marks a button busy as ui.setBusy does, without loading ui.js:
   * aria-disabled, the label kept but hidden, a Spinner in its place. It
   * never sets disabled, so a submit button keeps its name and value. */
  function setBusy(button, busy) {
    if (!button) {
      return;
    }
    if (busy) {
      if (button.getAttribute("data-busy") === "true") {
        return;
      }
      button.setAttribute("data-busy", "true");
      button.setAttribute("aria-busy", "true");
      button.setAttribute("aria-disabled", "true");
      if (button.classList.contains("btn") || button.classList.contains("icon-btn") || button.classList.contains("menu-item")) {
        if (!button.querySelector(".btn-label")) {
          var label = el("span", "btn-label");
          while (button.firstChild) {
            label.appendChild(button.firstChild);
          }
          button.appendChild(label);
        }
        button.classList.add("is-busy-centered");
        button.appendChild(spinnerSVG());
      }
    } else {
      button.removeAttribute("data-busy");
      button.removeAttribute("aria-busy");
      button.removeAttribute("aria-disabled");
      button.classList.remove("is-busy-centered");
      Array.prototype.forEach.call(button.querySelectorAll(".btn-spinner"), function (node) {
        node.parentNode.removeChild(node);
      });
    }
  }

  /* ---------- RelativeTime ---------- */

  if (document.querySelector("relative-time") && window.customElements && !window.customElements.get("relative-time")) {
    import("/static/vendor/relative-time-element-5.3.1.js").catch(function () {
      /* the server rendered the date as fallback text */
    });
  }

  /* ---------- Undo carried over a page load ---------- */

  var UNDO_KEY = "markspace.undo";

  /* carryUndo remembers an Undo for the next page load. entry is
   * {label, form: {url, fields}} or {label, request: {method, url, body}, go}. */
  function carryUndo(entry) {
    try {
      entry.at = Date.now();
      window.sessionStorage.setItem(UNDO_KEY, JSON.stringify(entry));
    } catch (err) {
      /* storage is disabled; the change still happens */
    }
  }

  function takeUndo() {
    try {
      var raw = window.sessionStorage.getItem(UNDO_KEY);
      window.sessionStorage.removeItem(UNDO_KEY);
      var entry = raw ? JSON.parse(raw) : null;
      return entry && Date.now() - (entry.at || 0) < 60000 ? entry : null;
    } catch (err) {
      return null;
    }
  }

  /* postForm submits fields to url as a normal form post. */
  function postForm(url, fields) {
    var form = el("form");
    form.method = "post";
    form.action = url;
    form.hidden = true;
    Object.keys(fields).forEach(function (name) {
      var input = el("input");
      input.type = "hidden";
      input.name = name;
      input.value = fields[name];
      form.appendChild(input);
    });
    form.setAttribute("data-no-busy", "");
    body.appendChild(form);
    form.submit();
  }

  function runUndo(entry) {
    if (entry.form) {
      postForm(entry.form.url, entry.form.fields || {});
      return new Promise(function () { /* the page navigates */ });
    }
    var req = entry.request;
    return api(req.method || "POST", req.url, req.body).then(function () {
      if (entry.go) {
        window.location.href = entry.go;
      } else {
        window.location.reload();
      }
    });
  }

  /* undoFields builds the fields of the inverse form from a submit button:
   * the form's hidden fields, action from data-undo-action and every
   * data-undo-field-* attribute. */
  function undoFields(button) {
    var fields = {};
    var form = button.form;
    if (form) {
      Array.prototype.forEach.call(form.querySelectorAll('input[type="hidden"]'), function (input) {
        fields[input.name] = input.value;
      });
    }
    fields.action = button.getAttribute("data-undo-action");
    Array.prototype.forEach.call(button.attributes, function (attr) {
      if (attr.name.indexOf("data-undo-field-") === 0) {
        var name = attr.name.slice(16).replace(/-([a-z])/g, function (m, c) { return c.toUpperCase(); });
        fields[name] = attr.value;
      }
    });
    return fields;
  }

  /* A submit button with data-undo-action acts at once. The row it acted on
   * is gone after the page loads again, so the Undo shows in a dismissible
   * info Banner at the top of that page (docs/primer.md 5.7). */
  document.addEventListener("submit", function (event) {
    var button = event.submitter;
    if (event.defaultPrevented || !button || !button.hasAttribute("data-undo-action")) {
      return;
    }
    carryUndo({
      label: button.getAttribute("data-undo-label") || "Done",
      form: { url: button.form.getAttribute("action") || window.location.pathname, fields: undoFields(button) }
    });
  }, true);

  /* api sends a JSON request and resolves with the decoded body. A non-2xx
   * response rejects with an Error carrying status and the server message. */
  function api(method, url, payload) {
    var init = {
      method: method,
      credentials: "same-origin",
      headers: { Accept: "application/json" }
    };
    if (payload !== undefined) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(payload);
    }
    return fetch(url, init).then(function (res) {
      var type = res.headers.get("Content-Type") || "";
      var parse = type.indexOf("application/json") >= 0 ? res.json() : res.text();
      return parse.then(function (data) {
        if (!res.ok) {
          var err = new Error((data && data.error) || res.statusText || "Request failed");
          err.status = res.status;
          err.data = data;
          throw err;
        }
        return data;
      });
    });
  }

  /* listOf accepts either a bare array or an object wrapping one. */
  function listOf(data, key) {
    if (Array.isArray(data)) {
      return data;
    }
    if (data && Array.isArray(data[key])) {
      return data[key];
    }
    if (data && Array.isArray(data.items)) {
      return data.items;
    }
    return [];
  }

  function noteHref(note) {
    if (!note) {
      return "#";
    }
    var slug = note.spaceSlug || body.getAttribute("data-space-slug");
    if (slug && note.path) {
      var path = String(note.path).replace(/\.md$/, "");
      return "/s/" + encodeURIComponent(slug) + "/n/" +
        path.split("/").map(encodeURIComponent).join("/");
    }
    return "/n/" + encodeURIComponent(note.id);
  }

  window.Markspace = {
    api: api,
    el: el,
    debounce: debounce,
    listOf: listOf,
    noteHref: noteHref,
    flash: flash,
    ui: ui,
    status: status,
    notify: notify,
    announce: announce
  };

  /* ---------- Flash messages, errors and busy forms ---------- */

  (function () {
    var ok = document.querySelector('[data-flash="ok"]');
    var error = document.querySelector('[data-flash="error"]');
    /* An Undo carried from the page before shows unless that change failed. */
    var undo = takeUndo();
    if (undo && error) {
      undo = null;
    }
    if (ok && !undo) {
      /* A success Banner is not essential, so it is announced without focus. */
      announce(ok.textContent.trim());
    }
    if (undo) {
      ui()
        .then(function (mod) {
          if (ok && ok.parentNode) {
            ok.parentNode.removeChild(ok);
          }
          var banner = mod.banner({
            variant: "info",
            title: undo.label,
            dismissible: true,
            into: bannerHost(),
            actions: [{
              label: "Undo",
              onClick: function (event) {
                var button = event.currentTarget;
                mod.setBusy(button, true, { message: "Undoing" });
                return runUndo(undo).catch(function (err) {
                  mod.setBusy(button, false);
                  banner.parentNode && banner.parentNode.removeChild(banner);
                  status(err && err.message ? err.message : "The change could not be undone.", { kind: "error" });
                });
              }
            }]
          });
        })
        .catch(function () {
          /* the change stays; only the Undo is lost */
        });
    }
    Array.prototype.forEach.call(document.querySelectorAll("[data-js-only]"), function (node) {
      node.hidden = false;
    });
    /* Browser preferences: a checkbox with data-pref stores "1" in localStorage.
     * A preference applies at once, so the change is only announced. */
    Array.prototype.forEach.call(document.querySelectorAll('input[type="checkbox"][data-pref]'), function (box) {
      var key = box.getAttribute("data-pref");
      box.checked = storageGet(key) === "1";
      box.addEventListener("change", function () {
        storageSet(key, box.checked ? "1" : null);
        announce(box.checked ? "On" : "Off");
      });
    });
    if (error && document.querySelector(".field-error:not([hidden]), .form-validation:not([hidden])")) {
      error.hidden = true;
    }
    var field = document.querySelector(".field.has-error input, .field.has-error select, .field.has-error textarea");
    if (field) {
      field.focus();
    }
    /* A reload should not repeat a one-time message. */
    if (/[?&](flash|error|field)=/.test(window.location.search) && window.history.replaceState) {
      var params = new URLSearchParams(window.location.search);
      params.delete("flash");
      params.delete("error");
      params.delete("field");
      var query = params.toString();
      window.history.replaceState(window.history.state, "", window.location.pathname + (query ? "?" + query : "") + window.location.hash);
    }
  })();

  document.addEventListener("submit", function (event) {
    var form = event.target;
    if (event.defaultPrevented || form.hasAttribute("data-no-busy") || (form.getAttribute("method") || "").toLowerCase() !== "post") {
      return;
    }
    if (form.getAttribute("data-submitting") === "true") {
      event.preventDefault();
      return;
    }
    form.setAttribute("data-submitting", "true");
    var popover = form.closest("[popover]");
    if (popover && popover.hidePopover && popover.matches(":popover-open")) {
      popover.hidePopover();
    }
    setBusy(event.submitter || form.querySelector('[type="submit"]'), true);
  });

  window.addEventListener("pageshow", function () {
    Array.prototype.forEach.call(document.querySelectorAll("form[data-submitting]"), function (form) {
      form.removeAttribute("data-submitting");
    });
    Array.prototype.forEach.call(document.querySelectorAll("[data-busy]"), function (button) {
      setBusy(button, false);
    });
  });

  /* A file drop label shows the chosen file name. */
  document.addEventListener("change", function (event) {
    var input = event.target;
    var zone = input.closest && input.closest("[data-file-drop]");
    if (!zone || !input.files) {
      return;
    }
    var label = zone.querySelector("[data-file-label]");
    if (label && input.files.length) {
      label.textContent = input.files[0].name;
    }
  });

  ["dragenter", "dragover"].forEach(function (type) {
    document.addEventListener(type, function (event) {
      var zone = event.target.closest && event.target.closest("[data-file-drop]");
      if (zone) {
        event.preventDefault();
        zone.classList.add("is-over");
      }
    });
  });

  ["dragleave", "drop"].forEach(function (type) {
    document.addEventListener(type, function (event) {
      var zone = event.target.closest && event.target.closest("[data-file-drop]");
      if (!zone) {
        return;
      }
      zone.classList.remove("is-over");
      if (type === "drop" && event.dataTransfer && event.dataTransfer.files.length) {
        event.preventDefault();
        var input = zone.querySelector('input[type="file"]');
        input.files = event.dataTransfer.files;
        input.dispatchEvent(new Event("change", { bubbles: true }));
      }
    });
  });

  /* ---------- Theme ---------- */

  /* The head script sets data-color-mode before first paint, and "auto"
   * follows the system through CSS. The theme SegmentedControl in the
   * account menu stores light or dark, or removes the key for the system
   * theme, and applies the choice at once (docs/primer.md 2.3). A choice made in
   * another tab arrives as a storage event. */
  var THEME_LABELS = { light: "Light theme", dark: "Dark theme", system: "Use system theme" };

  function themePreference() {
    var stored = storageGet(THEME_KEY);
    return stored === "light" || stored === "dark" ? stored : "system";
  }

  function showTheme(pref) {
    root.setAttribute("data-color-mode", pref === "light" || pref === "dark" ? pref : "auto");
    Array.prototype.forEach.call(document.querySelectorAll("[data-theme-value]"), function (choice) {
      var on = choice.getAttribute("data-theme-value") === pref;
      choice.setAttribute("aria-checked", on ? "true" : "false");
      var item = choice.closest(".segmented-control-item");
      if (item) {
        item.classList.toggle("is-selected", on);
      }
    });
  }

  showTheme(themePreference());

  document.addEventListener("click", function (event) {
    var choice = event.target.closest && event.target.closest("[data-theme-value]");
    if (!choice) {
      return;
    }
    var pref = choice.getAttribute("data-theme-value");
    storageSet(THEME_KEY, pref === "system" ? null : pref);
    showTheme(pref);
    announce(THEME_LABELS[pref] || "");
  });

  /* Left and Right move between the segments. The menu moves Up and Down. */
  document.addEventListener("keydown", function (event) {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") {
      return;
    }
    var choice = event.target.closest && event.target.closest("[data-theme-value]");
    var group = choice && choice.closest(".segmented-control");
    if (!group) {
      return;
    }
    var choices = Array.prototype.slice.call(group.querySelectorAll("[data-theme-value]"));
    var next = choices.indexOf(choice) + (event.key === "ArrowRight" ? 1 : -1);
    event.preventDefault();
    choices[(next + choices.length) % choices.length].focus();
  });

  window.addEventListener("storage", function (event) {
    if (event.key === THEME_KEY || event.key === null) {
      showTheme(themePreference());
    }
  });

  /* ---------- Sidebar: collapsible column, drawer below 768 px ---------- */

  /* The drawer range is Primer's narrow range (docs/primer.md 3.7). */
  var SIDEBAR_KEY = "markspace.sidebar";
  var narrowQuery = window.matchMedia ? window.matchMedia("(max-width: calc(48rem - 0.02px))") : null;
  // The root element also carries data-sidebar="collapsed", so the query
  // names the pane element. A bare [data-sidebar] matched <html> and made the
  // whole page inert while the sidebar was hidden.
  var sidebar = document.querySelector(".sidebar[data-sidebar]");
  var drawerReturn = null;

  function isNarrow() {
    return !!(narrowQuery && narrowQuery.matches);
  }

  /* syncSidebar sets aria-expanded and inert from the current state. */
  function syncSidebar() {
    if (!sidebar) {
      return;
    }
    var open = isNarrow()
      ? root.getAttribute("data-drawer") === "open"
      : root.getAttribute("data-sidebar") !== "collapsed";
    sidebar.inert = !open;
    Array.prototype.forEach.call(document.querySelectorAll("[data-sidebar-toggle]"), function (button) {
      button.setAttribute("aria-expanded", open ? "true" : "false");
    });
  }

  function setDrawer(open) {
    if (open) {
      drawerReturn = document.activeElement;
      root.setAttribute("data-drawer", "open");
      // The open sheet is a modal Dialog titled "Navigation" (docs/primer.md 5.1).
      sidebar.setAttribute("role", "dialog");
      sidebar.setAttribute("aria-modal", "true");
      sidebar.setAttribute("aria-labelledby", "sidebar-sheet-title");
    } else {
      root.removeAttribute("data-drawer");
      sidebar.removeAttribute("role");
      sidebar.removeAttribute("aria-modal");
      sidebar.removeAttribute("aria-labelledby");
    }
    // The page behind the modal sheet takes no pointer or keyboard input.
    Array.prototype.forEach.call(document.querySelectorAll(".server-frame > .app-header, .server-frame .app-content"), function (el) {
      el.inert = !!open;
    });
    syncSidebar();
    if (open) {
      var first = sidebar.querySelector(".sidebar-inner a[href], .sidebar-inner button") || sidebar.querySelector("[data-sidebar-close]");
      if (first) {
        first.focus();
      }
    } else if (drawerReturn && drawerReturn.focus) {
      drawerReturn.focus();
      drawerReturn = null;
    }
  }

  function toggleSidebar() {
    if (!sidebar) {
      return;
    }
    if (isNarrow()) {
      setDrawer(root.getAttribute("data-drawer") !== "open");
      return;
    }
    var collapsed = root.getAttribute("data-sidebar") !== "collapsed";
    if (collapsed) {
      root.setAttribute("data-sidebar", "collapsed");
    } else {
      root.removeAttribute("data-sidebar");
    }
    storageSet(SIDEBAR_KEY, collapsed ? "collapsed" : null);
    syncSidebar();
  }

  if (sidebar) {
    syncSidebar();
    if (narrowQuery) {
      var onNarrow = function () {
        root.removeAttribute("data-drawer");
        syncSidebar();
      };
      if (narrowQuery.addEventListener) {
        narrowQuery.addEventListener("change", onNarrow);
      } else if (narrowQuery.addListener) {
        narrowQuery.addListener(onNarrow);
      }
    }
  }

  document.addEventListener("click", function (event) {
    if (event.target.closest("[data-sidebar-toggle]")) {
      toggleSidebar();
      return;
    }
    if (event.target.closest("[data-sidebar-close]") && root.getAttribute("data-drawer") === "open") {
      setDrawer(false);
      return;
    }
    if (event.target.closest("[data-scrim]") && root.getAttribute("data-drawer") === "open") {
      setDrawer(false);
    }
  });

  document.addEventListener("keydown", function (event) {
    if (!sidebar) {
      return;
    }
    if (event.key === "\\" && (event.ctrlKey || event.metaKey) && !event.altKey) {
      event.preventDefault();
      toggleSidebar();
      return;
    }
    if (root.getAttribute("data-drawer") !== "open") {
      return;
    }
    if (event.key === "Escape") {
      setDrawer(false);
      return;
    }
    /* Keep Tab inside the open drawer. */
    if (event.key === "Tab") {
      var items = Array.prototype.filter.call(
        sidebar.querySelectorAll("a[href], button:not([disabled]), input, select, [tabindex]:not([tabindex='-1'])"),
        function (node) { return node.offsetParent !== null; }
      );
      if (!items.length) {
        return;
      }
      var first = items[0];
      var last = items[items.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      } else if (!sidebar.contains(document.activeElement)) {
        event.preventDefault();
        first.focus();
      }
    }
  });

  /* Reload buttons, such as Try again on the offline page. */
  document.addEventListener("click", function (event) {
    if (event.target.closest("[data-reload]")) {
      window.location.reload();
    }
  });

  /* ---------- Note tree ---------- */

  function treeKey() {
    var space = body.getAttribute("data-space-id");
    return space ? TREE_KEY + space : null;
  }

  function readTreeState() {
    var key = treeKey();
    if (!key) {
      return {};
    }
    try {
      return JSON.parse(storageGet(key) || "{}") || {};
    } catch (err) {
      return {};
    }
  }

  function writeTreeState(state) {
    var key = treeKey();
    if (key) {
      storageSet(key, JSON.stringify(state));
    }
  }

  function setItemOpen(item, open) {
    var children = item.querySelector(":scope > .tree-children");
    var button = item.querySelector(":scope > .tree-row [data-tree-toggle]");
    item.classList.toggle("open", open);
    if (children) {
      children.hidden = !open;
    }
    if (button) {
      button.setAttribute("aria-expanded", open ? "true" : "false");
    }
  }

  function restoreTree(scope) {
    var state = readTreeState();
    var items = (scope || document).querySelectorAll("[data-tree-item]");
    Array.prototype.forEach.call(items, function (item) {
      var id = item.getAttribute("data-tree-item");
      if (item.classList.contains("open")) {
        return; /* the server opened the path to the current note */
      }
      if (state[id] === true) {
        setItemOpen(item, true);
      }
    });
  }

  document.addEventListener("click", function (event) {
    var button = event.target.closest("[data-tree-toggle]");
    if (!button) {
      return;
    }
    event.preventDefault();
    var item = button.closest("[data-tree-item]");
    if (!item) {
      return;
    }
    var open = !item.classList.contains("open");
    setItemOpen(item, open);
    var state = readTreeState();
    if (open) {
      state[item.getAttribute("data-tree-item")] = true;
    } else {
      delete state[item.getAttribute("data-tree-item")];
    }
    writeTreeState(state);
  });

  restoreTree(document);

  /* refreshTree fetches the current page and swaps in its tree fragment. The
   * fragment is this application's own template output, parsed inertly. */
  var refreshTree = debounce(function () {
    var current = document.querySelector("[data-note-tree]");
    if (!current) {
      return;
    }
    fetch(window.location.pathname, { credentials: "same-origin", headers: { Accept: "text/html" } })
      .then(function (res) {
        return res.ok ? res.text() : Promise.reject(new Error(res.statusText));
      })
      .then(function (html) {
        var doc = new DOMParser().parseFromString(html, "text/html");
        var fresh = doc.querySelector("[data-note-tree]");
        if (!fresh) {
          return;
        }
        var imported = document.importNode(fresh, true);
        current.parentNode.replaceChild(imported, current);
        restoreTree(imported);
      })
      .catch(function () {
        /* keep the stale tree */
      });
  }, 300);

  /* ---------- Quick open ---------- */

  var quick = document.querySelector("[data-quickopen]");
  if (quick) {
    var quickInput = quick.querySelector("[data-quickopen-input]");
    var quickList = quick.querySelector("[data-quickopen-results]");
    var quickItems = [];
    var quickIndex = -1;
    var quickSeq = 0;
    var lastFocus = null;

    var renderQuick = function (notes) {
      quickList.textContent = "";
      quickItems = notes;
      quickIndex = notes.length ? 0 : -1;
      notes.forEach(function (note, i) {
        var li = el("li");
        li.setAttribute("role", "option");
        li.setAttribute("aria-selected", i === quickIndex ? "true" : "false");
        li.appendChild(el("span", "", (note.icon ? note.icon + " " : "") + (note.title || "Untitled")));
        if (note.spaceSlug || note.path) {
          li.appendChild(el("span", "muted", (note.spaceSlug ? note.spaceSlug + "/" : "") + (note.path || "")));
        }
        li.addEventListener("mousedown", function (event) {
          event.preventDefault();
          window.location.href = noteHref(note);
        });
        quickList.appendChild(li);
      });
      if (!notes.length && quickInput.value.trim()) {
        quickList.appendChild(el("li", "muted", "No matching notes"));
      }
      announce(notes.length === 1 ? "1 result" : notes.length + " results");
    };

    var highlightQuick = function () {
      Array.prototype.forEach.call(quickList.children, function (li, i) {
        li.setAttribute("aria-selected", i === quickIndex ? "true" : "false");
        if (i === quickIndex && li.scrollIntoView) {
          li.scrollIntoView({ block: "nearest" });
        }
      });
    };

    var searchQuick = debounce(function () {
      var q = quickInput.value.trim();
      var seq = ++quickSeq;
      if (!q) {
        renderQuick([]);
        return;
      }
      api("GET", "/api/suggest?limit=12&q=" + encodeURIComponent(q))
        .then(function (data) {
          if (seq === quickSeq) {
            renderQuick(listOf(data, "notes"));
          }
        })
        .catch(function () {
          if (seq === quickSeq) {
            renderQuick([]);
          }
        });
    }, 150);

    var openQuick = function () {
      lastFocus = document.activeElement;
      quick.hidden = false;
      quickInput.value = "";
      renderQuick([]);
      quickInput.focus();
    };

    var closeQuick = function () {
      quick.hidden = true;
      if (lastFocus && lastFocus.focus) {
        lastFocus.focus();
      }
    };

    quickInput.addEventListener("input", searchQuick);
    quickInput.addEventListener("keydown", function (event) {
      if (event.key === "ArrowDown") {
        event.preventDefault();
        if (quickItems.length) {
          quickIndex = (quickIndex + 1) % quickItems.length;
          highlightQuick();
        }
      } else if (event.key === "ArrowUp") {
        event.preventDefault();
        if (quickItems.length) {
          quickIndex = (quickIndex - 1 + quickItems.length) % quickItems.length;
          highlightQuick();
        }
      } else if (event.key === "Enter") {
        event.preventDefault();
        if (quickIndex >= 0 && quickItems[quickIndex]) {
          window.location.href = noteHref(quickItems[quickIndex]);
        } else if (quickInput.value.trim()) {
          window.location.href = "/search?q=" + encodeURIComponent(quickInput.value.trim());
        }
      } else if (event.key === "Escape") {
        event.preventDefault();
        closeQuick();
      }
    });

    quick.addEventListener("mousedown", function (event) {
      if (event.target === quick) {
        closeQuick();
      }
    });

    document.addEventListener("keydown", function (event) {
      var isK = event.key === "k" || event.key === "K";
      if (!isK || !(event.ctrlKey || event.metaKey) || event.altKey) {
        return;
      }
      /* inside the editor Ctrl+K inserts a link instead */
      if (event.target.closest && event.target.closest("[data-editor-text]")) {
        return;
      }
      event.preventDefault();
      if (quick.hidden) {
        openQuick();
      } else {
        closeQuick();
      }
    });

    document.addEventListener("click", function (event) {
      if (event.target.closest("[data-quick-open]")) {
        event.preventDefault();
        openQuick();
      }
    });
  }

  /* ---------- Delegated API actions ---------- */

  function removeRow(button) {
    var row = button.closest("[data-row], [data-comment-id], li, tr");
    if (row && row.parentNode) {
      row.parentNode.removeChild(row);
    }
  }

  function fail(err) {
    status(err && err.message ? err.message : "The request failed.", { kind: "error" });
  }

  var actions = [
    {
      attr: "data-trash",
      run: function (id) {
        return api("POST", "/api/notes/" + encodeURIComponent(id) + "/trash").then(function () {
          var title = document.querySelector(".note-title");
          carryUndo({
            label: "Moved “" + (title ? title.textContent.trim() : "the note") + "” to the trash",
            request: { method: "POST", url: "/api/notes/" + encodeURIComponent(id) + "/restore" },
            go: window.location.pathname
          });
          var slug = body.getAttribute("data-space-slug");
          window.location.href = slug ? "/s/" + encodeURIComponent(slug) : "/";
        });
      }
    },
    {
      attr: "data-restore",
      run: function (id, button) {
        return api("POST", "/api/notes/" + encodeURIComponent(id) + "/restore").then(function () {
          removeRow(button);
          refreshTree();
          announce("Restored");
        });
      }
    },
    {
      attr: "data-delete-note",
      run: function (id, button) {
        return api("DELETE", "/api/notes/" + encodeURIComponent(id)).then(function () {
          removeRow(button);
          announce("Deleted forever");
        });
      }
    },
    {
      attr: "data-star",
      run: function (id, button) {
        var starred = button.getAttribute("data-starred") === "true";
        return api(starred ? "DELETE" : "PUT", "/api/notes/" + encodeURIComponent(id) + "/star").then(function () {
          starred = !starred;
          button.setAttribute("data-starred", starred ? "true" : "false");
          button.setAttribute("aria-pressed", starred ? "true" : "false");
          button.textContent = starred ? "★ Starred" : "☆ Star";
          var hidden = button.form && button.form.querySelector("input[name=action]");
          if (hidden) {
            hidden.value = starred ? "unstar" : "star";
          }
        });
      }
    },
    {
      attr: "data-comment-resolve",
      run: function (id, button) {
        var resolved = button.getAttribute("data-resolved") === "true";
        return api("POST", "/api/comments/" + encodeURIComponent(id) + "/resolve", { resolved: !resolved })
          .then(function () {
            window.location.reload();
          });
      }
    },
    {
      attr: "data-comment-delete",
      run: function (id, button) {
        return api("DELETE", "/api/comments/" + encodeURIComponent(id)).then(function () {
          removeRow(button);
          announce("Comment deleted");
        });
      }
    },
    {
      attr: "data-remove-member",
      run: function (id, button) {
        var space = button.getAttribute("data-space");
        var role = button.getAttribute("data-role") || "editor";
        var name = button.getAttribute("data-name") || "the member";
        var row = button.closest("tr, [data-row], li");
        var members = "/api/spaces/" + encodeURIComponent(space) + "/members";
        return ui().then(function (mod) {
          mod.undoable({
            label: "Removed " + name,
            where: row,
            run: function () {
              return api("DELETE", members + "/" + encodeURIComponent(id));
            },
            undo: function () {
              return api("POST", members, { userId: id, role: role });
            }
          });
        });
      }
    },
    {
      attr: "data-revoke-token",
      run: function (id, button) {
        return api("DELETE", "/api/tokens/" + encodeURIComponent(id)).then(function () {
          removeRow(button);
          announce("Token revoked");
        });
      }
    },
    {
      attr: "data-delete-provider",
      run: function (id) {
        return api("DELETE", "/api/ai/providers/" + encodeURIComponent(id)).then(function () {
          window.location.reload();
        });
      }
    },
    {
      attr: "data-provider-test",
      run: function (id) {
        var out = document.querySelector('[data-provider-test-result="' + CSS.escape(id) + '"]');
        if (out) {
          out.textContent = "Testing";
          out.className = "small muted";
        }
        return api("POST", "/api/ai/providers/" + encodeURIComponent(id) + "/test")
          .then(function () {
            if (out) {
              out.textContent = "The provider responded.";
              out.className = "small inline-message inline-message-success";
            }
          })
          .catch(function (err) {
            if (out) {
              out.textContent = "Test failed: " + (err.message || "no response");
              out.className = "small inline-message inline-message-critical";
            }
          });
      }
    }
  ];

  function actionFor(button) {
    for (var i = 0; i < actions.length; i++) {
      if (button.hasAttribute(actions[i].attr)) {
        return actions[i];
      }
    }
    return null;
  }

  /* runAction runs a delegated action with the button busy and reports a
   * failure in a critical Banner. It resolves when the request finishes. */
  function runAction(action, button) {
    closeMenuOf(button);
    setBusy(button, true);
    return action.run(button.getAttribute(action.attr), button)
      .then(function () {
        if (document.body.contains(button)) {
          setBusy(button, false);
        }
      }, function (err) {
        if (document.body.contains(button)) {
          setBusy(button, false);
        }
        throw err;
      });
  }

  document.addEventListener("click", function (event) {
    var button = event.target.closest && event.target.closest(actions.map(function (a) { return "[" + a.attr + "]"; }).join(","));
    if (!button) {
      return;
    }
    event.preventDefault();
    if (button.getAttribute("aria-disabled") === "true") {
      return;
    }
    runAction(actionFor(button), button).catch(fail);
  });

  /* ---------- ConfirmationDialog for data-confirm ---------- */

  /* A button with data-confirm asks first in a Primer ConfirmationDialog
   * (docs/primer.md 5.6 and 6.2). data-confirm holds the question, and a second
   * sentence becomes the body with data-confirm-detail. The confirm button
   * repeats the button's label and is danger for danger buttons. The dialog
   * stays open with a busy confirm button until the action finishes. */
  function confirmParts(button) {
    var text = (button.getAttribute("data-confirm") || "").trim();
    var label = (button.textContent || "").trim() || (button.getAttribute("data-confirm-label") || "").replace(/^Confirm\s+/i, "") || "Continue";
    var title = text;
    var rest = "";
    var match = text.match(/^(.+?[?.!])\s+(.+)$/);
    if (match) {
      title = match[1];
      rest = match[2];
    }
    if (!title) {
      title = label + "?";
    }
    var detail = button.getAttribute("data-confirm-detail") || "";
    return {
      title: title,
      body: [rest, detail].filter(Boolean).join(" "),
      confirmLabel: label,
      danger: button.classList.contains("danger") || button.classList.contains("btn-danger") || button.hasAttribute("data-confirm-danger")
    };
  }

  function closeMenuOf(button) {
    var pop = button.closest && button.closest("[popover]");
    if (pop && pop.hidePopover && pop.matches(":popover-open")) {
      pop.hidePopover();
    }
  }

  document.addEventListener("click", function (event) {
    var button = event.target.closest && event.target.closest("[data-confirm]");
    if (!button || button.getAttribute("aria-disabled") === "true") {
      return;
    }
    if (button._confirmed) {
      button._confirmed = false;
      return;
    }
    event.preventDefault();
    event.stopImmediatePropagation();
    var parts = confirmParts(button);
    var trigger = button.closest("[popover]") ? document.querySelector('[popovertarget="' + button.closest("[popover]").id + '"]') : button;
    closeMenuOf(button);
    ui().then(function (mod) {
      mod.confirmDialog({
        title: parts.title,
        body: parts.body,
        confirmLabel: parts.confirmLabel,
        danger: parts.danger,
        returnFocus: trigger || button,
        onConfirm: function () {
          var action = actionFor(button);
          if (action) {
            return runAction(action, button);
          }
          if (button.form) {
            button._confirmed = true;
            if (button.form.requestSubmit) {
              button.form.requestSubmit(button);
            } else {
              button.click();
            }
            /* The page navigates; the dialog keeps its busy button until then. */
            return new Promise(function (resolve) {
              window.addEventListener("pagehide", resolve);
              setTimeout(resolve, 15000);
            });
          }
          button._confirmed = true;
          button.click();
          return undefined;
        }
      });
    }).catch(function () {
      /* without the component kit the action goes ahead as a plain click */
      button._confirmed = true;
      button.click();
    });
  }, true);

  /* ---------- ActionMenu on server pages ---------- */

  /* Row menus are ActionMenu markup with the popover attribute: a trigger
   * with popovertarget and a [data-action-menu] or .row-menu container of
   * .menu-item buttons and links. This adds Primer's roles, positioning next
   * to the trigger, and keyboard handling (docs/primer.md 5.5). */
  var MENU_SELECTOR = "[data-action-menu][popover], .row-menu[popover]";

  function menuItems(menu) {
    return Array.prototype.filter.call(menu.querySelectorAll(".menu-item, [role=menuitem], [role=menuitemradio]"), function (item) {
      return item.getAttribute("aria-disabled") !== "true" && !item.hidden && !item.closest("[hidden]");
    });
  }

  function triggerOf(menu) {
    return menu.id ? document.querySelector('[popovertarget="' + menu.id + '"]') : null;
  }

  function placeMenu(menu, trigger) {
    if (!trigger || (window.matchMedia && window.matchMedia("(max-width: calc(48rem - 0.02px))").matches)) {
      menu.style.top = "";
      menu.style.left = "";
      menu.style.maxHeight = "";
      return;
    }
    var rect = trigger.getBoundingClientRect();
    var vw = window.innerWidth;
    var vh = window.innerHeight;
    /* A menu taller than the viewport less 8px at each edge scrolls inside. */
    menu.style.maxHeight = "";
    if (menu.offsetHeight > vh - 16) {
      menu.style.maxHeight = Math.max(0, vh - 16) + "px";
    }
    var width = menu.offsetWidth;
    var height = menu.offsetHeight;
    var top = rect.bottom + 4;
    if (top + height > vh - 8 && rect.top - 4 - height >= 8) {
      top = rect.top - 4 - height;
    }
    /* Kebab menus at the end of a row align to the trigger's end. */
    var left = rect.right - width;
    if (left < 8) {
      left = rect.left;
    }
    left = Math.max(8, Math.min(left, vw - width - 8));
    top = Math.max(8, Math.min(top, vh - height - 8));
    menu.style.top = Math.round(top) + "px";
    menu.style.left = Math.round(left) + "px";
  }

  function enhanceMenu(menu) {
    if (menu._enhanced) {
      return;
    }
    menu._enhanced = true;
    menu.setAttribute("role", "menu");
    var trigger = triggerOf(menu);
    if (trigger) {
      trigger.setAttribute("aria-haspopup", "true");
      trigger.setAttribute("aria-expanded", "false");
      if (!trigger.id) {
        trigger.id = menu.id + "-trigger";
      }
      menu.setAttribute("aria-labelledby", trigger.id);
    }
    Array.prototype.forEach.call(menu.querySelectorAll(".menu-item"), function (item) {
      if (!item.hasAttribute("role")) {
        item.setAttribute("role", "menuitem");
      }
      item.setAttribute("tabindex", "-1");
    });
    Array.prototype.forEach.call(menu.querySelectorAll(".menu-sep"), function (sep) {
      sep.setAttribute("role", "separator");
    });
    Array.prototype.forEach.call(menu.querySelectorAll("form"), function (form) {
      form.setAttribute("role", "none");
    });
    Array.prototype.forEach.call(menu.querySelectorAll(".menu-label"), function (heading) {
      heading.setAttribute("role", "presentation");
    });
    /* On narrow viewports the menu fills the screen with a Close button. */
    var close = el("button", "btn btn-icon btn-invisible row-menu-close");
    close.type = "button";
    close.setAttribute("aria-label", "Close menu");
    close.innerHTML = '<span class="btn-visual" aria-hidden="true"><svg class="octicon" viewBox="0 0 16 16" width="16" height="16" fill="currentColor" aria-hidden="true" focusable="false"><path d="M3.72 3.72a.75.75 0 0 1 1.06 0L8 6.94l3.22-3.22a.749.749 0 0 1 1.275.326.749.749 0 0 1-.215.734L9.06 8l3.22 3.22a.749.749 0 0 1-.326 1.275.749.749 0 0 1-.734-.215L8 9.06l-3.22 3.22a.751.751 0 0 1-1.042-.018.751.751 0 0 1-.018-1.042L6.94 8 3.72 4.78a.75.75 0 0 1 0-1.06Z"></path></svg></span>';
    close.addEventListener("click", function () {
      menu.hidePopover();
    });
    menu.insertBefore(close, menu.firstChild);

    var typed = "";
    var typedTimer = 0;
    menu.addEventListener("toggle", function (event) {
      var open = event.newState === "open";
      if (trigger) {
        trigger.setAttribute("aria-expanded", open ? "true" : "false");
      }
      if (open) {
        /* A Tooltip never stays over an open menu. */
        if (uiModule) {
          uiModule.then(function (mod) {
            if (typeof mod.hideTooltips === "function") {
              mod.hideTooltips();
            }
          }, function () {});
        }
        placeMenu(menu, trigger);
        var items = menuItems(menu);
        var start = menu._focusLast ? items[items.length - 1] : items[0];
        menu._focusLast = false;
        if (start) {
          start.focus();
        }
      } else if (trigger && (menu.contains(document.activeElement) || document.activeElement === body)) {
        trigger.focus();
      }
    });
    menu.addEventListener("keydown", function (event) {
      var items = menuItems(menu);
      var index = items.indexOf(document.activeElement);
      var move = function (i) {
        if (items.length) {
          items[(i + items.length) % items.length].focus();
        }
      };
      switch (event.key) {
        case "ArrowDown":
          event.preventDefault();
          move(index < 0 ? 0 : index + 1);
          return;
        case "ArrowUp":
          event.preventDefault();
          move(index < 0 ? items.length - 1 : index - 1);
          return;
        case "Home":
        case "PageUp":
          event.preventDefault();
          move(0);
          return;
        case "End":
        case "PageDown":
          event.preventDefault();
          move(items.length - 1);
          return;
        case "Escape":
          event.preventDefault();
          event.stopPropagation();
          menu.hidePopover();
          if (trigger) {
            trigger.focus();
          }
          return;
        case "Tab":
          /* Focus goes back to the trigger, so Tab moves onward from there. */
          if (trigger) {
            trigger.focus();
          }
          menu.hidePopover();
          return;
        default:
          if (event.key && event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey && /\S/.test(event.key)) {
            clearTimeout(typedTimer);
            typed += event.key.toLowerCase();
            typedTimer = setTimeout(function () {
              typed = "";
            }, 500);
            var labels = items.map(function (item) { return item.textContent.trim().toLowerCase(); });
            var from = typed.length > 1 && index >= 0 ? index : index + 1;
            for (var n = 0; n < items.length; n++) {
              var j = (((from + n) % items.length) + items.length) % items.length;
              if (labels[j].indexOf(typed) === 0) {
                event.preventDefault();
                items[j].focus();
                break;
              }
            }
          }
      }
    });
    if (trigger) {
      trigger.addEventListener("keydown", function (event) {
        if ((event.key === "ArrowDown" || event.key === "ArrowUp") && !menu.matches(":popover-open")) {
          event.preventDefault();
          menu._focusLast = event.key === "ArrowUp";
          menu.showPopover();
        }
      });
    }
  }

  function enhanceMenus(scope) {
    Array.prototype.forEach.call((scope || document).querySelectorAll(MENU_SELECTOR), enhanceMenu);
  }

  enhanceMenus(document);
  window.addEventListener("resize", function () {
    Array.prototype.forEach.call(document.querySelectorAll(MENU_SELECTOR), function (menu) {
      if (menu.matches(":popover-open")) {
        placeMenu(menu, triggerOf(menu));
      }
    });
  });

  /* ---------- Tooltips ---------- */

  /* Icon buttons in server markup carry a title. On first hover or keyboard
   * focus the title becomes a Primer Tooltip, so the browser's own tooltip
   * never shows (docs/primer.md 5.6). */
  var TOOLTIP_SELECTOR = ".icon-btn[title], .btn-icon[title], .row-menu-btn[title], [data-tooltip]";

  function attachTooltip(target, show) {
    if (target._tooltipPending || target._tooltip) {
      return;
    }
    var text = target.getAttribute("data-tooltip") || target.getAttribute("title") || "";
    target.removeAttribute("title");
    if (!text) {
      return;
    }
    target._tooltipPending = true;
    ui().then(function (mod) {
      var name = (target.getAttribute("aria-label") || "").trim();
      var type = !name || name === text ? "label" : "description";
      var tip = mod.tooltip(target, text, { type: type });
      if (show) {
        tip.show();
      }
    }).catch(function () {
      target._tooltipPending = false;
    });
  }

  document.addEventListener("pointerover", function (event) {
    if (event.pointerType === "touch") {
      var touched = event.target.closest && event.target.closest(TOOLTIP_SELECTOR);
      if (touched) {
        touched.removeAttribute("title");
      }
      return;
    }
    var target = event.target.closest && event.target.closest(TOOLTIP_SELECTOR);
    if (target) {
      attachTooltip(target, true);
    }
  });

  document.addEventListener("focusin", function (event) {
    var target = event.target.closest && event.target.closest(TOOLTIP_SELECTOR);
    if (target) {
      var visible = true;
      try {
        visible = target.matches(":focus-visible");
      } catch (err) {
        visible = true;
      }
      attachTooltip(target, visible);
    }
  });

  /* Selects that submit their form on change. */
  document.addEventListener("change", function (event) {
    var select = event.target.closest("select[data-autosubmit]");
    if (select && select.form) {
      if (select.form.requestSubmit) {
        select.form.requestSubmit();
      } else {
        select.form.submit();
      }
    }
  });

  /* Copy buttons swap their icon for a check for 2 s and announce "Copied". */
  document.addEventListener("click", function (event) {
    var button = event.target.closest("[data-copy]");
    if (!button) {
      return;
    }
    event.preventDefault();
    var value = button.getAttribute("data-copy");
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(value).then(function () {
        ui().then(function (mod) {
          mod.copyFeedback(button);
        }, function () {
          announce("Copied");
        });
      }, function () {
        status("Copying failed. Select the text and copy it by hand.", { kind: "error" });
      });
    }
  });

  /* Dangling wikilinks point nowhere. */
  document.addEventListener("click", function (event) {
    if (event.target.closest(".md-body a.wikilink-new")) {
      event.preventDefault();
    }
  });

  /* ---------- Version history ---------- */

  /* safeDiff rebuilds renderer diff markup from an inert parse, keeping only
   * a small tag set, diff-* classes and text. No attribute but class survives. */
  function safeDiff(html) {
    var allowed = { DIV: 1, SPAN: 1, PRE: 1, CODE: 1, INS: 1, DEL: 1, MARK: 1, P: 1 };
    var doc = new DOMParser().parseFromString(String(html), "text/html");
    function copy(node) {
      if (node.nodeType === 3) {
        return document.createTextNode(node.nodeValue);
      }
      if (node.nodeType !== 1) {
        return null;
      }
      var out = document.createElement(allowed[node.tagName] ? node.tagName.toLowerCase() : "span");
      var classes = (node.getAttribute("class") || "").split(/\s+/).filter(function (c) {
        return /^diff(-[a-z]+)?$/.test(c);
      });
      if (classes.length) {
        out.className = classes.join(" ");
      }
      Array.prototype.forEach.call(node.childNodes, function (child) {
        var copied = copy(child);
        if (copied) {
          out.appendChild(copied);
        }
      });
      return out;
    }
    var wrap = el("div", "diff");
    Array.prototype.forEach.call(doc.body.childNodes, function (child) {
      var copied = copy(child);
      if (copied) {
        wrap.appendChild(copied);
      }
    });
    return wrap;
  }

  document.addEventListener("click", function (event) {
    var link = event.target.closest("[data-history]");
    if (!link) {
      return;
    }
    var panel = document.querySelector("[data-history-panel]");
    var list = panel && panel.querySelector("[data-history-list]");
    if (!panel || !list) {
      return;
    }
    event.preventDefault();
    var noteId = link.getAttribute("data-history");
    panel.hidden = false;
    panel.scrollIntoView({ block: "start" });
    list.textContent = "";
    list.setAttribute("aria-busy", "true");
    list.appendChild(el("p", "muted", "Loading versions"));
    api("GET", "/api/notes/" + encodeURIComponent(noteId) + "/versions")
      .then(function (data) {
        var versions = listOf(data, "versions");
        list.textContent = "";
        list.removeAttribute("aria-busy");
        if (!versions.length) {
          list.appendChild(el("p", "muted", "No earlier versions"));
          return;
        }
        var ul = el("ul", "row-list");
        versions.forEach(function (v) {
          var li = el("li", "row");
          var when = v.createdAt ? new Date(v.createdAt).toLocaleString() : "";
          li.appendChild(el("span", "grow", (v.label ? v.label + " · " : "") + when));
          var diffBox = el("div");
          diffBox.style.flexBasis = "100%";
          var compare = el("button", "btn btn-sm", "Compare");
          compare.type = "button";
          compare.addEventListener("click", function () {
            setBusy(compare, true);
            api("GET", "/api/notes/" + encodeURIComponent(noteId) + "/versions/" + encodeURIComponent(v.id) + "/diff")
              .then(function (res) {
                var html = typeof res === "string" ? res : (res && (res.html || res.diff)) || "";
                diffBox.textContent = "";
                diffBox.appendChild(safeDiff(html));
              })
              .catch(fail)
              .then(function () {
                setBusy(compare, false);
              });
          });
          var restore = el("button", "btn btn-sm", "Restore");
          restore.type = "button";
          /* Restoring saves the current text as a version first, so the
           * Undo restores that new version. */
          restore.addEventListener("click", function () {
            var base = "/api/notes/" + encodeURIComponent(noteId) + "/versions";
            var before = {};
            versions.forEach(function (x) {
              before[x.id] = true;
            });
            setBusy(restore, true);
            api("POST", base + "/" + encodeURIComponent(v.id) + "/restore")
              .then(function () {
                return api("GET", base);
              })
              .then(function (after) {
                var fresh = listOf(after, "versions").filter(function (x) {
                  return !before[x.id];
                })[0];
                if (fresh) {
                  carryUndo({
                    label: "Version restored",
                    request: { method: "POST", url: base + "/" + encodeURIComponent(fresh.id) + "/restore" }
                  });
                }
                window.location.reload();
              })
              .catch(function (err) {
                setBusy(restore, false);
                fail(err);
              });
          });
          li.appendChild(compare);
          li.appendChild(restore);
          li.appendChild(diffBox);
          ul.appendChild(li);
        });
        list.appendChild(ul);
      })
      .catch(function (err) {
        list.textContent = "";
        list.removeAttribute("aria-busy");
        list.appendChild(el("p", "inline-message inline-message-critical", err.message || "Versions are unavailable."));
      });
  });

  /* ---------- Unsaved changes ---------- */

  /* A form with data-unsaved warns before its changes are lost (docs/primer.md
   * 6.1). Input and change events mark the form changed while its fields
   * differ from their values before the first edit. A submit that no other
   * handler cancels clears the mark. The browser asks on beforeunload, and a
   * link to another page of the app asks first in a ConfirmationDialog.
   *
   * installUnsavedWarning takes its globals as arguments, so
   * test/client/unsaved.test.mjs runs this section on a fake DOM. */
  function installUnsavedWarning(doc, win, loadUI, clickRoot) {
    var SELECTOR = "form[data-unsaved]";

    function formState(form) {
      var parts = [];
      Array.prototype.forEach.call(form.querySelectorAll("input, select, textarea"), function (field) {
        var name = field.getAttribute("name");
        var type = (field.getAttribute("type") || "").toLowerCase();
        if (!name || field.disabled || type === "submit" || type === "button") {
          return;
        }
        var value;
        if (type === "checkbox" || type === "radio") {
          value = field.checked ? "1" : "0";
        } else if (type === "file") {
          value = String(field.files ? field.files.length : 0);
        } else if (field.multiple && field.options) {
          value = Array.prototype.filter.call(field.options, function (o) { return o.selected; }).map(function (o) { return o.value; }).join(" ");
        } else {
          value = field.value;
        }
        parts.push(name + "=" + value);
      });
      return parts.join("\n");
    }

    function remember(form) {
      if (!form._unsaved) {
        form._unsaved = { initial: formState(form), dirty: false };
      }
      return form._unsaved;
    }

    function formOf(target) {
      if (!target || target.nodeType !== 1) {
        return null;
      }
      var form = target.form || target.closest("form");
      return form && form.matches(SELECTOR) ? form : null;
    }

    function update(event) {
      var form = formOf(event.target);
      if (form) {
        var state = remember(form);
        state.dirty = formState(form) !== state.initial;
      }
    }

    function dirtyForms() {
      return Array.prototype.filter.call(doc.querySelectorAll(SELECTOR), function (form) {
        return !!(form._unsaved && form._unsaved.dirty);
      });
    }

    function clearAll() {
      Array.prototype.forEach.call(doc.querySelectorAll(SELECTOR), function (form) {
        if (form._unsaved) {
          form._unsaved.dirty = false;
        }
      });
    }

    function leavesPage(link) {
      if (link.hasAttribute("download") || link.hasAttribute("data-dialog-open") || link.hasAttribute("popovertarget")) {
        return false;
      }
      var target = link.getAttribute("target");
      if (target && target !== "_self") {
        return false;
      }
      var here;
      var url;
      try {
        here = new URL(win.location.href);
        url = new URL(link.getAttribute("href"), here.href);
      } catch (err) {
        return false;
      }
      if (url.origin !== here.origin || !/^https?:$/.test(url.protocol)) {
        return false;
      }
      return url.pathname + url.search !== here.pathname + here.search;
    }

    /* The values at load are the baseline. A form added later takes its
     * baseline on first focus. */
    Array.prototype.forEach.call(doc.querySelectorAll(SELECTOR), remember);
    doc.addEventListener("focusin", function (event) {
      var form = formOf(event.target);
      if (form) {
        remember(form);
      }
    }, true);
    doc.addEventListener("input", update, true);
    doc.addEventListener("change", update, true);
    /* A form that saves through a script, such as an AI route form, sends
     * markspace:saved, and its current values become the baseline. */
    doc.addEventListener("markspace:saved", function (event) {
      var form = formOf(event.target);
      if (form) {
        form._unsaved = { initial: formState(form), dirty: false };
      }
    });
    /* Failed validation cancels the submit in a capture listener, so the
     * mark stays. */
    doc.addEventListener("submit", function (event) {
      var form = formOf(event.target);
      if (form && form._unsaved && !event.defaultPrevented) {
        form._unsaved.dirty = false;
      }
    });

    win.addEventListener("beforeunload", function (event) {
      if (dirtyForms().length) {
        event.preventDefault();
        event.returnValue = "";
      }
    });

    /* The click listener sits on the window, after every document listener,
     * so a link that opens a Dialog or asks its own confirmation has already
     * cancelled the navigation. */
    clickRoot.addEventListener("click", function (event) {
      if (event.defaultPrevented || event.button > 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
        return;
      }
      var link = event.target && event.target.closest ? event.target.closest("a[href]") : null;
      if (!link || !dirtyForms().length || !leavesPage(link)) {
        return;
      }
      event.preventDefault();
      var href = new URL(link.getAttribute("href"), win.location.href).href;
      loadUI().then(function (mod) {
        mod.confirmDialog({
          title: "Discard unsaved changes?",
          body: "Leaving this page discards the changes you made.",
          confirmLabel: "Discard changes",
          cancelLabel: "Keep editing",
          returnFocus: link,
          onConfirm: function () {
            clearAll();
            win.location.assign(href);
          }
        });
      }).catch(function () {
        /* Without the component kit the browser's beforeunload prompt asks. */
        win.location.assign(href);
      });
    });

    return { dirtyForms: dirtyForms };
  }

  installUnsavedWarning(document, window, ui, window);

  /* ---------- Live events ---------- */

  function startEvents() {
    if (!window.EventSource || !body.classList.contains("app")) {
      return;
    }
    var delay = 1000;
    var maxDelay = 30000;
    var source = null;
    var me = body.getAttribute("data-user-id") || "";

    var connect = function () {
      source = new EventSource("/api/events", { withCredentials: true });
      source.onopen = function () {
        delay = 1000;
      };
      source.onmessage = function (message) {
        handleEvent(message.data);
      };
      ["tree.changed", "note.updated", "note.created", "note.deleted", "comment.created"].forEach(function (type) {
        source.addEventListener(type, function (message) {
          handleEvent(message.data, type);
        });
      });
      source.onerror = function () {
        source.close();
        setTimeout(connect, delay);
        delay = Math.min(delay * 2, maxDelay);
      };
    };

    var handleEvent = function (raw, fallbackType) {
      var ev;
      try {
        ev = JSON.parse(raw);
      } catch (err) {
        return;
      }
      var type = ev.type || fallbackType;
      var space = body.getAttribute("data-space-id");
      var note = body.getAttribute("data-note-id");
      if (type === "tree.changed" && space && ev.spaceId === space) {
        refreshTree();
      }
      if ((type === "note.created" || type === "note.deleted") && space && ev.spaceId === space) {
        refreshTree();
      }
      if (type === "note.updated" && note && ev.noteId === note) {
        document.dispatchEvent(new CustomEvent("markspace:note-updated", { detail: ev }));
        if (ev.actorId && ev.actorId === me) {
          return;
        }
        var banner = document.querySelector("[data-note-changed]");
        if (banner) {
          banner.hidden = false;
        }
      }
    };

    connect();
    window.addEventListener("beforeunload", function () {
      if (source) {
        source.close();
      }
    });
  }

  startEvents();
})();
