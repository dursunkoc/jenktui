# /// script
# requires-python = ">=3.10"
# dependencies = ["pillow", "fonttools"]
# ///
"""Render the ANSI frames written by TestScreenshots into PNG screenshots.

    SHOT_DIR=$PWD/docs/screenshots/frames go test -tags screenshots -run TestScreenshots .
    uv run docs/screenshots/render.py

Reads docs/screenshots/frames/*.ansi, writes docs/images/*.png. macOS fonts
(Menlo, with Apple Symbols / Arial Unicode as fallbacks for missing glyphs).
"""
import re
import sys
from pathlib import Path

from fontTools.ttLib import TTCollection, TTFont
from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parent
FRAMES = ROOT / "frames"
OUT = ROOT.parent / "images"

SCALE = 2
SIZE = 15 * SCALE
PAD_X, PAD_Y = 22 * SCALE, 16 * SCALE
BAR = 34 * SCALE  # window title bar
BG = (30, 30, 46)
FG = (205, 214, 244)
BAR_BG = (24, 24, 37)

BASIC = [
    (69, 71, 90), (243, 139, 168), (166, 227, 161), (249, 226, 175),
    (137, 180, 250), (245, 194, 231), (148, 226, 213), (186, 194, 222),
    (88, 91, 112), (243, 139, 168), (166, 227, 161), (249, 226, 175),
    (137, 180, 250), (245, 194, 231), (148, 226, 213), (166, 173, 200),
]

SGR = re.compile(r"\x1b\[([0-9;]*)m")
OTHER_ESC = re.compile(r"\x1b\[[0-9;?]*[A-Za-ln-z]|\x1b\][^\x07]*\x07")

FONT_FILES = [
    ("/System/Library/Fonts/Menlo.ttc", 0, 1),  # regular, bold
    ("/System/Library/Fonts/Apple Symbols.ttf", 0, 0),
    ("/System/Library/Fonts/Supplemental/Arial Unicode.ttf", 0, 0),
    ("/System/Library/Fonts/SFNSMono.ttf", 0, 0),
]


def load_fonts():
    fonts = []
    for path, idx, bold_idx in FONT_FILES:
        if not Path(path).exists():
            continue
        if path.endswith(".ttc"):
            cmap = set(TTCollection(path).fonts[idx].getBestCmap())
        else:
            cmap = set(TTFont(path).getBestCmap())
        regular = ImageFont.truetype(path, SIZE, index=idx)
        bold = ImageFont.truetype(path, SIZE, index=bold_idx) if bold_idx else regular
        fonts.append((cmap, regular, bold))
    if not fonts:
        sys.exit("no monospace font found (macOS fonts expected)")
    return fonts


def xterm256(n):
    if n < 16:
        return BASIC[n]
    if n >= 232:
        v = 8 + (n - 232) * 10
        return (v, v, v)
    n -= 16
    steps = [0, 95, 135, 175, 215, 255]
    return (steps[n // 36], steps[n // 6 % 6], steps[n % 6])


def parse_line(line):
    """Yield (char, fg, bg, bold, faint) per cell."""
    fg, bg, bold, faint, rev = None, None, False, False, False
    cells, pos = [], 0
    line = OTHER_ESC.sub("", line)
    for m in SGR.finditer(line):
        for ch in line[pos:m.start()]:
            cells.append((ch, fg, bg, bold, faint, rev))
        pos = m.end()
        p = [int(x) if x else 0 for x in m.group(1).split(";")] if m.group(1) else [0]
        i = 0
        while i < len(p):
            c = p[i]
            if c == 0:
                fg, bg, bold, faint, rev = None, None, False, False, False
            elif c == 1:
                bold = True
            elif c == 2:
                faint = True
            elif c == 7:
                rev = True
            elif c == 22:
                bold = faint = False
            elif c == 27:
                rev = False
            elif 30 <= c <= 37:
                fg = BASIC[c - 30]
            elif 90 <= c <= 97:
                fg = BASIC[c - 90 + 8]
            elif 40 <= c <= 47:
                bg = BASIC[c - 40]
            elif 100 <= c <= 107:
                bg = BASIC[c - 100 + 8]
            elif c == 39:
                fg = None
            elif c == 49:
                bg = None
            elif c in (38, 48) and i + 1 < len(p):
                if p[i + 1] == 2 and i + 4 < len(p):
                    col = (p[i + 2], p[i + 3], p[i + 4])
                    i += 4
                elif p[i + 1] == 5 and i + 2 < len(p):
                    col = xterm256(p[i + 2])
                    i += 2
                else:
                    col = None
                if col:
                    if c == 38:
                        fg = col
                    else:
                        bg = col
                i += 1
            i += 1
    for ch in line[pos:]:
        cells.append((ch, fg, bg, bold, faint, rev))
    return cells


def render(name, text, fonts):
    lines = text.rstrip("\n").split("\n")
    rows = [parse_line(l) for l in lines]
    cols = max(len(r) for r in rows)
    probe = fonts[0][1]
    cw = probe.getlength("M")
    ascent, descent = probe.getmetrics()
    ch = int((ascent + descent) * 1.12)
    w = int(cols * cw) + 2 * PAD_X
    h = len(rows) * ch + 2 * PAD_Y + BAR
    img = Image.new("RGB", (w, h), BG)
    d = ImageDraw.Draw(img)
    d.rectangle([0, 0, w, BAR], fill=BAR_BG)
    for i, c in enumerate([(255, 95, 86), (255, 189, 46), (39, 201, 63)]):
        cx, cy, r = PAD_X + i * 22 * SCALE // 1 + 6 * SCALE, BAR // 2, 6 * SCALE
        d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=c)
    for y, row in enumerate(rows):
        for x, (c, fg, bg, bold, faint, rev) in enumerate(row):
            f, b = fg or FG, bg or BG
            if rev:
                f, b = b, f
            if faint and not rev:
                f = tuple((a + z) // 2 for a, z in zip(f, BG))
            px, py = PAD_X + x * cw, BAR + PAD_Y + y * ch
            if b != BG:
                d.rectangle([px, py, px + cw, py + ch], fill=b)
            if c == " ":
                continue
            for cmap, regular, boldf in fonts:
                if ord(c) in cmap:
                    d.text((px, py), c, font=boldf if bold else regular, fill=f)
                    break
            else:
                print(f"{name}: no glyph for {c!r} (U+{ord(c):04X})", file=sys.stderr)
    # rounded corners
    mask = Image.new("L", (w, h), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, w - 1, h - 1], radius=14 * SCALE, fill=255)
    out = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    out.paste(img, (0, 0), mask)
    OUT.mkdir(parents=True, exist_ok=True)
    out.save(OUT / f"{name}.png", optimize=True)
    print(f"wrote {OUT / (name + '.png')} ({w}x{h})")


def main():
    fonts = load_fonts()
    frames = sorted(FRAMES.glob("*.ansi"))
    if not frames:
        sys.exit(f"no frames in {FRAMES}; run the screenshots test first")
    for f in frames:
        render(f.stem, f.read_text(), fonts)


main()
