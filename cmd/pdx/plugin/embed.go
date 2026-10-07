// Package plugin embeds the Purdex Claude Code plugin (the mod and the
// pdx-team skill) into the pdx binary. The files live under ./purdex/ and are
// the same tree `claude plugin test` and `claude plugin validate` run on.
package plugin

import (
	"embed"
	"io/fs"
	"path"
	"strings"
)

// tree holds purdex/ whole. The all: prefix is what brings the dot-folder
// .claude-plugin/ in; a plain pattern skips names starting with '.'.
//
//go:embed all:purdex
var tree embed.FS

// Files is the plugin folder as an fs.FS rooted at the folder that holds
// .claude-plugin/plugin.json, without the files Claude Code itself lays into
// a plugin folder it loads in place (`claude --plugin-dir <this folder>`):
// tsconfig.json at the root and .claude-plugin/types/ (the editor's types,
// megabytes, with their own .gitignore). A binary built from a working tree
// where that happened never extracts them to a user's data dir.
func Files() fs.FS {
	sub, err := fs.Sub(tree, "purdex")
	if err != nil {
		panic("plugin: embedded tree lacks purdex/: " + err.Error())
	}
	return filtered{sub}
}

// generated reports whether a path inside the plugin folder is one Claude
// Code writes when it loads the folder, not part of the plugin.
func generated(name string) bool {
	name = path.Clean(name)
	return name == "tsconfig.json" || name == ".claude-plugin/types" || strings.HasPrefix(name, ".claude-plugin/types/")
}

// filtered hides generated paths from Open, ReadDir and so from fs.WalkDir.
type filtered struct{ fs.FS }

func (f filtered) Open(name string) (fs.File, error) {
	if generated(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	file, err := f.FS.Open(name)
	if err != nil {
		return nil, err
	}
	if d, ok := file.(fs.ReadDirFile); ok {
		return filteredDir{ReadDirFile: d, dir: name}, nil
	}
	return file, nil
}

func (f filtered) ReadDir(name string) ([]fs.DirEntry, error) {
	ents, err := fs.ReadDir(f.FS, name)
	if err != nil {
		return nil, err
	}
	out := ents[:0]
	for _, e := range ents {
		if !generated(path.Join(name, e.Name())) {
			out = append(out, e)
		}
	}
	return out, nil
}

type filteredDir struct {
	fs.ReadDirFile
	dir string
}

func (d filteredDir) ReadDir(n int) ([]fs.DirEntry, error) {
	ents, err := d.ReadDirFile.ReadDir(n)
	out := ents[:0]
	for _, e := range ents {
		if !generated(path.Join(d.dir, e.Name())) {
			out = append(out, e)
		}
	}
	return out, err
}
