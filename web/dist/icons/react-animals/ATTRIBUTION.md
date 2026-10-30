# react-animals artwork

Source: https://github.com/arvinpoddar/react-animals
Pinned npm release: react-animals@0.0.82
Original assets (all included byte-for-byte unchanged):
- panda.png: connected (green)
- koala.png: degraded / recovering (yellow)
- grizzly.png: disconnected (red)
- rabbit.png: unknown (neutral gray)

Package integrity: sha512-kxuRHOqt1Y+uA1ecuPR0fJhA7o4XXc/rz1rC+dcS1lYC+OkDC+/LrGn5+ixz5IjrImYKAFpni7kNWJ2w45OUiQ==

The npm package declares its code license as MIT; preserve UPSTREAM-package.json.
The original README explicitly attributes the artwork: "all images designed by Jefferson Cheng and property of Google. Images from Github repo by wayou."
Preserve UPSTREAM-README.md for the original artwork attribution and source links. The code license is not represented here as a separate grant from Google for the artwork.

The upstream component renders these original PNGs. The interface directly uses
those PNG alpha channels as CSS masks and applies existing semantic status colors.
There is no SVG wrapper, vector redraw, hand-drawn shape, or additional expression.
The PNGs are packaged in the Go binary together with web/dist by go:embed; the
running program does not need npm, React, a CDN, or Internet access for artwork.

Reproduce assets from the npm release, with integrity verification:

    python3 scripts/vendor-react-animal.py
