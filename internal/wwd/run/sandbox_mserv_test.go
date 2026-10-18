//go:build mserv && linux

package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// WWD-4d-3: the sandbox on a real Linux host (mserv). Build it on another host and copy the binary:
//
//	GOOS=linux GOARCH=amd64 go test -c -tags mserv -o wwd-run-mserv.test ./internal/wwd/run/
//	WWD_MSERV_E2E=1 ./wwd-run-mserv.test -test.v -test.run 'MServ/<scenario>'
//
// It makes transient units named wwd-j_4d3-<scenario> in wwd.slice, a directory under WWD_E2E_DIR (default ~/.local/share/wwd-4d3)
// and probe files named 4d3-*; it clears its own units one by one (no wildcard). docs/wwd-c2-matrix.md is not about this: the
// results of a run are in docs/wwd-4d3-mserv.md.

func e2eDir(t *testing.T) string {
	t.Helper()
	if os.Getenv("WWD_MSERV_E2E") != "1" {
		t.Skip("set WWD_MSERV_E2E=1 on mserv")
	}
	d := os.Getenv("WWD_E2E_DIR")
	if d == "" {
		d = filepath.Join(os.Getenv("HOME"), ".local", "share", "wwd-4d3")
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

type e2eRun struct {
	Result
	out, log string
	took     time.Duration
}

// runSB runs argv in a fresh workspace under the sandbox and reads back the files.
func runSB(t *testing.T, name string, sb Sandbox, timeout time.Duration, ctx context.Context, argv ...string) e2eRun {
	t.Helper()
	ws := filepath.Join(e2eDir(t), "ws-"+name)
	_ = os.RemoveAll(ws)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if sb.User == "" {
		sb.User = os.Getenv("USER")
	}
	id := "j_4d3-" + name
	t.Cleanup(func() {
		_ = exec.Command("sudo", "systemctl", "reset-failed", "wwd-"+id+".service").Run() // our own unit only
		_ = os.RemoveAll(ws)
	})
	spec := Spec{JobID: id, Argv: argv, Dir: ws, Workspace: ws, ParentEnv: []string{"PATH=" + os.Getenv("PATH"), "SECRET=hunter2"},
		Timeout: timeout, KillAfter: 3 * time.Second, OutDir: filepath.Join(ws, ".out"), Sandbox: &sb}
	start := time.Now()
	r := Run(ctx, spec)
	e := e2eRun{Result: r, took: time.Since(start)}
	if r.StdoutPath != "" {
		b, _ := os.ReadFile(r.StdoutPath)
		e.out = string(b)
		b, _ = os.ReadFile(r.LogPath)
		e.log = string(b)
	}
	t.Logf("%s: outcome=%s exit=%d signal=%q reason=%q err=%v took=%s\nstdout:\n%s", name, r.Outcome, r.ExitCode, r.Signal, r.Reason, r.Err, e.took.Round(time.Millisecond), e.out)
	return e
}

func failedUnits(t *testing.T) string {
	t.Helper()
	b, _ := exec.Command("systemctl", "--failed", "--no-legend").Output()
	var mine []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, "wwd-j_4d3") {
			mine = append(mine, l)
		}
	}
	return strings.Join(mine, "\n")
}

func shOut(script string) (string, error) {
	b, err := exec.Command("sh", "-c", script).CombinedOutput()
	return string(b), err
}

