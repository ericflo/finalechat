#!/usr/bin/env python3
"""Render the Finalechat app icons.

The mark is a speech bubble whose tail points down-left (an agent talking to
you) with a single bright "unread" dot, on an indigo-to-violet gradient tile.
Run from web/: python3 scripts/gen-icons.py
"""
from __future__ import annotations

import math
from pathlib import Path

from PIL import Image, ImageDraw

OUT = Path(__file__).resolve().parent.parent / "public" / "icons"
OUT.mkdir(parents=True, exist_ok=True)

TOP = (91, 82, 232)      # indigo
BOTTOM = (149, 76, 233)  # violet
DOT = (255, 184, 77)     # amber


def lerp(a, b, t):
    return tuple(int(round(a[i] + (b[i] - a[i]) * t)) for i in range(3))


def gradient(size: int) -> Image.Image:
    img = Image.new("RGB", (size, size))
    px = img.load()
    for y in range(size):
        for x in range(size):
            t = (x + y) / (2 * size)
            px[x, y] = lerp(TOP, BOTTOM, t)
    return img


def rounded_mask(size: int, radius: int) -> Image.Image:
    mask = Image.new("L", (size, size), 0)
    ImageDraw.Draw(mask).rounded_rectangle((0, 0, size - 1, size - 1), radius=radius, fill=255)
    return mask


def draw_mark(img: Image.Image, scale: float, offset: tuple[float, float] = (0, 0)) -> None:
    """Draw the bubble mark. scale is the size of the artwork box in pixels."""
    d = ImageDraw.Draw(img)
    s = scale
    ox, oy = offset
    # Bubble body.
    left, top = ox + s * 0.19, oy + s * 0.24
    right, bottom = ox + s * 0.81, oy + s * 0.68
    r = s * 0.16
    d.rounded_rectangle((left, top, right, bottom), radius=r, fill=(255, 255, 255))
    # Tail: a triangle from the bottom-left of the bubble, pointing down-left.
    tail = [
        (ox + s * 0.30, bottom - s * 0.02),
        (ox + s * 0.44, bottom - s * 0.02),
        (ox + s * 0.26, oy + s * 0.80),
    ]
    d.polygon(tail, fill=(255, 255, 255))
    # Three "message" lines inside the bubble, in the gradient colour.
    line_color = lerp(TOP, BOTTOM, 0.45)
    lw = s * 0.055
    x0 = left + s * 0.10
    for i, frac in enumerate((0.34, 0.24, 0.30)):
        y = top + s * 0.12 + i * s * 0.11
        d.rounded_rectangle((x0, y, x0 + s * frac, y + lw), radius=lw / 2, fill=line_color)
    # Unread dot at the top-right of the bubble.
    cx, cy, cr = right - s * 0.02, top + s * 0.02, s * 0.085
    d.ellipse((cx - cr - s * 0.012, cy - cr - s * 0.012, cx + cr + s * 0.012, cy + cr + s * 0.012), fill=lerp(TOP, BOTTOM, 0.2))
    d.ellipse((cx - cr, cy - cr, cx + cr, cy + cr), fill=DOT)


def render(size: int, maskable: bool, radius_frac: float) -> Image.Image:
    # Render at 4x and downsample for smooth edges.
    big = size * 4
    img = gradient(big).convert("RGBA")
    if maskable:
        # Safe zone: keep the artwork within the inner 80%.
        draw_mark(img, big * 0.8, (big * 0.1, big * 0.1))
        out = img
    else:
        draw_mark(img, big)
        alpha = rounded_mask(big, int(big * radius_frac))
        out = Image.new("RGBA", (big, big), (0, 0, 0, 0))
        out.paste(img, (0, 0), alpha)
    return out.resize((size, size), Image.LANCZOS)


def main() -> None:
    render(192, False, 0.22).save(OUT / "icon-192.png", optimize=True)
    render(512, False, 0.22).save(OUT / "icon-512.png", optimize=True)
    render(192, True, 0).save(OUT / "maskable-192.png", optimize=True)
    render(512, True, 0).save(OUT / "maskable-512.png", optimize=True)
    # iOS applies its own corner mask, so the touch icon is a full square.
    apple = render(180, True, 0)
    apple.save(OUT / "apple-touch-icon.png", optimize=True)
    # Notification badge: monochrome silhouette on transparent.
    badge = Image.new("RGBA", (96 * 4, 96 * 4), (0, 0, 0, 0))
    draw_mark(badge, 96 * 4)
    badge = badge.resize((96, 96), Image.LANCZOS)
    px = badge.load()
    for y in range(96):
        for x in range(96):
            r, g, b, a = px[x, y]
            px[x, y] = (255, 255, 255, a if (r + g + b) > 600 else 0)
    badge.save(OUT / "badge-96.png", optimize=True)
    svg = f"""<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 100 100\">
  <defs><linearGradient id=\"g\" x1=\"0\" y1=\"0\" x2=\"1\" y2=\"1\"><stop offset=\"0\" stop-color=\"rgb{TOP}\"/><stop offset=\"1\" stop-color=\"rgb{BOTTOM}\"/></linearGradient></defs>
  <rect width=\"100\" height=\"100\" rx=\"22\" fill=\"url(#g)\"/>
  <rect x=\"19\" y=\"24\" width=\"62\" height=\"44\" rx=\"16\" fill=\"#fff\"/>
  <polygon points=\"30,66 44,66 26,80\" fill=\"#fff\"/>
  <rect x=\"29\" y=\"36\" width=\"34\" height=\"5.5\" rx=\"2.75\" fill=\"rgb{lerp(TOP, BOTTOM, 0.45)}\"/>
  <rect x=\"29\" y=\"47\" width=\"24\" height=\"5.5\" rx=\"2.75\" fill=\"rgb{lerp(TOP, BOTTOM, 0.45)}\"/>
  <rect x=\"29\" y=\"58\" width=\"30\" height=\"5.5\" rx=\"2.75\" fill=\"rgb{lerp(TOP, BOTTOM, 0.45)}\"/>
  <circle cx=\"79\" cy=\"26\" r=\"9.7\" fill=\"rgb{lerp(TOP, BOTTOM, 0.2)}\"/>
  <circle cx=\"79\" cy=\"26\" r=\"8.5\" fill=\"rgb{DOT}\"/>
</svg>
"""
    (OUT / "favicon.svg").write_text(svg)
    print("wrote", sorted(p.name for p in OUT.iterdir()))


if __name__ == "__main__":
    main()
