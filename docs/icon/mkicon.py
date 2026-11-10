#!/usr/bin/env python3
"""SingBoxWin app icon: white abstract S-ribbon on a dark tile.

Replaces the earlier navy tile + white paper plane (read as Telegram).
The glyph is one continuous ribbon that sweeps top-right -> top bend ->
left side -> diagonal through the centre -> right side -> bottom bend ->
bottom-left. Width swells at the two bends and narrows at the terminals,
which is what makes it read as a folded silk strand instead of a letterform.
"""
from PIL import Image, ImageDraw, ImageFilter
import io, math, os, struct, sys

SIZES = [16, 24, 32, 48, 64, 128, 256]
SS = 4  # supersample factor

# ---- palette -------------------------------------------------------------
BG_TOP = (33, 41, 66)      # deep indigo (clearly above Windows taskbar grey)
BG_BOT = (52, 63, 96)
RIBBON_HI = (255, 255, 255)
RIBBON_LO = (226, 233, 248)

# ---- S ribbon spine (unit box, y grows downwards) ------------------------
SPINE = [
    (0.68, -0.60),
    (0.10, -0.88),
    (-0.54, -0.62),
    (-0.56, -0.18),
    (0.28, 0.04),
    (0.54, 0.44),
    (0.04, 0.88),
    (-0.64, 0.58),
]


def catmull(pts, samples=260):
    """Uniform Catmull-Rom through pts (duplicated ends)."""
    p = [pts[0]] + list(pts) + [pts[-1]]
    out = []
    for i in range(len(p) - 3):
        p0, p1, p2, p3 = p[i], p[i + 1], p[i + 2], p[i + 3]
        for j in range(samples):
            t = j / samples
            t2, t3 = t * t, t * t * t
            x = 0.5 * ((2 * p1[0]) + (-p0[0] + p2[0]) * t +
                       (2 * p0[0] - 5 * p1[0] + 4 * p2[0] - p3[0]) * t2 +
                       (-p0[0] + 3 * p1[0] - 3 * p2[0] + p3[0]) * t3)
            y = 0.5 * ((2 * p1[1]) + (-p0[1] + p2[1]) * t +
                       (2 * p0[1] - 5 * p1[1] + 4 * p2[1] - p3[1]) * t2 +
                       (-p0[1] + 3 * p1[1] - 3 * p2[1] + p3[1]) * t3)
            out.append((x, y))
    out.append(pts[-1])
    return out


def ribbon_polygon(path, half_w, w_profile):
    left, right = [], []
    n = len(path)
    for i, (x, y) in enumerate(path):
        x0, y0 = path[max(0, i - 1)]
        x1, y1 = path[min(n - 1, i + 1)]
        dx, dy = x1 - x0, y1 - y0
        ln = math.hypot(dx, dy) or 1.0
        nx, ny = -dy / ln, dx / ln
        w = half_w * w_profile(i / (n - 1))
        left.append((x + nx * w, y + ny * w))
        right.append((x - nx * w, y - ny * w))
    return left + right[::-1]


def w_profile(t):
    """Half-width multiplier: 1.0 at the two bends, ~0.55 at the terminals."""
    return 0.55 + 0.45 * (math.sin(2 * math.pi * t) ** 2) ** 0.45


def lerp(a, b, t):
    return tuple(int(round(a[i] + (b[i] - a[i]) * t)) for i in range(3))


