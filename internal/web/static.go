package web

import "embed"

// staticFS holds the page's CSS/JS, the ◐ favicon, and the self-hosted
// Montserrat faces (byte-identical to satelle-homepage's — one face across the
// product, sty_cdac294e), embedded so the binary stays self-contained (the page
// travels with it — no external asset host, no font CDN).
//
//go:embed static/app.css static/app.js static/favicon.svg static/fonts
var staticFS embed.FS
