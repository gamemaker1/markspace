// Renders plugin declarative UI trees with the Primer components in ui.js.
//
// A plugin panel, page or command result is a JSON tree built from the node
// types in docs/plugins.md. validatePluginUI applies the rules of ValidateUI in
// internal/module/uitree.go and reports the same error codes and JSON
// pointers. renderPluginUI draws a valid tree. It never draws part of an
// invalid tree. It shows an InlineMessage with the error instead.
//
// Plugin strings reach the page as text nodes and attribute values. The one
// exception is a markdown node, whose HTML comes from the note renderer. That
// renderer escapes raw HTML and refuses unsafe link targets.
//
// Actions. renderPluginUI calls onAction(actionId, payload, values), and the
// host posts the action and resolves the command response. A form passes its
// values by field name. A table row action passes the row cells. A button
// passes {}. The renderer applies {ui} with update and shows {toast} as a
// Banner at the top of the container. The host acts on navigate, openPanel
// and job, because onAction sees every response first. A rejected promise, or
// a response shaped {error, field}, shows the error on the form field it
// names, on the form, or in a Banner.
//
// Context. context.subscribe(type, fn) returns an unsubscribe function and
// feeds progress nodes with job.updated events. context.lookup(kind, query)
// resolves to [{id, label, description}] for note, space and folder fields.
// context.headingLevel sets the level of the first section heading and
// defaults to 2.

import {
  h,
  icon,
  ICON_NAMES,
  uid,
  button,
  iconButton,
  setBusy,
  actionMenu,
  confirmDialog,
  banner,
  inlineMessage,
  counterLabel,
  label as primerLabel,
  progressBar,
  blankslate,
  formControl,
  clearFieldErrors,
  showFieldErrors,
  details,
  autocomplete,
  selectPanel,
} from "./ui.js";
import { render as renderMarkdown } from "./markdown.js";
import { upload as apiUpload } from "./api.js";

/* ---------- Validation ---------- */

const MAX_NODES = 2000;
const MAX_DEPTH = 32;
const MAX_BYTES = 1 << 20;
// No JSON text under 4 MiB holds more values than this. The server refuses
// larger input before it decodes it.
const MAX_VALUES = 4 << 20;

const FIELD_ORDER = {
  stack: ["children", "gap"],
  section: ["title", "description", "count", "children"],
  form: ["id", "title", "description", "fields", "submit", "action", "more"],
  list: ["items", "empty"],
  table: ["columns", "rows", "rowActions"],
  text: ["value"],
  markdown: ["value"],
  progress: ["value", "max", "label", "jobId"],
  badge: ["label", "tone"],
  button: ["label", "variant", "action", "confirm"],
  link: ["label", "to"],
  empty: ["title", "body", "action"],
  code: ["value", "language"],
};

const GAPS = ["s", "m", "l"];
const TONES = ["neutral", "good", "warn", "bad", "accent"];
const VARIANTS = ["primary", "secondary", "danger"];
const FIELD_TYPES = ["text", "textarea", "number", "url", "select", "checkbox", "file", "note", "folder", "space"];
const FIELD_KEYS = ["type", "name", "label", "hint", "value", "required", "options"];
const OPTION_KEYS = ["value", "label"];
const ITEM_KEYS = ["title", "subtitle", "icon", "link", "badge", "actions"];
const BADGE_KEYS = ["type", "label", "tone"];
const COLUMN_KEYS = ["key", "label", "numeric"];
const TARGET_KEYS = ["itemId", "url"];
const ACTION_KEYS = ["id", "payload"];

const IDENT = /^[a-z0-9][a-z0-9._-]{0,62}$/;
const OBJECT_ID = /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;

/** A validation failure. The path is a linked list of keys and indexes. */
class UIError extends Error {
  constructor(code, path, message) {
    super(message);
    this.code = code;
    this.path = path;
  }
}

const at = (parent, key) => ({ parent, key });

function segments(path) {
  const out = [];
  for (let p = path; p; p = p.parent) out.push(p.key);
  return out.reverse();
}

