package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The App-only compaction route has no identity check, so an agent must not get a tool for it: no pdx source and no
// skill names it (a lead uses the member-compaction route). Mutation gate: name the route in a source file → red.
func TestPdx_NoCallerOfTheAppCompactionRoute(t *testing.T) {
	const route = "/api/team/compaction/"
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		slash := filepath.ToSlash(path)
		isSrc := strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
		if !isSrc && !strings.HasPrefix(slash, "plugin/purdex/skills/") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), route) {
			t.Errorf("%s names %s: no pdx code or skill may call the App's compaction route", path, route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
