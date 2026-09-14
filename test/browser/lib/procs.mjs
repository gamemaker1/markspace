// Free ports, child processes and readiness checks for the audit harness.
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { openSync } from "node:fs";

const children = new Set();

/** Asks the OS for a free TCP port on 127.0.0.1. Never returns 8080. */
export async function freePort() {
  for (;;) {
    const port = await new Promise((resolve, reject) => {
      const srv = createServer();
      srv.unref();
      srv.on("error", reject);
      srv.listen(0, "127.0.0.1", () => {
        const { port } = srv.address();
        srv.close(() => resolve(port));
      });
    });
    if (port !== 8080) return port;
  }
}

/** Starts a child process with stdout and stderr in logFile. */
export function start(name, cmd, args, { env = {}, cwd, logFile } = {}) {
  const fd = openSync(logFile, "a");
  const child = spawn(cmd, args, { cwd, env: { ...process.env, ...env }, stdio: ["ignore", fd, fd] });
  child.name = name;
  child.logFile = logFile;
  children.add(child);
  child.on("exit", () => children.delete(child));
  return child;
}

/** Stops every child process started by start(). */
export async function stopAll() {
  const list = [...children];
  for (const c of list) {
    try {
      c.kill("SIGTERM");
    } catch {
      /* already gone */
    }
  }
  const deadline = Date.now() + 5000;
  while (children.size && Date.now() < deadline) await new Promise((r) => setTimeout(r, 100));
  for (const c of children) {
    try {
      c.kill("SIGKILL");
    } catch {
      /* already gone */
    }
  }
}

for (const sig of ["SIGINT", "SIGTERM"]) {
  process.on(sig, async () => {
    await stopAll();
    process.exit(130);
  });
}

/** Polls a URL until it answers with a status below 500. */
export async function waitForHttp(url, { timeout = 30000, child } = {}) {
  const end = Date.now() + timeout;
  let last = "";
  while (Date.now() < end) {
    if (child && child.exitCode !== null) throw new Error(`${child.name} exited with ${child.exitCode}, see ${child.logFile}`);
    try {
      const r = await fetch(url);
      if (r.status < 500) return;
      last = String(r.status);
    } catch (e) {
      last = e.message;
    }
    await new Promise((r) => setTimeout(r, 150));
  }
  throw new Error(`${url} not ready after ${timeout} ms: ${last}`);
}
