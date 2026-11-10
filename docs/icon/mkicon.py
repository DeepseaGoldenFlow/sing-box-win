#!/usr/bin/env python3
"""FINAL SingBoxWin app icon.

Deep-navy squircle + white tilted paper plane (send mark, 20 deg), glyph
optically centred by its alpha bounding box. Emits a 7-size .ico where the
small sizes are classic BMP/DIB frames (max compatibility with the Win32
resource compiler and the shell) and 128/256 are PNG frames.
"""
from PIL import Image, ImageDraw
import os, struct, sys

SIZES = [16, 24, 32, 48, 64, 128, 256]
SS = 4
TOP = (10, 34, 73)
BOT = (20, 72, 140)
ANGLE = 20.0

PLANE = [(2.0, 3.0), (22.5, 12.0), (2.0, 21.0), (2.0, 13.1), (15.6, 12.0), (2.0, 10.9)]


def lerp(a, b, t):
    return tuple(int(round(a[i] + (b[i] - a[i]) * t)) for i in range(3))


def render(size: int) -> Image.Image:
    n = size * SS
    img = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    grad = Image.new("RGB", (1, n))
    px = grad.load()
    for y in range(n):
        px[0, y] = lerp(TOP, BOT, y / max(1, n - 1))
    grad = grad.resize((n, n))
    mask = Image.new("L", (n, n), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, n - 1, n - 1], radius=int(n * .215), fill=255)
    img.paste(grad, (0, 0), mask)
    sheen = Image.new("L", (n, n), 0)
    ImageDraw.Draw(sheen).rounded_rectangle([0, 0, n - 1, int(n * .46)], radius=int(n * .215), fill=6)
    img = Image.alpha_composite(img, Image.merge("RGBA", (
        Image.new("L", (n, n), 255), Image.new("L", (n, n), 255),
        Image.new("L", (n, n), 255), sheen)))

    # glyph, drawn in a 24-unit box spanning 60% of the tile
    xs = [p[0] for p in PLANE]; ys = [p[1] for p in PLANE]
    span = max(max(xs) - min(xs), max(ys) - min(ys))
    scale = n * .60 / span
    mx, my = (max(xs) + min(xs)) / 2, (max(ys) + min(ys)) / 2
    pts = [(n / 2 + (p[0] - mx) * scale, n / 2 + (p[1] - my) * scale) for p in PLANE]

    glyph = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    ImageDraw.Draw(glyph).polygon(pts, fill=(255, 255, 255, 255))
    glyph = glyph.rotate(ANGLE, resample=Image.BICUBIC, center=(n / 2, n / 2))
    l, t, r, b = glyph.getchannel("A").getbbox()
    glyph = glyph.crop((l, t, r, b))
    canvas = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    canvas.paste(glyph, (int((n - glyph.width) / 2), int((n - glyph.height) / 2)), glyph)
    img = Image.alpha_composite(img, canvas)
    return img.resize((size, size), Image.LANCZOS)


def dib_frame(im: Image.Image) -> bytes:
    """32-bit BGRA bottom-up DIB + empty 1bpp AND mask, as stored inside .ico."""
    w, h = im.size
    px = im.convert("RGBA").load()
    xor = bytearray()
    for y in range(h - 1, -1, -1):
        for x in range(w):
            r, g, b, a = px[x, y]
            xor += bytes((b, g, r, a))
    row = ((w + 31) // 32) * 4
    and_mask = bytes(row * h)
    header = struct.pack("<IiiHHIIiiII", 40, w, h * 2, 1, 32, 0, len(xor) + len(and_mask), 0, 0, 0, 0)
    return header + bytes(xor) + and_mask


def write_ico(frames: dict, path: str):
    entries, blobs = [], []
    offset = 6 + 16 * len(frames)
    for s in sorted(frames):
        im = frames[s]
        blob = dib_frame(im) if s <= 64 else _png_blob(im)
        entries.append(struct.pack("<BBBBHHII", s if s < 256 else 0, s if s < 256 else 0,
                                   0, 0, 1, 32, len(blob), offset))
        offset += len(blob)
        blobs.append(blob)
    with open(path, "wb") as f:
        f.write(struct.pack("<HHH", 0, 1, len(frames)))
        for e in entries:
            f.write(e)
        for b in blobs:
            f.write(b)


def _png_blob(im):
    import io
    buf = io.BytesIO()
    im.save(buf, format="PNG", optimize=True)
    return buf.getvalue()


def main(outdir: str):
    os.makedirs(outdir, exist_ok=True)
    frames = {s: render(s) for s in SIZES}
    ico = os.path.join(outdir, "app.ico")
    write_ico(frames, ico)
    for s in SIZES:
        frames[s].save(os.path.join(outdir, f"app-{s}.png"))
    frames[256].resize((512, 512), Image.LANCZOS).save(os.path.join(outdir, "preview-512.png"))
    # sanity: reload and report
    chk = Image.open(ico)
    print("wrote", ico, os.path.getsize(ico), "bytes; sizes:", sorted(chk.info.get("sizes", [])))


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else ".")