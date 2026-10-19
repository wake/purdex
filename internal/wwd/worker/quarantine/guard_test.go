package quarantine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Add and Begin rely on the hold they saw under the store lock still being there when they commit. That holds because nothing but this
// package takes an owner's hold away behind the runner's back: locks.Table.ReleaseOwner has one caller outside tests, retire, under the
// store lock. A new caller anywhere else breaks that, so this test fails on it; the way to take an owner's hold away is Clear.
func TestReleaseOwnerIsCalledOnlyFromThisPackage(t *testing.T) {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find the source of this test")
	}
	pkgDir := filepath.Dir(self)
	root := filepath.Clean(filepath.Join(pkgDir, "..", "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s has no go.mod: %v", root, err)
	}
	fset := token.NewFileSet()
	var callers []string
	inside := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", ".scratch", "node_modules", "vendor", "spa", "dist", "out":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Logf("not parsed, skipped: %s: %v", path, perr)
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ReleaseOwner" {
				if filepath.Dir(path) != pkgDir {
					callers = append(callers, fset.Position(call.Pos()).String())
				} else {
					inside++
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if inside == 0 {
		t.Fatal("the scan found no ReleaseOwner call in this package (retire has one): the scan is not looking at the code")
	}
	if len(callers) > 0 {
		t.Fatalf("ReleaseOwner is called from outside the quarantine package: %v\n"+
			"Add and Begin rely on nobody taking an owner's hold away between their check and their commit; use Clear (see the package doc)", callers)
	}
}
