package main

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/facts"
	wrun "github.com/wake/purdex/internal/wwd/run"
)

func envGet(env []string, name string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
}

// A tool the facts report is a tool a job finds: the job's PATH is the very list the tool probe searches, extra directories first.
func TestAJobPathIsTheListTheToolProbeSearches(t *testing.T) {
	home := t.TempDir()
	extra := []string{"/srv/tc/node/bin"}
	parent := []string{"PATH=/usr/bin:/bin", "SECRET=x", "LANG=C"}

	got := envGet(jobParentEnv(parent, home, extra, nil), "PATH")
	probe := facts.NewToolsReader(home, "/usr/bin:/bin", extra)
	if want := strings.Join(probe.PathDirs, ":"); got != want {
		t.Fatalf("job PATH %q != probe dirs %q", got, want)
	}
	if !strings.HasPrefix(got, "/srv/tc/node/bin:") {
		t.Errorf("extra directories must lead: %q", got)
	}
}

func TestJobParentEnvKeepsTheRestAndWorksWithoutAPath(t *testing.T) {
	home := t.TempDir()
	env := jobParentEnv([]string{"LANG=C", "TZ=UTC"}, home, []string{"/x/bin"}, nil)
	if envGet(env, "LANG") != "C" || envGet(env, "TZ") != "UTC" {
		t.Errorf("other names must stay: %v", env)
	}
	if !strings.HasPrefix(envGet(env, "PATH"), "/x/bin:") {
		t.Errorf("a parent without PATH still gets the tool list: %v", env)
	}
}

// Through the runner's own BuildEnv, as the worker does it: the job sees the extra directory in front of the default ones.
func TestTheJobEnvironmentCarriesTheExtraDirectories(t *testing.T) {
	home := t.TempDir()
	parent := jobParentEnv([]string{"PATH=/usr/bin"}, home, []string{"/srv/tc/node/bin"}, nil)
	env, err := wrun.BuildEnv("j_0123456789abcdef", "", t.TempDir(), nil, parent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(envGet(env, "PATH"), "/srv/tc/node/bin:") {
		t.Errorf("PATH = %q", envGet(env, "PATH"))
	}
}

// ---- the host's own variables for its jobs ([jobs.env], #3933) ----

const browsersPath = "/Users/wake/Assist/cache.noindex/ms-playwright"

// A name the host gave and the task lists reaches the job with the host's value, through the runner's own BuildEnv; the job's HOME is still
// the workspace's, not the real one.
func TestAHostVariableReachesAJobThatListsIt(t *testing.T) {
	ws := t.TempDir()
	parent := jobParentEnv([]string{"PATH=/usr/bin"}, t.TempDir(), nil, map[string]string{"PLAYWRIGHT_BROWSERS_PATH": browsersPath})
	env, err := wrun.BuildEnv("j_0123456789abcdef", "", ws, []string{"PLAYWRIGHT_BROWSERS_PATH"}, parent)
	if err != nil {
		t.Fatal(err)
	}
	if got := envGet(env, "PLAYWRIGHT_BROWSERS_PATH"); got != browsersPath {
		t.Errorf("PLAYWRIGHT_BROWSERS_PATH = %q", got)
	}
	if got := envGet(env, "HOME"); !strings.HasPrefix(got, ws) {
		t.Errorf("HOME = %q: the job's home stays the workspace's", got)
	}
}

// The whitelist is the task's: a name the host gave and the task does not list is not in the job.
func TestAHostVariableDoesNotReachAJobThatDoesNotListIt(t *testing.T) {
	parent := jobParentEnv([]string{"PATH=/usr/bin"}, t.TempDir(), nil, map[string]string{"PLAYWRIGHT_BROWSERS_PATH": browsersPath, "OTHER": "x"})
	env, err := wrun.BuildEnv("j_0123456789abcdef", "", t.TempDir(), []string{"OTHER"}, parent)
	if err != nil {
		t.Fatal(err)
	}
	if got := envGet(env, "PLAYWRIGHT_BROWSERS_PATH"); got != "" {
		t.Errorf("PLAYWRIGHT_BROWSERS_PATH = %q: the task did not ask for it", got)
	}
	if got := envGet(env, "OTHER"); got != "x" {
		t.Errorf("OTHER = %q", got)
	}
}

// The host's value is the one the job gets, over what the LaunchAgent or the unit happened to hand wwd; the rest of the environment stays.
func TestTheHostsValueWinsOverTheInheritedOne(t *testing.T) {
	env := jobParentEnv([]string{"PATH=/usr/bin", "PLAYWRIGHT_BROWSERS_PATH=/inherited", "LANG=C"}, t.TempDir(), nil, map[string]string{"PLAYWRIGHT_BROWSERS_PATH": browsersPath})
	if got := envGet(env, "PLAYWRIGHT_BROWSERS_PATH"); got != browsersPath {
		t.Errorf("PLAYWRIGHT_BROWSERS_PATH = %q", got)
	}
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "PLAYWRIGHT_BROWSERS_PATH=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the name is there %d times: %v", n, env)
	}
	if envGet(env, "LANG") != "C" {
		t.Errorf("the rest must stay: %v", env)
	}
}

// Without host variables the environment is what it was.
func TestNoHostVariablesChangeNothing(t *testing.T) {
	a := jobParentEnv([]string{"PATH=/usr/bin", "LANG=C"}, "/h", nil, nil)
	b := jobParentEnv([]string{"PATH=/usr/bin", "LANG=C"}, "/h", nil, map[string]string{})
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Errorf("%v != %v", a, b)
	}
}

// The worker's inputs carry the config's [jobs.env] into the environment the jobs read from (the very method loopConfig calls).
func TestTheWorkersInputsCarryTheConfigsJobsEnv(t *testing.T) {
	in := workerInputs{Home: t.TempDir()}
	in.Cfg.Jobs.Env = map[string]string{"PLAYWRIGHT_BROWSERS_PATH": browsersPath}
	env, err := wrun.BuildEnv("j_0123456789abcdef", "", t.TempDir(), []string{"PLAYWRIGHT_BROWSERS_PATH"}, in.jobParentEnv([]string{"PATH=/usr/bin"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := envGet(env, "PLAYWRIGHT_BROWSERS_PATH"); got != browsersPath {
		t.Errorf("PLAYWRIGHT_BROWSERS_PATH = %q", got)
	}
}
