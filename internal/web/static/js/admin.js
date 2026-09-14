// Server-page behaviour shared by the administration pages (docs/primer.md 5.6,
// 6.1 and 6.6). Every action stays a form post, so the pages work without
// this script.
//
// - A Dialog source is a hidden element with class dialog-source that holds
//   one form. A button or link with data-dialog-open="<source id>" opens it
//   in a Primer Dialog. The elements of its .dialog-source-footer move into
//   the Dialog footer. A source with data-dialog-autoopen opens on load, which
//   is how a refused form comes back. Its [data-dialog-error] text shows as a
//   critical Banner under the Dialog header. data-dialog-role="alertdialog"
//   makes a ConfirmationDialog, 320px wide and centred on narrow viewports.
//   data-dialog-focus holds a selector for the element that takes focus.
// - A form with data-validate checks its fields on submit and shows Primer
//   validation messages instead of the browser's.
// - [data-saved-message] text is announced once on load.
// - The unsaved changes warning for data-unsaved forms lives in app.js.

import { announce, openDialog, showFieldErrors } from "./ui.js";

/** fieldLabel returns the visible label text of a control, without markers. */
function fieldLabel(control) {
  const label = control.id ? control.form?.querySelector(`label[for="${CSS.escape(control.id)}"]`) || document.querySelector(`label[for="${CSS.escape(control.id)}"]`) : control.closest("label");
  const text = (label ? label.textContent : control.name || "").replace(/\*/g, "").replace(/\s+/g, " ").trim();
  return text;
}

/** fieldMessage returns the Primer validation message of an invalid control. */
export function fieldMessage(control) {
  const v = control.validity;
  if (!v || v.valid) {
    return "";
  }
  if (control.dataset.errorMessage) {
    return control.dataset.errorMessage;
  }
  const name = fieldLabel(control).toLowerCase();
  if (v.valueMissing) {
    return control.type === "checkbox" || control.type === "radio" || control.tagName === "SELECT" ? `Choose ${name}` : `Enter ${name}`;
  }
  if (v.typeMismatch && control.type === "email") {
    return "Enter an email address, such as ada@example.com";
  }
  if (v.typeMismatch && control.type === "url") {
    return "Enter an address that starts with http:// or https://";
  }
  if (v.tooShort) {
    return `Use at least ${control.minLength} characters`;
  }
  if (v.rangeUnderflow) {
    return `Enter ${control.min} or more`;
  }
  if (v.rangeOverflow) {
    return `Enter ${control.max} or less`;
  }
  if (v.badInput || v.stepMismatch) {
    return control.type === "number" ? "Enter a number" : `Check ${name}`;
  }
  return `Check ${name}`;
}

/**
 * validateForm shows the validation messages of a form's invalid fields and
 * reports whether the form may be submitted. One or two errors focus the
 * first invalid field, and three or more show the error summary Banner.
 */
export function validateForm(form) {
  const errors = {};
  let count = 0;
  for (const control of form.elements) {
    if (!control.name || control.disabled || control.type === "hidden" || !control.willValidate || control.closest("[hidden]:not(.dialog-source)")) {
      continue;
    }
    const message = fieldMessage(control);
    if (message && !(control.name in errors)) {
      errors[control.name] = message;
      count++;
    }
  }
  form.querySelectorAll('[aria-invalid="true"]').forEach((control) => {
    if (!(control.name in errors)) {
      control.removeAttribute("aria-invalid");
      const wrap = control.closest(".field, .form-control");
      const error = wrap && wrap.querySelector(".field-error, .form-validation");
      if (error) {
        error.hidden = true;
      }
      if (wrap) {
        wrap.classList.remove("has-error");
      }
    }
  });
  if (count === 0) {
    return true;
  }
  showFieldErrors(form, errors);
  return false;
}

document.addEventListener(
  "submit",
  (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || !form.hasAttribute("data-validate")) {
      return;
    }
    const submitter = event.submitter;
    if (submitter && submitter.hasAttribute("formnovalidate")) {
      return;
    }
    if (!validateForm(form)) {
      event.preventDefault();
      event.stopImmediatePropagation();
    }
  },
  true
);

/** triggerOf returns the button that opened the popover holding el. */
function popoverTrigger(el) {
  const pop = el.closest("[popover]");
  return pop && pop.id ? document.querySelector(`[popovertarget="${CSS.escape(pop.id)}"]`) : null;
}