function jsonPointer(path) {
  return segments(path)
    .map((s) => "/" + (typeof s === "number" ? String(s) : s.replace(/~/g, "~0").replace(/\//g, "~1")))
    .join("");
}

function jsonPath(path) {
  return "$" + segments(path).map((s) => (typeof s === "number" ? `[${s}]` : `.${s}`)).join("");
}

function fail(code, path, message) {
  throw new UIError(code, path, message);
}

const hasOwn = (o, k) => Object.prototype.hasOwnProperty.call(o, k);
const isObject = (v) => v !== null && typeof v === "object" && !Array.isArray(v);

function kindName(v) {
  if (v === null) return "null";
  if (Array.isArray(v)) return "an array";
  if (typeof v === "string") return "a string";
  if (typeof v === "boolean") return "a boolean";
  if (typeof v === "number") return "a number";
  return "an object";
}

/** Orders keys by their UTF-8 bytes, which is code point order. */
function byteOrder(a, b) {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    const x = a.codePointAt(i);
    const y = b.codePointAt(i);
    if (x !== y) return x - y;
    if (x > 0xffff) i++;
  }
  return a.length - b.length;
}

const LONE_SURROGATE = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/;

function wellFormed(s) {
  return typeof s.isWellFormed === "function" ? s.isWellFormed() : !LONE_SURROGATE.test(s);
}

function isPlainObject(v) {
  const proto = Object.getPrototypeOf(v);
  return proto === Object.prototype || proto === null;
}

/**
 * Checks that a value is JSON: null, booleans, finite numbers, well-formed
 * strings, arrays and plain objects. Object members that are undefined are
 * absent, as JSON.stringify drops them. Returns an error code or "".
 */
function jsonProblem(tree) {
  if (tree === undefined) return "invalid_json";
  const stack = [tree];
  let seen = 0;
  while (stack.length) {
    const v = stack.pop();
    if (++seen > MAX_VALUES) return "too_large";
    if (v === null || typeof v === "boolean" || v === undefined) continue;
    if (typeof v === "number") {
      if (!Number.isFinite(v)) return "invalid_json";
      continue;
    }
    if (typeof v === "string") {
      if (!wellFormed(v)) return "invalid_json";
      continue;
    }
    if (Array.isArray(v)) {
      for (const el of v) stack.push(el === undefined ? null : el);
      continue;
    }
    if (typeof v === "object" && isPlainObject(v)) {
      for (const k of Object.keys(v)) {
        if (!wellFormed(k)) return "invalid_json";
        stack.push(v[k]);
      }
      continue;
    }
    return "invalid_json";
  }
  return "";
}

/** The size of the compact encoding that Go writes for the same value. */
function encodedSize(json) {
  let bytes = new TextEncoder().encode(json).length;
  // Go escapes U+2028 and U+2029 as \u2028 and \u2029 even without HTML
  // escaping. That is 6 bytes instead of 3.
  for (let i = json.indexOf("\u2028"); i >= 0; i = json.indexOf("\u2028", i + 1)) bytes += 3;
  for (let i = json.indexOf("\u2029"); i >= 0; i = json.indexOf("\u2029", i + 1)) bytes += 3;
  return bytes;
}

const isContainer = (v) => v !== null && typeof v === "object";

/** Pass 2. Visits every object in pre-order, keys in byte order. */
function checkLimits(root) {
  let count = 0;
  const stack = [{ value: root, path: null, depth: 0 }];
  while (stack.length) {
    const { value, path, depth } = stack.pop();
    if (Array.isArray(value)) {
      for (let i = value.length - 1; i >= 0; i--) {
        if (isContainer(value[i])) stack.push({ value: value[i], path: at(path, i), depth });
      }
      continue;
    }
    const d = depth + 1;
    if (d > MAX_DEPTH) fail("too_deep", path, `depth ${d} exceeds ${MAX_DEPTH}`);
    if (++count > MAX_NODES) fail("too_many_nodes", path, `the tree has more than ${MAX_NODES} nodes`);
    const keys = Object.keys(value).sort(byteOrder);
    for (let i = keys.length - 1; i >= 0; i--) {
      const child = value[keys[i]];
      if (isContainer(child)) stack.push({ value: child, path: at(path, keys[i]), depth: d });
    }
  }
}

/* Pass 3 helpers. Each reads one member of an object and checks its type. */

function object(v, path, code, what) {
  if (!isObject(v)) fail(code, path, `${what} must be an object, got ${kindName(v)}`);
  return v;
}

function unknownKeys(o, path, allowed) {
  for (const k of Object.keys(o).sort(byteOrder)) {
    if (!allowed.includes(k)) fail("unknown_field", at(path, k), `unknown field "${k}"`);
  }
}

function str(o, path, k, required) {
  if (!hasOwn(o, k)) {
    if (required) fail("missing_field", at(path, k), `missing ${k}`);
    return undefined;
  }
  if (typeof o[k] !== "string") fail("invalid_field", at(path, k), `${k} must be a string, got ${kindName(o[k])}`);
  return o[k];
}

function oneOf(o, path, k, values, required = false) {
  const s = str(o, path, k, required);
  if (s !== undefined && !values.includes(s)) fail("invalid_field", at(path, k), `${k} must be one of ${values.join(", ")}`);
  return s;
}

function bool(o, path, k) {
  if (!hasOwn(o, k)) return false;
  if (typeof o[k] !== "boolean") fail("invalid_field", at(path, k), `${k} must be a boolean`);
  return o[k];
}

function list(o, path, k, required) {
  if (!hasOwn(o, k)) {
    if (required) fail("missing_field", at(path, k), `missing ${k}`);
    return undefined;
  }
  if (!Array.isArray(o[k])) fail("invalid_field", at(path, k), `${k} must be an array`);
  return o[k];
}

function num(o, path, k, required) {
  if (!hasOwn(o, k)) {
    if (required) fail("missing_field", at(path, k), `missing ${k}`);
    return undefined;
  }
  if (typeof o[k] !== "number") fail("invalid_field", at(path, k), `${k} must be a number`);
  return o[k];
}

const isScalar = (v) => v === null || typeof v === "string" || typeof v === "number" || typeof v === "boolean";

function checkNode(v, path, only = "") {
  const o = object(v, path, "not_object", "a node");
  const type = hasOwn(o, "type") && typeof o.type === "string" ? o.type : "";
  if (!hasOwn(FIELD_ORDER, type)) fail("unknown_type", path, `unknown node type "${type}"`);
  if (only && type !== only) fail("invalid_field", path, `expected a ${only} node, got ${type}`);
  unknownKeys(o, path, ["type", ...FIELD_ORDER[type]]);
  NODE_CHECKS[type](o, path);
}

function checkNodes(o, path, k, required, only = "") {
  const arr = list(o, path, k, required);
  if (arr) arr.forEach((el, i) => checkNode(el, at(at(path, k), i), only));
}

function checkFields(o, path, k, required, names) {
  const arr = list(o, path, k, required);
  if (arr) arr.forEach((el, i) => checkField(el, at(at(path, k), i), names));
}

function checkField(v, path, names) {
  const o = object(v, path, "not_object", "a field");
  unknownKeys(o, path, FIELD_KEYS);
  const type = oneOf(o, path, "type", FIELD_TYPES, true);
  const name = str(o, path, "name", true);
  if (name === "") fail("invalid_field", at(path, "name"), "name must not be empty");
  if (names.has(name)) fail("duplicate_name", at(path, "name"), `duplicate field name "${name}"`);
  names.add(name);
  str(o, path, "label", false);
  str(o, path, "hint", false);
  if (hasOwn(o, "value") && !isScalar(o.value)) {
    fail("invalid_field", at(path, "value"), "value must be a string, number, boolean or null");
  }
  bool(o, path, "required");
  const isSelect = type === "select";
  const options = list(o, path, "options", isSelect);
  if (options && !isSelect) fail("invalid_field", at(path, "options"), "options is only allowed on select fields");
  (options || []).forEach((opt, i) => {
    const op = at(at(path, "options"), i);
    const oo = object(opt, op, "invalid_field", "an option");
    unknownKeys(oo, op, OPTION_KEYS);
    str(oo, op, "value", true);
    str(oo, op, "label", true);
  });
}

function checkAction(v, path) {
  const o = object(v, path, "invalid_field", "an action");
  unknownKeys(o, path, ACTION_KEYS);
  const id = str(o, path, "id", true);
  if (!IDENT.test(id)) fail("invalid_id", at(path, "id"), `action id "${id}" is not a valid id`);
  if (hasOwn(o, "payload") && !isObject(o.payload)) fail("invalid_field", at(path, "payload"), "payload must be an object");
}

function checkTarget(v, path) {
  const o = object(v, path, "invalid_field", "a link target");
  unknownKeys(o, path, TARGET_KEYS);
  if (hasOwn(o, "itemId") === hasOwn(o, "url")) fail("invalid_field", path, "a link target needs exactly one of itemId and url");
  if (hasOwn(o, "itemId")) {
    const id = str(o, path, "itemId", true);
    if (!OBJECT_ID.test(id)) fail("invalid_id", at(path, "itemId"), `invalid item id "${id}"`);
    return;
  }
  const url = str(o, path, "url", true);
  if (!validLinkURL(url)) fail("invalid_url", at(path, "url"), "a link url must be an absolute http or https URL");
}

function checkBadge(v, path) {
  const o = object(v, path, "invalid_field", "a badge");
  unknownKeys(o, path, BADGE_KEYS);
  if (hasOwn(o, "type") && o.type !== "badge") fail("invalid_field", at(path, "type"), "a list item badge has type badge");
  str(o, path, "label", true);
  oneOf(o, path, "tone", TONES);
}

const NODE_CHECKS = {
  stack(o, p) {
    checkNodes(o, p, "children", true);
    oneOf(o, p, "gap", GAPS);
  },
  section(o, p) {
    str(o, p, "title", true);
    str(o, p, "description", false);
    const count = num(o, p, "count", false);
    if (count !== undefined && (count < 0 || !Number.isInteger(count) || count > 2 ** 53)) {
      fail("invalid_field", at(p, "count"), "count must be an integer of at least 0");
    }
    checkNodes(o, p, "children", false);
  },
  form(o, p) {
    const id = str(o, p, "id", true);
    if (!IDENT.test(id)) fail("invalid_id", at(p, "id"), `form id "${id}" is not a valid id`);
    str(o, p, "title", false);
    str(o, p, "description", false);
    const names = new Set();
    checkFields(o, p, "fields", true, names);
    str(o, p, "submit", false);
    if (!hasOwn(o, "action")) fail("missing_field", at(p, "action"), "missing action");
    checkAction(o.action, at(p, "action"));
    checkFields(o, p, "more", false, names);
  },
  text(o, p) {
    str(o, p, "value", true);
  },
  markdown(o, p) {
    str(o, p, "value", true);
  },
  code(o, p) {
    str(o, p, "value", true);
    str(o, p, "language", false);
  },
  progress(o, p) {
    const value = num(o, p, "value", true);
    if (value < 0) fail("invalid_field", at(p, "value"), "value must be at least 0");
    const max = num(o, p, "max", false);
    if (max !== undefined && max <= 0) fail("invalid_field", at(p, "max"), "max must be above 0");
    if (max !== undefined && value > max) fail("invalid_field", at(p, "value"), `value ${value} exceeds max ${max}`);
    str(o, p, "label", false);
    const job = str(o, p, "jobId", false);
    if (job !== undefined && !OBJECT_ID.test(job)) fail("invalid_id", at(p, "jobId"), `invalid job id "${job}"`);
  },
  badge(o, p) {
    str(o, p, "label", true);
    oneOf(o, p, "tone", TONES);
  },
  button(o, p) {
    str(o, p, "label", true);
    oneOf(o, p, "variant", VARIANTS);
    if (!hasOwn(o, "action")) fail("missing_field", at(p, "action"), "missing action");
    checkAction(o.action, at(p, "action"));
    str(o, p, "confirm", false);
  },
  link(o, p) {
    str(o, p, "label", true);
    if (!hasOwn(o, "to")) fail("missing_field", at(p, "to"), "missing to");
    checkTarget(o.to, at(p, "to"));
  },
  empty(o, p) {
    str(o, p, "title", true);
    str(o, p, "body", false);
    if (hasOwn(o, "action")) checkNode(o.action, at(p, "action"), "button");
  },
  list(o, p) {
    const items = list(o, p, "items", true);
    items.forEach((raw, i) => {
      const ip = at(at(p, "items"), i);
      const item = object(raw, ip, "not_object", "a list item");
      unknownKeys(item, ip, ITEM_KEYS);
      str(item, ip, "title", true);
      str(item, ip, "subtitle", false);
      str(item, ip, "icon", false);
      if (hasOwn(item, "link")) checkTarget(item.link, at(ip, "link"));
      if (hasOwn(item, "badge")) checkBadge(item.badge, at(ip, "badge"));
      checkNodes(item, ip, "actions", false, "button");
    });
    str(o, p, "empty", false);
  },
  table(o, p) {
    const columns = list(o, p, "columns", true);
    columns.forEach((raw, i) => {
      const cp = at(at(p, "columns"), i);
      const col = object(raw, cp, "invalid_field", "a column");
      unknownKeys(col, cp, COLUMN_KEYS);
      str(col, cp, "key", true);
      str(col, cp, "label", true);
      bool(col, cp, "numeric");
    });
    const rows = list(o, p, "rows", true);
    rows.forEach((raw, i) => {
      const rp = at(at(p, "rows"), i);
      const row = object(raw, rp, "not_object", "a row");
      for (const k of Object.keys(row).sort(byteOrder)) {
        if (!isScalar(row[k])) fail("invalid_field", at(rp, k), "row values must be strings, numbers, booleans or null");
      }
    });
    checkNodes(o, p, "rowActions", false, "button");
  },
};

/* Link URLs follow ValidLinkURL, which uses Go's net/url and net/netip. */

const HOST_BYTES = new Set(Array.from("!\"$&'()*+,-.:;<=>[]_~0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", (c) => c.charCodeAt(0)));
const SPACES = new Set([0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0x85, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000]);
const isHex = (c) => /^[0-9A-Fa-f]$/.test(c || "");

function isSpace(cp) {
  return SPACES.has(cp) || (cp >= 0x2000 && cp <= 0x200a);
}

function validOptionalPort(port) {
  return port === "" || /^:[0-9]*$/.test(port);
}

/** Go's unescape for host and zone modes. Returns null for an error. */
function unescapeHost(s, mode) {
  let out = "";
  for (let i = 0; i < s.length; ) {
    const c = s.charCodeAt(i);
    if (c === 0x25) {
      if (i + 2 >= s.length || !isHex(s[i + 1]) || !isHex(s[i + 2])) return null;
      const v = parseInt(s.slice(i + 1, i + 3), 16);
      const pct25 = s.slice(i, i + 3) === "%25";
      if (mode === "host" && v < 0x80 && !pct25) return null;
      if (mode === "zone" && !pct25 && v !== 0x20 && !HOST_BYTES.has(v)) return null;
      out += String.fromCharCode(v);
      i += 3;
      continue;
    }
    if (c < 0x80 && !HOST_BYTES.has(c)) return null;
    out += s[i];
    i++;
  }
  return out;
}

/** Reports whether every percent sign starts a valid escape. */
function validEscapes(s) {
  for (let i = s.indexOf("%"); i >= 0; i = s.indexOf("%", i + 1)) {
    if (i + 2 >= s.length || !isHex(s[i + 1]) || !isHex(s[i + 2])) return false;
  }
  return true;
}

/** netip's IPv4 field parser: 4 decimal fields, no leading zeros. */
function validIPv4(s) {
  const parts = s.split(".");
  return parts.length === 4 && parts.every((p) => /^(0|[1-9][0-9]{0,2})$/.test(p) && Number(p) <= 255);
}

/** netip.ParseAddr for an address that must be IPv6, with an optional zone. */
function validIPv6Literal(addr) {
  const first = addr.search(/[.:%]/);
  if (first < 0 || addr[first] !== ":") return false;
  let s = addr;
  const pct = s.indexOf("%");
  if (pct >= 0) {
    if (pct === s.length - 1) return false;
    s = s.slice(0, pct);
  }
  let ellipsis = -1;
  let i = 0;
  if (s.startsWith("::")) {
    ellipsis = 0;
    s = s.slice(2);
    if (s === "") return true;
  }
  while (i < 16) {
    const m = /^[0-9A-Fa-f]*/.exec(s)[0];
    if (m.length === 0 || m.length > 4) return false;
    if (s[m.length] === ".") {
      if ((ellipsis < 0 && i !== 12) || i + 4 > 16) return false;
      if (!validIPv4(s)) return false;
      s = "";
      i += 4;
      break;
    }
    i += 2;
    s = s.slice(m.length);
    if (s === "") break;
    if (s[0] !== ":" || s.length === 1) return false;
    s = s.slice(1);
    if (s[0] === ":") {
      if (ellipsis >= 0) return false;
      ellipsis = i;
      s = s.slice(1);
      if (s === "") break;
    }
  }
  if (s !== "") return false;
  if (i < 16) return ellipsis >= 0;
  return ellipsis < 0;
}

/** Go's parseHost for http and https, with strict colons. Returns null for an error. */
function parseHost(host) {
  const open = host.lastIndexOf("[");
  if (open > 0) return null;
  if (open === 0) {
    const close = host.lastIndexOf("]");
    if (close < 0) return null;
    const colonPort = host.slice(close + 1);
    if (!validOptionalPort(colonPort)) return null;
    const inner = host.slice(1, close);
    const zone = inner.indexOf("%25");
    let addr;
    if (zone >= 0) {
      const a = unescapeHost(inner.slice(0, zone), "host");
      const z = unescapeHost(inner.slice(zone), "zone");
      if (a === null || z === null) return null;
      addr = a + z;
    } else {
      addr = unescapeHost(inner, "host");
      if (addr === null) return null;
    }
    if (!validIPv6Literal(addr)) return null;
    return "[" + addr + "]" + colonPort;
  }
  const colon = host.indexOf(":");
  if (colon >= 0 && !validOptionalPort(host.slice(colon))) return null;
  return unescapeHost(host, "host");
}

function hostname(host) {
  let out = host;
  const colon = out.lastIndexOf(":");
  if (colon >= 0 && validOptionalPort(out.slice(colon))) out = out.slice(0, colon);
  if (out.startsWith("[") && out.endsWith("]")) out = out.slice(1, -1);
  return out;
}

/**
 * Reports whether s is an absolute http or https URL with a host, as
 * ValidLinkURL does. Whitespace, control characters, backslashes and user
 * information are refused.
 */
function validLinkURL(s) {
  if (typeof s !== "string" || s === "") return false;
  for (const ch of s) {
    const cp = ch.codePointAt(0);
    if (cp < 0x20 || cp === 0x7f || ch === "\\" || isSpace(cp)) return false;
  }
  const hash = s.indexOf("#");
  let rest = hash >= 0 ? s.slice(0, hash) : s;
  if (hash >= 0 && !validEscapes(s.slice(hash + 1))) return false;
  const scheme = /^([A-Za-z][A-Za-z0-9+.-]*):/.exec(rest);
  if (!scheme) return false;
  const name = scheme[1].toLowerCase();
  if (name !== "http" && name !== "https") return false;
  rest = rest.slice(scheme[0].length);
  const query = rest.indexOf("?");
  if (query >= 0) rest = rest.slice(0, query);
  if (!rest.startsWith("//")) return false;
  let authority = rest.slice(2);
  let path = "";
  const slash = authority.indexOf("/");
  if (slash >= 0) {
    path = authority.slice(slash);
    authority = authority.slice(0, slash);
  }
  if (authority.includes("@")) return false;
  const host = parseHost(authority);
  if (host === null || !validEscapes(path)) return false;
  return hostname(host) !== "";
}

/**
 * Validates a tree and returns a JSON copy of it for rendering.
 * @returns {{result: {ok: true} | {ok: false, code: string, pointer: string, path: string, message: string}, value?: any}}
 */
function checkTree(tree) {
  const bad = (code, path, message) => ({ result: { ok: false, code, pointer: jsonPointer(path), path: jsonPath(path), message } });
  const problem = jsonProblem(tree);
  if (problem === "too_large") return bad("too_large", null, "the tree is larger than the input limit");
  if (problem) return bad("invalid_json", null, "the tree is not a JSON value");
  let json;
  try {
    json = JSON.stringify(tree);
  } catch {
    return bad("invalid_json", null, "the tree cannot be encoded as JSON");
  }
  const size = encodedSize(json);
  if (size > MAX_BYTES) return bad("too_large", null, `the tree is ${size} bytes, the limit is ${MAX_BYTES}`);
  const value = JSON.parse(json);
  try {
    checkLimits(value);
    checkNode(value, null);
  } catch (err) {
    if (err instanceof UIError) return bad(err.code, err.path, err.message);
    throw err;
  }
  return { result: { ok: true }, value };
}

/**
 * Validates a plugin UI tree with the rules of ValidateUI. The input is a
 * parsed JSON value.
 * @param {any} tree
 * @returns {{ok: true} | {ok: false, code: string, pointer: string, path: string, message: string}}
 */
export function validatePluginUI(tree) {
  return checkTree(tree).result;
}

/* ---------- Rendering ---------- */

const STACK_GAPS = { s: "var(--stack-gap-condensed)", l: "var(--stack-gap-spacious)" };
const LABEL_VARIANTS = { neutral: "secondary", good: "success", warn: "attention", bad: "danger", accent: "accent" };
const BUTTON_VARIANTS = { primary: "primary", secondary: "default", danger: "danger" };
const TOAST_VARIANTS = { info: "info", success: "success", warn: "warning", error: "critical" };
const TOAST_TITLES = { info: "Information", success: "Success", warning: "Warning", critical: "Error" };
const START = { "align-self": "flex-start" };
const MUTED_SMALL = { "font-size": "var(--text-body-size-small)", color: "var(--fgColor-muted)" };

const nextLevel = (level) => Math.min(6, level + 1);

function textOf(v) {
  if (v === null || v === undefined) return "";
  if (typeof v === "boolean") return v ? "Yes" : "No";
  return String(v);
}

function sectionHeading(level, title, { count, description, id } = {}) {
  return h(
    "div",
    { class: "section-heading" },
    h("h" + level, { class: "section-heading-title", id, text: title }, count !== undefined ? counterLabel(count) : null),
    description ? h("p", { class: "section-heading-description", text: description }) : null
  );
}

function itemLink(target, text) {
  if (target.itemId) return h("a", { href: "/i/" + encodeURIComponent(target.itemId), text });
  return h("a", { href: target.url, rel: "noopener noreferrer", text });
}

/**
 * Renders a plugin UI tree into container.
 * @param {Element} container
 * @param {any} tree a parsed UI tree
 *
 * A file field uploads its chosen file when the form is submitted, before
 * onAction runs (docs/plugins.md). The upload goes to POST /api/files in
 * context.spaceId, or in the space that context.spaceOf() resolves. The
 * field shows a ProgressBar while it uploads, the submit button is busy, and
 * the action receives {fileId, name, size, type} in place of the File. A
 * failed upload shows an error on the field and stops the submit. upload
 * replaces the HTTP call, for tests.
 * @param {{onAction?: (actionId: string, payload: object, values: object) => Promise<object>, context?: object, upload?: (path: string, file: File, options: {fields: object, onProgress: Function}) => Promise<object>}} [options]
 * @returns {{update: (tree: any) => void, destroy: () => void}}
 */
export function renderPluginUI(container, tree, { onAction, context = {}, upload = apiUpload } = {}) {
  const ctx = context || {};
  const root = h("div", { class: "plugin-ui", dataset: { pluginUi: "" } });
  container.appendChild(root);
  const banners = new Set();
  let cleanups = [];
  let destroyed = false;

  const addBanner = (options) => {
    const el = banner(Object.assign({ into: container }, options));
    banners.add(el);
    return el;
  };

  function showError(err, form) {
    const message = String((err && (err.message || err.error)) || "The action could not run.");
    const field = err && typeof err.field === "string" ? err.field : "";
    if (form && form.isConnected) {
      showFormErrors(form, field ? [{ field, message }] : [{ message }]);
      return;
    }
    addBanner({ variant: "critical", title: "The action failed", description: message });
  }

  /** Shows errors on a form and moves focus to the visible control. */
  function showFormErrors(form, errors) {
    const controls = form._pluginControls || new Map();
    const known = errors.filter((e) => e.field && controls.has(e.field));
    for (const e of known) {
      const box = controls.get(e.field).wrapper.closest("details");
      if (box) box.open = true;
    }
    const list = errors.map((e) => (e.field && controls.has(e.field) ? { field: e.field, message: e.message } : { message: e.message }));
    if (known.length && known.length === list.length) {
      showFieldErrors(form, list);
    } else if (known.length) {
      showFieldErrors(form, known);
      const general = list.find((e) => !e.field);
      if (general) {
        const el = banner({ variant: "critical", title: general.message });
        el.setAttribute("data-form-banner", "");
        form.insertBefore(el, form.firstChild);
      }
    } else {
      showFieldErrors(form, { message: list[0].message });
    }
    if (known.length > 0 && known.length < 3) {
      const target = controls.get(known[0].field).focus;
      if (target && document.activeElement !== target) target.focus();
    }
  }

  async function perform(action, values, { trigger = null, form = null } = {}) {
    if (typeof onAction !== "function" || destroyed) return null;
    const payload = isObject(action.payload) ? action.payload : {};
    if (trigger) setBusy(trigger, true);
    let response;
    try {
      response = await onAction(action.id, payload, values || {});
    } catch (err) {
      if (!destroyed) showError(err, form);
      return null;
    } finally {
      if (trigger) setBusy(trigger, false);
    }
    if (destroyed || !isObject(response)) return response;
    if (typeof response.error === "string") {
      showError(response, form);
    } else if (hasOwn(response, "ui")) {
      update(response.ui);
    } else if (isObject(response.toast)) {
      const variant = TOAST_VARIANTS[response.toast.kind] || "info";
      addBanner({ variant, title: TOAST_TITLES[variant], hideTitle: true, description: String(response.toast.message || ""), dismissible: variant !== "critical" });
    }
    return response;
  }

  const env = { ctx, perform, cleanups: () => cleanups, showFormErrors, upload };

  function update(next) {
    if (destroyed) return;
    const hadFocus = root.contains(document.activeElement);
    for (const fn of cleanups.splice(0)) {
      try {
        fn();
      } catch {
        // A failing unsubscribe must not stop the update.
      }
    }
    const checked = checkTree(next);
    if (!checked.result.ok) {
      const { code, pointer, message } = checked.result;
      root.replaceChildren(
        inlineMessage({ variant: "critical", text: `This plugin sent an interface that cannot be shown: ${message} (${code} at ${pointer || "/"}).` })
      );
    } else {
      const level = Math.min(6, Math.max(1, Number(ctx.headingLevel) || 2));
      root.replaceChildren(renderNode(checked.value, env, level, true));
    }
    if (hadFocus && !root.contains(document.activeElement)) {
      root.setAttribute("tabindex", "-1");
      root.focus();
    }
  }

  update(tree);

  return {
    update,
    destroy() {
      if (destroyed) return;
      destroyed = true;
      for (const fn of cleanups.splice(0)) {
        try {
          fn();
        } catch {
          // Ignored for the same reason as in update.
        }
      }
      for (const el of banners) el.remove();
      root.remove();
    },
  };
}

function renderNode(node, env, level, isRoot = false) {
  switch (node.type) {
    case "stack":
      return h(
        "div",
        { class: "stack plugin-ui-stack", style: STACK_GAPS[node.gap] ? { gap: STACK_GAPS[node.gap] } : null },
        node.children.map((child) => renderNode(child, env, level))
      );
    case "section": {
      const id = uid("plugin-section");
      const children = node.children || [];
      return h(
        "section",
        { class: "list-section plugin-ui-section", "aria-labelledby": id },
        sectionHeading(level, node.title, { count: node.count, description: node.description, id }),
        children.length ? h("div", { class: "stack" }, children.map((child) => renderNode(child, env, nextLevel(level)))) : null
      );
    }
    case "form":
      return renderForm(node, env, level);
    case "list":
      return renderList(node, env, level);
    case "table":
      return renderTable(node, env, level);
    case "text":
      return h("p", {
        class: "plugin-ui-text",
        text: node.value,
        style: { margin: "0", "font-size": "var(--text-body-size-medium)", color: "var(--fgColor-default)", "white-space": "pre-line", "overflow-wrap": "anywhere" },
      });
    case "markdown": {
      const el = h("div", { class: "md-body plugin-ui-markdown", style: { font: "var(--text-body-shorthand-medium)" } });
      // The note renderer escapes raw HTML, so its output is safe to insert.
      el.innerHTML = renderMarkdown(node.value).html;
      return el;
    }
    case "code": {
      const lang = node.language && /^[A-Za-z0-9_+-]{1,40}$/.test(node.language) ? node.language : "";
      return h(
        "div",
        { class: "md-body plugin-ui-code", role: "region", tabindex: "0", "aria-label": "Code" },
        h("pre", {}, h("code", { class: lang ? "language-" + lang : null, text: node.value }))
      );
    }
    case "progress":
      return renderProgress(node, env);
    case "badge": {
      const el = primerLabel(node.label, { variant: LABEL_VARIANTS[node.tone] || "default" });
      el.style.setProperty("align-self", "flex-start");
      return el;
    }
    case "button":
      return renderButton(node, env);
    case "link": {
      const a = itemLink(node.to, node.label);
      a.classList.add("plugin-ui-link");
      a.style.setProperty("align-self", "flex-start");
      a.style.setProperty("color", "var(--fgColor-link)");
      a.style.setProperty("text-decoration", "underline");
      return a;
    }
    case "empty": {
      const el = blankslate({ title: node.title, description: node.body || "", size: "small", border: !isRoot, headingLevel: level });
      if (node.action) {
        el.querySelector(".blankslate-inner").appendChild(h("div", { class: "blankslate-action" }, renderButton(node.action, env, { variant: "primary" })));
      }
      return el;
    }
  }
  return h("div");
}

/* Buttons and menus */

function renderButton(node, env, { variant, size = "medium" } = {}) {
  const el = button({ label: node.label, variant: variant || BUTTON_VARIANTS[node.variant] || "default", size });
  el.addEventListener("click", (event) => {
    event.preventDefault();
    if (el.getAttribute("aria-disabled") === "true" || el.getAttribute("data-busy") === "true") return;
    if (node.confirm) {
      confirmDialog({
        title: node.confirm,
        confirmLabel: node.label,
        danger: node.variant === "danger",
        returnFocus: el,
        onConfirm: () => env.perform(node.action, {}),
      });
      return;
    }
    env.perform(node.action, {}, { trigger: el });
  });
  return el;
}

/** An IconButton that opens an ActionMenu of button nodes. Danger items go last. */
function actionsMenu(name, buttons, env, values) {
  const trigger = iconButton({ icon: "kebab-horizontal", label: name, size: "small" });
  const item = (b) => ({
    label: b.label,
    danger: b.variant === "danger",
    confirm: b.confirm ? { title: b.confirm, confirmLabel: b.label } : undefined,
    onClick: () => env.perform(b.action, values),
  });
  actionMenu(trigger, () => {
    const safe = buttons.filter((b) => b.variant !== "danger").map(item);
    const danger = buttons.filter((b) => b.variant === "danger").map(item);
    return safe.length && danger.length ? [...safe, { separator: true }, ...danger] : [...safe, ...danger];
  });
  return trigger;
}

/* Lists and tables */

function renderList(node, env, level) {
  if (!node.items.length) {
    return blankslate({ title: node.empty || "No items", size: "small", border: true, headingLevel: level });
  }
  return h(
    "ul",
    { class: "list-rows plugin-ui-list", role: "list" },
    node.items.map((item) => {
      const glyph = item.icon && ICON_NAMES.includes(item.icon) ? item.icon : "";
      const title = item.link ? itemLink(item.link, item.title) : h("span", { text: item.title });
      title.style.setProperty("font-weight", "var(--base-text-weight-semibold)");
      title.style.setProperty("overflow-wrap", "anywhere");
      const badge = item.badge ? primerLabel(item.badge.label, { variant: LABEL_VARIANTS[item.badge.tone] || "default" }) : null;
      const actions = item.actions || [];
      let trailing = null;
      if (actions.length === 1) {
        trailing = renderButton(actions[0], env, { size: "small" });
      } else if (actions.length > 1) {
        trailing = actionsMenu(`Actions: ${item.title}`, actions, env, {});
      }
      return h(
        "li",
        { class: "list-row box-row" + (glyph ? "" : " no-lead") },
        glyph ? h("span", { class: "plugin-ui-item-icon", "aria-hidden": "true", style: { display: "flex", color: "var(--fgColor-muted)" } }, icon(glyph, 16)) : null,
        h(
          "div",
          { style: { display: "flex", "flex-direction": "column", gap: "var(--base-size-4)", "min-width": "0" } },
          h("div", { style: { display: "flex", "flex-wrap": "wrap", "align-items": "center", gap: "var(--base-size-8)", "font-size": "var(--text-body-size-medium)" } }, title, badge),
          item.subtitle ? h("span", { text: item.subtitle, style: MUTED_SMALL }) : null
        ),
        trailing ? h("div", { style: { display: "flex", "align-items": "center" } }, trailing) : null
      );
    })
  );
}

function renderTable(node, env, level) {
  if (!node.rows.length) {
    return blankslate({ title: "No rows", size: "small", border: true, headingLevel: level });
  }
  const rowActions = node.rowActions || [];
  const cell = (row, key) => (hasOwn(row, key) ? row[key] : null);
  return h(
    "div",
    { class: "table-scroll plugin-ui-table", role: "region", tabindex: "0", "aria-label": "Table" },
    h(
      "table",
      { class: "data-table" },
      h(
        "thead",
        {},
        h(
          "tr",
          {},
          node.columns.map((col) => h("th", { scope: "col", class: col.numeric ? "col-num" : null, text: col.label })),
          rowActions.length ? h("th", { scope: "col", class: "col-actions" }, h("span", { class: "sr-only", text: "Actions" })) : null
        )
      ),
      h(
        "tbody",
        {},
        node.rows.map((row, i) => {
          const first = node.columns.length ? textOf(cell(row, node.columns[0].key)) : "";
          return h(
            "tr",
            {},
            node.columns.map((col) => h("td", { class: col.numeric ? "col-num" : null, text: textOf(cell(row, col.key)) })),
            rowActions.length ? h("td", { class: "col-actions" }, actionsMenu(`Actions: ${first || "row " + (i + 1)}`, rowActions, env, Object.assign({}, row))) : null
          );
        })
      )
    )
  );
}

/* Progress */

function renderProgress(node, env) {
  const max = node.max !== undefined ? node.max : 100;
  const valueText = node.max !== undefined ? `${node.value} of ${node.max}` : `${node.value}%`;
  const bar = progressBar({ value: node.value, max, label: node.label || "Progress", valueText });
  const text = h("span", { class: "plugin-ui-progress-value", text: valueText, style: MUTED_SMALL });
  const el = h(
    "div",
    { class: "plugin-ui-progress", style: { display: "flex", "flex-direction": "column", gap: "var(--base-size-4)" } },
    h(
      "div",
      { style: { display: "flex", "justify-content": "space-between", gap: "var(--base-size-8)", "font-size": "var(--text-body-size-small)" } },
      h("span", { text: node.label || "", style: { "font-weight": "var(--base-text-weight-semibold)" } }),
      text
    ),
    bar
  );
  if (node.jobId && typeof env.ctx.subscribe === "function") {
    const off = env.ctx.subscribe("job.updated", (event) => {
      const data = isObject(event) && isObject(event.data) ? event.data : event;
      if (!isObject(data) || data.jobId !== node.jobId) return;
      const done = Number(data.progress);
      const total = Number(data.total);
      let share = total > 0 && Number.isFinite(done) ? done / total : null;
      if (data.status === "done") share = 1;
      if (share === null) return;
      const next = total > 0 ? `${done} of ${total}` : "Done";
      bar.set(Math.max(0, Math.min(1, share)) * max, next);
      text.textContent = next;
      if (data.status === "failed") bar.setAttribute("data-color", "danger");
      if (data.status === "done") bar.setAttribute("data-color", "success");
    });
    if (typeof off === "function") env.cleanups().push(off);
  }
  return el;
}

/* Forms */

function renderForm(node, env, level) {
  const form = h("form", { class: "stack plugin-ui-form", novalidate: true });
  const controls = new Map();
  form._pluginControls = controls;
  const id = uid("plugin-form");
  if (node.title) form.setAttribute("aria-labelledby", id);
  const fields = node.fields.map((field) => renderField(field, controls, env));
  const more = node.more && node.more.length ? details({ summary: "More options", content: h("div", { class: "stack" }, node.more.map((field) => renderField(field, controls, env))) }) : null;
  const submit = button({ label: node.submit || "Submit", variant: "primary", type: "submit" });
  form.append(
    ...[
      node.title ? sectionHeading(level, node.title, { description: node.description, id }) : node.description ? h("p", { class: "section-heading-description", text: node.description }) : null,
      ...fields,
      more,
      submit,
    ].filter(Boolean)
  );
  let busy = false;
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (busy) return;
    const values = {};
    const errors = [];
    for (const [name, control] of controls) {
      const { value, error } = control.read();
      values[name] = value;
      if (error) errors.push({ field: name, message: error });
    }
    if (errors.length) {
      env.showFormErrors(form, errors);
      return;
    }
    clearFieldErrors(form);
    busy = true;
    setBusy(submit, true);
    try {
      // Chosen files upload first. The action receives their records.
      for (const [name, control] of controls) {
        const file = control.wrapper && control.wrapper._pluginFile;
        if (!file || !values[name]) continue;
        try {
          values[name] = await file.upload(values[name], env);
        } catch (err) {
          env.showFormErrors(form, [{ field: name, message: `The file could not be uploaded. ${sentenceOf(err)}`.trim() }]);
          return;
        }
      }
      await env.perform(node.action, values, { form });
    } finally {
      busy = false;
      setBusy(submit, false);
    }
  });
  return form;
}

