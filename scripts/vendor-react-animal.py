#!/usr/bin/env python3
"""Vendor original PNGs from the pinned react-animals npm package.

All images remain byte-for-byte unchanged. The Go embed directive packages them
with the frontend; npm, React, and external asset requests are not used at runtime.
"""
import base64
import hashlib
import io
from pathlib import Path
import tarfile
import urllib.request

VERSION = "0.0.82"
SHA512 = "kxuRHOqt1Y+uA1ecuPR0fJhA7o4XXc/rz1rC+dcS1lYC+OkDC+/LrGn5+ixz5IjrImYKAFpni7kNWJ2w45OUiQ=="
ROOT = Path(__file__).resolve().parents[1]
DEST = ROOT / "web/dist/icons/react-animals"
ANIMALS = ("panda", "koala", "grizzly", "rabbit")
URL = f"https://registry.npmjs.org/react-animals/-/react-animals-{VERSION}.tgz"

def main():
    archive = urllib.request.urlopen(URL, timeout=30).read()
    actual = base64.b64encode(hashlib.sha512(archive).digest()).decode()
    if actual != SHA512:
        raise SystemExit("react-animals package integrity mismatch")
    DEST.mkdir(parents=True, exist_ok=True)
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as package:
        for name in ANIMALS:
            image = package.extractfile(f"package/src/animals/{name}.png").read()
            (DEST / f"{name}.png").write_bytes(image)
        for source, output in [("package/README.md", "UPSTREAM-README.md"),
                               ("package/package.json", "UPSTREAM-package.json")]:
            (DEST / output).write_bytes(package.extractfile(source).read())

if __name__ == "__main__":
    main()
