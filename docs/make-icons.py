#!/usr/bin/env python3
"""Render the zap favicon to PNG without any image library.

The mark is a rounded square with the bolt polygon from the site's SVG on it.
Point-in-polygon sampling with supersampling is enough at icon sizes, and it
means the PNGs stay in step with the SVG with no external tool.

Run: python3 docs/make-icons.py
"""

from __future__ import annotations

import struct
import zlib
from pathlib import Path

# The bolt, in the 24x24 coordinate space the site SVG uses.
BOLT = [
    (13, 2),
    (4, 14),
    (10, 14),
    (9, 22),
    (18, 10),
    (12, 10),
]

BACKDROP = (0x07, 0x09, 0x0F)
BOLT_COLOR = (0xFF, 0xD6, 0x0A)
SUPERSAMPLE = 4


def inside_bolt(x: float, y: float) -> bool:
    """Even-odd point-in-polygon test for the bolt."""
    inside = False
    count = len(BOLT)
    for i in range(count):
        x1, y1 = BOLT[i]
        x2, y2 = BOLT[(i + 1) % count]
        if (y1 > y) != (y2 > y):
            x_cross = x1 + (y - y1) * (x2 - x1) / (y2 - y1)
            if x < x_cross:
                inside = not inside
    return inside


def inside_rounded_rect(x: float, y: float, size: float, radius: float) -> bool:
    """The backdrop shape: a square with rounded corners."""
    left, top = radius, radius
    right, bottom = size - radius, size - radius
    if left <= x <= right or top <= y <= bottom:
        return 0 <= x <= size and 0 <= y <= size
    cx = left if x < left else right
    cy = top if y < top else bottom
    return (x - cx) ** 2 + (y - cy) ** 2 <= radius**2


def render(size: int) -> bytes:
    """Render one square icon and return raw RGBA rows."""
    radius = size * 7 / 32

    # Fit the bolt inside the icon with a margin, keeping its aspect ratio.
    min_x = min(p[0] for p in BOLT)
    max_x = max(p[0] for p in BOLT)
    min_y = min(p[1] for p in BOLT)
    max_y = max(p[1] for p in BOLT)
    span_x = max_x - min_x
    span_y = max_y - min_y
    margin = size * 0.19
    scale = min((size - 2 * margin) / span_x, (size - 2 * margin) / span_y)

    # Centre the scaled bolt.
    offset_x = (size - span_x * scale) / 2 - min_x * scale
    offset_y = (size - span_y * scale) / 2 - min_y * scale

    rows = bytearray()
    step = 1.0 / SUPERSAMPLE
    samples = SUPERSAMPLE * SUPERSAMPLE

    for py in range(size):
        rows.append(0)  # PNG filter type: none
        for px in range(size):
            backdrop_hits = 0
            bolt_hits = 0
            for sy in range(SUPERSAMPLE):
                for sx in range(SUPERSAMPLE):
                    fx = px + (sx + 0.5) * step
                    fy = py + (sy + 0.5) * step
                    if inside_rounded_rect(fx, fy, size, radius):
                        backdrop_hits += 1
                        bx = (fx - offset_x) / scale
                        by = (fy - offset_y) / scale
                        if inside_bolt(bx, by):
                            bolt_hits += 1

            if bolt_hits:
                coverage = bolt_hits / samples
                rows.extend(BOLT_COLOR + (round(255 * coverage),))
            elif backdrop_hits:
                coverage = backdrop_hits / samples
                rows.extend(BACKDROP + (round(255 * coverage),))
            else:
                rows.extend((0, 0, 0, 0))

    return bytes(rows)


def png(size: int) -> bytes:
    """Wrap rendered pixels in a PNG container."""

    def chunk(kind: bytes, data: bytes) -> bytes:
        return (
            struct.pack(">I", len(data))
            + kind
            + data
            + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
        )

    header = struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0)
    body = zlib.compress(render(size), 9)
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", header)
        + chunk(b"IDAT", body)
        + chunk(b"IEND", b"")
    )


def main() -> None:
    here = Path(__file__).parent
    for name, size in (("favicon-32.png", 32), ("apple-touch-icon.png", 180)):
        path = here / name
        path.write_bytes(png(size))
        print(f"wrote {path} ({size}x{size}, {path.stat().st_size} bytes)")


if __name__ == "__main__":
    main()
