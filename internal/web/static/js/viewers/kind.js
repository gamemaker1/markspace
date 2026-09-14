// Picks the viewer for a file and the CodeMirror language for text files.
// Pure functions without DOM access, shared by the file view and the tests.

/** Text files larger than this open the generic viewer. */
export const TEXT_MAX_BYTES = 5 * 1024 * 1024;

/** CSV and TSV files larger than this open the generic viewer. */
export const CSV_MAX_BYTES = 20 * 1024 * 1024;

const IMAGE_EXT = new Set(["png", "jpg", "jpeg", "gif", "webp", "svg", "avif", "bmp", "apng", "ico"]);
// Image types that browsers draw in an <img>. Others, such as TIFF and HEIC, open the generic viewer.
const IMAGE_MIME = new Set([
  "image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml", "image/avif", "image/bmp",
  "image/apng", "image/x-icon", "image/vnd.microsoft.icon",
]);
const VIDEO_EXT = new Set(["mp4", "m4v", "webm", "mov", "ogv", "mkv"]);
const AUDIO_EXT = new Set(["mp3", "wav", "ogg", "oga", "m4a", "flac", "aac", "opus", "weba"]);
const CSV_EXT = new Set(["csv", "tsv", "tab"]);
const MARKDOWN_EXT = new Set(["md", "markdown", "mdown", "mkd"]);
const TEXT_EXT = new Set([
  "txt", "text", "log", "json", "jsonc", "json5", "ndjson", "yaml", "yml", "toml", "ini", "cfg", "conf", "env",
  "xml", "html", "htm", "xhtml", "css", "scss", "less", "js", "mjs", "cjs", "jsx", "ts", "tsx", "py", "go", "rs",
  "c", "h", "cc", "cpp", "hpp", "cs", "java", "kt", "swift", "rb", "php", "pl", "lua", "r", "dart", "scala",
  "sh", "bash", "zsh", "fish", "ps1", "bat", "sql", "graphql", "proto", "vue", "svelte", "tex", "rst", "adoc",
  "diff", "patch", "properties", "gradle", "makefile", "dockerfile", "gitignore", "editorconfig", "srt", "vtt",
  ...MARKDOWN_EXT,
]);
const TEXT_MIME = [
  /^text\//, /^application\/(json|ld\+json|x-ndjson|xml|yaml|x-yaml|toml|javascript|x-javascript|ecmascript|x-sh|x-httpd-php|sql|graphql|x-tex)$/,
  /\+json$/, /\+xml$/,
];

/**
 * Returns the lowercased extension of a file name, or the whole lowercased
 * name for names such as "Makefile" that have none.
 * @param {string} name
 * @returns {string}
 */
export function extensionOf(name) {
  const base = String(name || "").split("/").pop().toLowerCase();
  const dot = base.lastIndexOf(".");
  if (dot < 0) return base;
  return base.slice(dot + 1);
}

/**
 * Picks the viewer for a file.
 * @param {{name?: string, mime?: string, size?: number}} file
 * @returns {"image"|"pdf"|"video"|"audio"|"text"|"csv"|"other"}
 */
export function viewerKind(file) {
  const mime = String((file && file.mime) || "").toLowerCase().split(";")[0].trim();
  const ext = extensionOf(file && file.name);
  const size = Number((file && file.size) || 0);
  if (mime === "application/pdf" || ext === "pdf") return "pdf";
  if (IMAGE_MIME.has(mime) || IMAGE_EXT.has(ext)) return "image";
  if (mime.startsWith("video/") || VIDEO_EXT.has(ext)) return "video";
  if (mime.startsWith("audio/") || AUDIO_EXT.has(ext)) return "audio";
  if (mime === "text/csv" || mime === "text/tab-separated-values" || CSV_EXT.has(ext)) {
    return size > CSV_MAX_BYTES ? "other" : "csv";
  }
  if (TEXT_EXT.has(ext) || TEXT_MIME.some((re) => re.test(mime))) {
    return size > TEXT_MAX_BYTES ? "other" : "text";
  }
  return "other";
}

const LANGUAGE_BY_EXT = {
  js: "javascript", mjs: "javascript", cjs: "javascript", jsx: "javascript",
  ts: "typescript", tsx: "typescript",
  json: "json", jsonc: "json", json5: "json", ndjson: "json",
  py: "python", go: "go",
  css: "css", scss: "css", less: "css",
  html: "html", htm: "html", xhtml: "html", xml: "html", svg: "html", vue: "html", svelte: "html",
  sql: "sql",
  sh: "shell", bash: "shell", zsh: "shell", fish: "shell",
};

/**
 * Returns the CodeMirror language name for a text file: one of the names in
 * codeLanguages of codemirror.js, "markdown", or "" for plain text.
 * @param {string} name
 * @param {string} [mime]
 * @returns {string}
 */
export function languageFor(name, mime = "") {
  const ext = extensionOf(name);
  if (MARKDOWN_EXT.has(ext)) return "markdown";
  if (LANGUAGE_BY_EXT[ext]) return LANGUAGE_BY_EXT[ext];
  const type = String(mime || "").toLowerCase().split(";")[0].trim();
  if (type === "text/markdown") return "markdown";
  if (type === "application/json" || type.endsWith("+json")) return "json";
  if (type === "text/javascript" || type === "application/javascript") return "javascript";
  if (type === "text/html" || type === "application/xml" || type === "text/xml" || type.endsWith("+xml")) return "html";
  if (type === "text/css") return "css";
  if (type === "application/x-sh") return "shell";
  if (type === "application/sql") return "sql";
  return "";
}
