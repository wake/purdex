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

	got := envGet(jobParentEnv(parent, home, extra), "PATH")
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
	env := jobParentEnv([]string{"LANG=C", "TZ=UTC"}, home, []string{"/x/bin"})
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
	parent := jobParentEnv([]string{"PATH=/usr/bin"}, home, []string{"/srv/tc/node/bin"})
	env, err := wrun.BuildEnv("j_0123456789abcdef", "", t.TempDir(), nil, parent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(envGet(env, "PATH"), "/srv/tc/node/bin:") {
		t.Errorf("PATH = %q", envGet(env, "PATH"))
	}
}