function requiredMarker() {
  return h("span", { class: "form-required field-required", "aria-hidden": "true", text: "*" });
}

const emptyValue = (v) => v === null || v === undefined || v === "" || v === false;

/**
 * Builds one field and registers it in controls. read() returns the value
 * with its type and the client validation message, if any.
 */
function renderField(field, controls, env) {
  const name = field.name;
  const text = field.label || name;
  const initial = field.value === undefined ? null : field.value;
  const requiredError = `${text} is required`;
  let wrapper;
  let focus;
  let read;
  const lookup = typeof env.ctx.lookup === "function" ? env.ctx.lookup : null;

  switch (field.type) {
    case "textarea":
    case "text":
    case "url":
    case "number": {
      wrapper = formControl({ label: text, name, type: field.type, value: initial === null ? "" : String(initial), hint: field.hint, required: field.required, rows: field.type === "textarea" ? 4 : undefined });
      const input = wrapper.querySelector("input, textarea");
      focus = input;
      read = () => {
        const raw = String(input.value || "");
        if (field.type === "number") {
          const bad = (input.validity && input.validity.badInput) || (raw.trim() !== "" && !Number.isFinite(Number(raw)));
          if (bad) return { value: null, error: "Enter a number" };
          const value = raw.trim() === "" ? null : Number(raw);
          return { value, error: field.required && value === null ? requiredError : "" };
        }
        if (field.type === "url" && raw.trim() !== "") {
          let ok = false;
          try {
            ok = /^https?:$/i.test(new URL(raw.trim()).protocol);
          } catch {
            ok = false;
          }
          if (!ok) return { value: raw, error: "Enter a full address, such as https://example.com" };
        }
        return { value: raw, error: field.required && raw.trim() === "" ? requiredError : "" };
      };
      break;
    }
    case "checkbox": {
      wrapper = formControl({ label: text, name, type: "checkbox", value: initial === true, hint: field.hint, required: field.required });
      const input = wrapper.querySelector("input");
      focus = input;
      read = () => ({ value: !!input.checked, error: field.required && !input.checked ? requiredError : "" });
      break;
    }
    case "select": {
      const options = field.options || [];
      const selected = initial === null ? null : String(initial);
      if (options.length && options.length <= 6) {
        ({ wrapper, focus, read } = radioGroup(field, text, selected, requiredError));
        break;
      }
      const choices = selected === null && !options.some((o) => o.value === "") ? [{ value: "", label: "Choose an option" }, ...options] : options;
      wrapper = formControl({ label: text, name, type: "select", value: selected === null ? "" : selected, options: choices, hint: field.hint, required: field.required });
      const input = wrapper.querySelector("select");
      focus = input;
      read = () => {
        const value = input.value === "" && selected === null ? null : input.value;
        return { value, error: field.required && emptyValue(value) ? requiredError : "" };
      };
      break;
    }
    case "file":
      ({ wrapper, focus, read } = fileField(field, text, requiredError));
      break;
    case "note":
    case "space":
      if (lookup) {
        ({ wrapper, focus, read } = itemField(field, text, initial, requiredError, lookup));
        break;
      }
    // Without a lookup, note and space fields take an id as text.
    // falls through
    case "folder":
      if (field.type === "folder" && lookup) {
        ({ wrapper, focus, read } = folderField(field, text, initial, requiredError, lookup));
        break;
      }
    // falls through
    default: {
      wrapper = formControl({ label: text, name, type: "text", value: initial === null ? "" : String(initial), hint: field.hint, required: field.required, autocomplete: "off" });
      const input = wrapper.querySelector("input");
      focus = input;
      read = () => {
        const value = String(input.value || "").trim() || null;
        return { value, error: field.required && value === null ? requiredError : "" };
      };
    }
  }
  wrapper.classList.add("plugin-ui-field");
  controls.set(name, { field, wrapper, focus, read });
  return wrapper;
}

