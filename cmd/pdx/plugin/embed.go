// Package plugin embeds the Purdex Claude Code plugin (the mod and the
// pdx-team skill) into the pdx binary. The files live under ./purdex/ and are
// the same tree `claude plugin test` and `claude plugin validate` run on.
package plugin

import (
	"embed"
	"io/fs"
)

// tree holds purdex/ whole. The all: prefix is what brings the dot-folder
// .claude-plugin/ in; a plain pattern skips names starting with '.'.
//
//go:embed all:purdex
var tree embed.FS

// Files is the plugin folder as an fs.FS rooted at the folder that holds
// .claude-plugin/plugin.json.
func Files() fs.FS {
	sub, err := fs.Sub(tree, "purdex")
	if err != nil {
		panic("plugin: embedded tree lacks purdex/: " + err.Error())
	}
	return sub
}
