package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	teammod "github.com/wake/purdex/internal/module/team"
)

// D-U23-2 (Review focus 5): nothing in pdx and nothing in the mod turns
// unattended mode on. The switch is the App's: no pdx command names it,
// no pdx code calls its route, and the mod's hooks never mention it; the
// skill does not teach the route either. (Not a security boundary — the
// App and pdx share one host token, plan deviation 6 — but an agent gets
// no tool for it.) Mutation gate: add a `pdx unattended` dispatcher case →
// red.
func TestPdx_NoCommandOrModCallTurnsUnattendedOn(t *testing.T) {
	route := teammod.UnattendedRoute
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := string(b)
		switch {
		case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
			if strings.Contains(s, route) || strings.Contains(s, "UnattendedRoute") {
				t.Errorf("%s names the unattended route: no pdx code may call it", path)
			}
		case strings.HasPrefix(filepath.ToSlash(path), "plugin/purdex/hooks/") && strings.HasSuffix(path, ".js"):
			if strings.Contains(strings.ToLower(s), "unattended") {
				t.Errorf("%s mentions unattended: the mod never touches the switch", path)
			}
		case filepath.ToSlash(path) == "plugin/purdex/skills/pdx-team/SKILL.md":
			if strings.Contains(s, route) {
				t.Errorf("%s teaches the unattended route", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// The dispatcher and its usage line: every string literal of main().
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var cases []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				v, _ := strconv.Unquote(lit.Value)
				if strings.Contains(strings.ToLower(v), "unattended") {
					t.Errorf("main.go: %s names unattended: pdx has no command for it", lit.Value)
				}
			}
			if cc, ok := n.(*ast.CaseClause); ok {
				for _, e := range cc.List {
					if lit, ok := e.(*ast.BasicLit); ok {
						cases = append(cases, lit.Value)
					}
				}
			}
			return true
		})
	}
	if !strings.Contains(strings.Join(cases, " "), `"lead"`) {
		t.Fatalf("main.go's dispatcher was not found (cases %v): the guard reads nothing", cases)
	}
}