function radioGroup(field, text, selected, requiredError) {
  const groupId = uid("plugin-radio");
  const radios = field.options.map((opt, i) => {
    const id = `${groupId}-${i}`;
    const input = h("input", { type: "radio", class: "radio", id, name: field.name });
    input.value = opt.value;
    input.checked = selected !== null && opt.value === selected;
    return { input, row: h("div", { class: "form-control-horizontal" }, input, h("div", { class: "form-control-horizontal-text" }, h("label", { class: "form-label", for: id, text: opt.label }))) };
  });
  const hint = field.hint ? h("p", { class: "form-caption field-hint", id: groupId + "-caption", text: field.hint }) : null;
  const wrapper = h(
    "fieldset",
    { class: "form-control field", dataset: { field: field.name }, "aria-describedby": hint ? hint.id : null },
    h("legend", { class: "form-label field-label" }, text, field.required ? requiredMarker() : null),
    hint,
    h("div", { class: "stack", style: { gap: "var(--base-size-8)" } }, radios.map((r) => r.row))
  );
  const read = () => {
    const checked = radios.find((r) => r.input.checked);
    const value = checked ? checked.input.value : null;
    return { value, error: field.required && value === null ? requiredError : "" };
  };
  return { wrapper, focus: radios[0].input, read };
}

