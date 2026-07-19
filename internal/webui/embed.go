package webui

import "embed"

// embedded holds the production SPA build. During dev the daemon serves from
// PDX_SPA_DIR instead (see Handler). The committed dist/index.html is a
// placeholder; production builds overwrite internal/webui/dist with spa/dist.
//
//go:embed all:dist
var embedded embed.FS
