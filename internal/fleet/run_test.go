package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as the fake ssh: when FLEET_FAKE_SSH names a spec file the process answers like ssh would.
type fakeSpec struct {
	Stdout   string
	Stderr   string
	Exit     int
	Sleep    bool   // hang until killed
	Flood    int    // write this many bytes of stdout
	ArgvFile string // where to record os.Args[1:]
}

func TestMain(m *testing.M) {
	if path := os.Getenv("FLEET_FAKE_SSH"); path != "" {
		raw, _ := os.ReadFile(path)
		var s fakeSpec
		_ = json.Unmarshal(raw, &s)
		if s.ArgvFile != "" {
			b, _ := json.Marshal(os.Args[1:])
			_ = os.WriteFile(s.ArgvFile, b, 0o600)
		}
		if s.Sleep {
			time.Sleep(time.Minute)
		}
		fmt.Fprint(os.Stdout, s.Stdout)
		if s.Flood > 0 {
			chunk := strings.Repeat("x", 4096)
			for n := 0; n < s.Flood; n += len(chunk) {
				fmt.Fprint(os.Stdout, chunk)
			}
		}
		fmt.Fprint(os.Stderr, s.Stderr)
		os.Exit(s.Exit)
	}
	os.Exit(m.Run())
}

func fakeRunner(t *testing.T, s fakeSpec) Runner {
	t.Helper()
	path := t.TempDir() + "/spec.json"
	b, _ := json.Marshal(s)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_FAKE_SSH", path)
	self, _ := os.Executable()
	return Runner{LookPath: func(string) (string, error) { return self, nil }, Timeout: 10 * time.Second}
}

func TestRun_ArgvIsFixedAndAliasFollowsDashDash(t *testing.T) {
	argv := t.TempDir() + "/argv.json"
	r := fakeRunner(t, fakeSpec{Stdout: linuxOut, ArgvFile: argv})
	res := r.Run(context.Background(), "web-1")
	if res.Status != StatusOK || res.Reading.NCPU != 4 {
		t.Fatalf("got %+v", res)
	}
	var got []string
	b, _ := os.ReadFile(argv)
	_ = json.Unmarshal(b, &got)
	want := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=yes", "--", "web-1", Script}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
}

func TestRun_AliasWithShellMetacharactersNeverReachesSSH(t *testing.T) {
	argv := t.TempDir() + "/argv.json"
	r := fakeRunner(t, fakeSpec{Stdout: linuxOut, ArgvFile: argv})
	for _, alias := range []string{"a;rm", "a b", "$(x)", "", "-oProxyCommand=x;", strings.Repeat("a", 65), "a\nb"} {
		os.Remove(argv)
		res := r.Run(context.Background(), alias)
		if res.Status != StatusError || res.Code != CodeAliasGone {
			t.Errorf("%q: got %+v", alias, res)
		}
		if _, err := os.Stat(argv); err == nil {
			t.Errorf("%q: ssh was started", alias)
		}
	}
}

func TestRun_LeadingDashAliasIsAfterDashDash(t *testing.T) {
	// "-x" passes the alias rule's character set; it is safe only because it comes after `--`.
	args := Args("-x")
	for i, a := range args {
		if a == "--" && args[i+1] == "-x" {
			return
		}
	}
	t.Fatalf("alias not after --: %q", args)
}

func TestRun_DarwinOK(t *testing.T) {
	res := fakeRunner(t, fakeSpec{Stdout: darwinOut}).Run(context.Background(), "mac")
	if res.Status != StatusOK || res.Code != CodeNone || res.Reading.OS != "Darwin" {
		t.Fatalf("got %+v", res)
	}
}

// A stderr full of things that must never reach an API payload.
const sensitiveStderr = "ssh: connect to host secret-host.corp.example port 22: Connection refused\n" +
	"wake@secret-host.corp.example: Permission denied (publickey).\n/Users/wake/.ssh/id_ed25519 not accessible\n"

func TestRun_Classification(t *testing.T) {
	cases := []struct {
		name   string
		spec   fakeSpec
		status Status
		code   Code
	}{
		{"host key", fakeSpec{Exit: 255, Stderr: "Host key verification failed.\n"}, StatusError, CodeHostKey},
		{"host key changed", fakeSpec{Exit: 255, Stderr: "@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @\n"}, StatusError, CodeHostKey},
		{"auth", fakeSpec{Exit: 255, Stderr: sensitiveStderr}, StatusError, CodeAuth},
		{"unreachable", fakeSpec{Exit: 255, Stderr: "ssh: connect to host secret-host.corp.example port 22: Operation timed out\n"}, StatusOffline, CodeUnreachable},
		{"remote exit", fakeSpec{Exit: 3, Stderr: sensitiveStderr}, StatusError, CodeRemoteExit},
		{"parse: no end marker", fakeSpec{Stdout: strings.Replace(linuxOut, "@@end\n", "", 1)}, StatusError, CodeParse},
		{"parse: garbage", fakeSpec{Stdout: "hello"}, StatusError, CodeParse},
		{"timeout", fakeSpec{Sleep: true}, StatusOffline, CodeTimeout},
		{"over 64 KiB", fakeSpec{Stdout: linuxOut, Flood: 80 << 10}, StatusError, CodeParse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := fakeRunner(t, tc.spec)
			r.Timeout = 10 * time.Second // a race-built fake can take over a second to start
			if tc.spec.Sleep {
				r.Timeout = 700 * time.Millisecond
			}
			res := r.Run(context.Background(), "box")
			if res.Status != tc.status || res.Code != tc.code {
				t.Fatalf("got %s/%s, want %s/%s", res.Status, res.Code, tc.status, tc.code)
			}
		})
	}
}

func TestRun_StderrOnlyInResultStderr(t *testing.T) {
	res := fakeRunner(t, fakeSpec{Exit: 255, Stderr: sensitiveStderr}).Run(context.Background(), "box")
	if res.Code != CodeAuth {
		t.Fatalf("got %+v", res)
	}
	for _, secret := range []string{"wake", "secret-host", "/Users", "id_ed25519", "Permission"} {
		if strings.Contains(string(res.Code), secret) || strings.Contains(string(res.Status), secret) {
			t.Errorf("%q leaked into status or code", secret)
		}
	}
	if !strings.Contains(res.Stderr, "secret-host") {
		t.Error("stderr should be kept for the daemon log")
	}
}

func TestRun_StderrCapped(t *testing.T) {
	res := fakeRunner(t, fakeSpec{Exit: 255, Stderr: strings.Repeat("e", 10<<10)}).Run(context.Background(), "box")
	if len(res.Stderr) != MaxStderr {
		t.Fatalf("stderr len %d", len(res.Stderr))
	}
}

func TestRun_MissingSSHAndCancel(t *testing.T) {
	r := Runner{LookPath: func(string) (string, error) { return "", os.ErrNotExist }}
	if res := r.Run(context.Background(), "box"); res.Status != StatusOffline || res.Code != CodeUnreachable {
		t.Fatalf("no ssh: %+v", res)
	}
	r = fakeRunner(t, fakeSpec{Sleep: true})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	if res := r.Run(ctx, "box"); res.Status != StatusOffline || res.Code != CodeUnreachable {
		t.Fatalf("cancel: %+v", res)
	}
}