function fieldLabel(field, text, id) {
  return h("span", { class: "form-label field-label", id }, text, field.required ? requiredMarker() : null);
}

function fileField(field, text, requiredError) {
  const id = uid("plugin-file");
  const input = h("input", { type: "file", name: field.name, id, hidden: true });
  const chosen = h("p", { class: "form-caption field-hint", id: id + "-name", text: "No file chosen" });
  const hint = field.hint ? h("p", { class: "form-caption field-hint", id: id + "-caption", text: field.hint }) : null;
  const pick = button({ label: "Choose file", leadingVisual: "upload", onClick: () => input.click() });
  pick.querySelector(".btn-label").id = id + "-button";
  pick.setAttribute("aria-labelledby", `${id}-label ${id}-button`);
  pick.setAttribute("aria-describedby", [chosen.id, hint ? hint.id : ""].filter(Boolean).join(" "));
  input.addEventListener("change", () => {
    const file = input.files && input.files[0];
    chosen.textContent = file ? file.name : "No file chosen";
  });
  const progress = h("div", { class: "plugin-ui-upload-progress", hidden: true });
  const wrapper = h("div", { class: "form-control field", dataset: { field: field.name } }, fieldLabel(field, text, id + "-label"), h("div", {}, pick), chosen, progress, hint, input);
  const read = () => {
    const file = (input.files && input.files[0]) || null;
    return { value: file, error: field.required && !file ? requiredError : "" };
  };
  // upload sends the file to POST /api/files in the context space and
  // returns the value the action receives.
  wrapper._pluginFile = {
    async upload(file, env) {
      if (!file || typeof file !== "object" || file.fileId) return file;
      let spaceId = env.ctx.spaceId || "";
      if (!spaceId && typeof env.ctx.spaceOf === "function") spaceId = (await env.ctx.spaceOf()) || "";
      if (!spaceId) throw new Error("A file needs a space. Open this page from a space.");
      const bar = progressBar({ value: 0, max: 100, size: "small", label: `Uploading ${file.name || "file"}`, valueText: "0%" });
      progress.replaceChildren(bar);
      progress.hidden = false;
      try {
        const res = await env.upload("/api/files", file, {
          fields: { spaceId, onConflict: "rename" },
          onProgress: (loaded, total) => {
            const percent = total > 0 ? Math.round((loaded / total) * 100) : 0;
            bar.set(percent, `${percent}%`);
          },
        });
        const rec = res && res.file ? res.file : res || {};
        if (!rec.id) throw new Error("The server did not return the uploaded file.");
        return { fileId: rec.id, name: rec.name || file.name || "", size: typeof file.size === "number" ? file.size : rec.size || 0, type: file.type || rec.mime || "" };
      } finally {
        progress.hidden = true;
        progress.replaceChildren();
      }
    },
  };
  return { wrapper, focus: pick, read };
}