def render(size: int) -> Image.Image:
    n = size * SS
    img = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    small = size <= 32
    # tiny frames are shown on a dark taskbar; nudge the tile lighter so the
    # squircle silhouette survives instead of reading as a floating glyph
    lift = (20, 22, 30) if small else (0, 0, 0)
    top = tuple(min(255, BG_TOP[i] + lift[i]) for i in range(3))
    bot = tuple(min(255, BG_BOT[i] + lift[i]) for i in range(3))

    # --- tile: squircle with vertical gradient -------------------------
    grad = Image.new("RGB", (1, n))
    gpx = grad.load()
    for y in range(n):
        gpx[0, y] = lerp(top, bot, y / max(1, n - 1))
    grad = grad.resize((n, n))
    mask = Image.new("L", (n, n), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, n - 1, n - 1], radius=int(n * .225), fill=255)
    img.paste(grad, (0, 0), mask)

    # soft top sheen
    sheen = Image.new("L", (n, n), 0)
    ImageDraw.Draw(sheen).rounded_rectangle(
        [0, 0, n - 1, int(n * .42)], radius=int(n * .225), fill=10)
    img = Image.alpha_composite(img, Image.merge("RGBA", (
        Image.new("L", (n, n), 255), Image.new("L", (n, n), 255),
        Image.new("L", (n, n), 255), sheen)))

    # hairline rim: keeps the squircle's silhouette on dark wallpapers /
    # dark taskbars where a near-black tile would otherwise vanish
    rim = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    ImageDraw.Draw(rim).rounded_rectangle(
        [0, 0, n - 1, n - 1], radius=int(n * .225),
        outline=(255, 255, 255, 72), width=max(1, int(round(n * .006))))
    img = Image.alpha_composite(img, rim)

    # --- ribbon ---------------------------------------------------------
    path = catmull(SPINE)
    xs = [p[0] for p in path]; ys = [p[1] for p in path]
    span = max(max(xs) - min(xs), max(ys) - min(ys))
    # glyph height as a fraction of the tile; tiny frames need a bigger,
    # thinner S with wider counters or the apertures close up into a blob
    if size <= 20:
        box, wfac, taper = 0.78, 0.200, 0.88
    elif size <= 32:
        box, wfac, taper = 0.72, 0.215, 0.86
    else:
        box, wfac, taper = 0.66, 0.235, None
    scale = n * box / span
    mx, my = (max(xs) + min(xs)) / 2, (max(ys) + min(ys)) / 2
    path = [(n / 2 + (x - mx) * scale, n / 2 + (y - my) * scale) for x, y in path]

    half_w = wfac * span * scale   # stroke thickness, in pixels

    # stamp round brushes along the spine -> no self-intersecting offset
    # polygon, so the tight bends stay clean instead of folding into seams
    glyph = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    gd = ImageDraw.Draw(glyph)
    m = len(path)
    for i, (px, py) in enumerate(path):
        t = i / (m - 1)
        # small frames go near-constant width: a strong taper turns to mush
        wmul = (taper + (1 - taper) * (math.sin(2 * math.pi * t) ** 2)) if small else w_profile(t)
        r = half_w * wmul
        gd.ellipse([px - r, py - r, px + r, py + r], fill=(255, 255, 255, 255))

    if small:
        img = Image.alpha_composite(img, glyph)
        return img.resize((size, size), Image.LANCZOS)

    # soft contact shadow so the ribbon sits on the tile
    sh_alpha = glyph.getchannel("A").point(lambda a: int(a * 0.34))
    sh_alpha = sh_alpha.filter(ImageFilter.GaussianBlur(max(1.0, n * 0.014)))
    shadow = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    shadow.paste((0, 0, 0, 255), (int(-n * 0.004), int(n * 0.012)), sh_alpha)
    img = Image.alpha_composite(img, shadow)

    # white -> pale periwinkle gradient down the strand
    tint = Image.new("RGB", (1, n))
    tpx = tint.load()
    for y in range(n):
        tpx[0, y] = lerp(RIBBON_HI, RIBBON_LO, y / max(1, n - 1))
    tint = tint.resize((n, n)).convert("RGBA")
    tint.putalpha(glyph.getchannel("A"))
    glyph = tint

    img = Image.alpha_composite(img, glyph)
    return img.resize((size, size), Image.LANCZOS)


