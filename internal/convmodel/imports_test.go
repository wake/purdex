package convmodel

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestImportBoundary keeps the conversation model reusable from other
// modules (U1-5, U4): the non-test files of convmodel import only the
// standard library, and those of convmodel/ccnorm only the standard library
// and convmodel. A path is standard library when its first element has no
// dot.
//
// It parses imports only, so a file added later is checked automatically.
func TestImportBoundary(t *testing.T) {
	const self = "github.com/wake/purdex/internal/convmodel"
	dirs := map[string][]string{
		".":      nil,
		"ccnorm": {self},
	}

	fset := token.NewFileSet()
	checked := 0
	for dir, extra := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) && dir != "." {
				continue // ccnorm arrives in U1-4b
			}
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			checked++
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}
			for _, imp := range f.Imports {
				p, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatalf("%s: unquoting %s: %v", path, imp.Path.Value, err)
				}
				first, _, _ := strings.Cut(p, "/")
				if !strings.Contains(first, ".") {
					continue // standard library
				}
				allowed := false
				for _, e := range extra {
					allowed = allowed || p == e
				}
				if !allowed {
					t.Errorf("%s imports %q: only the standard library%s may be imported", path, p, allowedSuffix(extra))
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test .go files found; import boundary check did not run")
	}
}

func allowedSuffix(extra []string) string {
	if len(extra) == 0 {
		return ""
	}
	return " and " + strings.Join(extra, ", ")
}