/** Returns the message of an error as a sentence. */
function sentenceOf(err) {
  const text = String((err && (err.message || err.error)) || "").trim();
  if (!text) return "";
  const first = text.charAt(0).toUpperCase() + text.slice(1);
  return /[.!?]$/.test(first) ? first : first + ".";
}

function itemField(field, text, initial, requiredError, lookup) {
  const hidden = h("input", { type: "hidden", name: field.name });
  hidden.value = typeof initial === "string" ? initial : "";
  const wrapper = formControl({ label: text, type: "text", value: hidden.value, hint: field.hint, required: field.required, leadingVisual: field.type === "space" ? "globe" : "search" });
  const input = wrapper.querySelector("input");
  wrapper.appendChild(hidden);
  let chosenLabel = hidden.value;
  input.addEventListener("input", () => {
    if (input.value !== chosenLabel) hidden.value = "";
  });
  autocomplete(input, {
    source: (query) => Promise.resolve(lookup(field.type, query)).then((items) => (Array.isArray(items) ? items : [])),
    filter: false,
    minLength: field.type === "space" ? 0 : 1,
    emptyText: field.type === "space" ? "No spaces match" : "No notes match",
    onSelect: (item) => {
      hidden.value = String(item.id);
      chosenLabel = String(item.label);
      input.value = chosenLabel;
    },
  });
  const read = () => {
    const value = hidden.value || null;
    if (!value && input.value.trim() !== "") return { value: null, error: field.type === "space" ? "Choose a space from the list" : "Choose a note from the list" };
    return { value, error: field.required && !value ? requiredError : "" };
  };
  return { wrapper, focus: input, read };
}

