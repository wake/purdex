package run

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const tagA, tagB = "ea225edf2a219ec6", "0af99a6091695385"

func TestBuildEnvPutsTheOriginInTheMark(t *testing.T) {
	env, err := BuildEnv("j_x", tagA, t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "WWD_JOB=j_x") || !slices.Contains(env, "WWD_ORIGIN="+tagA) {
		t.Fatalf("the mark is the id and the origin: %v", env)
	}
	// no origin: the old mark, nothing named WWD_ORIGIN
	env, err = BuildEnv("j_x", "", t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "WWD_ORIGIN") {
			t.Fatalf("a run with no origin must not name one: %v", env)
		}
	}
	// a tag that is not one is refused, and the task env cannot set it
	for _, bad := range []string{"air26", "EA225EDF2A219EC6", "ea225edf"} {
		if _, err := BuildEnv("j_x", bad, t.TempDir(), nil, nil); err == nil {
			t.Errorf("origin %q must be refused", bad)
		}
	}
	if _, err := BuildEnv("j_x", tagA, t.TempDir(), []string{"WWD_ORIGIN=" + tagB}, nil); err == nil {
		t.Error("the task env must not set WWD_ORIGIN")
	}
}

// The sweep asks the source for (tag, id): the run's own tag, and it is the same one each time it looks again.
func TestSweepAsksForTheRunsOwnMark(t *testing.T) {
	var mu sync.Mutex
	var asked [][2]string
	s := spec(t, "true")
	s.Origin = tagA
	s.KillAfter = 50 * time.Millisecond
	s.Procs = recordingSource(func(tag, job string) ([]int, error) {
		mu.Lock()
		asked = append(asked, [2]string{tag, job})
		mu.Unlock()
		return nil, nil
	})
	if res := Run(t.Context(), s); res.Outcome != Exited {
		t.Fatalf("outcome %v", res.Outcome)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asked) == 0 {
		t.Fatal("the sweep did not look")
	}
	for _, a := range asked {
		if a != [2]string{tagA, s.JobID} {
			t.Fatalf("asked for %v, want the run's own (%s, %s)", a, tagA, s.JobID)
		}
	}
}

type recordingSource func(tag, job string) ([]int, error)

func (f recordingSource) JobProcesses(tag, job string) ([]int, error) { return f(tag, job) }
