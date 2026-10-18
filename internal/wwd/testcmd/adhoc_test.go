package testcmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/fastlane"
	"github.com/wake/purdex/internal/wwd/submit"
)

func TestParseAdHocArgs(t *testing.T) {
	got, err := ParseArgs(strings.Fields("--adhoc --json -- go vet ./..."))
	if err != nil || !got.AdHoc || !got.JSON || !reflect.DeepEqual(got.Argv(), []string{"go", "vet", "./..."}) {
		t.Fatalf("got %+v err %v", got, err)
	}
	got, err = ParseArgs(strings.Fields("--adhoc -- echo --json --detach -- x"))
	if err != nil || got.JSON || got.Detach || !reflect.DeepEqual(got.Argv(), []string{"echo", "--json", "--detach", "--", "x"}) {
		t.Fatalf("what follows -- is the command, flags included: %+v err %v", got, err)
	}
	for _, line := range []string{
		"--adhoc",                // no command
		"--adhoc --",             // an empty command
		"--adhoc go vet",         // no --: the words would be a task
		"--adhoc --detach -- go", // never queued
		"--adhoc --class dev -- go",
		"--adhoc --list -- go",
		"--adhoc lint -- go", // a word before --
	} {
		if _, err := ParseArgs(strings.Fields(line)); err == nil {
			t.Errorf("%q: accepted", line)
		}
	}
}

// capture records the submission it is asked to register and lets the run go on unrecorded.
type capture struct{ bodies []submit.Body }

func (c *capture) Register(_ context.Context, b submit.Body) (fastlane.Handle, error) {
	c.bodies = append(c.bodies, b)
	return nil, fastlane.ErrUnrecorded
}

func noManifestRepo(t *testing.T) string {
	t.Helper()
	dir := repo(t, false)
	if _, err := os.Stat(filepath.Join(dir, ".wwd")); err == nil {
		t.Fatal("the repository has a manifest")
	}
	return dir
}

func TestAnAdHocCommandRunsHereUnderAHeavySlotWithItsWordsAsTheArgvAndNoManifest(t *testing.T) {
	g := &fakeGate{}
	rec := &capture{}
	api := &fakeAPI{}
	code, out, _ := run(t, Options{Args: args(t, "--adhoc", "--", "go", "vet", "./..."), API: api, Gate: g, Recorder: rec, Dir: noManifestRepo(t), Env: os.Environ()})
	if code != 0 || g.calls != 1 || api.submitted != 0 {
		t.Fatalf("code %d gate %d submitted %d\n%s", code, g.calls, api.submitted, out)
	}
	if g.last.Class != gate.ClassHeavy || !reflect.DeepEqual(g.last.Argv, []string{"go", "vet", "./..."}) {
		t.Fatalf("gate got class %q argv %q", g.last.Class, g.last.Argv)
	}
	if len(rec.bodies) != 1 || !rec.bodies[0].AdHoc || rec.bodies[0].Task != "adhoc" || rec.bodies[0].Cacheable || len(rec.bodies[0].Locks) != 0 {
		t.Fatalf("recorded %+v", rec.bodies)
	}
	if !strings.Contains(out, "undeclared command") {
		t.Fatalf("the report does not say it was undeclared:\n%s", out)
	}
}

func TestAnAdHocCommandRunsInTheDirectoryItWasAskedFromAndIsRecordedThere(t *testing.T) {
	root := noManifestRepo(t)
	sub := filepath.Join(root, "pkg", "inner")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	g := &fakeGate{}
	rec := &capture{}
	code, _, _ := run(t, Options{Args: args(t, "--adhoc", "--", "go", "test", "./..."), API: &fakeAPI{}, Gate: g, Recorder: rec, Dir: sub, Env: os.Environ()})
	if code != 0 || g.calls != 1 {
		t.Fatalf("code %d gate %d", code, g.calls)
	}
	if g.last.Dir != sub {
		t.Fatalf("ran in %q, want %q: `./...` means the directory it is typed in", g.last.Dir, sub)
	}
	if len(rec.bodies) != 1 || rec.bodies[0].Snapshot.RelCwd != "pkg/inner" {
		t.Fatalf("recorded %+v", rec.bodies)
	}
}

func TestAnAdHocCommandPassesShellCharactersAsWords(t *testing.T) {
	g := &fakeGate{}
	run(t, Options{Args: args(t, "--adhoc", "--", "echo", "a|b", "$HOME", ";"), API: &fakeAPI{}, Gate: g, Dir: noManifestRepo(t), Env: os.Environ()})
	if !reflect.DeepEqual(g.last.Argv, []string{"echo", "a|b", "$HOME", ";"}) {
		t.Fatalf("argv %q: no shell may touch the words", g.last.Argv)
	}
}

func TestAnAdHocCommandKeepsItsOwnExitCode(t *testing.T) {
	g := &fakeGate{code: 2}
	code, _, _ := run(t, Options{Args: args(t, "--adhoc", "--", "false"), API: &fakeAPI{}, Gate: g, Dir: noManifestRepo(t), Env: os.Environ()})
	if code != 2 {
		t.Fatalf("code %d", code)
	}
}

func TestAnAdHocCommandRunsUnrecordedWhenWwdIsNotThere(t *testing.T) {
	g := &fakeGate{}
	api := &fakeAPI{infoErr: fmt.Errorf("%w: dial unix: no such file", client.ErrUnavailable)}
	code, _, _ := run(t, Options{Args: args(t, "--adhoc", "--", "true"), API: api, Gate: g, Recorder: neverRecorder{t}, Dir: noManifestRepo(t), Env: os.Environ()})
	if code != 0 || g.calls != 1 {
		t.Fatalf("code %d gate %d", code, g.calls)
	}
}

func TestAnAdHocCommandIsNeverQueuedWhateverHappens(t *testing.T) {
	g := &fakeGate{}
	api := &fakeAPI{}
	rec := &busyRecorder{busy: 1 << 30}
	code, _, _ := run(t, Options{Args: args(t, "--adhoc", "--", "true"), API: api, Gate: g, Recorder: rec, Dir: noManifestRepo(t), Env: os.Environ(), LockWait: 20_000_000, LockPoll: 1_000_000})
	// an undeclared command has no locks, so a held lock can only be a refusal of the record itself: it waits and expires here
	if api.submitted != 0 {
		t.Fatalf("an undeclared command reached the queue (code %d)", code)
	}
}

func TestAnAdHocCommandOutsideARepositoryIsAUsageError(t *testing.T) {
	g := &fakeGate{}
	code, _, errOut := run(t, Options{Args: args(t, "--adhoc", "--", "true"), API: &fakeAPI{}, Gate: g, Dir: t.TempDir(), Env: os.Environ()})
	if code != ExitUsage || g.calls != 0 || !strings.Contains(errOut, "git repository") {
		t.Fatalf("code %d gate %d err %q", code, g.calls, errOut)
	}
}

func TestAnAdHocCommandOnARepositoryWithoutACommitRunsUnrecorded(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "init", "-q", "-b", "main")
	g := &fakeGate{}
	code, _, _ := run(t, Options{Args: args(t, "--adhoc", "--", "true"), API: &fakeAPI{}, Gate: g, Recorder: neverRecorder{t}, Dir: dir, Env: os.Environ()})
	if code != 0 || g.calls != 1 {
		t.Fatalf("code %d gate %d", code, g.calls)
	}
}
