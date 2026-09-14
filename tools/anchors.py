"""Reference implementation of comment anchors, and the generator for
test/vectors/anchors.json.

The Go package internal/anchor and the browser module static/js/anchors.js
must return exactly what this file returns for every case. docs/comments.md states
the algorithm in prose. Offsets count UTF-16 code units.

Run: python3 tools/anchors.py > test/vectors/anchors.json
"""
import json
import sys

CONTEXT = 32


def units(s):
    b = s.encode("utf-16-le", "surrogatepass")
    return [int.from_bytes(b[i:i + 2], "little") for i in range(0, len(b), 2)]


def text(us):
    return b"".join(u.to_bytes(2, "little") for u in us).decode("utf-16-le", "surrogatepass")


def is_high(u):
    return 0xD800 <= u <= 0xDBFF


def is_low(u):
    return 0xDC00 <= u <= 0xDFFF


def splits_pair(us, i):
    """Reports whether offset i falls between the halves of a surrogate pair."""
    return 0 < i < len(us) and is_low(us[i]) and is_high(us[i - 1])


def make_anchor(body, start, end):
    """Builds an anchor for the selection [start, end) of body, or None when
    the selection is empty after clamping."""
    B = units(body)
    start = max(0, min(start, len(B)))
    end = max(0, min(end, len(B)))
    if start > end:
        start, end = end, start
    if splits_pair(B, start):
        start -= 1
    if splits_pair(B, end):
        end += 1
    if start == end:
        return None
    ps = max(0, start - CONTEXT)
    if splits_pair(B, ps):
        ps += 1
    se = min(len(B), end + CONTEXT)
    if splits_pair(B, se):
        se -= 1
    return {
        "exact": text(B[start:end]),
        "prefix": text(B[ps:start]),
        "suffix": text(B[end:se]),
        "start": start,
        "end": end,
    }


def common_suffix(a, b):
    n = 0
    while n < len(a) and n < len(b) and a[len(a) - 1 - n] == b[len(b) - 1 - n]:
        n += 1
    return n


def common_prefix(a, b):
    n = 0
    while n < len(a) and n < len(b) and a[n] == b[n]:
        n += 1
    return n


def resolve(body, anchor):
    """Finds the anchor in body. Returns start, end and state.

    Every occurrence of the exact text is scored by how much of the stored
    prefix and suffix surround it, including the occurrence at the stored
    offsets. The best score wins, then the smallest distance to the stored
    start, then the earlier offset. The state is exact only when the winner
    sits at the stored offsets.
    """
    B = units(body)
    E = units(anchor.get("exact", ""))
    P = units(anchor.get("prefix", ""))
    S = units(anchor.get("suffix", ""))
    start = anchor.get("start", 0)
    end = anchor.get("end", 0)
    if not E:
        return {"start": -1, "end": -1, "state": "orphaned"}
    best = None
    for i in range(0, len(B) - len(E) + 1):
        if B[i:i + len(E)] != E:
            continue
        score = common_suffix(P, B[max(0, i - len(P)):i]) + common_prefix(S, B[i + len(E):i + len(E) + len(S)])
        key = (-score, abs(i - start), i)
        if best is None or key < best[0]:
            best = (key, i)
    if best is None:
        return {"start": -1, "end": -1, "state": "orphaned"}
    i = best[1]
    state = "exact" if i == start and end == start + len(E) else "moved"
    return {"start": i, "end": i + len(E), "state": state}


def find(body, target, nth=0):
    """Returns the UTF-16 start and end of the nth occurrence of target."""
    B, T = units(body), units(target)
    seen = -1
    for i in range(0, len(B) - len(T) + 1):
        if B[i:i + len(T)] == T:
            seen += 1
            if seen == nth:
                return i, i + len(T)
    raise ValueError(f"{target!r} occurrence {nth} not in {body!r}")


def main():
    body = "# Plan\n\nShip the **sync** engine.\n\nThen ship the sync engine docs.\n"
    s0, e0 = find(body, "sync")
    long_body = "a" * 40 + "TARGET" + "b" * 40
    lt = find(long_body, "TARGET")
    emoji_pair = "x\U0001F600y"
    hit_body = "\U0001F600" * 20 + "HIT"
    cjk = "café 你好 world"
    make_cases = [
        ("selection inside bold markup", body, s0, e0),
        ("reversed offsets are swapped", body, e0, s0),
        ("selection covering the markup", body, s0 - 2, e0 + 2),
        ("clamped past the end", "short", 2, 99),
        ("empty selection gives no anchor", body, 5, 5),
        ("context stops at 32 units", long_body, lt[0], lt[1]),
        ("start inside a surrogate pair moves back", emoji_pair, 2, 4),
        ("end inside a surrogate pair moves forward", emoji_pair, 0, 2),
        ("prefix never starts inside a pair", hit_body, *find(hit_body, "HIT")),
        ("CJK after an accented word", cjk, *find(cjk, "你好")),
    ]
    resolve_cases = []

    def add(name, body, anchor):
        resolve_cases.append({"name": name, "body": body, "anchor": anchor, "want": resolve(body, anchor)})

    a = make_anchor(body, s0, e0)
    add("unchanged text resolves exact", body, a)
    add("insertion before moves the anchor", "Intro line.\n" + body, a)
    add("offsets landing on another copy of the text pick the context match", body, dict(a, start=a["start"] + 30, end=a["end"] + 30))
    twice = "the sync step, then the sync step again"
    second = make_anchor(twice, *find(twice, "sync", 1))
    add("repeated text uses prefix and suffix", twice, dict(second, start=0, end=4))
    add("repeated text with no context uses distance to the hint", "sync sync sync", {"exact": "sync", "start": 6, "end": 10})
    add("tie on distance picks the earlier occurrence", "ab ab", {"exact": "ab", "start": 1, "end": 3})
    add("deleted text is orphaned", body.replace("**sync** ", "").replace("the sync engine", "the engine"), a)
    add("empty exact is orphaned", body, {"exact": "", "start": 0, "end": 0})
    add("out of range offsets still search", "tiny sync", {"exact": "sync", "start": 500, "end": 504})
    add("overlapping occurrences prefer full context over the stored offsets", "aaaa", {"exact": "aa", "prefix": "a", "suffix": "a", "start": 0, "end": 2})
    add("end not matching the exact length is not exact", body, dict(a, end=a["end"] + 1))
    emoji = "x\U0001F600 sync \U0001F600 sync"
    em = make_anchor(emoji, *find(emoji, "sync", 1))
    add("offsets count UTF-16 units around emoji", emoji, em)
    add("emoji inserted before shifts by two units", "\U0001F600" + emoji, em)
    add("case differs so the text is orphaned", body.replace("sync", "Sync"), a)

    out = {
        "context": CONTEXT,
        "make": [{"name": n, "body": b, "start": s, "end": e, "want": make_anchor(b, s, e)} for n, b, s, e in make_cases],
        "resolve": resolve_cases,
    }
    json.dump(out, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
