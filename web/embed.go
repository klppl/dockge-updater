package webui

import "embed"

// Files contains the complete browser interface.
//
//go:embed index.html tokens.css app.css app.js
var Files embed.FS