/**
 * openSourceDialog opens a Dialog source in a Primer Dialog. The source's
 * form moves into the Dialog body and returns to the source on close.
 * @param {HTMLElement} source
 * @param {{returnFocus?: Element, onClose?: Function}} [options]
 */
export function openSourceDialog(source, { returnFocus = null, onClose } = {}) {
  if (!source || source._dialog) {
    return source && source._dialog;
  }
  const nodes = Array.from(source.childNodes);
  const footerSource = source.querySelector(".dialog-source-footer");
  const footerNodes = footerSource ? Array.from(footerSource.childNodes) : [];
  const form = source.querySelector("form");
  const content = document.createElement("div");
  content.className = "dialog-source-body";
  content.append(...nodes);
  // The emptied source stays out of the page flow while its Dialog is open.
  source.hidden = true;
  const alert = source.dataset.dialogRole === "alertdialog";
  const handle = openDialog({
    title: source.dataset.dialogTitle || "",
    subtitle: source.dataset.dialogSubtitle || "",
    width: source.dataset.dialogWidth || (alert ? "medium" : "large"),
    role: alert ? "alertdialog" : "dialog",
    narrow: alert ? "center" : "auto",
    content,
    returnFocus,
    onClose: (reason) => {
      // Moves everything back, so the page keeps working after the Dialog.
      if (footerSource) {
        footerSource.append(...footerNodes);
      }
      source.append(...nodes);
      source.hidden = true;
      source._dialog = null;
      if (typeof onClose === "function") {
        onClose(reason);
      }
    },
  });
  source._dialog = handle;
  if (footerNodes.length) {
    const bar = document.createElement("div");
    bar.className = "dialog-footer";
    for (const node of footerNodes) {
      if (node.nodeType === 1 && form && node.matches("button[type=submit], button:not([type])") && !node.hasAttribute("form")) {
        if (!form.id) {
          form.id = `${source.id}-form`;
        }
        node.setAttribute("form", form.id);
      }
      bar.appendChild(node);
    }
    handle.element.appendChild(bar);
    handle.footer = bar;
  }
  const error = source.querySelector("[data-dialog-error]") || content.querySelector("[data-dialog-error]");
  if (error && error.textContent.trim()) {
    handle.showError(error.textContent.trim());
  }
  // The footer is in place now, so a focus selector may name a footer
  // button, such as Cancel in a confirmation.
  const invalid = content.querySelector('[aria-invalid="true"]');
  const chosen = source.dataset.dialogFocus ? handle.element.querySelector(source.dataset.dialogFocus) : null;
  if (invalid && source.hasAttribute("data-dialog-autoopen")) {
    invalid.focus();
  } else if (chosen) {
    chosen.focus();
  }
  return handle;
}

document.addEventListener("click", (event) => {
  const cancel = event.target.closest && event.target.closest("[data-dialog-cancel]");
  if (cancel) {
    const dialog = cancel.closest("dialog");
    const source = dialog && Array.from(document.querySelectorAll(".dialog-source")).find((s) => s._dialog && s._dialog.element === dialog);
    if (source) {
      event.preventDefault();
      source._dialog.close("cancel");
    }
    return;
  }
  const opener = event.target.closest && event.target.closest("[data-dialog-open]");
  if (!opener) {
    return;
  }
  const source = document.getElementById(opener.dataset.dialogOpen);
  if (!source) {
    return;
  }
  event.preventDefault();
  const returnFocus = popoverTrigger(opener) || opener;
  const pop = opener.closest("[popover]");
  if (pop && pop.matches(":popover-open")) {
    pop.hidePopover();
  }
  returnFocus.focus();
  openSourceDialog(source, { returnFocus });
});

/* A refused form comes back open in its Dialog. A refused page form focuses
   its first invalid field (docs/primer.md 6.6). */
const reopen = document.querySelector(".dialog-source[data-dialog-autoopen]");
if (reopen) {
  openSourceDialog(reopen);
} else {
  const invalid = document.querySelector('form [aria-invalid="true"]');
  if (invalid && !invalid.closest("[hidden]")) {
    invalid.focus();
    invalid.scrollIntoView({ block: "center" });
  }
}

/* Saved results are visible beside their Save button and announced. */
const saved = document.querySelector("[data-saved-message]");
if (saved && saved.textContent.trim()) {
  announce(saved.textContent.trim());
}
