# /// script
# requires-python = ">=3.10"
# dependencies = ["pillow"]
# ///
"""Draw jenktui's notification icon: a dark terminal window in the Run
Monitor's colours with a prompt and a row of build-status dots.

    uv run docs/icon/make_icon.py

Writes internal/sysint/icon.png (embedded in the binary; notify-send shows
it) and docs/icon/icon.png. Drawn with shapes only, so no fonts are needed.
"""
from pathlib import Path

from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parents[2]
OUTS = [ROOT / "internal/sysint/icon.png", ROOT / "docs/icon/icon.png"]

S = 8  # supersample, then shrink for smooth edges
N = 256
BG = (30, 30, 46)
BAR = (24, 24, 37)
PINK = (243, 139, 168)
ACCENT = (245, 194, 231)
GREEN = (166, 227, 161)
BLUE = (137, 180, 250)
YELLOW = (249, 226, 175)
MUTED = (88, 91, 112)


def px(v):
    return int(v * S)


def main():
    img = Image.new("RGBA", (px(N), px(N)), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    m = 12  # margin around the window
    d.rounded_rectangle([px(m), px(m), px(N - m), px(N - m)], radius=px(44), fill=BG)
    # title bar with the three window dots
    d.rounded_rectangle([px(m), px(m), px(N - m), px(62)], radius=px(44), fill=BAR)
    d.rectangle([px(m), px(44), px(N - m), px(62)], fill=BAR)
    for i, c in enumerate([PINK, YELLOW, GREEN]):
        cx, cy, r = 46 + i * 26, 38, 8
        d.ellipse([px(cx - r), px(cy - r), px(cx + r), px(cy + r)], fill=c)
    # prompt: a chevron and an underscore cursor
    w = px(15)
    d.line([px(44), px(92), px(82), px(122), px(44), px(152)], fill=ACCENT, width=w, joint="curve")
    for x, y in [(44, 92), (82, 122), (44, 152)]:
        d.ellipse([px(x) - w // 2, px(y) - w // 2, px(x) + w // 2, px(y) + w // 2], fill=ACCENT)
    d.rounded_rectangle([px(100), px(140), px(152), px(155)], radius=px(7), fill=ACCENT)
    # build history: success, success, failed (a cross), running
    y, r = 200, 15
    for i, kind in enumerate(["ok", "ok", "fail", "run"]):
        cx = 52 + i * 50
        if kind == "fail":
            k, lw = 12, px(9)
            d.line([px(cx - k), px(y - k), px(cx + k), px(y + k)], fill=PINK, width=lw)
            d.line([px(cx - k), px(y + k), px(cx + k), px(y - k)], fill=PINK, width=lw)
        else:
            d.ellipse([px(cx - r), px(y - r), px(cx + r), px(y + r)], fill=GREEN if kind == "ok" else BLUE)
    out = img.resize((N, N), Image.LANCZOS)
    for p in OUTS:
        out.save(p, optimize=True)
        print("wrote", p.relative_to(ROOT))


if __name__ == "__main__":
    main()
