// JSON client for the Markspace REST API.

/**
 * An error returned by the API or raised by a network failure. A network
 * failure has status 0.
 */
export class ApiError extends Error {
  /**
   * @param {number} status HTTP status, or 0 for a network failure
   * @param {string} message error text from the server body
   * @param {string} [field] the input field the error names, when any
   */
  constructor(status, message, field) {
    super(message || `request failed with status ${status}`);
    this.name = "ApiError";
    /** @type {number} */
    this.status = status;
    /** @type {string|undefined} */
    this.field = field || undefined;
  }
}

/**
 * Parses a response body as JSON. An empty body gives null and a body that is
 * not JSON gives its text.
 * @param {Response} res
 * @returns {Promise<any>}
 */
async function readBody(res) {
  if (res.status === 204) return null;
  const text = await res.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

/**
 * Builds an ApiError from a failed response body.
 * @param {number} status
 * @param {any} body
 * @returns {ApiError}
 */
function errorFrom(status, body) {
  if (body && typeof body === "object") {
    return new ApiError(status, body.error, body.field);
  }
  return new ApiError(status, typeof body === "string" && body.length < 200 ? body : "");
}

/**
 * Sends one JSON request and returns the parsed response body.
 * @param {string} method HTTP method
 * @param {string} path URL path, for example "/api/notes/abc"
 * @param {any} [body] request body, sent as JSON when not undefined
 * @param {{signal?: AbortSignal}} [options]
 * @returns {Promise<any>} the parsed body, or null for an empty response
 * @throws {ApiError} for a non-2xx status or a network failure (status 0).
 *   An aborted request rethrows the AbortError unchanged.
 */
export async function api(method, path, body, { signal } = {}) {
  const headers = { Accept: "application/json" };
  const init = { method, headers, credentials: "same-origin", signal };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(body);
  }
  let res;
  try {
    res = await fetch(path, init);
  } catch (err) {
    if (err && err.name === "AbortError") throw err;
    throw new ApiError(0, "network unavailable");
  }
  let data;
  try {
    data = await readBody(res);
  } catch (err) {
    if (err && err.name === "AbortError") throw err;
    throw new ApiError(0, "connection lost while reading the response");
  }
  if (!res.ok) throw errorFrom(res.status, data);
  return data;
}

/**
 * Uploads one file as the multipart field "file". Text fields go before the
 * file part, because the server streams the file and ignores later fields.
 * @param {string} path URL path, for example "/api/files"
 * @param {Blob|File} file the file to send
 * @param {{onProgress?: (loaded: number, total: number) => void, fields?: Object<string, string>,
 *   method?: string, filename?: string}} [options]
 *   onProgress receives the bytes sent so far and the total size. fields
 *   are text fields sent before the file. method defaults to POST. filename
 *   overrides the name of the file part.
 * @returns {Promise<any>} the parsed response body
 * @throws {ApiError} for a non-2xx status or a network failure (status 0)
 */
export async function upload(path, file, { onProgress, fields = null, method = "POST", filename = "" } = {}) {
  const form = new FormData();
  for (const [key, value] of Object.entries(fields || {})) {
    if (value !== undefined && value !== null) form.append(key, String(value));
  }
  form.append("file", file, filename || (file && file.name ? file.name : "file"));
  if (typeof XMLHttpRequest === "undefined") {
    let res;
    try {
      res = await fetch(path, { method, body: form, credentials: "same-origin" });
    } catch {
      throw new ApiError(0, "network unavailable");
    }
    const data = await readBody(res);
    if (!res.ok) throw errorFrom(res.status, data);
    if (onProgress) onProgress(file.size || 0, file.size || 0);
    return data;
  }
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open(method, path);
    xhr.withCredentials = true;
    xhr.setRequestHeader("Accept", "application/json");
    if (onProgress && xhr.upload) {
      xhr.upload.addEventListener("progress", (e) => {
        const total = e.lengthComputable ? e.total : file.size || 0;
        onProgress(Math.min(e.loaded, total || e.loaded), total);
      });
    }
    xhr.addEventListener("load", () => {
      let data = null;
      try {
        data = xhr.responseText ? JSON.parse(xhr.responseText) : null;
      } catch {
        data = xhr.responseText;
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        if (onProgress) onProgress(file.size || 0, file.size || 0);
        resolve(data);
      } else {
        reject(errorFrom(xhr.status, data));
      }
    });
    xhr.addEventListener("error", () => reject(new ApiError(0, "network unavailable")));
    xhr.addEventListener("abort", () => reject(new ApiError(0, "upload aborted")));
    xhr.send(form);
  });
}
