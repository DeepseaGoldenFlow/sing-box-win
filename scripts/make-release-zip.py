#!/usr/bin/env python3
# Build build/native.zip: forward-slash entries, runtime files under app/, plus a Chinese
# readme and a portable launcher at the top level. Pure stdlib.
import hashlib
import os
import posixpath
import sys
import tempfile
import zipfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SRC = os.path.join(ROOT, "build", "native")
OUT = os.path.join(ROOT, "build", "native.zip")

README = "使用说明.txt"
LAUNCHER = "sing-box for Windows.cmd"

README_TEXT = "\r\n".join([
    "sing-box for Windows",
    "",
    "使用步骤：",
    "1. 解压到任意目录（建议不要放在需要管理员权限的目录）。",
    "2. 双击「sing-box for Windows.cmd」，或直接运行 app\\SingBoxWin.Native.exe。",
    "3. 数据（配置、日志、设置）保存在 %APPDATA%\\sing-box-win。",
    "",
    "提示：本 zip 内所有文件都放在 app\\ 目录下，启动器只是替你打开 app\\SingBoxWin.Native.exe。",
    "",
])

LAUNCHER_TEXT = '@echo off\r\nstart "" "%~dp0app\\SingBoxWin.Native.exe"\r\n'


def fail(message):
    print("FAIL: " + message)
    sys.exit(1)


def add_text(zf, name, text, bom=True):
    zf.writestr(name, text.encode("utf-8-sig" if bom else "utf-8"))


def build():
    if not os.path.isdir(SRC):
        fail("input directory missing: " + SRC)
    files = []
    for dirpath, _dirnames, filenames in os.walk(SRC):
        for filename in filenames:
            full = os.path.join(dirpath, filename)
            rel = os.path.relpath(full, SRC)
            files.append((full, posixpath.join("app", *rel.split(os.sep))))
    files.sort(key=lambda item: item[1])
    if not files:
        fail("no files in " + SRC)

    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    with zipfile.ZipFile(OUT, "w", zipfile.ZIP_DEFLATED) as zf:
        for full, arcname in files:
            zf.write(full, arcname)
        add_text(zf, README, README_TEXT)
        add_text(zf, LAUNCHER, LAUNCHER_TEXT, bom=False)

    verify(files)


def verify(files):
    with zipfile.ZipFile(OUT) as zf:
        names = zf.namelist()
        for name in names:
            if "\\" in name:
                fail("entry name contains a backslash: " + name)
        if "app/Assets/app.ico" not in names:
            fail("missing app/Assets/app.ico")
        if not any(n.startswith("app/Strings/") and not n.endswith("/") for n in names):
            fail("missing app/Strings/ files")
        top = sorted(n for n in names if "/" not in n)
        if top != sorted([README, LAUNCHER]):
            fail("unexpected top-level entries: " + repr(top))
        with tempfile.TemporaryDirectory() as tmp:
            zf.extractall(tmp)
            if not os.path.isdir(os.path.join(tmp, "app", "Assets")):
                fail("app/Assets is not a directory after extraction")
            if not os.path.isdir(os.path.join(tmp, "app", "Strings")):
                fail("app/Strings is not a directory after extraction")

    digest = hashlib.sha256()
    with open(OUT, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            digest.update(chunk)
    print("OK: %d app files, %d entries, %d bytes, sha256=%s" % (len(files), len(files) + 2, os.path.getsize(OUT), digest.hexdigest()))


if __name__ == "__main__":
    build()