def dib_frame(im: Image.Image) -> bytes:
    w, h = im.size
    px = im.convert("RGBA").load()
    xor = bytearray()
    for y in range(h - 1, -1, -1):
        for x in range(w):
            r, g, b, a = px[x, y]
            xor += bytes((b, g, r, a))
    row = ((w + 31) // 32) * 4
    and_mask = bytes(row * h)
    header = struct.pack("<IiiHHIIiiII", 40, w, h * 2, 1, 32, 0,
                         len(xor) + len(and_mask), 0, 0, 0, 0)
    return header + bytes(xor) + and_mask


def _png_blob(im):
    buf = io.BytesIO()
    im.save(buf, format="PNG", optimize=True)
    return buf.getvalue()


def write_ico(frames, path):
    entries, blobs = [], []
    offset = 6 + 16 * len(frames)
    for s in sorted(frames):
        im = frames[s]
        blob = dib_frame(im) if s <= 64 else _png_blob(im)
        entries.append(struct.pack("<BBBBHHII", s if s < 256 else 0,
                                   s if s < 256 else 0, 0, 0, 1, 32,
                                   len(blob), offset))
        offset += len(blob)
        blobs.append(blob)
    with open(path, "wb") as f:
        f.write(struct.pack("<HHH", 0, 1, len(frames)))
        for e in entries:
            f.write(e)
        for b in blobs:
            f.write(b)



def zoom_sheet(frames, outdir):
    tiles = []
    for s in [16, 24, 32]:
        up = frames[s].resize((s * 10, s * 10), Image.NEAREST)
        tiles.append((s, up))
    w = sum(t[1].width for t in tiles) + 40 * (len(tiles) + 1)
    sheet = Image.new("RGB", (w, 16 * 10 + 80), (238, 238, 242))
    x = 40
    for s, up in tiles:
        sheet.paste(up, (x, 40), up)
        x += up.width + 40
    sheet.save(os.path.join(outdir, "zoom-small.png"))


def write_svg(frames, path):
    """Emit an SVG carrying exactly the same ribbon geometry as the raster."""
    size, n = 512, 256
    path_pts = catmull(SPINE)
    xs = [q[0] for q in path_pts]; ys = [q[1] for q in path_pts]
    span = max(max(xs) - min(xs), max(ys) - min(ys))
    scale = n * 0.66 / span
    mx, my = (max(xs) + min(xs)) / 2, (max(ys) + min(ys)) / 2
    pts = [(n / 2 + (x - mx) * scale, n / 2 + (y - my) * scale) for x, y in path_pts]
    poly = ribbon_polygon(pts, 0.235 * span * scale, w_profile)
    poly = [(round(x * size / n, 1), round(y * size / n, 1)) for x, y in poly]
    d = "M " + " L ".join("%s %s" % (x, y) for x, y in poly) + " Z"
    svg = (
        '<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512" '
        'viewBox="0 0 512 512">\n'
        '  <title>sing-box for Windows</title>\n'
        '  <!-- white abstract S ribbon; generated by mkicon.py -->\n'
        '  <defs>\n'
        '    <linearGradient id="tile" x1="0" y1="0" x2="0" y2="1">\n'
        '      <stop offset="0" stop-color="rgb({t0})"/>\n'
        '      <stop offset="1" stop-color="rgb({t1})"/>\n'
        '    </linearGradient>\n'
        '    <linearGradient id="strand" x1="0" y1="0" x2="0" y2="1">\n'
        '      <stop offset="0" stop-color="rgb({s0})"/>\n'
        '      <stop offset="1" stop-color="rgb({s1})"/>\n'
        '    </linearGradient>\n'
        '  </defs>\n'
        '  <rect width="512" height="512" rx="115" fill="url(#tile)"/>\n'
        '  <rect x="0.75" y="0.75" width="510.5" height="510.5" rx="114.5" '
        'fill="none" stroke="#ffffff" stroke-opacity="0.28" stroke-width="1.5"/>\n'
        '  <path d="{d}" fill="url(#strand)"/>\n'
        '</svg>\n'
    ).format(t0="{} {} {}".format(*BG_TOP), t1="{} {} {}".format(*BG_BOT),
             s0="{} {} {}".format(*RIBBON_HI), s1="{} {} {}".format(*RIBBON_LO), d=d)
    with open(path, "w", encoding="utf-8") as f:
        f.write(svg)


def main(outdir):
    os.makedirs(outdir, exist_ok=True)
    frames = {s: render(s) for s in SIZES}
    ico = os.path.join(outdir, "app.ico")
    write_ico(frames, ico)
    for s in SIZES:
        frames[s].save(os.path.join(outdir, f"app-{s}.png"))
    frames[256].resize((512, 512), Image.LANCZOS).save(
        os.path.join(outdir, "preview-512.png"))
    # contact sheet: 256 + 64 + 48 + 32 + 24 + 16 on light and dark
    ladder = [256, 128, 64, 48, 32, 24, 16]
    wid = 24 + sum(s + 18 for s in ladder)
    sheet = Image.new("RGB", (wid, 620), (240, 240, 244))
    d = ImageDraw.Draw(sheet)
    d.rectangle([0, 315, wid, 620], fill=(32, 32, 36))     # Windows dark taskbar
    for row, y0 in ((0, 14), (1, 330)):
        x = 12
        for s in ladder:
            sheet.paste(frames[s], (x, y0), frames[s])
            d.text((x + 2, y0 + 268), f"{s}", fill=(120, 120, 126) if row else (90, 90, 96))
            x += s + 18
    sheet.save(os.path.join(outdir, "contact-sheet.png"))
    zoom_sheet(frames, outdir)
    write_svg(frames, os.path.join(outdir, "app.svg"))
    chk = Image.open(ico)
    print("wrote", ico, os.path.getsize(ico), "bytes; sizes:", sorted(chk.info.get("sizes", [])))


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else ".")