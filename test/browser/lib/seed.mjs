// Seeds a fresh Markspace through the HTTP API with enough data for scroll
// and overflow problems to show: 2 sign-in accounts, 2 spaces, a folder tree
// 4 levels deep with 40 items, a 3000-word note, a note with 5 shares, 3
// public links, 25 comment threads and 30 versions, open suggestions, AI
// usage, the Word count plugin with jobs, trash, and one sandbox per worker
// for the states that change data.
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { png, pdf, csv, video } from "./media.mjs";

export const ADMIN = { email: "ada@example.com", name: "Ada Lovelace", password: "analytical engine" };
export const MEMBER = { email: "grace@example.com", name: "Grace Hopper", password: "another good password" };
const PEOPLE = [
  ["alan@example.com", "Alan Turing"],
  ["katherine.johnson.long.address@example.com", "Katherine Johnson"],
  ["hedy@example.com", "Hedy Lamarr"],
  ["margaret@example.com", "Margaret Hamilton"],
];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/** makeAnchor from docs/comments.md for bodies without surrogate pairs. */
export function makeAnchor(body, start, end) {
  start = Math.max(0, Math.min(start, body.length));
  end = Math.max(0, Math.min(end, body.length));
  if (start > end) [start, end] = [end, start];
  if (start === end) return null;
  return { exact: body.slice(start, end), prefix: body.slice(Math.max(0, start - 32), start), suffix: body.slice(end, end + 32), start, end };
}

/** Anchor on the n-th occurrence of needle in body. */
function anchorOn(body, needle, nth = 0) {
  let at = -1;
  for (let i = 0; i <= nth; i++) {
    at = body.indexOf(needle, at + 1);
    if (at < 0) return null;
  }
  return makeAnchor(body, at, at + needle.length);
}

const WORDS = "survey quay tide gauge silt depth crane rail lighthouse terminal grain dredging estimate reading harbour pier wall spring winter north east channel buoy vessel berth sounding chart datum mooring breakwater sediment".split(" ");

/** About count words of plain prose, deterministic. */
function prose(count, seed) {
  let s = seed;
  const out = [];
  let sentence = [];
  for (let i = 0; i < count; i++) {
    s = (s * 1103515245 + 12345) % 2147483648;
    sentence.push(WORDS[s % WORDS.length]);
    if (sentence.length >= 9 + (s % 7)) {
      const t = sentence.join(" ");
      out.push(t[0].toUpperCase() + t.slice(1) + ".");
      sentence = [];
    }
  }
  if (sentence.length) out.push(sentence.join(" ") + ".");
  return out.join(" ");
}

function longNote() {
  const parts = ["# Harbour field manual", "", "This manual collects the working methods for the harbour survey. See [[Methods]], [[Equipment]] and [[Harbour survey]] for the current season.", ""];
  for (let s = 1; s <= 12; s++) {
    parts.push(`## Section ${s}: ${["Tides", "Silt", "Cranes", "Lighthouse", "Terminal", "Dredging", "Buoys", "Berths", "Soundings", "Charts", "Moorings", "Breakwater"][s - 1]}`, "");
    parts.push(prose(110, s * 7), "", prose(90, s * 13), "");
    if (s % 3 === 1) parts.push("![[east wall.png]]", "");
    if (s % 3 === 2) {
      parts.push("| Pier | Depth (m) | Reading | Checked by |", "| --- | ---: | ---: | --- |");
      for (let r = 1; r <= 6; r++) parts.push(`| Pier ${s}${r} | ${(r * 1.7).toFixed(1)} | ${1000 + r * 13} | Katherine Johnson |`);
      parts.push("");
    }
    if (s % 3 === 0) parts.push("```python", "def depth(reading, datum=0.42):", '    """Convert a gauge reading to metres below datum."""', "    return round(reading / 1000 - datum, 3)", "```", "");
    parts.push(`- [ ] Check the ${WORDS[s]} readings for section ${s}`, `- [x] Photograph the ${WORDS[s + 3]} before the tide`, `- [ ] Link [[Meeting notes]] to section ${s}`, "");
    parts.push(prose(40, s * 29), "");
  }
  return parts.join("\n");
}

