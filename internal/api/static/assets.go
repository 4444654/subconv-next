package static

import "embed"

//go:embed index.html app.js account.js style.css login.html login.css login.js favicon.svg
var Assets embed.FS
