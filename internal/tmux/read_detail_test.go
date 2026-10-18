// internal/tmux/read_detail_test.go — the "tmux.read" total, told apart by which read it was (#3316).
//
// The total tag "tmux.read" is what the watcher and the existing readers use; it keeps its name and its meaning. Each read also counts
// as an execstat event named after the method that made it, so that GET /api/debug/execstat says which reads the 175 a minute are. They
// are events, not tags, because the report's sum adds the tags up and would count a fork twice.
package tmux_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/wake/purdex/internal/execstat"
	"github.com/wake/purdex/internal/tmux"
)

func series(list []execstat.Series, name string) execstat.Series {
	for _, s := range list {
		if s.Name == name {
			return s
		}
	}
	return execstat.Series{}
}

func TestReadCountsUnderItsNameAndStaysInTheTotal(t *testing.T) {
	stubTmux(t, "#!/bin/sh\nexit 0\n")
	execstat.ResetTags()
	r := &tmux.RealExecutor{}
	ctx := context.Background()
	_, _ = r.ListAllPanes(ctx)
	_, _ = r.ListAllPanes(ctx)
	_, _ = r.ListPanePlacements(ctx)

	if got := series(execstat.Tags(), "tmux.read").Total; got != 3 {
		t.Fatalf("tag tmux.read = %d, want 3 (the total keeps counting every read)", got)
	}
	if got := series(execstat.Events(), "tmux.read.ListAllPanes").Total; got != 2 {
		t.Fatalf("event tmux.read.ListAllPanes = %d, want 2", got)
	}
	if got := series(execstat.Events(), "tmux.read.ListPanePlacements").Total; got != 1 {
		t.Fatalf("event tmux.read.ListPanePlacements = %d, want 1", got)
	}
	for _, s := range execstat.Tags() {
		if len(s.Name) > len("tmux.read.") && s.Name[:len("tmux.read.")] == "tmux.read." {
			t.Fatalf("%q is a tag: a detail must be an event or the report's sum counts the fork twice", s.Name)
		}
	}
}

// A read that does not fork tmux counts nothing; a fork that is not one of the reads (tmux.cmd) is not in the detail.
func TestDetailIsOnlyOfReads(t *testing.T) {
	stubTmux(t, "#!/bin/sh\nexit 0\n")
	execstat.ResetTags()
	if err := (&tmux.RealExecutor{}).KillSession("x"); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	for _, s := range execstat.Events() {
		if len(s.Name) >= len("tmux.read.") && s.Name[:len("tmux.read.")] == "tmux.read." {
			t.Fatalf("a kill-session counted as the read %q", s.Name)
		}
	}
}

// The names are literals at the call sites (a small fixed set, never built from an argument), each of them its own, and they begin with
// "tmux.read.". Mutation gate: pass a variable, or the same name twice, and this is red.
func TestEveryReadSiteNamesItselfWithAUniqueLiteral(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	nameRe := regexp.MustCompile(`^tmux\.read\.[A-Za-z]+$`)
	seen := map[string]string{}
	sites := 0
	fset := token.NewFileSet()
	for _, f := range files {
		if filepath.Ext(f) != ".go" || len(f) > 8 && f[len(f)-8:] == "_test.go" {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || (id.Name != "boundedRead" && id.Name != "tmuxCmdContext") {
				return true
			}
			if fd := enclosingFunc(file, call); fd == "boundedRead" {
				return true // the helper passes its own parameter on
			}
			sites++
			pos := fset.Position(call.Pos()).String()
			if len(call.Args) < 2 {
				t.Errorf("%s: %s has no detail argument", pos, id.Name)
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: the detail of %s is not a string literal", pos, id.Name)
				return true
			}
			name, _ := strconv.Unquote(lit.Value)
			if !nameRe.MatchString(name) {
				t.Errorf("%s: detail %q does not look like tmux.read.<Method>", pos, name)
			}
			if prev, dup := seen[name]; dup {
				t.Errorf("%s: detail %q is also used at %s", pos, name, prev)
			}
			seen[name] = pos
			return true
		})
	}
	if sites < 10 {
		t.Fatalf("found only %d read sites; the scan is not seeing the package", sites)
	}
}

func enclosingFunc(file *ast.File, n ast.Node) string {
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Pos() <= n.Pos() && n.End() <= fd.End() {
			return fd.Name.Name
		}
	}
	return ""
}
