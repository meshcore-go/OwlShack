#!/usr/bin/env python3
"""Regenerate the PWA / favicon icons in public/ from the app's theme tokens.

One-time dev utility (NOT part of build.sh). Run after changing the brand mark
or the --primary / --background oklch tokens in src/index.css:

    python3 web/frontend/scripts/gen-icons.py   # needs Pillow

Draws the lucide 'Radio' mark (phosphor green on warm-charcoal) at 4x
supersample for crisp edges. Colors are computed from oklch so they match the
in-app logo exactly.
"""
import math
import os
from PIL import Image, ImageDraw

OUT = os.path.normpath(os.path.join(os.path.dirname(__file__), "..", "public"))


def oklch_to_rgb(L, C, H):
    h = math.radians(H)
    a, b = C * math.cos(h), C * math.sin(h)
    l_ = L + 0.3963377774 * a + 0.2158037573 * b
    m_ = L - 0.1055613458 * a - 0.0638541728 * b
    s_ = L - 0.0894841775 * a - 1.2914855480 * b
    l, m, s = l_**3, m_**3, s_**3
    r = 4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s
    g = -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s
    bl = -0.0041960863 * l - 0.7034186147 * m + 1.7076147010 * s

    def gamma(c):
        c = max(0.0, min(1.0, c))
        return 12.92 * c if c <= 0.0031308 else 1.055 * (c ** (1 / 2.4)) - 0.055

    return tuple(round(gamma(x) * 255) for x in (r, g, bl))


PRIMARY = oklch_to_rgb(0.78, 0.18, 155)  # --primary (dark theme)
BG = oklch_to_rgb(0.15, 0.005, 240)      # --background (dark theme)
SS = 4                                   # supersample factor


def draw_radio(size, *, maskable):
    S = size * SS
    img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    if maskable:
        d.rectangle([0, 0, S, S], fill=BG + (255,))
        glyph = 0.46  # smaller, inside the maskable safe zone
    else:
        radius = int(S * 0.18)
        d.rounded_rectangle([0, 0, S - 1, S - 1], radius=radius, fill=BG + (255,))
        bw = max(2, int(S * 0.015))
        inset = int(S * 0.06)
        d.rounded_rectangle(
            [inset, inset, S - 1 - inset, S - 1 - inset],
            radius=int(radius * 0.8), outline=PRIMARY + (70,), width=bw,
        )
        glyph = 0.56

    cx = cy = S / 2
    unit = (S * glyph) / 24.0  # lucide Radio uses a 24-unit viewBox
    stroke = max(2, int(2.4 * unit))
    col = PRIMARY + (255,)

    r_dot = 2.05 * unit
    d.ellipse([cx - r_dot, cy - r_dot, cx + r_dot, cy + r_dot], fill=col)

    def arc(radius, start, end):
        bb = [cx - radius, cy - radius, cx + radius, cy + radius]
        d.arc(bb, start, end, fill=col, width=stroke)
        for ang in (start, end):  # fake round caps
            a = math.radians(ang)
            ex, ey = cx + radius * math.cos(a), cy + radius * math.sin(a)
            cap = stroke / 2
            d.ellipse([ex - cap, ey - cap, ex + cap, ey + cap], fill=col)

    for radius in (5.4 * unit, 9.4 * unit):
        arc(radius, -52, 52)    # right-facing waves
        arc(radius, 128, 232)   # left-facing waves

    return img.resize((size, size), Image.LANCZOS)


def save(img, name):
    path = os.path.join(OUT, name)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    img.save(path)
    print("wrote", os.path.relpath(path), img.size)


if __name__ == "__main__":
    print("primary #%02x%02x%02x  bg #%02x%02x%02x" % (PRIMARY + BG))
    save(draw_radio(192, maskable=False), "icons/icon-192.png")
    save(draw_radio(512, maskable=False), "icons/icon-512.png")
    save(draw_radio(512, maskable=True), "icons/maskable-512.png")
    save(draw_radio(180, maskable=False), "apple-touch-icon.png")
    save(draw_radio(32, maskable=False), "favicon-32.png")
