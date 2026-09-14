// Seeds a note with a long version history spread over several days, for the
// version history flow and the history audit states.
//
// The store keeps each snapshot as <dataDir>/internal/history/<spaceId>/
// <noteId>/<unix-millis>.md and reads the version time from the file name, the
// author from updated_by and the plugin from updated_via. The API can only
// create snapshots at the current time, so the seed makes one real snapshot
// through the API, then writes the dated snapshots next to it with the same
// frontmatter layout.
import { readdirSync, readFileSync, writeFileSync, rmSync, statSync } from "node:fs";
import { join } from "node:path";

const DAY = 86400e3;

/** Finds the history directory of a note under the data directory. */
function historyDir(dataDir, noteId) {
  const base = join(dataDir, "internal", "history");
  for (const space of readdirSync(base)) {
    const dir = join(base, space, noteId);
    try {
      if (statSync(dir).isDirectory()) return dir;
    } catch {
      /* not this space */
    }
  }
  throw new Error(`no history directory for note ${noteId} under ${base}`);
}

/**
 * The times of the seeded versions, newest first: 6 today, 5 yesterday, 12
 * earlier this week and the rest one to four weeks back.
 * @param {number} count
 * @param {number} now unix milliseconds
 * @returns {number[]}
 */
export function historyTimes(count, now) {
  const start = new Date(now);
  start.setHours(0, 0, 0, 0);
  const today = start.getTime();
  const out = [];
  const minutesIntoToday = Math.max(10, Math.floor((now - today) / 60e3) - 5);
  for (let i = 0; i < 6 && out.length < count; i++) out.push(now - Math.round(((i + 1) * minutesIntoToday * 60e3) / 7));
  for (let i = 0; i < 5 && out.length < count; i++) out.push(today - DAY + (20 - i * 3) * 3600e3 + 17 * 60e3);
  for (let d = 2; d <= 5 && out.length < count; d++) for (let i = 0; i < 3 && out.length < count; i++) out.push(today - d * DAY + (18 - i * 4) * 3600e3 + (7 + i) * 60e3);
  for (let d = 8; out.length < count; d += 1) out.push(today - d * DAY + 10 * 3600e3 + (d % 50) * 60e3);
  return out;
}

/**
 * Creates the note "History sample" with count dated versions.
 * @param {import("playwright-core").APIRequestContext} api signed in as the note owner
 * @param {{base: string, dataDir: string, spaceId: string, authors: string[], via?: string, count?: number}} options
 *   authors are user ids. Every seventh version is written through the plugin named by via.
 * @returns {Promise<{note: object, versions: {id: string, createdAt: number, authorId: string, via: string, content: string}[], current: string}>}
 */
export async function seedHistory(api, { base, dataDir, spaceId, authors, via = "word-count", count = 40 }) {
  const lines = (n, seed) => Array.from({ length: n }, (_, i) => `- Reading ${seed}.${i + 1}: depth ${((seed * 7 + i * 3) % 90) + 10} cm at the north quay`);
  const bodyFor = (i) => `# History sample\n\nSurvey log, revision ${i}.\n\n${lines(3 + (i % 5), i).join("\n")}\n\n## Notes\n\n${i % 3 === 0 ? "The crane rail is sound.\n" : "Silt depth rose since spring.\n"}`;
  const created = await api.post(`${base}/api/notes`, { data: { spaceId, title: "History sample", content: bodyFor(0) } });
  if (!created.ok()) throw new Error("history note: " + created.status());
  const note = await created.json();
  const snap = await api.patch(`${base}/api/notes/${note.id}`, { data: { content: bodyFor(1), snapshot: true } });
  if (!snap.ok()) throw new Error("history snapshot: " + snap.status());
  const dir = historyDir(dataDir, note.id);
  const files = readdirSync(dir).filter((f) => f.endsWith(".md"));
  if (!files.length) throw new Error("the API snapshot was not written to " + dir);
  const sample = readFileSync(join(dir, files[0]), "utf8");
  const m = /^---\n([\s\S]*?)\n---\n/.exec(sample);
  if (!m) throw new Error("snapshot without frontmatter: " + files[0]);
  const header = m[1].split("\n").filter((l) => !/^(updated_by|updated_via|updated|label):/.test(l));
  for (const f of files) rmSync(join(dir, f));
  const times = historyTimes(count, Date.now());
  const versions = [];
  // Oldest first, so version n + 1 builds on version n.
  times.slice().reverse().forEach((t, i) => {
    const authorId = authors[i % authors.length];
    const byPlugin = via && i % 7 === 3;
    const content = bodyFor(i + 1);
    const fm = [...header, `updated_by: ${authorId}`, ...(byPlugin ? [`updated_via: ${via}`] : []), `updated: ${new Date(t).toISOString()}`];
    writeFileSync(join(dir, `${t}.md`), `---\n${fm.join("\n")}\n---\n${content}`);
    versions.push({ id: String(t), createdAt: t, authorId, via: byPlugin ? via : "", content });
  });
  // The current text differs from the newest version.
  const current = bodyFor(count + 1) + "\nToday's edit, not yet a version.\n";
  const upd = await api.patch(`${base}/api/notes/${note.id}`, { data: { content: current } });
  if (!upd.ok()) throw new Error("history current text: " + upd.status());
  return { note, versions: versions.reverse(), current };
}