func TestMServ(t *testing.T) {
	bg := context.Background()
	t.Run("A_basic", func(t *testing.T) {
		r := runSB(t, "A", Sandbox{}, 60*time.Second, bg, "sh", "-c", "id -un; pwd; cat /proc/self/cgroup; env | sort")
		if r.Outcome != Exited || r.ExitCode != 0 {
			t.Fatalf("result %+v", r.Result)
		}
		for _, want := range []string{"WWD_JOB=j_4d3-A", "wwd.slice", "wwd-j_4d3-A.service"} {
			if !strings.Contains(r.out, want) {
				t.Errorf("missing %q", want)
			}
		}
		if strings.Contains(r.out, "SECRET") || strings.Contains(r.out, "hunter2") {
			t.Error("the daemon's environment reached the job")
		}
		if f := failedUnits(t); f != "" {
			t.Errorf("failed unit left: %s", f)
		}
	})
	t.Run("B_exit_and_streams", func(t *testing.T) {
		r := runSB(t, "B", Sandbox{}, 60*time.Second, bg, "sh", "-c", "echo to-out; echo to-err >&2; exit 3")
		if r.Outcome != Exited || r.ExitCode != 3 || strings.Contains(r.out, "to-err") || !strings.Contains(r.log, "to-err") {
			t.Fatalf("result %+v out %q log %q", r.Result, r.out, r.log)
		}
	})
	t.Run("C_deadline_and_fuse", func(t *testing.T) {
		r := runSB(t, "C1", Sandbox{}, 3*time.Second, bg, "sleep", "60")
		if r.Outcome != TimedOut || r.Err != nil || r.took > 15*time.Second {
			t.Fatalf("deadline: %+v took %s", r.Result, r.took)
		}
		// the RuntimeMaxSec fuse itself, on a real systemd: the unit's Result must read as a timeout
		id := "wwd-j_4d3-C2"
		t.Cleanup(func() { _ = exec.Command("sudo", "systemctl", "reset-failed", id+".service").Run() })
		out, _ := shOut("sudo systemd-run --wait --pipe --unit=" + id + " --slice=wwd.slice -p RuntimeMaxSec=3 sleep 60")
		t.Logf("systemd-run said: %s", out)
		sb := Sandbox{}.withDefaults()
		show, err := sb.show(id)
		o, _, _, reason, cerr := classify(show, stopNone)
		t.Logf("fuse: show=%v err=%v -> %s %q %v", show, err, o, reason, cerr)
		if o != TimedOut || reason != "timeout" {
			t.Errorf("a RuntimeMaxSec expiry must classify as TimedOut/timeout, got %s %q", o, reason)
		}
	})
	t.Run("D_oom", func(t *testing.T) {
		r := runSB(t, "D", Sandbox{MemoryMax: "64M"}, 60*time.Second, bg, "python3", "-c", "x = b'x' * (300 << 20); print(len(x))")
		if r.Outcome != Exited || r.Reason != "oom" || r.took > 5*time.Second {
			t.Fatalf("result %+v took %s", r.Result, r.took)
		}
	})
	t.Run("E_docker_socket", func(t *testing.T) {
		probe := "test -S /run/docker.sock && echo SOCKET_VISIBLE; docker ps >/dev/null 2>&1 && echo DOCKER_WORKS; " +
			"curl -sS --max-time 3 --unix-socket /run/docker.sock http://localhost/version >/dev/null 2>&1 && echo CURL_WORKS; echo done"
		r := runSB(t, "E", Sandbox{}, 60*time.Second, bg, "sh", "-c", probe)
		for _, bad := range []string{"SOCKET_VISIBLE", "DOCKER_WORKS", "CURL_WORKS"} {
			if strings.Contains(r.out, bad) {
				t.Errorf("sandbox: %s", bad)
			}
		}
		// the control: the same probe without the hidden path must reach it, or the probe proves nothing
		out, _ := shOut("sudo systemd-run --wait --pipe --unit=wwd-j_4d3-E2 --slice=wwd.slice -p PrivateTmp=yes sh -c '" + probe + "'")
		t.Cleanup(func() { _ = exec.Command("sudo", "systemctl", "reset-failed", "wwd-j_4d3-E2.service").Run() })
		t.Logf("control (socket not hidden): %s", out)
		if !strings.Contains(out, "SOCKET_VISIBLE") {
			t.Errorf("the control did not see the socket: the probe is not valid on this host")
		}
	})
	t.Run("F_writes", func(t *testing.T) {
		_ = os.Remove(filepath.Join(os.Getenv("HOME"), "4d3-home"))
		_ = os.Remove("/tmp/4d3-private")
		r := runSB(t, "F", Sandbox{}, 60*time.Second, bg, "sh", "-c",
			"touch ./4d3-ws && echo WS_OK; touch "+filepath.Join(os.Getenv("HOME"), "4d3-home")+" 2>/dev/null && echo HOME_WRITABLE; "+
				"touch /tmp/4d3-private && echo TMP_OK; sudo -n true 2>/dev/null && echo SUDO_WORKS; echo done")
		if !strings.Contains(r.out, "WS_OK") || !strings.Contains(r.out, "TMP_OK") || strings.Contains(r.out, "HOME_WRITABLE") || strings.Contains(r.out, "SUDO_WORKS") {
			t.Errorf("writes: %q", r.out)
		}
		if _, err := os.Stat("/tmp/4d3-private"); err == nil {
			t.Error("/tmp is not private: the job's file is on the host")
		}
	})
	t.Run("G_protectsystem_strict", func(t *testing.T) {
		// paths outside the home directory that this user can write on mserv (found with find -writable): /var/crash and
		// /var/log/installer by group, /var/tmp by the sticky bit (PrivateTmp covers /var/tmp too: a write there is private, not
		// denied). /opt and /usr/local hold nothing this user owns, and /srv/backups is real data: neither is probed.
		script := "for p in /var/tmp/4d3-x /var/crash/4d3-x /var/log/installer/4d3-x; do if touch $p 2>/dev/null; then echo WROTE:$p; rm -f $p; else echo DENIED:$p; fi; done; php -v | head -1"
		for _, extra := range [][]string{nil, {"ProtectSystem=strict"}} {
			name := "G-" + map[bool]string{false: "without", true: "with"}[extra != nil]
			r := runSB(t, name, Sandbox{ExtraProperties: extra}, 120*time.Second, bg, "sh", "-c", script)
			t.Logf("%s: %s", name, r.out)
		}
		// a small composer project: install time and a passing test, without and with the property
		// composer is not installed on mserv: its phar is fetched into the workspace (the cache then starts empty, as it will for a job)
		proj := `s=$(date +%s)
curl -sS --max-time 60 -o composer.phar https://getcomposer.org/download/latest-stable/composer.phar &&
mkdir -p p/tests && cd p &&
cat > composer.json <<'EOF'
{"require-dev":{"phpunit/phpunit":"^11"}}
EOF
cat > tests/T.php <<'EOF'
<?php
class T extends PHPUnit\Framework\TestCase { public function testA(): void { $this->assertTrue(true); } }
EOF
php ../composer.phar install --no-interaction --no-progress 2>&1 | tail -2
echo INSTALL_SECONDS=$(( $(date +%s) - s ))
vendor/bin/phpunit tests 2>&1 | tail -4`
		for _, extra := range [][]string{nil, {"ProtectSystem=strict"}} {
			name := "Gc-" + map[bool]string{false: "without", true: "with"}[extra != nil]
			r := runSB(t, name, Sandbox{ExtraProperties: extra}, 300*time.Second, bg, "sh", "-c", proj)
			if r.Outcome != Exited || r.ExitCode != 0 || !strings.Contains(r.out, "OK (1 test") {
				t.Errorf("%s: composer/phpunit did not pass: %+v\n%s", name, r.Result, r.out)
			}
		}
	})
	t.Run("H_cancel_term_then_kill", func(t *testing.T) {
		ctx, cancel := context.WithCancel(bg)
		go func() { time.Sleep(2 * time.Second); cancel() }()
		r := runSB(t, "H", Sandbox{}, 0, ctx, "sh", "-c", "trap '' TERM; while :; do sleep 1; done")
		if r.Outcome != Cancelled || r.Err != nil || r.took < 4*time.Second || r.took > 15*time.Second {
			t.Fatalf("result %+v took %s (TERM is ignored: KILL must follow after the 3 s grace)", r.Result, r.took)
		}
	})
	t.Run("I_same_name_old_failed_unit", func(t *testing.T) {
		id := "wwd-j_4d3-I"
		t.Cleanup(func() { _ = exec.Command("sudo", "systemctl", "reset-failed", id+".service").Run() })
		_, _ = shOut("sudo systemd-run --wait --pipe --unit=" + id + " --slice=wwd.slice sh -c 'exit 1'")
		r := runSB(t, "I", Sandbox{}, 60*time.Second, bg, "true")
		if r.Outcome != Exited || r.ExitCode != 0 {
			t.Fatalf("an old failed unit's Result was read: %+v", r.Result)
		}
	})
	t.Run("Z_nothing_left", func(t *testing.T) {
		if f := failedUnits(t); f != "" {
			t.Errorf("failed wwd-j_4d3 units left:\n%s", f)
		}
	})
}