/**
 * Seeds the instance. Returns ids and URLs for the states.
 * @param {object} pw playwright-core module
 * @param {string} base
 * @param {{tmp: string, dataDir: string, pluginBase: string, workers: number}} opts
 */
export async function seed(pw, base, { tmp, dataDir, pluginBase, workers = 4 }) {
  const log = [];
  const api = await pw.request.newContext({ baseURL: base });
  const j = async (r) => {
    const t = await r.text();
    try {
      return JSON.parse(t);
    } catch {
      return t;
    }
  };
  const call = async (label, promise, { soft = false } = {}) => {
    const r = await promise;
    const body = await j(r);
    if (!r.ok()) {
      const msg = `seed ${label}: ${r.status()} ${JSON.stringify(body).slice(0, 200)}`;
      if (!soft) throw new Error(msg);
      log.push(msg);
    }
    return body;
  };
  const post = (label, url, data, o) => call(label, api.post(url, { data }), o);

  await post("signup", "/api/auth/signup", ADMIN);
  await post("login", "/api/auth/login", { email: ADMIN.email, password: ADMIN.password });
  const settings = await call("settings", api.get("/api/settings"));
  await call("settings patch", api.patch("/api/settings", { data: { ...settings, aiEnabled: true, aiAllowUserKeys: true, aiAllowUserRoutes: true, aiMonthlyTokenBudget: 2000000 } }));

  await post("member", "/api/users", { email: MEMBER.email, name: MEMBER.name, password: MEMBER.password });
  for (const [email, name] of PEOPLE) await post("user " + email, "/api/users", { email, name, password: "another good password" });
  const users = (await call("users", api.get("/api/users?limit=50"))).items;
  const uid = (email) => users.find((u) => u.email === email).id;

  let spaces = (await call("spaces", api.get("/api/spaces"))).items || [];
  if (!spaces.length) spaces = [await post("space", "/api/spaces", { name: "Harbour works" })];
  await post("space 2", "/api/spaces", { name: "Team handbook" });
  spaces = (await call("spaces", api.get("/api/spaces"))).items;
  const space = spaces[0];
  const space2 = spaces.find((s) => s.id !== space.id);
  for (const sp of [space, space2]) {
    await post("member of " + sp.slug, `/api/spaces/${sp.id}/members`, { userId: uid(MEMBER.email), role: "editor" });
    for (const [email] of PEOPLE.slice(0, 2)) await post("people of " + sp.slug, `/api/spaces/${sp.id}/members`, { userId: uid(email), role: "viewer" }, { soft: true });
  }

  /* Folder tree, 4 levels, 40 items. */
  const folder = (name, parentId = "", sp = space) => post("folder " + name, "/api/folders", { spaceId: sp.id, parentId, name });
  const note = (title, content, parentId = "", extra = {}, sp = space) => post("note " + title, "/api/notes", { spaceId: sp.id, parentId, title, content, ...extra });
  const upload = async (parentId, name, mime, buffer, sp = space) => {
    const r = await api.post("/api/files", { multipart: { spaceId: sp.id, parentId, file: { name, mimeType: mime, buffer } } });
    const body = await j(r);
    if (!r.ok()) throw new Error(`seed upload ${name}: ${r.status()} ${JSON.stringify(body).slice(0, 200)}`);
    return body;
  };
  const projects = await folder("Projects");
  const harbour = await folder("Harbour works", projects.id);
  const surveys = await folder("Surveys", harbour.id);
  const season = await folder("2026 season with a long folder name for truncation", surveys.id);
  const photos = await folder("Photos");
  const archive = await folder("Archive");
  const empty = await folder("Empty box");

  const S = { base, space, space2, users: { admin: uid(ADMIN.email), member: uid(MEMBER.email) }, folders: { projects, harbour, surveys, season, photos, archive, empty }, files: {}, notes: {}, links: {}, sandboxes: [], log };

  S.files.png = await upload(photos.id, "east wall.png", "image/png", png(960, 640, 1));
  S.files.png2 = await upload(photos.id, "north quay at low tide.png", "image/png", png(640, 960, 3));
  await upload(season.id, "gauge 1.png", "image/png", png(320, 240, 5));
  await upload(season.id, "gauge 2.png", "image/png", png(320, 240, 6));
  S.files.pdf = await upload(surveys.id, "report.pdf", "application/pdf", pdf(8));
  S.files.csv = await upload(surveys.id, "tides.csv", "text/csv", csv(80));
  S.files.json = await upload(harbour.id, "readings.json", "application/json", Buffer.from(JSON.stringify({ site: "north quay", readings: Array.from({ length: 30 }, (_, i) => ({ pier: i + 1, depth: i * 0.37 })) }, null, 2)));
  S.files.text = await upload(archive.id, "field log.txt", "text/plain", Buffer.from(prose(400, 3)));
  mkdirSync(join(tmp, "media"), { recursive: true });
  const mp4 = video(join(tmp, "media"));
  if (mp4) S.files.video = await upload(photos.id, "tide.mp4", "video/mp4", mp4);
  else log.push("seed: no video, ffmpeg missing");
  for (let i = 1; i <= 8; i++) await upload(archive.id, `log ${String(i).padStart(2, "0")}.txt`, "text/plain", Buffer.from(`entry ${i}\n`.repeat(i * 3)));

  S.notes.methods = await note("Methods", "# Methods\n\nReadings were taken at low tide with a weighted line. Back to [[Harbour survey]].\n", harbour.id, { tags: ["survey"] });
  S.notes.equipment = await note("Equipment", "# Equipment\n\n- Weighted line\n- Echo sounder\n- Tide gauge\n", harbour.id, { tags: ["survey", "kit"] });
  S.notes.meeting = await note("Meeting notes", "# Meeting notes\n\nAgreed to repeat the east wall survey. See [[Harbour survey]].\n", "", { tags: ["meeting"] });
  S.notes.manual = await note("Harbour field manual", longNote(), projects.id, { tags: ["survey", "manual"] });
  for (let i = 1; i <= 6; i++) await note(`Day ${i} readings`, `# Day ${i} readings\n\n${prose(80, i * 5)}\n`, season.id, { tags: ["survey"] });
  await note("Deep note", "# Deep note\n\nFour levels down.\n", season.id);
  await note("Welcome", "# Welcome\n\nStart with [[Harbour field manual]].\n", "", {}, space2);
  await note("Travel policy", `# Travel policy\n\n${prose(200, 11)}\n`, "", {}, space2);
  await note("Photo index", "# Photo index\n\n![[east wall.png]]\n\n![[north quay at low tide.png]]\n", photos.id);
  S.notes.empty = await note("Empty page", "");
  await note("Old archive note", `# Old archive note\n\n${prose(60, 2)}\n`, archive.id);

  /* The rail note. */
  const body = `# Harbour survey

The survey covers the north quay, the grain terminal and the old lighthouse. See [[Methods]] for how readings were taken.

![[east wall.png]]

## Findings

- Silt depth rose **18 cm** since spring
- The crane rail is sound
- The lighthouse lamp needs a new lens

## Estimate

The closing estimate of forty tonnes looks too high for one tide.
Readings were taken at low tide.

## Next steps

${prose(160, 17)}

${prose(120, 19)}
`;
  const survey = await note("Harbour survey", body, harbour.id, { tags: ["survey"] });
  S.notes.survey = survey;
  for (let v = 1; v <= 30; v++) {
    await call("version " + v, api.patch(`/api/notes/${survey.id}`, { data: { content: body + `\nRevision ${v}: ${prose(8, v)}\n`, snapshot: true } }));
    await sleep(15);
  }
  await call("final body", api.patch(`/api/notes/${survey.id}`, { data: { content: body, snapshot: true } }));
  const current = (await call("survey", api.get(`/api/notes/${survey.id}`))).content || body;
  const people = [MEMBER.email, ...PEOPLE.map((p) => p[0])];
  const access = ["edit", "comment", "view", "edit", "comment"];
  for (const [i, email] of people.entries()) await post("share " + email, `/api/notes/${survey.id}/shares`, { noteId: survey.id, userId: uid(email), access: access[i], includeChildren: i === 3 });
  const expires = new Date(Date.now() + 14 * 86400e3).toISOString();
  S.links.survey = await post("link 1", `/api/notes/${survey.id}/links`, { noteId: survey.id, access: "view" });
  S.links.password = await post("link 2", `/api/notes/${survey.id}/links`, { noteId: survey.id, access: "view", password: "tide", expiresAt: expires });
  await post("link 3", `/api/notes/${survey.id}/links`, { noteId: survey.id, access: "comment", includeChildren: true });
  S.links.folder = await post("folder link", `/api/items/${harbour.id}/links`, { noteId: harbour.id, access: "view", includeChildren: true }, { soft: true });
  S.links.file = await post("file link", `/api/items/${S.files.pdf.id}/links`, { noteId: S.files.pdf.id, access: "view" }, { soft: true });
  S.links.manual = await post("manual link", `/api/notes/${S.notes.manual.id}/links`, { noteId: S.notes.manual.id, access: "view" });

  const anchors = ["north quay", "grain terminal", "old lighthouse", "Silt depth rose", "crane rail is sound", "new lens", "closing estimate", "forty tonnes", "one tide", "Readings were taken at low tide"];
  const threads = [];
  for (let i = 0; i < 25; i++) {
    const anchor = i < anchors.length ? anchorOn(current, anchors[i]) : null;
    const text = i % 4 === 0 ? `Thread ${i + 1}: the estimate in section ${i + 1} needs a second reading before we publish, and the gauge log for that morning should be attached so the port authority can check the datum correction.` : `Thread ${i + 1}: can we confirm this with the port authority?`;
    const c = await post("comment " + i, `/api/notes/${survey.id}/comments`, { noteId: survey.id, body: text, ...(anchor ? { anchor } : {}) });
    threads.push(c);
    if (i % 3 === 0) await post("reply " + i, `/api/notes/${survey.id}/comments`, { noteId: survey.id, parentId: c.id, body: "Agreed. I will check it on the next low tide." });
    if (i % 5 === 4) await post("resolve " + i, `/api/comments/${c.id}/resolve`, { resolved: true });
  }
  S.threads = threads.map((t) => t.id);

  /* Open suggestions with anchors (docs/comments.md, suggestions module). */
  const sugg = [
    ["Tighten the estimate", "The figure is too high for one tide.", [["closing estimate of forty tonnes looks too high", "final estimate of thirty tonnes seems more likely"]]],
    ["Name the lighthouse", "", [["old lighthouse", "Harbour Point lighthouse"], ["grain terminal", "east grain terminal"]]],
    ["Fix the findings list", "Units should be spelled out.", [["18 cm", "18 centimetres"]]],
  ];
  for (const [title, rationale, edits] of sugg) {
    const e = edits.map(([needle, text]) => ({ anchor: anchorOn(current, needle), text })).filter((x) => x.anchor);
    await post("suggestion " + title, `/api/notes/${survey.id}/suggestions`, { title, rationale, edits: e }, { soft: true });
  }

  /* Stars, member content, tokens, invite. */
  for (const n of [survey, S.notes.manual, S.notes.meeting]) await call("star", api.put(`/api/notes/${n.id}/star`), { soft: true });
  await post("token 1", "/api/tokens", { name: "Backup script", ttlDays: 90 });
  await post("token 2", "/api/tokens", { name: "Phone sync shortcut with a long descriptive name", ttlDays: 0 });
  await post("invite", "/api/invites", { email: "linus@example.com", spaceId: space.id, role: "editor" }, { soft: true });

  const member = await pw.request.newContext({ baseURL: base });
  await member.post("/api/auth/login", { data: { email: MEMBER.email, password: MEMBER.password } });
  const mnote = await j(await member.post("/api/notes", { data: { spaceId: space2.id, title: "Grace's draft", content: "# Grace's draft\n\nShared with Ada for review.\n" } }));
  if (mnote && mnote.id) {
    await member.post(`/api/notes/${mnote.id}/shares`, { data: { noteId: mnote.id, userId: uid(ADMIN.email), access: "comment" } });
    await member.post(`/api/notes/${survey.id}/comments`, { data: { noteId: survey.id, body: "Grace here: the photos are in the Photos folder." } });
  }
  await member.dispose();

  /* Sandboxes for states that change data, one per worker. */
  for (let w = 0; w < workers; w++) {
    const box = await folder(`Sandbox ${w + 1}`);
    const target = await folder(`Target ${w + 1}`, box.id);
    const move = await note(`Move me ${w + 1}`, "# Move me\n\nThis note moves and comes back.\n", box.id);
    const trash = await note(`Trash me ${w + 1}`, "# Trash me\n\nThis note goes to the trash and comes back.\n", box.id);
    const shareBody = "# Share sandbox\n\nThe tide table needs a check before Friday.\n";
    const shared = await note(`Share sandbox ${w + 1}`, shareBody, box.id);
    for (const email of [PEOPLE[2][0], PEOPLE[3][0]]) await post("sandbox share", `/api/notes/${shared.id}/shares`, { noteId: shared.id, userId: uid(email), access: "view" });
    const c1 = await post("sandbox comment", `/api/notes/${shared.id}/comments`, { noteId: shared.id, body: "Please check the tide table.", anchor: anchorOn(shareBody, "tide table") });
    await post("sandbox comment 2", `/api/notes/${shared.id}/comments`, { noteId: shared.id, body: "A general comment." });
    const img = S.files.png.url ? `![East wall](${String(S.files.png.url).replace(/ /g, "%20")})` : "![[east wall.png]]";
    const editor = await note(`Editor sandbox ${w + 1}`, body.replace("![[east wall.png]]", img), box.id);
    S.sandboxes.push({ box, target, move, trash, shared, editor, comment: c1.id, people: [PEOPLE[2][1], PEOPLE[3][1]] });
  }

  /* Trash. */
  const t1 = await note("Old draft", "# Old draft\n\nSuperseded.\n", archive.id);
  const t2 = await note("Budget 2025", "# Budget 2025\n\nReplaced by the 2026 budget.\n");
  await post("trash 1", `/api/notes/${t1.id}/trash`, {});
  await post("trash 2", `/api/notes/${t2.id}/trash`, {});
  const tf = await upload(archive.id, "scratch notes.txt", "text/plain", Buffer.from("scratch\n"));
  await post("trash file", `/api/items/${tf.id}/trash`, {});

  /* AI usage: real calls through the fake server, then 30 days of history. */
  for (const task of ["summarize", "improve", "ask"]) {
    await post("ai " + task, "/api/ai/run", { task, spaceId: space.id, noteId: survey.id, text: "The closing estimate of forty tonnes looks too high for one tide.", question: task === "ask" ? "What does the survey cover?" : undefined }, { soft: true });
  }
  const db = join(dataDir, "markspace.db");
  if (existsSync(db)) {
    try {
      execFileSync("sqlite3", [db], { input: usageSQL(S.users, space, survey), stdio: ["pipe", "ignore", "pipe"] });
    } catch (e) {
      log.push("seed usage rows: " + String(e.message).slice(0, 200));
    }
  }

  /* Plugin install and jobs (docs/plugins.md, p4host/realrun.py). */
  try {
    const prev = await post("plugin preview", "/api/admin/plugins/preview", { baseUrl: pluginBase });
    const inst = await post("plugin install", "/api/admin/plugins", { baseUrl: prev.baseUrl, sha256: prev.sha256, grants: [{ spaceId: space.id, role: "editor" }, { spaceId: space2.id, role: "viewer" }] });
    S.plugin = { id: inst.plugin && inst.plugin.id, panel: "word-count.counts" };
    await sleep(800);
    for (let i = 0; i < 2; i++) {
      const act = await post("plugin action", "/api/modules/plugin.word-count/actions/count-space", { context: { itemId: survey.id }, payload: { spaceId: space.id } }, { soft: true });
      if (act && act.job) S.jobId = act.job.id;
    }
    await post("plugin command", "/api/commands/word-count.count", { context: { itemId: survey.id } }, { soft: true });
    if (existsSync(db)) {
      const now = new Date().toISOString().replace(/\.(\d{3})Z$/, ".$1000000Z");
      const sql = `.timeout 5000
INSERT INTO jobs (id, kind, module, space_id, created_by, status, title, progress, total, message, payload, attempts, max_attempts, created_at, updated_at) VALUES ('auditpaused1', 'count-space', 'plugin.word-count', '${space.id}', 'plugin:word-count', 'paused', 'Count the archive', 212, 540, 'Paused by Ada Lovelace', '{"spaceId":"${space.id}"}', 1, 3, '${now}', '${now}');
INSERT INTO jobs (id, kind, module, space_id, created_by, status, title, progress, total, message, payload, attempts, max_attempts, created_at, updated_at) VALUES ('auditfailed1', 'count-space', 'plugin.word-count', '${space.id}', 'plugin:word-count', 'failed', 'Count words in a space that no longer exists on this instance', 3, 40, 'plugin returned 503: the space was deleted while the count ran', '{"spaceId":"${space.id}"}', 3, 3, '${now}', '${now}');
INSERT INTO job_log (job_id, at, level, message) VALUES ('auditpaused1', '${now}', 'info', 'Counted 212 of 540 notes'), ('auditfailed1', '${now}', 'error', 'plugin returned 503');
`;
      try {
        execFileSync("sqlite3", [db], { input: sql, stdio: ["pipe", "ignore", "pipe"] });
      } catch (e) {
        log.push("seed job rows: " + String(e.message).slice(0, 200));
      }
    }
  } catch (e) {
    log.push(String(e.message));
  }

  await api.dispose();
  return S;
}

