// Client-side note identifiers in the core.NewID format.
//
// An id is 16 bytes encoded as 26 characters of lowercase base32 with the
// alphabet below and no padding. Bytes 0 to 5 hold the millisecond timestamp,
// big-endian, and bytes 6 to 15 hold 80 random bits.

const ALPHABET = "0123456789abcdefghjkmnpqrstvwxyz";
const ID_PATTERN = /^[0-9a-hjkmnp-tv-z]{26}$/;

/**
 * Encodes bytes as unpadded base32 with the core.NewID alphabet, most
 * significant bit first, the same way Go's encoding/base32 does.
 * @param {Uint8Array} bytes
 * @returns {string}
 */
export function encodeBase32(bytes) {
  let out = "";
  let buffer = 0;
  let bits = 0;
  for (const byte of bytes) {
    buffer = (buffer << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      out += ALPHABET[(buffer >> bits) & 31];
    }
    buffer &= (1 << bits) - 1;
  }
  if (bits > 0) {
    out += ALPHABET[(buffer << (5 - bits)) & 31];
  }
  return out;
}

/**
 * Builds an id from a millisecond timestamp and 10 random bytes.
 * @param {number} ms milliseconds since the Unix epoch
 * @param {Uint8Array} random at least 10 bytes; the first 10 are used
 * @returns {string}
 */
export function idFromParts(ms, random) {
  const buf = new Uint8Array(16);
  let t = Math.max(0, Math.floor(ms));
  for (let i = 5; i >= 0; i--) {
    buf[i] = t % 256;
    t = Math.floor(t / 256);
  }
  buf.set(random.subarray(0, 10), 6);
  return encodeBase32(buf);
}

/**
 * Returns a new 26-character id: a 48-bit millisecond timestamp followed by
 * 80 bits from crypto.getRandomValues, so ids sort by creation time.
 * @returns {string}
 */
export function newId() {
  const random = new Uint8Array(10);
  globalThis.crypto.getRandomValues(random);
  return idFromParts(Date.now(), random);
}

/**
 * Reports whether s has the shape core.ValidID accepts.
 * @param {string} s
 * @returns {boolean}
 */
export function isId(s) {
  return typeof s === "string" && ID_PATTERN.test(s);
}
