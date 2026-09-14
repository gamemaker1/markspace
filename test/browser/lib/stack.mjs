// Starts Markspace, the fake AI server and the Word count plugin on free
// ports with fresh data in a temporary directory.
import { mkdirSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { freePort, start, waitForHttp } from "./procs.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
export const REPO = resolve(HERE, "../../..");
export const DEFAULT_FAKEAI_SERVER = join(HERE, "..", "fakeai.py");

/**
 * Starts the three processes. Returns {base, fakeBase, pluginBase, dataDir}.
 * @param {{bin: string, tmp: string}} opts
 */
export async function startStack({ bin, tmp }) {
  const [port, fakePort, pluginPort] = [await freePort(), await freePort(), await freePort()];
  const dataDir = join(tmp, "data");
  const logs = join(tmp, "logs");
  mkdirSync(dataDir, { recursive: true });
  mkdirSync(logs, { recursive: true });
  mkdirSync(join(tmp, "plugin-data"), { recursive: true });
  const base = `http://127.0.0.1:${port}`;
  const fakeBase = `http://127.0.0.1:${fakePort}`;
  const pluginBase = `http://127.0.0.1:${pluginPort}`;

  // FAKEAI_SERVER may name another server script that takes the port as its last argument.
  const fakeServer = process.env.FAKEAI_SERVER || DEFAULT_FAKEAI_SERVER;
  let fake = null;
  if (existsSync(fakeServer)) {
    fake = start("fakeai", "python3", [fakeServer, String(fakePort)], { logFile: join(logs, "fakeai.log") });
  } else {
    console.warn(`audit: fake AI server ${fakeServer} not found, AI states will show errors`);
  }

  const config = `listen: "127.0.0.1:${port}"
dataDir: "${dataDir}"
instanceName: "Harbour"
allowSignup: true
watchFiles: false
logLevel: warn
ai:
  enabled: true
  allowUserKeys: true
  fetchPublicPrices: false
  allowPrivateURLs: true
  providers:
    - id: fake
      name: Fake models
      kind: openai
      baseUrl: ${fakeBase}/v1
      model: fake-default
      embedModel: fake-embed
      vision: true
      imageGeneration: true
      imageEditing: true
      imageModel: fake-default
jobs:
  workers: 2
plugins:
  markspaceUrl: "${base}"
  allowPrivateURLs: true
`;
  const configPath = join(tmp, "markspace.yaml");
  writeFileSync(configPath, config);
  const server = start("markspace", bin, ["-config", configPath], { logFile: join(logs, "markspace.log"), cwd: tmp });
  const plugin = start("word-count", "python3", [join(REPO, "sdk/example/example.py")], {
    env: { PYTHONPATH: join(REPO, "sdk/python"), HOST: "127.0.0.1", PORT: String(pluginPort), PLUGIN_DATA: join(tmp, "plugin-data"), LOG_LEVEL: "WARNING" },
    logFile: join(logs, "plugin.log"),
  });
  await waitForHttp(base + "/healthz", { child: server });
  await waitForHttp(pluginBase + "/health", { child: plugin }).catch((e) => console.warn("audit: plugin not ready: " + e.message));
  if (fake) await waitForHttp(fakeBase + "/v1/models", { child: fake }).catch((e) => console.warn("audit: fake AI not ready: " + e.message));
  return { base, fakeBase, pluginBase, dataDir, logs };
}
