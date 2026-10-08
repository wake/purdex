package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// slotFile is the one non-test file allowed to put a `hook` frame on the bus:
// it holds emitNormalizedToCode, which only the emit slot calls.
const slotFile = "hookemitter.go"

// hookBroadcastSites returns "file:line" for every call of the form
// X.Broadcast(<code>, "hook", ...) in the package's non-test sources, and the
// number of files scanned.
func hookBroadcastSites(t *testing.T, dir string) (sites []string, files int) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		files++
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Broadcast" {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(lit.Value); err == nil && v == "hook" {
				pos := fset.Position(call.Pos())
				sites = append(sites, filepath.Base(pos.Filename)+":"+strconv.Itoa(pos.Line))
			}
			return true
		})
	}
	return sites, files
}

// TestEmitSlot_AllHookFramesGoThroughIt: a `hook` frame that is broadcast
// outside the emit slot is read, ordered and numbered by nobody. The only
// Broadcast(_, "hook", _) in the package is emitNormalizedToCode's. Fail
// closed: finding no source files, or not finding the slot's own call, is a
// failure, not a pass.
func TestEmitSlot_AllHookFramesGoThroughIt(t *testing.T) {
	sites, files := hookBroadcastSites(t, ".")
	if files == 0 {
		t.Fatal("scanned no source files")
	}
	var stray []string
	slotCalls := 0
	for _, s := range sites {
		if strings.HasPrefix(s, slotFile+":") {
			slotCalls++
		} else {
			stray = append(stray, s)
		}
	}
	if slotCalls != 1 {
		t.Fatalf("found %d hook Broadcast calls in %s, want exactly 1 (is the scan looking at the right files? %d scanned)", slotCalls, slotFile, files)
	}
	if len(stray) != 0 {
		t.Fatalf("hook frames broadcast outside the emit slot: %v (go through emitSession)", stray)
	}
}
