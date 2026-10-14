package webui

import "embed"

//go:embed index.html app.css app.js mark.jpg
var Files embed.FS
