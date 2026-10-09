package convfeed

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestImportBoundary keeps the follower free of the daemon: the non-test files of this package import only the
// standard library, convmodel and ccnorm (a path is standard library when its first element has no dot).
func TestImportBoundary(t *testing.T) {
	allowed := map[string]bool{
		"github.com/wake/purdex/internal/convmodel":        true,
		"github.com/wake/purdex/internal/convmodel/ccnorm": true,
	}
	checked := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.ImportsOnly)
		if err != nil {
			return err
		}
		checked++
		for _, im := range f.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			first, _, _ := strings.Cut(p, "/")
			if strings.Contains(first, ".") && !allowed[p] {
				t.Errorf("%s imports %s: only the standard library, convmodel and ccnorm are allowed", path, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 3 {
		t.Fatalf("only %d source files checked", checked)
	}
}
