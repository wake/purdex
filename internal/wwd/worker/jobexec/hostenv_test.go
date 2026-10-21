package jobexec

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// ---- the host's variables ([jobs.env], #3933) reach the command of a job and the install of its dependencies, and nothing else ----

const hostBrowsers = "/Users/wake/Assist/cache.noindex/ms-playwright"

func hostEnvExec(t *testing.T) *Exec {
	t.Helper()
	return New(Config{
		WorkRoot:  t.TempDir(),
		ParentEnv: []string{"PATH=/usr/bin", "GOPROXY=https://proxy.example", "PLAYWRIGHT_BROWSERS_PATH=/inherited"},
		HostEnv:   map[string]string{"PLAYWRIGHT_BROWSERS_PATH": hostBrowsers, "GOENV_LIKE": "x"},
	})
}

func lookup(env []string, name string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

func wsOf(t *testing.T) snapshot.Workspace {
	t.Helper()
	return snapshot.Workspace{Dir: filepath.Join(t.TempDir(), "ws")}
}

// The command of a job reads the host's value for a name its task lists, over what the worker inherited; the worker's own copy of the
// environment (the one its own subprocesses use) is not touched.
func TestTheCommandOfAJobSeesTheHostsValueForAListedName(t *testing.T) {
	e := hostEnvExec(t)
	spec := e.runSpec(testRef, job("t"), wsOf(t), runner.Plan{Argv: []string{"true"}, TaskEnv: []string{"PLAYWRIGHT_BROWSERS_PATH"}})
	env, err := run.BuildEnv("j_0123456789abcdef", "", t.TempDir(), spec.TaskEnv, spec.ParentEnv)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := lookup(env, "PLAYWRIGHT_BROWSERS_PATH"); got != hostBrowsers {
		t.Errorf("PLAYWRIGHT_BROWSERS_PATH = %q, want the host's", got)
	}
	n := 0
	for _, kv := range spec.ParentEnv {
		if strings.HasPrefix(kv, "PLAYWRIGHT_BROWSERS_PATH=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("PLAYWRIGHT_BROWSERS_PATH is in the command's environment %d times: the host's value stands in for the inherited one", n)
	}
	if _, ok := lookup(env, "GOENV_LIKE"); ok {
		t.Error("a name the task did not list reached the job")
	}
	if _, ok := lookup(env, "GOPROXY"); ok {
		t.Error("a name the task did not list reached the job (inherited)")
	}
}

// The install of the dependencies is run under the same environment as the job's command, so a package manager finds what the job will.
func TestTheInstallOfDependenciesSeesTheHostsValueToo(t *testing.T) {
	e := hostEnvExec(t)
	spec := e.installSpec(testRef, job("t"), wsOf(t), "package-lock.json", installer{argv: []string{"npm", "ci"}, cacheEnv: "npm_config_cache"}, []string{"PLAYWRIGHT_BROWSERS_PATH"}, t.TempDir())
	env, err := run.BuildEnv("j_0123456789abcdef", "", t.TempDir(), spec.TaskEnv, spec.ParentEnv)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := lookup(env, "PLAYWRIGHT_BROWSERS_PATH"); got != hostBrowsers {
		t.Errorf("PLAYWRIGHT_BROWSERS_PATH = %q, want the host's", got)
	}
}

// The environment the worker's own subprocesses read (the module download, the tool probe) is the worker's, as it was: a host variable the
// tasks may list is not a setting of `go mod download` or of a tool lookup (codex R1).
func TestTheWorkersOwnEnvironmentDoesNotCarryTheHostsVariables(t *testing.T) {
	e := hostEnvExec(t)
	// after the environments of a job's command and of an install were made, not before: making them must not touch the worker's own
	e.runSpec(testRef, job("t"), wsOf(t), runner.Plan{Argv: []string{"true"}})
	e.installSpec(testRef, job("t"), wsOf(t), "package-lock.json", installer{argv: []string{"npm", "ci"}, cacheEnv: "npm_config_cache"}, nil, t.TempDir())
	if got, _ := lookup(e.cfg.ParentEnv, "PLAYWRIGHT_BROWSERS_PATH"); got != "/inherited" {
		t.Errorf("the worker's own PLAYWRIGHT_BROWSERS_PATH = %q: the host's value must not reach its subprocesses", got)
	}
	if _, ok := lookup(e.cfg.ParentEnv, "GOENV_LIKE"); ok {
		t.Error("a host variable is in the worker's own environment")
	}
}

// Without host variables the spec reads the worker's environment itself (no copy, nothing added).
func TestWithoutHostVariablesTheSpecHasTheWorkersEnvironment(t *testing.T) {
	e := New(Config{WorkRoot: t.TempDir(), ParentEnv: []string{"PATH=/usr/bin", "LANG=C"}})
	spec := e.runSpec(testRef, job("t"), wsOf(t), runner.Plan{Argv: []string{"true"}})
	if strings.Join(spec.ParentEnv, "\n") != "PATH=/usr/bin\nLANG=C" {
		t.Errorf("ParentEnv = %v", spec.ParentEnv)
	}
}
