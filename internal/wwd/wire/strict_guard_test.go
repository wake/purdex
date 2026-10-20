package wire

import (
	"fmt"
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

// strictReaders are the only places that may use the strict Decode (#3710), each as "file:function". Each reads a request body that a
// person, a script or the App wrote (a PUT, a POST), where a field that is not known is a typo and ignoring it silently would be the
// worse outcome. Everything a reader takes from somewhere that may be a later build — a peer's answer or push, an event, a record read
// back from disk, the body of a peer's worker call — uses DecodeLenient, so that a field added later is not a break.
//
// A site is a function that mentions wire.Decode, as a call or as a value: a new reader in a file that is on the list is a new site and
// fails here; so does a wrapper around Decode (the wrapper's own body is the site, wherever it is called from).
var strictReaders = map[string]string{
	"internal/module/jobs/control.go:handlePosition":           "POST position (from the App)",
	"internal/module/pool/control.go:handlePutWorker":          "PUT worker (from the App, checked before it is forwarded)",
	"internal/module/pool/control.go:handlePostException":      "POST exception (from the App, checked before it is forwarded)",
	"internal/wwd/daemon/worker_routes.go:handlePutWorker":     "PUT /v1/worker (local socket)",
	"internal/wwd/daemon/worker_routes.go:handlePostException": "POST /v1/exceptions (local socket)",
	"internal/wwd/jobsapi/api.go:Submit":                       "POST /v1/jobs (local socket)",
	"internal/wwd/jobsapi/api.go:Position":                     "POST position (local socket)",
	"internal/wwd/localruns/localruns.go:Handler":              "POST /v1/local-runs (local socket)",
}

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
			inWire := strings.HasPrefix(rel, "internal/wwd/wire/")
			sites, err := strictSites(path, nil, inWire)
			if err != nil {
				t.Errorf("%s: %v", rel, err)
				return nil
			}
			for _, fn := range sites {
				found[rel+":"+fn] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var extra, stale []string
	for s := range found {
		if _, ok := strictReaders[s]; !ok {
			extra = append(extra, s)
		}
	}
	for s := range strictReaders {
		if !found[s] {
			stale = append(stale, s)
		}
	}
	sort.Strings(extra)
	sort.Strings(stale)
	if len(extra) > 0 {
		t.Errorf("these functions use the strict wire.Decode and are not on the list of request-body readers (use wire.DecodeLenient; or add them to strictReaders with the request they read): %v", extra)
	}
	if len(stale) > 0 {
		t.Errorf("these are on strictReaders and no longer use wire.Decode: %v", stale)
	}
}

// strictSites are the functions of one Go source (the file at path, or src when given) that mention the strict wire.Decode — called,
// instantiated or taken as a value — under whatever name the file imports the package by. A file that dot-imports wire is an error: it
// would call Decode with no qualifier, past this check. inWire says the file is in package wire itself, where the bare Decode counts.
func strictSites(path string, src any, inWire bool) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	if err != nil {
		return nil, err
	}
	local := "" // the name this file gives the wire package
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p != "github.com/wake/purdex/internal/wwd/wire" {
			continue
		}
		local = "wire"
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if local == "." {
			return nil, fmt.Errorf("dot import of the wire package: Decode could be called without a qualifier, past the check of strict readers")
		}
	}
	var sites []string
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if mentionsDecode(d, local, inWire) {
				sites = append(sites, d.Name.Name)
			}
		case *ast.GenDecl:
			// a use at package level (a var initialised with the function) has no function to name: it is a site called "(package)"
			if d.Tok != token.IMPORT && mentionsDecode(d, local, inWire) {
				sites = append(sites, "(package)")
			}
		}
	}
	sort.Strings(sites)
	return sites, nil
}

// mentionsDecode says whether the declaration refers to the strict Decode: local.Decode (called, instantiated as local.Decode[T], or taken
// as a value), or the bare Decode when inWire. The name of a function being declared and the field of a selector (dec.Decode of the
// json package, a method called Decode) are not references.
func mentionsDecode(decl ast.Node, local string, inWire bool) bool {
	notRef := map[*ast.Ident]bool{}
	ast.Inspect(decl, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			notRef[x.Name] = true
		case *ast.SelectorExpr:
			notRef[x.Sel] = true
		}
		return true
	})
	hit := false
	ast.Inspect(decl, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && local != "" && id.Name == local && x.Sel.Name == "Decode" {
				hit = true
			}
		case *ast.Ident:
			if inWire && x.Name == "Decode" && !notRef[x] {
				hit = true
			}
		}
		return true
	})
	return hit
}

// The bypasses a reviewer would try, as fixtures: each must be found, so that none of them can pass the guard.
func TestTheStrictGuardFindsTheWaysAroundIt(t *testing.T) {
	const head = "package x\n\nimport (\n\twire \"github.com/wake/purdex/internal/wwd/wire\"\n)\n"
	cases := map[string]struct {
		src  string
		want []string
	}{
		"a call":                      {head + "func Read(b []byte) { wire.Decode[int](b) }\n", []string{"Read"}},
		"a second reader in a file":   {head + "func A(b []byte) { wire.Decode[int](b) }\nfunc B(b []byte) { wire.Decode[string](b) }\n", []string{"A", "B"}},
		"a generic wrapper":           {head + "func readAs[T any](b []byte) (T, error) { return wire.Decode[T](b) }\nfunc Use(b []byte) { readAs[int](b) }\n", []string{"readAs"}},
		"a function value":            {head + "func Pick() any { return wire.Decode[int] }\n", []string{"Pick"}},
		"a method":                    {head + "type S struct{}\nfunc (S) Read(b []byte) { wire.Decode[int](b) }\n", []string{"Read"}},
		"inside a closure":            {head + "func H() func([]byte) { return func(b []byte) { wire.Decode[int](b) } }\n", []string{"H"}},
		"a package level value":       {head + "var d = wire.Decode[int]\n", []string{"(package)"}},
		"another import name":         {"package x\n\nimport w \"github.com/wake/purdex/internal/wwd/wire\"\nfunc R(b []byte) { w.Decode[int](b) }\n", []string{"R"}},
		"lenient only":                {head + "func R(b []byte) { wire.DecodeLenient[int](b) }\n", nil},
		"some other package's Decode": {"package x\n\nimport wire \"example.com/other\"\nfunc R(b []byte) { wire.Decode[int](b) }\n", nil},
	}
	for name, c := range cases {
		got, err := strictSites("x.go", c.src, false)
		if err != nil || strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: sites %v, err %v, want %v", name, got, err, c.want)
		}
	}
	// the bare Decode is a site only inside package wire
	if got, _ := strictSites("x.go", "package wire\nfunc R(b []byte) { Decode[int](b) }\n", true); len(got) != 1 {
		t.Errorf("the bare Decode in package wire: %v", got)
	}
	if got, _ := strictSites("x.go", "package other\nfunc Decode[T any](b []byte) {}\nfunc R(b []byte) { Decode[int](b) }\n", false); len(got) != 0 {
		t.Errorf("a Decode of another package: %v", got)
	}
	// a dot import is refused outright
	if _, err := strictSites("x.go", "package x\n\nimport . \"github.com/wake/purdex/internal/wwd/wire\"\nfunc R(b []byte) { Decode[int](b) }\n", false); err == nil {
		t.Error("a dot import of wire passed the guard")
	}
}
