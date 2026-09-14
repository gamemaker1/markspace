// Video and audio viewer: native elements that stream with Range requests,
// under a toolbar with the playback speed menu and a loop toggle.

import { h, icon, button, openMenu } from "../ui.js";
import { toolButton, toolbar, errorState } from "./common.js";
import { viewerKind } from "./kind.js";

/** Playback rates offered in the speed menu. */
export const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 2];

/**
 * Formats a playback rate, such as "1.5×".
 * @param {number} rate
 * @returns {string}
 */
export function speedLabel(rate) {
  return `${Number(rate)}×`;
}

/**
 * Mounts a native <video> or <audio> element. The kind follows viewerKind.
 * @param {HTMLElement} container
 * @param {{file: object, url: string, downloadUrl: string, isNarrow: () => boolean, signal: AbortSignal}} ctx
 * @returns {Promise<{unmount(): void}>}
 */
export async function mountViewer(container, ctx) {
  const file = ctx.file || {};
  const audio = viewerKind(file) === "audio";
  let media;
  let body;
  if (audio) {
    media = h("audio", { class: "fv-audio", controls: true, preload: "metadata", src: ctx.url });
    body = h(
      "div",
      { class: "fv-media-body fv-media-audio" },
      h("div", { class: "fv-audio-card" }, h("span", { class: "fv-audio-icon", "aria-hidden": "true" }, icon("audio", 24)), h("p", { class: "fv-audio-name", text: file.name || "Audio" }), media)
    );
  } else {
    media = h("video", { class: "fv-video", controls: true, playsInline: true, preload: "metadata", src: ctx.url });
    media.setAttribute("playsinline", "");
    if (file.thumb) media.setAttribute("poster", file.thumb + (file.thumb.includes("?") ? "&" : "?") + "w=1024");
    body = h("div", { class: "fv-media-body fv-media-video" }, media);
  }
  media.setAttribute("aria-label", file.name || (audio ? "Audio" : "Video"));

  const speed = button({
    label: "Speed " + speedLabel(1),
    variant: "invisible",
    trailingVisual: "triangle-down",
    className: "fv-speed",
    attrs: { "aria-haspopup": "true", "aria-expanded": "false" },
    onClick: () => openSpeedMenu(),
  });
  const openSpeedMenu = (focus = "first") =>
    openMenu(
      speed,
      [
        { group: "Playback speed" },
        ...SPEEDS.map((rate) => ({
          label: speedLabel(rate),
          checked: Math.abs(media.playbackRate - rate) < 0.001,
          selectionType: "single",
          onClick: () => {
            media.playbackRate = rate;
          },
        })),
      ],
      { placement: "bottom-start", label: "Playback speed", focus }
    );
  speed.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      openSpeedMenu(event.key === "ArrowUp" ? "last" : "first");
    }
  });
  const loop = toolButton({
    label: "Loop",
    icon: "sync",
    pressed: false,
    onClick: () => {
      media.loop = !media.loop;
      loop.setAttribute("aria-pressed", media.loop ? "true" : "false");
    },
  });
  const onRate = () => {
    const text = speed.querySelector(".btn-label");
    if (text) text.textContent = "Speed " + speedLabel(media.playbackRate);
  };
  media.addEventListener("ratechange", onRate);

  const root = h("div", { class: "fv-media" + (audio ? " is-audio" : " is-video") }, toolbar(audio ? "Audio" : "Video", [speed, loop]), body);
  media.addEventListener("error", () => {
    if (media.getAttribute("src")) root.replaceChildren(errorState(ctx, audio ? "This audio file could not be played." : "This video could not be played."));
  });
  container.appendChild(root);
  return {
    unmount() {
      media.removeEventListener("ratechange", onRate);
      media.pause();
      media.removeAttribute("src");
      media.load();
      root.remove();
    },
  };
}
