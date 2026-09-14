"""Fake OpenAI-compatible server for the layout audit and the flow checks.

Usage: python3 fakeai.py <port>

Standard library only. Serves:
- GET /v1/models, OpenRouter style, with prices, context length and modalities
- POST /v1/chat/completions, plain and streamed, with the usage frame when
  stream_options.include_usage is set
- POST /v1/embeddings
- POST /v1/images/generations and /v1/images/edits, a small PNG

Answers are fixed text, so every AI state renders the same way.
"""
import base64
import json
import struct
import sys
import zlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ANSWER = "The survey covers the north quay, the grain terminal and the old lighthouse [1]. Silt depth rose since spring [1]."
REWRITE = "The closing estimate of thirty tonnes fits one tide."

MODELS = [
    ("fake-default", "Fake default", ["text", "image"], ["text"], "0.000001", "0.000002"),
    ("fake-ask", "Fake ask", ["text"], ["text"], "0.0000005", "0.0000015"),
    ("fake-summarise", "Fake summarise", ["text"], ["text"], "0.0000002", "0.0000004"),
    ("fake-vision", "Fake vision", ["text", "image"], ["text"], "0.000002", "0.000004"),
    ("fake-image", "Fake image", ["text", "image"], ["image"], "0.00001", "0.00004"),
    ("fake-embed", "Fake embed", ["text"], ["embeddings"], "0.0000001", "0"),
]


def png(width=64, height=64):
    raw = b"".join(b"\x00" + bytes([200, 120, 180]) * width for _ in range(height))

    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)

    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")


def text_of(messages):
    out = []
    for m in messages or []:
        c = m.get("content")
        if isinstance(c, str):
            out.append(c)
        elif isinstance(c, list):
            out.extend(p.get("text", "") for p in c if isinstance(p, dict))
    return "\n".join(out)


def reply_for(body):
    prompt = text_of(body.get("messages"))
    last = prompt.splitlines()[-1] if prompt else ""
    if "forty tonnes" in prompt and "?" not in last:
        return REWRITE
    if "json" in prompt.lower() and "suggest" in prompt.lower():
        return "[]"
    return ANSWER


def read_json(raw):
    try:
        return json.loads(raw or b"{}")
    except ValueError:
        return {}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def send_json(self, obj, status=200):
        data = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path.split("?")[0].rstrip("/").endswith("/models"):
            data = []
            for mid, name, inputs, outputs, prompt, completion in MODELS:
                data.append({
                    "id": mid,
                    "object": "model",
                    "name": name,
                    "context_length": 32000,
                    "architecture": {"input_modalities": inputs, "output_modalities": outputs},
                    "pricing": {"prompt": prompt, "completion": completion, "image": "0", "request": "0"},
                    "top_provider": {"context_length": 32000, "max_completion_tokens": 4096},
                })
            self.send_json({"object": "list", "data": data})
            return
        self.send_json({"error": {"message": "not found", "type": "not_found"}}, 404)

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b""
        path = self.path.split("?")[0]
        if path.endswith("/chat/completions"):
            body = read_json(raw)
            text = reply_for(body)
            usage = {"prompt_tokens": 120, "completion_tokens": 30, "total_tokens": 150}
            if not body.get("stream"):
                self.send_json({"id": "fake", "object": "chat.completion", "model": body.get("model", "fake-default"), "choices": [{"index": 0, "message": {"role": "assistant", "content": text}, "finish_reason": "stop"}], "usage": usage})
                return
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.end_headers()
            for i, word in enumerate(text.split(" ")):
                piece = word if i == 0 else " " + word
                frame = {"id": "fake", "object": "chat.completion.chunk", "choices": [{"index": 0, "delta": {"content": piece}, "finish_reason": None}]}
                self.wfile.write(b"data: " + json.dumps(frame).encode() + b"\n\n")
            self.wfile.write(b"data: " + json.dumps({"id": "fake", "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]}).encode() + b"\n\n")
            if (body.get("stream_options") or {}).get("include_usage"):
                self.wfile.write(b"data: " + json.dumps({"id": "fake", "choices": [], "usage": usage}).encode() + b"\n\n")
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()
            return
        if path.endswith("/embeddings"):
            inputs = read_json(raw).get("input") or []
            if isinstance(inputs, str):
                inputs = [inputs]
            data = []
            for i, t in enumerate(inputs):
                h = zlib.crc32(str(t).encode())
                data.append({"object": "embedding", "index": i, "embedding": [((h >> (k * 3)) & 7) / 7.0 + 0.01 for k in range(8)]})
            self.send_json({"object": "list", "data": data, "usage": {"prompt_tokens": 10, "total_tokens": 10}})
            return
        if path.endswith("/images/generations") or path.endswith("/images/edits"):
            n = 1
            if "json" in (self.headers.get("Content-Type") or ""):
                try:
                    n = int(read_json(raw).get("n") or 1)
                except (TypeError, ValueError):
                    n = 1
            b64 = base64.b64encode(png()).decode()
            self.send_json({"created": 0, "data": [{"b64_json": b64} for _ in range(max(1, n))], "usage": {"input_tokens": 10, "output_tokens": 10}})
            return
        self.send_json({"error": {"message": "not found", "type": "not_found"}}, 404)


if __name__ == "__main__":
    port = int(sys.argv[-1]) if len(sys.argv) > 1 else 18241
    ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
