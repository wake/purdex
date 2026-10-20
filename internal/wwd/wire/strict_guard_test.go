package wire

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// strictReaders are the only files that may call the strict Decode (#3710): each reads a request body that a person, a script or the App
// wrote (a PUT, a POST), where a field that is not known is a typo and ignoring it silently would be the worse outcome. Everything a
// reader takes from somewhere that may be a later build — a peer's answer or push, an event, a record read back from disk, the body of a
// peer's worker call — uses DecodeLenient, so that a field added later is not a break.
var strictReaders = map[string]string{
	"internal/module/jobs/control.go":      "POST position (from the App)",
	"internal/module/pool/control.go":      "PUT worker, POST exception (from the App, checked before they are forwarded)",
	"internal/wwd/daemon/worker_routes.go": "PUT /v1/worker, POST /v1/exceptions (local socket)",
	"internal/wwd/jobsapi/api.go":          "POST /v1/jobs, POST position (local socket)",
	"internal/wwd/localruns/localruns.go":  "POST /v1/local-runs (local socket)",
}

// The guard behind the policy: it reads the source and finds every call of wire.Decode (also under another import name), and a file that
// is not on the list above is a new strict reader. A file on the list that no longer calls it must be taken off, so the list only
// says what is true.
func TestStrictDecodeOnlyReadsRequestBodies(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	found := map[string]bool{}
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); name == "node_modules" || name == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if callsStrictDecode(t, path, rel) {
				found[rel] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var extra, stale []string
	for f := range found {
		if _, ok := strictReaders[f]; !ok {
			extra = append(extra, f)
		}
	}
	for f := range strictReaders {
		if !found[f] {
			stale = append(stale, f)
		}
	}
	sort.Strings(extra)
	sort.Strings(stale)
	if len(extra) > 0 {
		t.Errorf("these files call the strict wire.Decode and are not request-body readers (use wire.DecodeLenient; or add the file to strictReaders with the request it reads): %v", extra)
	}
	if len(stale) > 0 {
		t.Errorf("these files are on strictReaders and no longer call wire.Decode: %v", stale)
	}
}

// callsStrictDecode says whether the Go file at path calls wire.Decode[T](…). In package wire itself it is the bare Decode.
func callsStrictDecode(t *testing.T, path, rel string) bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	local := "" // the name this file gives the wire package
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p == "github.com/wake/purdex/internal/wwd/wire" {
			local = "wire"
			if imp.Name != nil {
				local = imp.Name.Name
			}
		}
	}
	inWire := f.Name.Name == "wire" && strings.HasPrefix(rel, "internal/wwd/wire/")
	hit := false
	ast.Inspect(f, func(n ast.Node) bool {
		var fn ast.Expr
		switch c := n.(type) {
		case *ast.CallExpr:
			fn = c.Fun
		default:
			return true
		}
		if ix, ok := fn.(*ast.IndexExpr); ok { // Decode[T]( … )
			fn = ix.X
		} else if ix, ok := fn.(*ast.IndexListExpr); ok {
			fn = ix.X
		}
		switch x := fn.(type) {
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && local != "" && id.Name == local && x.Sel.Name == "Decode" {
				hit = true
			}
		case *ast.Ident:
			if inWire && x.Name == "Decode" {
				hit = true
			}
		}
		return true
	})
	return hit
}
