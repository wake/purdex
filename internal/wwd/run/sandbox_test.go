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
    if [ -e "$FAKE/hang" ]; then
      while [ ! -e "$FAKE/stopped" ]; do sleep 0.02; done
      exit 0
    fi
    exit "$(cat "$FAKE/exit" 2>/dev/null || echo 0)" ;;
  systemctl)
    echo "systemctl $*" >> "$FAKE/calls"
    [ "$2" != stop ] || : > "$FAKE/stopped"
    exit 0 ;;
esac
exit 99
`

const fakeSystemctl = `#!/bin/sh
echo "systemctl $*" >> "$FAKE/calls"
[ -e "$FAKE/no-unit" ] && { echo "Result="; exit 0; }
cat "$FAKE/show"
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
	got := sandboxArgv(sb, "wwd-j_test1", "/ws", "/ws/sub", 10*time.Minute, 10*time.Second, []string{"HOME=/ws/.home", "WWD_JOB=j_test1"}, "/usr/bin/go", []string{"test", "./..."})
	want := []string{"systemd-run", "--wait", "--pipe", "--uid=wake", "--unit=wwd-j_test1", "--slice=wwd.slice",
		"-p", "CPUQuota=200%", "-p", "MemoryMax=4G", "-p", "MemorySwapMax=0", "-p", "RuntimeMaxSec=660", "-p", "TimeoutStopSec=10",
		"-p", "PrivateTmp=yes", "-p", "ProtectHome=read-only", "-p", "ReadWritePaths=/ws", "-p", "WorkingDirectory=/ws/sub",
		"-p", "NoNewPrivileges=yes", "-p", "InaccessiblePaths=-/run/docker.sock",
		"env", "-i", "HOME=/ws/.home", "WWD_JOB=j_test1", "nice", "-n", "10", "ionice", "-c3", "/usr/bin/go", "test", "./..."}
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
	got := sandboxArgv(Sandbox{User: "wake"}.withDefaults(), "wwd-j", "/ws", "/ws", 0, time.Second, nil, "/bin/true", nil)
	if strings.Contains(strings.Join(got, " "), "RuntimeMaxSec") {
		t.Fatalf("a job without a timeout has no fuse: %q", got)
	}
}

func TestSandboxExtraPathsAreAdded(t *testing.T) {
	sb := Sandbox{User: "wake", ReadWritePaths: []string{"/var/cache/composer"}, InaccessiblePaths: []string{"/var/run/docker.sock"}}.withDefaults()
	j := strings.Join(sandboxArgv(sb, "wwd-j", "/ws", "/ws", time.Minute, time.Second, nil, "/bin/true", nil), " ")
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
	if !w.called(t, "systemctl show wwd-j_test1 -p ActiveState,Result,ExecMainStatus,ExecMainCode") {
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
