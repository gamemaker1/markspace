// Admin plugins page. app.js enhances the row menus and copy buttons, and
// /static/js/admin.js opens the Dialog sources and validates their forms.
// This script opens Uninstall as a ConfirmationDialog and dismisses Banners.
// Every action stays a form post, so the page works without this script.

import { openSourceDialog } from "/static/js/admin.js";

const page = document.querySelector("[data-plugins-page]");

/**
 * asConfirmation turns an open Dialog source into a Primer
 * ConfirmationDialog: role alertdialog, centred on narrow viewports, and
 * focus on Cancel because the confirm button is danger.
 */
function asConfirmation(handle) {
  if (!handle) {
    return;
  }
  handle.element.setAttribute("role", "alertdialog");
  handle.element.setAttribute("data-narrow", "center");
  const body = handle.element.querySelector(".plugin-confirm-body");
  if (body) {
    body.id = body.id || `${handle.element.getAttribute("aria-labelledby")}-body`;
    handle.element.setAttribute("aria-describedby", body.id);
  }
  const cancel = handle.footer && handle.footer.querySelector("[data-dialog-cancel]");
  if (cancel) {
    cancel.focus();
  }
}

page?.addEventListener("click", (event) => {
  const uninstall = event.target.closest("[data-plugin-uninstall]");
  if (uninstall) {
    const source = document.getElementById(uninstall.dataset.pluginUninstall);
    if (!source) {
      return;
    }
    event.preventDefault();
    const pop = uninstall.closest("[popover]");
    const trigger = pop && pop.id ? document.querySelector(`[popovertarget="${CSS.escape(pop.id)}"]`) : uninstall;
    if (pop && pop.matches(":popover-open")) {
      pop.hidePopover();
    }
    if (trigger) {
      trigger.focus();
    }
    asConfirmation(openSourceDialog(source, { returnFocus: trigger }));
    return;
  }
  const dismiss = event.target.closest("[data-banner-dismiss]");
  if (dismiss) {
    const banner = dismiss.closest("[data-plugin-banner]");
    const next = banner.nextElementSibling;
    banner.remove();
    const target = next && next.matches("section") ? next.querySelector("h2, [tabindex]") : null;
    (target || page.querySelector("h1")).focus();
  }
});

/* An uninstall that comes back refused, or the no-script confirm step, opens
   on load through admin.js. It still gets the confirmation behaviour. */
for (const source of document.querySelectorAll(".dialog-source[data-confirmation]")) {
  if (source._dialog) {
    asConfirmation(source._dialog);
  }
}
