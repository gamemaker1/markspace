#!/usr/bin/env python3
"""Render the Markspace PNG icons with the Python standard library.

The mark is a blue square with a white folded page and three text lines.
Shapes are drawn with signed distance functions, which give antialiased
edges without an imaging library. The PNG encoder uses zlib and struct.

Usage: python3 tools/icons.py [output-directory]
The default output directory is internal/web/static/icons.
"""

import math
import os
import struct
import sys
import zlib

DESIGN = 512.0
BLUE = (0xA2, 0x1C, 0xAF)  # fuchsia-700, the app accent
WHITE = (0xFF, 0xFF, 0xFF)
FOLD = (0xC9, 0xD6, 0xF5)

PAGE = [(150, 112), (290, 112), (362, 184), (362, 400), (150, 400)]
FOLD_TRIANGLE = [(290, 112), (290, 184), (362, 184)]
LINES = [(186, 236, 326, 256), (186, 284, 326, 304), (186, 332, 282, 352)]


def sd_round_rect(px, py, box, radius):
    """Signed distance from a point to a rounded rectangle, negative inside."""
    x0, y0, x1, y1 = box
    cx, cy = (x0 + x1) / 2, (y0 + y1) / 2
    qx = abs(px - cx) - ((x1 - x0) / 2 - radius)
    qy = abs(py - cy) - ((y1 - y0) / 2 - radius)
    outside = math.hypot(max(qx, 0.0), max(qy, 0.0))
    return outside + min(max(qx, qy), 0.0) - radius


def convex_distance(points):
    """Return a signed distance function for a convex polygon."""
    area = sum(ax * by - bx * ay for (ax, ay), (bx, by) in zip(points, points[1:] + points[:1]))
    edges = []
    for (ax, ay), (bx, by) in zip(points, points[1:] + points[:1]):
        ex, ey = bx - ax, by - ay
        length = math.hypot(ex, ey)
        nx, ny = (ey / length, -ex / length) if area > 0 else (-ey / length, ex / length)
        edges.append((ax, ay, nx, ny))

    def distance(px, py):
        return max((px - ax) * nx + (py - ay) * ny for ax, ay, nx, ny in edges)

    return distance


class Canvas:
    """An RGBA canvas with straight alpha, painted with source-over."""

    def __init__(self, size):
        self.size = size
        self.pixels = [[0.0, 0.0, 0.0, 0.0] for _ in range(size * size)]

    def paint(self, distance, color, bounds, scale=1.0):
        """Fill the shape whose design-space distance function is given.

        bounds is the design-space box that contains the shape. scale shrinks
        the shape around the canvas centre.
        """
        s = self.size
        k = s / DESIGN
        cx = DESIGN / 2
        x0, y0, x1, y1 = bounds
        tx = lambda v: (cx + (v - cx) * scale) * k
        px0 = max(0, int(tx(x0)) - 2)
        py0 = max(0, int(tx(y0)) - 2)
        px1 = min(s, int(math.ceil(tx(x1))) + 2)
        py1 = min(s, int(math.ceil(tx(y1))) + 2)
        r, g, b = (c / 255.0 for c in color)
        for y in range(py0, py1):
            dy = ((y + 0.5) / k - cx) / scale + cx
            row = y * s
            for x in range(px0, px1):
                dx = ((x + 0.5) / k - cx) / scale + cx
                coverage = 0.5 - distance(dx, dy) * k * scale
                if coverage <= 0:
                    continue
                a = min(1.0, coverage)
                p = self.pixels[row + x]
                out_a = a + p[3] * (1 - a)
                for i, c in enumerate((r, g, b)):
                    p[i] = (c * a + p[i] * p[3] * (1 - a)) / out_a
                p[3] = out_a

    def png(self):
        """Encode the canvas as an 8-bit RGBA PNG."""
        s = self.size
        raw = bytearray()
        for y in range(s):
            raw.append(0)
            for x in range(s):
                p = self.pixels[y * s + x]
                raw.extend(int(round(min(1.0, max(0.0, v)) * 255)) for v in p)

        def chunk(kind, data):
            body = kind + data
            return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

        header = struct.pack(">IIBBBBB", s, s, 8, 6, 0, 0, 0)
        return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header) + chunk(b"IDAT", zlib.compress(bytes(raw), 9)) + chunk(b"IEND", b"")


def render(size, rounded, glyph_scale):
    """Draw the mark on a canvas of the given pixel size."""
    canvas = Canvas(size)
    if rounded:
        canvas.paint(lambda x, y: sd_round_rect(x, y, (0, 0, 512, 512), 96), BLUE, (0, 0, 512, 512))
    else:
        canvas.paint(lambda x, y: -1000.0, BLUE, (0, 0, 512, 512))
    canvas.paint(convex_distance(PAGE), WHITE, (150, 112, 362, 400), glyph_scale)
    canvas.paint(convex_distance(FOLD_TRIANGLE), FOLD, (290, 112, 362, 184), glyph_scale)
    for box in LINES:
        canvas.paint(lambda x, y, box=box: sd_round_rect(x, y, box, 10), BLUE, box, glyph_scale)
    return canvas.png()


def check_png(data, size):
    """Decode a PNG enough to prove it is well formed and return its size."""
    assert data[:8] == b"\x89PNG\r\n\x1a\n", "bad signature"
    pos, idat, width, height = 8, b"", 0, 0
    while pos < len(data):
        (length,) = struct.unpack(">I", data[pos:pos + 4])
        kind = data[pos + 4:pos + 8]
        body = data[pos + 8:pos + 8 + length]
        (crc,) = struct.unpack(">I", data[pos + 8 + length:pos + 12 + length])
        assert zlib.crc32(kind + body) & 0xFFFFFFFF == crc, "bad CRC in " + kind.decode()
        if kind == b"IHDR":
            width, height = struct.unpack(">II", body[:8])
        elif kind == b"IDAT":
            idat += body
        pos += 12 + length
    raw = zlib.decompress(idat)
    assert (width, height) == (size, size), "wrong size"
    assert len(raw) == height * (1 + 4 * width), "wrong pixel data length"
    return width, height


def main():
    here = os.path.dirname(os.path.abspath(__file__))
    out = sys.argv[1] if len(sys.argv) > 1 else os.path.join(here, "..", "static", "icons")
    targets = [
        ("icon-192.png", 192, True, 1.0),
        ("icon-512.png", 512, True, 1.0),
        # The maskable icon fills the square and keeps the glyph well inside
        # the central 80 percent safe zone.
        ("icon-maskable-512.png", 512, False, 0.8),
        # iOS rounds the corners itself and shows transparency as black.
        ("apple-touch-icon.png", 180, False, 0.9),
    ]
    for name, size, rounded, scale in targets:
        data = render(size, rounded, scale)
        check_png(data, size)
        with open(os.path.join(out, name), "wb") as f:
            f.write(data)
        print(f"{name}: {size}x{size}, {len(data)} bytes")


if __name__ == "__main__":
    main()
