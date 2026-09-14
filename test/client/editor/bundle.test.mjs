// Checks the CodeMirror internals that editor.js reads. repositionTooltips
// calls the tooltip plugin's maybeMeasure through view.plugins, because the
// vendored bundle does not export repositionTooltips. A new bundle that
// renames these members fails here instead of silently leaving tooltips
// under the on-screen keyboard.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";

register("data:text/javascript," + encodeURIComponent(`
export async function resolve(specifier, context, next) {
  if (specifier.startsWith("/static/")) {
    return { url: new URL("../../../internal/web/static/" + specifier.slice(8), ${JSON.stringify(import.meta.url)}).href, shortCircuit: true };
  }
  return next(specifier, context);
}`));

const bundle = readFileSync(new URL("../../../internal/web/static/vendor/codemirror.js", import.meta.url), "utf8");
const { repositionTooltips } = await import("../../../internal/web/static/js/editor.js");

test("the EditorView keeps its plugin instances in view.plugins", () => {
  assert.match(bundle, /this\.plugins=\[\],this\.pluginMap=new Map/, "EditorView constructor");
  assert.match(bundle, /this\.plugins=this\.state\.facet\(\w+\)\.map\(\(\w+\)=>new \w+\(\w+\)\)/, "plugins rebuilt on reconfigure");
});

test("a plugin instance exposes the plugin object as value", () => {
  // PluginInstance: constructor(spec) { this.spec = spec; this.mustUpdate = null; this.value = null }
  assert.match(bundle, /this\.spec=\w+,this\.mustUpdate=null,this\.value=null/);
});

test("the tooltip plugin has manager.tooltips and maybeMeasure", () => {
  assert.match(bundle, /maybeMeasure\(\)\{if\(this\.manager\.tooltips\.length\)\{if\(this\.view\.inView\)this\.view\.requestMeasure\(this\.measureReq\)/);
  assert.match(bundle, /this\.manager=new \w+\(\w+,\w+\.tooltipSpace|this\.manager=new \w+\(/);
});

test("repositionTooltips measures only tooltip plugins", () => {
  const calls = [];
  const view = {
    plugins: [
      { value: { manager: { tooltips: [1] }, maybeMeasure: () => calls.push("tooltip") } },
      { value: { update() {} } },
      { value: null },
      null,
    ],
  };
  assert.equal(repositionTooltips(view), 1);
  assert.deepEqual(calls, ["tooltip"]);
  assert.equal(repositionTooltips({}), 0);
  assert.equal(repositionTooltips(null), 0);
});