/** SQL for 30 days of ai_usage rows and two budgets (primer-p7/seed.mjs). */
function usageSQL(users, space, note) {
  let seedN = 42;
  const rnd = () => {
    seedN = (seedN * 1664525 + 1013904223) % 4294967296;
    return seedN / 4294967296;
  };
  const pick = (a) => a[Math.floor(rnd() * a.length)];
  const q = (s) => "'" + String(s).replace(/'/g, "''") + "'";
  const stamp = (ms) => new Date(ms).toISOString().replace(/\.(\d{3})Z$/, ".$1000000Z");
  const now = Date.now();
  const today = Math.floor(now / 86400e3) * 86400e3;
  const plan = [
    { user: users.admin, calls: 12, mix: [["ask", "fake-ask"], ["rewrite", "fake-default"], ["chat", "fake-default"], ["search", "fake-embed"], ["summarize", "fake-summarise", "word-count"]] },
    { user: users.member, calls: 6, mix: [["ask", "fake-default"], ["chat", "fake-ask"], ["translate", "fake-default", "com.example.translate"]] },
  ];
  const sql = [".timeout 5000", "BEGIN;"];
  let n = 0;
  for (let d = 29; d >= 0; d--) {
    for (const p of plan) {
      const calls = Math.round(p.calls * (0.5 + rnd()));
      for (let c = 0; c < calls; c++) {
        const [feature, model, plugin = ""] = pick(p.mix);
        const created = Math.min(now - 60e3, today - d * 86400e3 + Math.floor(rnd() * 86400e3));
        const input = 400 + Math.floor(rnd() * 4000);
        const output = feature === "search" ? 0 : 80 + Math.floor(rnd() * 700);
        const roll = rnd();
        const status = roll < 0.04 ? "error" : roll < 0.05 ? "canceled" : "ok";
        const error = status === "error" ? "provider returned 429: rate limit reached for fake-default on tokens per min. Limit 30000, used 29870, requested 812." : "";
        sql.push(`INSERT INTO ai_usage (id, user_id, provider_id, model, feature, plugin_id, space_id, note_id, input_tokens, output_tokens, images_in, images_out, duration_ms, status, error, cost_micros, estimated, created_at) VALUES (${q("audit" + n++)}, ${q(p.user)}, 'fake', ${q(model)}, ${q(feature)}, ${q(plugin)}, ${q(space.id)}, ${q(note.id)}, ${input}, ${output}, 0, 0, ${700 + Math.floor(rnd() * 5000)}, ${q(status)}, ${q(error)}, ${input + output * 2}, 0, ${q(stamp(created))});`);
      }
    }
  }
  sql.push(`INSERT OR REPLACE INTO ai_budgets (subject_kind, subject_id, monthly_tokens, monthly_cost, lifted_until) VALUES ('user', ${q(users.member)}, 150000, NULL, '');`);
  sql.push(`INSERT OR REPLACE INTO ai_budgets (subject_kind, subject_id, monthly_tokens, monthly_cost, lifted_until) VALUES ('plugin', 'word-count', 40000, NULL, '');`);
  sql.push("COMMIT;");
  return sql.join("\n") + "\n";
}
