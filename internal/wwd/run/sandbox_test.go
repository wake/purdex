package run

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The sandbox runs the command as a transient systemd service (spec §5, M-L2). These tests put fake `sudo` and `systemctl`
// scripts where the runner looks for them, so the argument lists, the outcome mapping and the clean-up are checked on any host.

const fakeSudo = `#!/bin/sh
echo "sudo $*" >> "$FAKE/calls"
case "$1" in
  systemd-run)
    cat "$FAKE/out" 2>/dev/null
    if [ -e "$FAKE/err" ]; then cat "$FAKE/err" >&2; fi
    [ ! -e "$FAKE/late" ] || sleep 0.3
    : > "$FAKE/created"
    ef=$(printf '%s\n' "$@" | grep -m1 '\.wwd-exit\.[0-9a-f]*$')
    if [ -e "$FAKE/wrapper-exit" ] && [ -n "$ef" ]; then cat "$FAKE/wrapper-exit" > "$ef"; fi
    if [ -e "$FAKE/hang" ]; then
      while [ ! -e "$FAKE/stopped" ]; do sleep 0.02; done
      exit 0
    fi
    exit "$(cat "$FAKE/exit" 2>/dev/null || echo 0)" ;;
  systemctl)
    echo "systemctl $*" >> "$FAKE/calls"
    if [ "$2" = reset-failed ]; then
      [ ! -e "$FAKE/reset-fails" ] || exit 1
      [ -e "$FAKE/loaded" ] || [ -e "$FAKE/created" ] || exit 1 # as systemctl does for a unit that is not loaded
    fi
    if [ "$2" = stop ]; then
      [ -e "$FAKE/created" ] || exit 5   # no such unit yet
      [ -e "$FAKE/nostop" ] || : > "$FAKE/stopped"
    fi
    exit 0 ;;
esac
exit 99
`

const fakeSystemctl = `#!/bin/sh
echo "systemctl $*" >> "$FAKE/calls"
n=$(cat "$FAKE/show-count" 2>/dev/null || echo 0); n=$((n + 1)); echo $n > "$FAKE/show-count"
if [ -e "$FAKE/show-fails" ]; then exit 1; fi
[ "$(cat "$FAKE/show-fails-at" 2>/dev/null)" != "$n" ] || exit 1
[ -e "$FAKE/gone" ] && { printf 'ActiveState=inactive\nLoadState=not-found\n'; exit 0; }
[ -e "$FAKE/no-unit" ] && { printf 'ActiveState=inactive\nLoadState=not-found\nResult=\n'; exit 0; }
cat "$FAKE/show"
if [ -e "$FAKE/loaded" ] || [ -e "$FAKE/created" ]; then echo "LoadState=loaded"; else echo "LoadState=not-found"; fi
if [ -e "$FAKE/stale" ]; then echo "InvocationID=old"; elif [ -e "$FAKE/created" ]; then echo "InvocationID=run2"; else echo "InvocationID=run1"; fi
`

type fakeWorld struct {
	dir string
	sb  *Sandbox
}

func newWorld(t *testing.T) *fakeWorld {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"sudo": fakeSudo, "systemctl": fakeSystemctl} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAKE", dir)
	oldRetry, oldGrace, oldSettle := stopRetry, stopGrace, settle
	stopRetry, stopGrace, settle = 50*time.Millisecond, 300*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { stopRetry, stopGrace, settle = oldRetry, oldGrace, oldSettle })
	w := &fakeWorld{dir: dir, sb: &Sandbox{User: "wake", SudoPath: filepath.Join(dir, "sudo"), SystemctlPath: filepath.Join(dir, "systemctl")}}
	w.set("show", "ActiveState=inactive\nResult=success\nExecMainStatus=0\nExecMainCode=1\n")
	return w
}

func (w *fakeWorld) set(name, body string) {
	if err := os.WriteFile(filepath.Join(w.dir, name), []byte(body), 0o644); err != nil {
		panic(err)
	}
}

