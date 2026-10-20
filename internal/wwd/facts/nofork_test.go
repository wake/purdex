package facts

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// forkMarkers are what starts a child process. The sampler runs every 5 s on every host, so a fork there is a cost paid forever
// (spec 2026-10-20 §4): readings come from sysctl, Mach, purego dlopen, /proc and /sys.
var forkMarkers = []string{"exec.Command", "execstat.", "os.StartProcess", "syscall.ForkExec", "syscall.Exec"}

// noForkExceptions are the files allowed to start a process, each with why it is not on the per-sample path.
var noForkExceptions = map[string]string{
	"facts/tools.go":    "asks the toolchains for their versions; the answer is cached for 10 minutes, so it is not on every sample",
	"hostmem/vmstat.go": "runs vm_stat for the old gate / resources / fleet / monitor readers only; wwd does not call it, and WWD-9 removes it",
}

// nonTestGoFiles lists the non-test .go files of the sampler's packages as "<pkg>/<file>".
func nonTestGoFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, pkg := range []string{".", "../../hostmem"} {
		ents, err := os.ReadDir(pkg)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			n := e.Name()
			if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
				continue
			}
			abs, err := filepath.Abs(pkg)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, filepath.Base(abs)+"/"+n)
		}
	}
	return out
}

func forkingFiles(t *testing.T, files []string) []string {
	t.Helper()
	var hit []string
	for _, f := range files {
		dir := "."
		if strings.HasPrefix(f, "hostmem/") {
			dir = "../../hostmem"
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.Base(f)))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range forkMarkers {
			if strings.Contains(string(b), m) {
				hit = append(hit, f)
				break
			}
		}
	}
	sort.Strings(hit)
	return hit
}

func TestSamplerSourcesDoNotFork(t *testing.T) {
	files := nonTestGoFiles(t)
	if len(files) < 20 {
		t.Fatalf("only %d source files found; the scan is looking in the wrong place", len(files))
	}
	var bad []string
	seen := map[string]bool{}
	for _, f := range forkingFiles(t, files) {
		seen[f] = true
		if _, ok := noForkExceptions[f]; !ok {
			bad = append(bad, f)
		}
	}
	if len(bad) > 0 {
		t.Errorf("these files start a process (exec.Command, execstat., os.StartProcess, syscall.ForkExec, syscall.Exec); "+
			"the sampler must not fork: %v", bad)
	}
	for f := range noForkExceptions { // an exception that no longer forks is a stale entry, and would hide a later fork
		if !seen[f] {
			t.Errorf("exception %s no longer starts a process: remove it from noForkExceptions", f)
		}
	}
}
