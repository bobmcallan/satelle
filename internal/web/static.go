package web

import "embed"

// staticFS holds the page's CSS/JS, embedded so the binary stays self-contained
// (the page travels with it — no external asset host).
//
//go:embed static/app.css static/app.js
var staticFS embed.FS
