// Session lookup with an offline fallback, and sign-out.

import { api } from "./api.js";
import { deleteStore } from "./store.js";

const USER_KEY = "markspace.user";
const ME_TIMEOUT_MS = 10000;

/**
 * Reads the cached user from localStorage.
 * @returns {object|null}
 */
function cachedUser() {
  try {
    const raw = globalThis.localStorage && globalThis.localStorage.getItem(USER_KEY);
    return raw ? JSON.parse(raw) : null;
  } catch {
    return null;
  }
}

/**
 * Writes or clears the cached user.
 * @param {object|null} user
 */
function cacheUser(user) {
  try {
    if (!globalThis.localStorage) return;
    if (user) globalThis.localStorage.setItem(USER_KEY, JSON.stringify(user));
    else globalThis.localStorage.removeItem(USER_KEY);
  } catch {
    // Storage can be unavailable in private modes. The session still works.
  }
}

/**
 * Loads the signed-in user from GET /api/auth/me and caches it in
 * localStorage under "markspace.user". Without a connection, or when the
 * server does not answer, it returns the cached user with offline true.
 * @returns {Promise<{user: object|null, offline: boolean}>}
 */
export async function getSession() {
  if (globalThis.navigator && globalThis.navigator.onLine === false) {
    return { user: cachedUser(), offline: true };
  }
  const controller = typeof AbortController !== "undefined" ? new AbortController() : null;
  const timer = controller ? setTimeout(() => controller.abort(), ME_TIMEOUT_MS) : null;
  try {
    const me = await api("GET", "/api/auth/me", undefined, { signal: controller ? controller.signal : undefined });
    const user = (me && me.user) || null;
    cacheUser(user);
    return { user, offline: false };
  } catch (err) {
    if (err && err.status === 401) {
      cacheUser(null);
      return { user: null, offline: false };
    }
    // A network failure, a timeout or a server error: work from the cache.
    return { user: cachedUser(), offline: true };
  } finally {
    if (timer) clearTimeout(timer);
  }
}

/**
 * Signs out: deletes the cached user's IndexedDB database and cached
 * attachments, clears the cached user, then posts the logout form, which
 * navigates to /login. The caller confirms first when the outbox is not empty.
 * @returns {Promise<void>}
 */
export async function signOut() {
  const user = cachedUser();
  if (user && user.id) {
    try {
      await deleteStore(user.id);
    } catch {
      // The database may be missing or blocked. Sign-out continues.
    }
  }
  try {
    if (globalThis.caches) {
      for (const name of await globalThis.caches.keys()) {
        if (name.startsWith("markspace-files")) await globalThis.caches.delete(name);
      }
    }
  } catch {
    // Cache Storage can be unavailable. Sign-out continues.
  }
  cacheUser(null);
  const form = document.createElement("form");
  form.method = "post";
  form.action = "/logout";
  form.style.display = "none";
  document.body.appendChild(form);
  form.submit();
}
