# Tunnel health artwork

Provided by the project owner as one transparent 1254 × 1254 PNG sprite sheet.
Split into quadrants, trimmed, resized with preserved aspect ratio, and centered
on transparent 512 × 512 PNG canvases. Original colors are preserved.

- connected.png: green sign, penguin — healthy
- warning.png: yellow sign, corgi — degraded / recovering
- error.png: red sign, rabbit — disconnected / error
- unknown.png: gray sign, cat — stopped / unknown

These images are embedded in the Go binary and shared by tunnel cards and the
LazyCat deployment launcher icon. No image service or runtime dependency is used.
