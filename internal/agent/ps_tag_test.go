package agent

import (
	"strings"
	"testing"
)

func TestPsWhat(t *testing.T) {
	for want, cases := range map[string][][]string{
		"start": {{"-p", "42", "-o", "lstart="}},
		"ppid":  {{"-p", "42", "-o", "ppid="}},
		"comm":  {{"-p", "42", "-o", "comm="}},
		"args":  {{"-p", "42", "-o", "args="}},
		"table": {{"-A", "-o", "pid=,ppid=,lstart="}, {"-Ao", "pid=,ppid="}, {"-axo", "pid=,ppid=,pcpu=,rss=,ucomm="}, {"-e", "-o", "pid="}},
		"other": {{"-p", "42", "-o", "etime="}, {"-p", "42"}, nil},
	} {
		for _, args := range cases {
			if got := psWhat(args); got != want {
				t.Errorf("psWhat(%v) = %q, want %q", args, got, want)
			}
		}
	}
}

func TestPsShortCaller(t *testing.T) {
	for in, want := range map[string]string{
		"github.com/wake/purdex/internal/module/agent.(*Module).verifyEvent":                    "module/agent.verifyEvent",
		"github.com/wake/purdex/internal/module/agent.(*Module).sweepOnce.func1":                "module/agent.sweepOnce",
		"github.com/wake/purdex/internal/module/agent.(*Module).startSweep.func1.2":             "module/agent.startSweep",
		"github.com/wake/purdex/internal/agent/probe.(*Prober).listProcessTree":                 "agent/probe.listProcessTree",
		"github.com/wake/purdex/internal/agent/probe.PidAncestorIncludes":                       "agent/probe.PidAncestorIncludes",
		"github.com/wake/purdex/internal/module/resources.watchEnv.SharedProcessSnapshot.func2": "module/resources.watchEnv.SharedProcessSnapshot",
		"main.runStart": "main.runStart",
		"github.com/wake/purdex/internal/module/x.(*T).weird name!": "module/x.weird_name_",
	} {
		if got := psShortCaller(in); got != want {
			t.Errorf("psShortCaller(%q) = %q, want %q", in, got, want)
		}
	}
	long := "github.com/wake/purdex/internal/module/" + strings.Repeat("a", 200) + ".f"
	if got := psShortCaller(long); len(got) > 64 {
		t.Errorf("a long name is cut to 64 bytes, got %d", len(got))
	}
}

// The tag a real fork is counted under names what it asked and the first caller outside this package.
func TestPsTagNamesTheCaller(t *testing.T) {
	tag := psTagFromHere([]string{"-p", "1", "-o", "lstart="})
	if !strings.HasPrefix(tag, "agent.ps.start@") || !strings.Contains(tag, "TestPsTagNamesTheCaller") && !strings.Contains(tag, "testing.") {
		t.Errorf("tag = %q, want agent.ps.start@<a function outside the agent package>", tag)
	}
	if strings.Contains(tag, "psTagFromHere") {
		t.Errorf("tag = %q names a function of the agent package", tag)
	}
}

// A fork made on a goroutine of this package's own (nothing of another package on its stack) is named by the goroutine's function, not
// left without a who: the periodic start-text recalibration is such a fork.
func TestPsTagOnAGoroutineOfThePackageItself(t *testing.T) {
	done := make(chan string)
	go func() { done <- psTagFromHere([]string{"-p", "1", "-o", "lstart="}) }()
	tag := <-done
	if !strings.HasPrefix(tag, "agent.ps.start@agent.TestPsTagOnAGoroutineOfThePackageItself") {
		t.Errorf("tag = %q, want agent.ps.start@agent.TestPsTagOnAGoroutineOfThePackageItself (the goroutine's function)", tag)
	}
}

// psTagFromHere is psTag called one frame down, as runPS calls it.
func psTagFromHere(args []string) string { return psTag(args) }
