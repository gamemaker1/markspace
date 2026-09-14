import { test } from "node:test";
import assert from "node:assert/strict";

import { newId, isId, idFromParts, encodeBase32 } from "../../internal/web/static/js/ids.js";

// The shape core.ValidID accepts, written as a Go-compatible regexp.
const GO_ID_SHAPE = /^[0-9a-hjkmnp-tv-z]{26}$/;
const ALPHABET = "0123456789abcdefghjkmnpqrstvwxyz";

/** Decodes the first 48 bits of an id back to milliseconds. */
function idTime(id) {
  let bits = "";
  for (const ch of id) bits += ALPHABET.indexOf(ch).toString(2).padStart(5, "0");
  return parseInt(bits.slice(0, 48), 2);
}

test("newId has the core.NewID shape", () => {
  for (let i = 0; i < 1000; i++) {
    const id = newId();
    assert.match(id, GO_ID_SHAPE);
    assert.equal(isId(id), true);
  }
});

test("idFromParts matches a vector produced by Go's core.NewID layout", () => {
  const random = Uint8Array.from({ length: 10 }, (_, i) => 0xf0 + i);
  assert.equal(idFromParts(1757754000123, random), "06cm4kc2zfrf3wqkyktzdxzrz4");
});

test("ids carry the millisecond timestamp and sort by time", () => {
  const zero = new Uint8Array(10);
  const high = new Uint8Array(10).fill(255);
  const times = [0, 1, 999, 2 ** 40, 1757754000123, 1757754000124, 2 ** 48 - 1];
  const ids = [];
  for (const [i, ms] of times.entries()) {
    const id = idFromParts(ms, i % 2 ? high : zero);
    assert.equal(idTime(id), ms);
    ids.push(id);
  }
  assert.deepEqual([...ids].sort(), ids);

  const before = Date.now();
  const id = newId();
  const after = Date.now();
  assert.ok(idTime(id) >= before && idTime(id) <= after);
});

test("random bits differ between ids", () => {
  const seen = new Set();
  for (let i = 0; i < 2000; i++) seen.add(newId().slice(10));
  assert.equal(seen.size, 2000);
});

test("encodeBase32 pads the last group like Go's NoPadding encoder", () => {
  assert.equal(encodeBase32(new Uint8Array([])), "");
  assert.equal(encodeBase32(new Uint8Array([0xff])), "zw");
  assert.equal(encodeBase32(new Uint8Array(16).fill(0)), "0".repeat(26));
});

test("isId rejects other shapes", () => {
  for (const bad of ["", "0".repeat(25), "0".repeat(27), "0".repeat(25) + "i", "0".repeat(25) + "l", "0".repeat(25) + "o", "0".repeat(25) + "u", "0".repeat(25) + "A", null, 42]) {
    assert.equal(isId(bad), false, String(bad));
  }
});
