// Generic file viewer: a Blankslate with the file type Octicon, the size,
// type and dates, and a primary Download button.

import { fileIconName } from "../ui.js";
import { noPreviewState } from "./common.js";

/**
 * Mounts the generic viewer.
 * @param {HTMLElement} container
 * @param {{file: object, url: string, downloadUrl: string, isNarrow: () => boolean, signal: AbortSignal}} ctx
 * @returns {Promise<{unmount(): void}>}
 */
export async function mountViewer(container, ctx) {
  const file = ctx.file || {};
  const root = noPreviewState(ctx, {
    icon: fileIconName(file.mime, file.name),
    title: "No preview for this file",
    description: "Download it to open it in another app.",
  });
  root.classList.add("fv-other");
  container.appendChild(root);
  return {
    unmount() {
      root.remove();
    },
  };
}
