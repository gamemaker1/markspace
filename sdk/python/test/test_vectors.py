"""Run every case in test/vectors/protocol.json."""

from __future__ import annotations

import base64
import unittest

import support  # noqa: F401 - sets import paths

from markspace import ids, ui
from markspace.errors import SignatureError, ValidationError
from markspace.responses import validate_command_response, validate_install
from markspace.signature import ReplayGuard, sign, verify

V = support.vectors()


def b64(s: str) -> bytes:
    return base64.b64decode(s)


def generate(spec: dict):
    kind, n = spec["kind"], spec["n"]
    if kind == "wide":
        return {"type": "stack", "children": [{"type": "text", "value": "x"} for _ in range(n)]}
    if kind == "wideUnknown":
        return {"type": "stack", "children": [{"type": "nope"} for _ in range(n)]}
    if kind == "buttons":
        return {"type": "stack", "children": [{"type": "button", "label": "b", "action": {"id": "a"}} for _ in range(n)]}
    if kind == "selectOptions":
        return {"type": "form", "id": "f", "action": {"id": "a"},
                "fields": [{"type": "select", "name": "s", "options": [{"value": "v", "label": "V"} for _ in range(n)]}]}
    if kind == "deepPayload":
        payload: dict = {}
        for _ in range(n - 1):
            payload = {"a": payload}
        return {"type": "button", "label": "b", "action": {"id": "a", "payload": payload}}
    if kind == "wideList":
        return {"type": "list", "items": [{"title": "x"} for _ in range(n)]}
    if kind == "wideTable":
        return {"type": "table", "columns": [{"key": "a", "label": "A"}], "rows": [{"a": 1} for _ in range(n)]}
    if kind == "deep":
        node = {"type": "stack", "children": []}
        for _ in range(n - 1):
            node = {"type": "stack", "children": [node]}
        return node
    if kind == "bigText":
        return {"type": "text", "value": "a" * n}
    if kind == "bigUnknown":
        return {"type": "nope", "value": "a" * n}
    raise AssertionError(f"unknown generator {kind}")


class SignatureVectors(unittest.TestCase):
    def test_sign(self):
        cases = V["signature"]["sign"]
        self.assertGreater(len(cases), 0)
        for c in cases:
            with self.subTest(c["name"]):
                got = sign(b64(c["secretBase64"]), c["timestamp"], c["method"], c["path"], c["deliveryId"], b64(c["bodyBase64"]))
                self.assertEqual(got, c["header"])

    def test_verify(self):
        window = V["signature"]["windowSeconds"]
        for c in V["signature"]["verify"]:
            with self.subTest(c["name"]):
                try:
                    verify(b64(c["secretBase64"]), c["header"], c["method"], c["path"], c["deliveryId"], b64(c["bodyBase64"]),
                           c["now"], window)
                    got = None
                except SignatureError as exc:
                    got = exc.code
                self.assertEqual(got, c["error"])

    def test_replay_sequences(self):
        window = V["signature"]["windowSeconds"]
        for seq in V["signature"]["replaySequences"]:
            guard = ReplayGuard(window=window)
            secret = b64(seq["secretBase64"])
            for i, r in enumerate(seq["requests"]):
                with self.subTest(f"{seq['name']} #{i} {r['note']}"):
                    try:
                        verify(secret, r["header"], r["method"], r["path"], r["deliveryId"], b64(r["bodyBase64"]), r["now"], window)
                        guard.check(r["deliveryId"], r["header"], r["now"])
                        got = None
                    except SignatureError as exc:
                        got = exc.code
                    self.assertEqual(got, r["error"])


class IDVectors(unittest.TestCase):
    def test_rules(self):
        for c in V["ids"]["cases"]:
            with self.subTest(rule=c["rule"], value=c["value"]):
                self.assertEqual(ids.RULES[c["rule"]](c["value"]), c["valid"])

    def test_every_rule_is_implemented(self):
        self.assertEqual(set(V["ids"]["rules"]), set(ids.RULES))

    def test_event_matches(self):
        for c in V["ids"]["eventMatches"]:
            with self.subTest(pattern=c["pattern"], type=c["type"]):
                self.assertEqual(ids.match_event(c["pattern"], c["type"]), c["match"])


def _outcome(fn, value):
    try:
        fn(value)
    except ValidationError as exc:
        return {"code": exc.code, "path": exc.path}
    return None


class UIVectors(unittest.TestCase):
    def test_limits(self):
        limits = V["ui"]["limits"]
        self.assertEqual((ui.MAX_NODES, ui.MAX_BYTES, ui.MAX_DEPTH),
                         (limits["maxNodes"], limits["maxBytes"], limits["maxDepth"]))

    def test_cases(self):
        for c in V["ui"]["cases"]:
            with self.subTest(c["name"]):
                tree = generate(c["generate"]) if "generate" in c else c["tree"]
                self.assertEqual(_outcome(ui.validate, tree), c["error"])

    def test_error_codes_are_listed(self):
        listed = set(V["ui"]["errorCodes"])
        for c in V["ui"]["cases"]:
            if c["error"]:
                self.assertIn(c["error"]["code"], listed)


class CommandResponseVectors(unittest.TestCase):
    def test_cases(self):
        for c in V["commandResponses"]["cases"]:
            with self.subTest(c["name"]):
                self.assertEqual(_outcome(validate_command_response, c["response"]), c["error"])


class InstallVectors(unittest.TestCase):
    def test_cases(self):
        for c in V["install"]["cases"]:
            with self.subTest(c["name"]):
                self.assertEqual(_outcome(lambda b: validate_install(b, c["manifestId"]), c["body"]), c["error"])


class BuilderOutput(unittest.TestCase):
    def test_builders_produce_valid_trees(self):
        tree = ui.stack(
            ui.section("Counts", ui.text("412 words"), ui.markdown("*x*"), ui.code("x = 1", "python"), count=2),
            ui.form("f", [ui.field("select", "mode", options=[ui.option("a", "A")]), ui.field("checkbox", "on")],
                    ui.action("go", {"x": 1}), submit="Go", more=[ui.field("number", "n", value=3)]),
            ui.list([ui.item("a", link=ui.target(item_id="n1"), badge={"label": "new", "tone": "accent"},
                             actions=[ui.button("Open", ui.action("open"))])], empty="None"),
            ui.table([ui.column("k", "K"), ui.column("v", "V", numeric=True)], [{"k": "a", "v": 1}],
                     row_actions=[ui.button("Drop", ui.action("drop"), variant="danger", confirm="Sure?")]),
            ui.progress(1, max=2, label="Half", job_id="job1"),
            ui.link("Site", url="https://example.com"),
            ui.empty("Nothing", body="Yet", action=ui.button("Start", ui.action("start"))),
            gap="m",
        )
        ui.validate(tree)


if __name__ == "__main__":
    unittest.main()