func (w *fakeWorld) calls(t *testing.T) []string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(w.dir, "calls"))
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (w *fakeWorld) called(t *testing.T, sub string) bool {
	t.Helper()
	for _, c := range w.calls(t) {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func sandboxSpec(t *testing.T, w *fakeWorld, argv ...string) Spec {
	s := spec(t, argv...)
	s.Sandbox = w.sb
	s.ParentEnv = []string{"PATH=/usr/bin:/bin"}
	return s
}

func TestSandboxArgvIsTheSpecCommand(t *testing.T) {
	sb := Sandbox{User: "wake"}.withDefaults()
	got := sandboxArgv(sb, "wwd-j_test1", "/ws", "/ws/sub", "/ws/.wwd-exit", 10*time.Minute, 10*time.Second, []string{"HOME=/ws/.home", "WWD_JOB=j_test1"}, "/usr/bin/go", []string{"test", "./..."})
	want := []string{"systemd-run", "--wait", "--pipe", "--uid=wake", "--unit=wwd-j_test1", "--slice=wwd.slice",
		"-p", "CPUQuota=200%", "-p", "MemoryMax=4G", "-p", "MemorySwapMax=0", "-p", "RuntimeMaxSec=660", "-p", "TimeoutStopSec=10",
		"-p", "PrivateTmp=yes", "-p", "ProtectHome=read-only", "-p", "ProtectSystem=strict", "-p", "ReadWritePaths=/ws", "-p", "WorkingDirectory=/ws/sub",
		"-p", "NoNewPrivileges=yes", "-p", "InaccessiblePaths=-/run/docker.sock",
		"env", "-i", "HOME=/ws/.home", "WWD_JOB=j_test1", "nice", "-n", "10", "ionice", "-c3", "sh", "-c", exitWrapper, "/ws/.wwd-exit", "/usr/bin/go", "test", "./..."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv =\n%q\nwant\n%q", got, want)
	}
	for _, a := range got {
		if a == "--scope" || a == "--quiet" || a == "--collect" {
			t.Fatalf("%s must not be used: a scope rejects the sandbox, quiet and collect hide the unit's Result", a)
		}
	}
}

func TestSandboxArgvNoTimeoutHasNoRuntimeFuse(t *testing.T) {
	got := sandboxArgv(Sandbox{User: "wake"}.withDefaults(), "wwd-j", "/ws", "/ws", "/ws/.wwd-exit", 0, time.Second, nil, "/bin/true", nil)
	if strings.Contains(strings.Join(got, " "), "RuntimeMaxSec") {
		t.Fatalf("a job without a timeout has no fuse: %q", got)
	}
}

func TestSandboxExtraPathsAreAdded(t *testing.T) {
	sb := Sandbox{User: "wake", ReadWritePaths: []string{"/var/cache/composer"}, InaccessiblePaths: []string{"/var/run/docker.sock"}}.withDefaults()
	j := strings.Join(sandboxArgv(sb, "wwd-j", "/ws", "/ws", "/ws/.wwd-exit", time.Minute, time.Second, nil, "/bin/true", nil), " ")
	for _, want := range []string{"ReadWritePaths=/ws /var/cache/composer", "InaccessiblePaths=-/run/docker.sock /var/run/docker.sock"} {
		if !strings.Contains(j, want) {
			t.Fatalf("missing %q in %s", want, j)
		}
	}
}

func TestParseShow(t *testing.T) {
	m := parseShow("ActiveState=failed\nResult=oom-kill\nExecMainStatus=9\nExecMainCode=2\nstray line\n")
	if m["Result"] != "oom-kill" || m["ExecMainStatus"] != "9" || m["ActiveState"] != "failed" {
		t.Fatalf("show = %v", m)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name     string
		show     string
		stop     stopReason
		outcome  Outcome
		code     int
		signal   string
		reason   string
		startErr bool
	}{
		{"success", "Result=success\nExecMainStatus=0\n", stopNone, Exited, 0, "", "", false},
		{"exit code", "Result=exit-code\nExecMainStatus=3\n", stopNone, Exited, 3, "", "", false},
		{"oom", "Result=oom-kill\nExecMainStatus=9\n", stopNone, Exited, -1, "killed", "oom", false},
		{"signal", "Result=signal\nExecMainStatus=15\n", stopNone, Exited, -1, "terminated", "", false},
		{"fuse", "Result=timeout\nExecMainStatus=15\n", stopNone, TimedOut, -1, "", "timeout", false},
		{"stopped for the deadline", "Result=success\nExecMainStatus=0\n", stopDeadline, TimedOut, -1, "", "", false},
		{"stopped by cancel", "Result=success\nExecMainStatus=0\n", stopCancel, Cancelled, -1, "", "", false},
		{"no unit", "Result=\n", stopNone, StartFailed, -1, "", "", true},
		{"resources", "Result=resources\n", stopNone, StartFailed, -1, "", "", true},
	}
	for _, c := range cases {
		o, code, sig, reason, err := classify(parseShow(c.show), c.stop)
		if o != c.outcome || code != c.code || sig != c.signal || reason != c.reason || (err != nil) != c.startErr {
			t.Errorf("%s: got %s %d %q %q %v", c.name, o, code, sig, reason, err)
		}
	}
}

func TestSandboxRunExitCodeOutputAndCleanUp(t *testing.T) {
	w := newWorld(t)
	w.set("out", "hello\n")
	w.set("err", "oops\n")
	w.set("exit", "3")
	w.set("show", "ActiveState=failed\nResult=exit-code\nExecMainStatus=3\nExecMainCode=1\n")
	r := Run(context.Background(), sandboxSpec(t, w, "sh", "-c", "true"))
	if r.Outcome != Exited || r.ExitCode != 3 {
		t.Fatalf("result = %+v", r)
	}
	if got := readFile(t, r.StdoutPath); got != "hello\n" {
		t.Fatalf("stdout = %q", got)
	}
	if log := readFile(t, r.LogPath); !strings.Contains(log, "hello\n") || !strings.Contains(log, "oops\n") {
		t.Fatalf("log = %q", log)
	}
	if !w.called(t, "systemctl show wwd-j_test1 -p ActiveState,LoadState,InvocationID,Result,ExecMainStatus,ExecMainCode") {
		t.Fatalf("the outcome must come from the unit: %q", w.calls(t))
	}
	calls := w.calls(t)
	last := calls[len(calls)-1]
	if !strings.Contains(last, "reset-failed wwd-j_test1") {
		t.Fatalf("reset-failed must come after reading the Result: %q", calls)
	}
	if r.Escaped != nil {
		t.Fatalf("the unit's cgroup holds the job: nothing escapes, got %v", r.Escaped)
	}
}

func TestSandboxOOMIsKilledWithReason(t *testing.T) {
	w := newWorld(t)
	w.set("exit", "1") // systemd-run exits 1 for an OOM kill as it does for a timeout: only the unit's Result tells them apart
	w.set("show", "ActiveState=failed\nResult=oom-kill\nExecMainStatus=9\nExecMainCode=2\n")
	r := Run(context.Background(), sandboxSpec(t, w, "true"))
	if r.Outcome != Exited || r.Signal != "killed" || r.Reason != "oom" {
		t.Fatalf("result = %+v", r)
	}
}

func TestSandboxFuseIsATimeout(t *testing.T) {
	w := newWorld(t)
	w.set("exit", "1")
	w.set("show", "ActiveState=failed\nResult=timeout\nExecMainStatus=15\nExecMainCode=2\n")
	r := Run(context.Background(), sandboxSpec(t, w, "true"))
	if r.Outcome != TimedOut || r.Reason != "timeout" {
		t.Fatalf("result = %+v", r)
	}
}

func TestSandboxDeadlineStopsTheUnit(t *testing.T) {
	w := newWorld(t)
	w.set("hang", "")
	s := sandboxSpec(t, w, "sleep", "60")
	s.Timeout = 200 * time.Millisecond
	start := time.Now()
	r := Run(context.Background(), s)
	if r.Outcome != TimedOut {
		t.Fatalf("result = %+v", r)
	}
	if !w.called(t, "systemctl stop wwd-j_test1") {
		t.Fatalf("killing the client leaves the service running: the unit must be stopped: %q", w.calls(t))
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
}

func TestSandboxCancelStopsTheUnit(t *testing.T) {
	w := newWorld(t)
	w.set("hang", "")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	r := Run(ctx, sandboxSpec(t, w, "sleep", "60"))
	if r.Outcome != Cancelled || !w.called(t, "systemctl stop wwd-j_test1") {
		t.Fatalf("result = %+v calls %q", r, w.calls(t))
	}
}

func TestSandboxUnitThatNeverExistedIsStartFailed(t *testing.T) {
	w := newWorld(t)
	w.set("no-unit", "")
	w.set("exit", "1")
	r := Run(context.Background(), sandboxSpec(t, w, "true"))
	if r.Outcome != StartFailed || r.Err == nil {
		t.Fatalf("result = %+v", r)
	}
}

func TestSandboxRejectsAJobIDThatIsNotAUnitName(t *testing.T) {
	w := newWorld(t)
	s := sandboxSpec(t, w, "true")
	s.JobID = "j bad;id"
	if r := Run(context.Background(), s); r.Outcome != StartFailed {
		t.Fatalf("result = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(w.dir, "calls")); err == nil {
		t.Fatal("nothing may be started for an invalid job id")
	}
}

func TestSandboxNeedsAUser(t *testing.T) {
	w := newWorld(t)
	w.sb.User = ""
	if r := Run(context.Background(), sandboxSpec(t, w, "true")); r.Outcome != StartFailed {
		t.Fatalf("result = %+v", r)
	}
}

func TestSandboxStopBeforeTheUnitExistsIsSentAgain(t *testing.T) {
	w := newWorld(t)
	w.set("hang", "")
	w.set("late", "") // systemd-run makes the unit 0.3 s after it started: the first stop finds nothing
	s := sandboxSpec(t, w, "sleep", "60")
	s.Timeout = 100 * time.Millisecond
	r := Run(context.Background(), s)
	if r.Outcome != TimedOut || r.Err != nil {
		t.Fatalf("result = %+v", r)
	}
	n := 0
	for _, c := range w.calls(t) {
		if strings.HasPrefix(c, "sudo systemctl stop") {
			n++
		}
	}
	if n < 2 {
		t.Fatalf("the stop must be sent again until the unit is there: %d stops in %q", n, w.calls(t))
	}
}

func TestSandboxAStopThatNeverWorksIsAnError(t *testing.T) {
	w := newWorld(t)
	w.set("hang", "")
	w.set("nostop", "")
	s := sandboxSpec(t, w, "sleep", "60")
	s.Timeout = 100 * time.Millisecond
	// the unit looks free before it is made and active after: it never goes inactive
	go func() {
		for i := 0; i < 400; i++ {
			if _, err := os.Stat(filepath.Join(w.dir, "created")); err == nil {
				w.set("show", "ActiveState=active\nResult=success\n")
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	r := Run(context.Background(), s)
	if r.Err == nil || !strings.Contains(r.Err.Error(), "may still be running") {
		t.Fatalf("a unit that cannot be proven stopped is an error, not a clean timeout: %+v", r)
	}
}

func TestSandboxRefusesToStartNextToARunningUnit(t *testing.T) {
	w := newWorld(t)
	w.set("show", "ActiveState=active\nResult=success\n")
	r := Run(context.Background(), sandboxSpec(t, w, "true"))
	if r.Outcome != StartFailed || r.Err == nil {
		t.Fatalf("result = %+v", r)
	}
	if w.called(t, "systemd-run") {
		t.Fatalf("nothing may be started: %q", w.calls(t))
	}
}

func TestSandboxClearsAnOldFailedUnitBeforeStarting(t *testing.T) {
	w := newWorld(t)
	w.set("loaded", "")
	w.set("show", "ActiveState=failed\nResult=exit-code\nExecMainStatus=1\n")
	Run(context.Background(), sandboxSpec(t, w, "true"))
	calls := w.calls(t)
	first, run := -1, -1
	for i, c := range calls {
		if strings.HasPrefix(c, "sudo systemctl reset-failed") && first < 0 {
			first = i
		}
		if strings.HasPrefix(c, "sudo systemd-run") && run < 0 {
			run = i
		}
	}
	if first < 0 || run < 0 || first > run {
		t.Fatalf("an old unit of the same name must be cleared before the new one starts, or its Result is read as this run's: %q", calls)
	}
}

func TestSandboxRefusesAPathWithWhiteSpace(t *testing.T) {
	w := newWorld(t)
	w.sb.ReadWritePaths = []string{"/var/cache/my cache"}
	if r := Run(context.Background(), sandboxSpec(t, w, "true")); r.Outcome != StartFailed {
		t.Fatalf("result = %+v", r)
	}
}

func TestTaskEnvNameCannotBeAnOption(t *testing.T) {
	for _, e := range []string{"--unset=WWD_JOB=1", "-i=1", "A B=1", "1A=1"} {
		s := spec(t, "true")
		s.TaskEnv = []string{e}
		if r := Run(context.Background(), s); r.Outcome != StartFailed {
			t.Errorf("%q: result = %+v", e, r)
		}
	}
}

func TestSandboxAResultOfAnOldInvocationIsNotThisRuns(t *testing.T) {
	w := newWorld(t)
	w.set("loaded", "")
	w.set("stale", "") // the unit keeps its old invocation id: systemd-run never made a new one
	w.set("exit", "1")
	w.set("show", "ActiveState=failed\nResult=oom-kill\nExecMainStatus=9\n")
	r := Run(context.Background(), sandboxSpec(t, w, "true"))
	if r.Outcome != StartFailed || r.Reason == "oom" {
		t.Fatalf("an old unit's Result was taken as this run's: %+v", r)
	}
}

func TestSandboxCannotAskOrCannotClearIsStartFailed(t *testing.T) {
	for _, mode := range []string{"show-fails", "reset-fails", "show-fails-at"} {
		w := newWorld(t)
		w.set("loaded", "")
		w.set(mode, "2") // show-fails-at: the second query, after the clearing, is the one that fails
		if r := Run(context.Background(), sandboxSpec(t, w, "true")); r.Outcome != StartFailed {
			t.Errorf("%s: result = %+v", mode, r)
		}
		if w.called(t, "systemd-run") {
			t.Errorf("%s: nothing may be started: %q", mode, w.calls(t))
		}
	}
}

func TestSandboxAFailedQueryDoesNotProveTheUnitStopped(t *testing.T) {
	w := newWorld(t)
	w.set("hang", "")
	s := sandboxSpec(t, w, "sleep", "60")
	s.Timeout = 100 * time.Millisecond
	// the unit looks fine before the run; once it exists the queries fail: the stop cannot be proven
	go func() {
		for i := 0; i < 400; i++ {
			if _, err := os.Stat(filepath.Join(w.dir, "created")); err == nil {
				w.set("show-fails", "")
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	r := Run(context.Background(), s)
	if r.Err == nil || !strings.Contains(r.Err.Error(), "not proven stopped") {
		t.Fatalf("result = %+v", r)
	}
}

func TestSandboxDoesNotClearAUnitThatIsNotLoaded(t *testing.T) {
	// systemctl exits 1 for reset-failed on a unit that was never there: that must not stop a first run (found on mserv)
	w := newWorld(t)
	r := Run(context.Background(), sandboxSpec(t, w, "true"))
	if r.Outcome != Exited || r.ExitCode != 0 {
		t.Fatalf("result = %+v", r)
	}
	first := w.calls(t)
	for i, c := range first {
		if strings.HasPrefix(c, "sudo systemd-run") {
			for _, before := range first[:i] {
				if strings.Contains(before, "reset-failed") {
					t.Fatalf("reset-failed before the start for a unit that is not loaded: %q", first)
				}
			}
		}
	}
}

func TestSandboxASuccessfulUnitIsGoneAndExitZeroIsTheSuccess(t *testing.T) {
	// measured on mserv: systemd unloads a unit that ended well at once, so there is no Result left to read
	w := newWorld(t)
	w.set("gone", "")
	w.set("wrapper-exit", "0") // what the one-line shell around the command leaves behind
	r := Run(context.Background(), sandboxSpec(t, w, "true"))
	if r.Outcome != Exited || r.ExitCode != 0 || r.Err != nil {
		t.Fatalf("result = %+v", r)
	}
	// but if systemd-run itself failed, nothing was started: no unit and a non-zero exit is not a success
	w2 := newWorld(t)
	w2.set("gone", "")
	w2.set("exit", "1")
	if r := Run(context.Background(), sandboxSpec(t, w2, "true")); r.Outcome != StartFailed {
		t.Fatalf("result = %+v", r)
	}
}

func TestSandboxAUnitStoppedFromOutsideIsNotASuccess(t *testing.T) {
	// a stop makes systemd-run exit 0 and the (successful) unit disappear; only the exit file shows the command did not finish
	w := newWorld(t)
	w.set("gone", "")
	r := Run(context.Background(), sandboxSpec(t, w, "sleep", "60"))
	if r.Outcome == Exited || r.Err == nil || !strings.Contains(r.Err.Error(), "stopped from outside") {
		t.Fatalf("result = %+v", r)
	}
}

func TestSandboxTheExitFileCarriesTheCommandsOwnStatus(t *testing.T) {
	w := newWorld(t)
	w.set("gone", "")
	w.set("wrapper-exit", "7")
	if r := Run(context.Background(), sandboxSpec(t, w, "true")); r.Outcome != Exited || r.ExitCode != 7 {
		t.Fatalf("result = %+v", r)
	}
}

func TestSandboxAFileOrDirectoryOfThePlainNameCannotSpoilTheAnswer(t *testing.T) {
	// a stale file, or a task that makes ".wwd-exit" for its own reasons, is not the runner's status file (whose name is new every run)
	w := newWorld(t)
	w.set("gone", "")
	w.set("wrapper-exit", "0")
	s := sandboxSpec(t, w, "true")
	if err := os.Mkdir(filepath.Join(s.Workspace, ".wwd-exit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := Run(context.Background(), s); r.Outcome != Exited || r.ExitCode != 0 || r.Err != nil {
		t.Fatalf("a directory of the plain name spoiled the answer: %+v", r)
	}
}

func TestSandboxTheExitFileNameIsNewEveryRunAndIsRemoved(t *testing.T) {
	w := newWorld(t)
	w.set("gone", "")
	w.set("wrapper-exit", "0")
	s := sandboxSpec(t, w, "true")
	Run(context.Background(), s)
	Run(context.Background(), s)
	var names []string
	for _, c := range w.calls(t) {
		if strings.HasPrefix(c, "sudo systemd-run") {
			for _, f := range strings.Fields(c) {
				if strings.Contains(f, ".wwd-exit.") {
					names = append(names, f)
				}
			}
		}
	}
	if len(names) != 2 || names[0] == names[1] {
		t.Fatalf("names = %v", names)
	}
	for _, n := range names {
		if _, err := os.Stat(n); err == nil {
			t.Errorf("%s was left behind", n)
		}
	}
}

func TestSandboxASystemdSetUpFailureIsNotTheTasksRedTest(t *testing.T) {
	// 226/NAMESPACE (a ReadWritePaths entry that is not there) ends the unit as exit-code before the shell ran: no status file
	w := newWorld(t)
	w.set("loaded", "")
	w.set("exit", "226")
	w.set("show", "ActiveState=failed\nResult=exit-code\nExecMainStatus=226\nExecMainCode=1\n")
	r := Run(context.Background(), sandboxSpec(t, w, "true"))
	if r.Outcome != StartFailed || r.Err == nil || !strings.Contains(r.Err.Error(), "could not set unit") {
		t.Fatalf("result = %+v", r)
	}
}

func TestSandboxTheSameStatusFromTheCommandItselfIsItsResult(t *testing.T) {
	// the command's own shell left 226 in the status file: the task really exited 226, and 199 and 244 are no systemd status
	for _, code := range []string{"226", "199", "244"} {
		w := newWorld(t)
		w.set("exit", code)
		w.set("wrapper-exit", code)
		w.set("show", "ActiveState=failed\nResult=exit-code\nExecMainStatus="+code+"\nExecMainCode=1\n")
		r := Run(context.Background(), sandboxSpec(t, w, "true"))
		if r.Outcome != Exited || r.Err != nil {
			t.Errorf("%s: result = %+v", code, r)
		}
	}
	w := newWorld(t)
	w.set("exit", "199")
	w.set("show", "ActiveState=failed\nResult=exit-code\nExecMainStatus=199\nExecMainCode=1\n")
	if r := Run(context.Background(), sandboxSpec(t, w, "true")); r.Outcome != Exited || r.ExitCode != 199 {
		t.Errorf("199 without a status file is still an exit: %+v", r)
	}
}

func TestSandboxAnExtraWritablePathThatIsNotThereIsRefusedBeforeAnythingStarts(t *testing.T) {
	w := newWorld(t)
	w.sb.ReadWritePaths = []string{filepath.Join(t.TempDir(), "no-such-cache")}
	r := Run(context.Background(), sandboxSpec(t, w, "true"))
	if r.Outcome != StartFailed || r.Err == nil || !strings.Contains(r.Err.Error(), "no-such-cache") {
		t.Fatalf("result = %+v", r)
	}
	if w.called(t, "systemd-run") {
		t.Fatalf("nothing may be started: %q", w.calls(t))
	}
	w2 := newWorld(t)
	w2.sb.ReadWritePaths = []string{t.TempDir()}
	if r := Run(context.Background(), sandboxSpec(t, w2, "true")); r.Outcome != Exited {
		t.Fatalf("an existing extra path is fine: %+v", r)
	}
}
