package execstat

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The guard behind GET /api/debug/execstat's claim to count every child process (host-probe dedupe plan D0). It reads the
// source, not the text: an import under another name, a dot import, a bare exec.Cmd literal and the process-start calls of os,
// syscall and the pty library all count as a way to fork. A file that forks must be on one of the two lists below.

// forkAPIs are the calls that start a child process, by import path.
var forkAPIs = map[string]map[string]bool{
	"os/exec":               {"Command": true, "CommandContext": true},
	"os":                    {"StartProcess": true},
	"syscall":               {"ForkExec": true, "StartProcess": true},
	"github.com/creack/pty": {"Start": true, "StartWithSize": true, "StartWithAttrs": true},
}

// forkSites are the places in one Go source that start a child process other than through this package.
func forkSites(src string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		return nil, err
	}
	var sites []string
	names := map[string]string{} // the name an import is bound to → its path
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if _, watched := forkAPIs[path]; !watched {
			continue
		}
		name := filepath.Base(path)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == "." {
			sites = append(sites, "dot import of "+path)
			continue
		}
		if name != "_" {
			names[name] = path
		}
	}
	pointers := map[ast.Expr]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			if !ok {
				return true
			}
			if path, bound := names[id.Name]; bound && id.Obj == nil { // Obj == nil: the package, not a local of that name
				if forkAPIs[path][x.Sel.Name] {
					site := id.Name + "." + x.Sel.Name + " at " + fset.Position(x.Pos()).String()
					if path == "github.com/creack/pty" {
						sites = append(sites, ptyPrefix+site)
					} else {
						sites = append(sites, site)
					}
				}
			}
		case *ast.StarExpr: // *exec.Cmd is only a reference; a use of the type that is not behind a star creates a value
			if isExecCmd(x.X, names) {
				pointers[x.X] = true
			}
		}
		if e, ok := n.(ast.Expr); ok && isExecCmd(e, names) && !pointers[e] {
			// new(exec.Cmd), exec.Cmd{}, var c exec.Cmd, [N]exec.Cmd, a struct field, a type alias: every way to hold a Cmd by value
			sites = append(sites, "exec.Cmd value at "+fset.Position(e.Pos()).String())
		}
		return true
	})
	return sites, nil
}

// ptyPrefix marks the sites that are calls of the pty library: the library starts the embedded Cmd itself, so the call is a
// fork the helpers cannot see. They are not refused outright but listed (ptyCounted).
const ptyPrefix = "pty:"

// ptyCounted are the files that start a child through the pty library, with how many such calls each has. Each one reports its
// start by hand (Cmd.NoteStarted when it worked, Cmd.NoteStartFailed when not), and a test of that file proves both counts; a new
// pty start, or one more call in a listed file, fails the guard until it is listed here with such a test.
var ptyCounted = map[string]int{
	"internal/terminal/relay.go": 1, // TestRelayCountsItsForkAndItsFailedStart
}

// isExecCmd says whether the type expression is os/exec's Cmd (by whatever name exec is imported under).
func isExecCmd(e ast.Expr, names map[string]string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Obj == nil && names[id.Name] == "os/exec" && sel.Sel.Name == "Cmd" // Obj == nil: the package, not a local of that name
}

// cliOnly are the files of the pdx binary that run only in the short-lived CLI process, never in the daemon: a counter in a
// process that lives for one command says nothing (pdx hook sends its own count with the event, see the plan).
var cliOnly = map[string]string{
	"cmd/pdx/daemon.go":               "`pdx start`/`pdx stop`: starts the daemon and tails its log",
	"cmd/pdx/hook.go":                 "`pdx hook`: the Claude Code hook command",
	"cmd/pdx/hook_pid_resolver.go":    "`pdx hook`: finds the pid of the agent",
	"cmd/pdx/ask.go":                  "`pdx ask`",
	"cmd/pdx/statusline_proxy.go":     "`pdx statusline-proxy`",
	"cmd/pdx/msg_selftest.go":         "`pdx msg selftest`",
	"internal/gate/run.go":            "`pdx gate`: the wrapper process that runs the gated command (a process of its own, not the daemon)",
	"internal/wwd/fastlane/direct.go": "`pdx test`: runs the fast-lane command in the caller's tree (a CLI process of its own, never the daemon)",
}

// pending are the daemon's files that still fork on their own. The list may only shrink: each batch of the plan (D0b, D0c)
// moves files onto Command/CommandContext and deletes them from here (the value says which batch); a new fork in the daemon has
// to use the helpers.
var pending = map[string]string{}