function folderField(field, text, initial, requiredError, lookup) {
  const id = uid("plugin-folder");
  const hidden = h("input", { type: "hidden", name: field.name });
  hidden.value = typeof initial === "string" ? initial : "";
  const pick = button({ label: hidden.value || "Choose folder", leadingVisual: "file-directory-fill", trailingAction: "triangle-down" });
  pick.querySelector(".btn-label").id = id + "-button";
  pick.setAttribute("aria-labelledby", `${id}-label ${id}-button`);
  const hint = field.hint ? h("p", { class: "form-caption field-hint", id: id + "-caption", text: field.hint }) : null;
  if (hint) pick.setAttribute("aria-describedby", hint.id);
  pick.addEventListener("click", () => {
    let items = [];
    const panel = selectPanel(pick, {
      title: text,
      items: [],
      selected: hidden.value || null,
      filterLabel: "Filter folders",
      placeholder: "Filter folders",
      emptyTitle: "No folders",
      onSelect: (chosen) => {
        if (chosen === null || chosen === undefined) return;
        hidden.value = String(chosen);
        const item = items.find((it) => String(it.id) === hidden.value);
        pick.querySelector(".btn-label").textContent = item ? item.label : hidden.value;
      },
    });
    panel.setLoading(true, "Loading folders");
    Promise.resolve(lookup("folder", ""))
      .then((found) => {
        items = Array.isArray(found) ? found : [];
        panel.setLoading(false);
        panel.setItems(items);
      })
      .catch(() => panel.setError("Folders could not be loaded"));
  });
  const wrapper = h("div", { class: "form-control field", dataset: { field: field.name } }, fieldLabel(field, text, id + "-label"), h("div", {}, pick), hint, hidden);
  const read = () => {
    const value = hidden.value || null;
    return { value, error: field.required && !value ? requiredError : "" };
  };
  return { wrapper, focus: pick, read };
}
