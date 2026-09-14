package nex

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestImportBoundary enforces spec invariant I6: package nex is allowed to
// import the Nexen module's root assembly package
// (lab.protype.tw/wake/nexen) and its /api, /config and /sandbox
// subpackages — the embedding seam Nexen publishes — and nothing else
// under lab.protype.tw/wake/nexen/. Reaching into any other Nexen
// subpackage would mean this module bypasses Assemble and starts coupling
// to Nexen's internals directly.
//
// It parses every non-test .go file in this package with go/parser in
// ImportsOnly mode (cheap: it does not need a full parse or type-check)
// and checks each import's literal path, so a future file added to this
// package is checked automatically without updating this test.
func TestImportBoundary(t *testing.T) {
	const modulePath = "lab.protype.tw/wake/nexen"
	allowed := map[string]bool{
		modulePath:              true,
		modulePath + "/api":     true,
		modulePath + "/config":  true,
		modulePath + "/sandbox": true,
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}

	fset := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++

		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}

		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquoting import %s: %v", name, imp.Path.Value, err)
			}
			if path != modulePath && !strings.HasPrefix(path, modulePath+"/") {
				continue
			}
			if !allowed[path] {
				t.Errorf("%s imports %q: only %s and its /api, /config, /sandbox subpackages may be imported", name, path, modulePath)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no non-test .go files found in package directory; import boundary check did not run")
	}
}