func TestEveryForkInTheDaemonGoesThroughTheHelpers(t *testing.T) {
	root := filepath.Join("..", "..")
	hit := map[string][]string{}
	ptySeen := map[string]bool{}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if rel == "internal/execstat" || strings.Contains(rel, "/testdata") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sites, err := forkSites(string(b))
			if err != nil {
				return err
			}
			var forks []string
			ptys := 0
			for _, site := range sites {
				if strings.HasPrefix(site, ptyPrefix) {
					ptys++
				} else {
					forks = append(forks, site)
				}
			}
			ptySeen[rel] = true
			if ptys != ptyCounted[rel] {
				t.Errorf("%s has %d pty.Start* calls, ptyCounted says %d: list a pty start with the test that proves its counts", rel, ptys, ptyCounted[rel])
			}
			if len(forks) > 0 {
				hit[rel] = forks
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for f := range ptyCounted {
		if !ptySeen[f] {
			t.Errorf("%s is on ptyCounted but was not found", f)
		}
	}
	var fresh []string
	for f, sites := range hit {
		_, cli := cliOnly[f]
		_, pend := pending[f]
		if !cli && !pend {
			fresh = append(fresh, f+": "+strings.Join(sites, "; "))
		}
	}
	sort.Strings(fresh)
	if len(fresh) > 0 {
		t.Errorf("a child process is started outside the helpers (use execstat.Command / execstat.CommandContext with a tag; a file of the CLI-only commands goes on cliOnly):\n  %s", strings.Join(fresh, "\n  "))
	}
	for _, list := range []map[string]string{cliOnly, pending} {
		for f := range list {
			if _, ok := hit[f]; !ok {
				t.Errorf("%s is on a list of files that fork but no longer forks: delete the entry (the lists may only shrink)", f)
			}
		}
	}
	if os.Getenv("EXECSTAT_PRINT_SITES") != "" {
		var all []string
		for f := range hit {
			all = append(all, f)
		}
		sort.Strings(all)
		t.Logf("fork files:\n%s", strings.Join(all, "\n"))
	}
}

func TestTheGuardSeesEveryWayToFork(t *testing.T) {
	cases := map[string]bool{
		`package p; import "os/exec"; var _ = exec.Command("ps")`:                                           true,
		`package p; import "os/exec"; func f(c context.Context) { exec.CommandContext(c, "ps") }`:           true,
		`package p; import ex "os/exec"; var _ = ex.Command("ps")`:                                          true,  // another name
		`package p; import . "os/exec"; var _ = Command("ps")`:                                              true,  // dot import
		`package p; import "os/exec"; var _ = &exec.Cmd{Path: "x"}`:                                         true,  // a literal
		`package p; import "os/exec"; func f() { c := new(exec.Cmd); c.Start() }`:                           true,  // new
		`package p; import ex "os/exec"; func f() { var c ex.Cmd; c.Start() }`:                              true,  // a variable
		`package p; import "os/exec"; var cmds [1]exec.Cmd; var _ = cmds`:                                   true,  // an array
		`package p; import "os/exec"; type t struct{ cmd exec.Cmd }`:                                        true,  // a field
		`package p; import "os/exec"; type A = exec.Cmd; var a A`:                                           true,  // an alias
		`package p; import "os/exec"; var s []*exec.Cmd; var _ = s`:                                         false, // references
		`package p; import "os/exec"; func f(exec struct{ Cmd int }) int { return exec.Cmd }`:               false, // a local named exec
		`package p; import "os/exec"; func f(c *exec.Cmd) { c.Start() }`:                                    false, // a parameter is not a creation
		`package p; import "os"; var _, _ = os.StartProcess("x", nil, nil)`:                                 true,
		`package p; import "syscall"; var _, _ = syscall.ForkExec("x", nil, nil)`:                           true,
		`package p; import "github.com/creack/pty"; var _, _ = pty.Start(nil)`:                              true,
		`package p; import "github.com/creack/pty"; var _, _ = pty.StartWithSize(nil, nil)`:                 true,
		`package p; import "github.com/wake/purdex/internal/execstat"; var _ = execstat.Command("t", "ps")`: false,
		`package p; import "os/exec"; var e *exec.ExitError; var _ = e`:                                     false, // a type, not a fork
		`package p; import "os/exec"; func f(exec struct{ Command func() }) { exec.Command() }`:             false, // a local named exec
		`package p; import "os"; var _ = os.Getpid()`:                                                       false,
	}
	for src, want := range cases {
		sites, err := forkSites(src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if (len(sites) > 0) != want {
			t.Errorf("%s: sites %v, want fork=%v", src, sites, want)
		}
	}
}
