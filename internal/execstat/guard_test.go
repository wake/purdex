package execstat

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// cliOnly are the files of the pdx binary that run only in the short-lived CLI process, never in the daemon: a counter in a
// process that lives for one command says nothing. Every other non-test exec.Command in this module goes through Command or
// CommandContext of this package, so its forks show in GET /api/debug/execstat.
var cliOnly = map[string]string{
	"cmd/pdx/daemon.go":            "`pdx start`/`pdx stop`: starts the daemon and tails its log",
	"cmd/pdx/hook.go":              "`pdx hook`: the Claude Code hook command",
	"cmd/pdx/hook_pid_resolver.go": "`pdx hook`: finds the pid of the agent",
	"cmd/pdx/ask.go":               "`pdx ask`",
	"cmd/pdx/lease_run.go":         "`pdx lease run`: runs the command it leases for",
	"cmd/pdx/reap_children.go":     "`pdx lease run`: reaps what its command left",
	"cmd/pdx/statusline_proxy.go":  "`pdx statusline-proxy`",
	"cmd/pdx/msg_selftest.go":      "`pdx msg selftest`",
}

var execCall = regexp.MustCompile(`\bexec\.(Command|CommandContext)\(`)

// The daemon counts every child process it starts (host-probe dedupe plan D0): a bare exec.Command is a fork nobody can see.
func TestNoUncountedExecInTheDaemonPackages(t *testing.T) {
	root := filepath.Join("..", "..")
	var found []string
	seenCLI := map[string]bool{}
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
			if !execCall.Match(b) {
				return nil
			}
			if _, ok := cliOnly[rel]; ok {
				seenCLI[rel] = true
				return nil
			}
			found = append(found, rel)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(found)
	if len(found) > 0 {
		t.Errorf("exec.Command / exec.CommandContext outside internal/execstat (use execstat.Command / execstat.CommandContext with a tag, or list the file in cliOnly if it runs only in the CLI):\n  %s", strings.Join(found, "\n  "))
	}
	for f := range cliOnly {
		if !seenCLI[f] {
			t.Errorf("cliOnly lists %s, which no longer calls exec.Command: remove the entry", f)
		}
	}
}

func TestTheGuardSeesABareExecCall(t *testing.T) {
	for src, want := range map[string]bool{
		"c := exec.Command(\"ps\")":            true,
		"c := exec.CommandContext(ctx, x)":     true,
		"c := execstat.Command(\"t\", \"ps\")": false,
		"// exec.CommandX is not it":           false,
		"var e *exec.Cmd":                      false,
	} {
		if got := execCall.MatchString(src); got != want {
			t.Errorf("%q: %v, want %v", src, got, want)
		}
	}
}
