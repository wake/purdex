package main

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// WWD-9 9a-1: `pdx setup --agent cc` takes the shims of the retired gate out of ~/.config/pdx/shims. The tests run the real
// entry (runSetup, in a process of its own: it exits), on both of its paths: the daemon answers, and it does not.

// TestSetupHelperProcess is runSetup in a child process; it does nothing in the test run itself.
func TestSetupHelperProcess(t *testing.T) {
	if os.Getenv("PDX_SETUP_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	// the test main of this package puts a home of its own in HOME (gatetest): the home of this run is the one the test made
	if err := os.Setenv("HOME", os.Getenv("PDX_SETUP_HOME")); err != nil {
		panic(err)
	}
	runSetup(args)
	os.Exit(0)
}

// setupEnv is a home whose config names the daemon at host:port and no other (the real daemon of the machine this runs on is never reached).
func setupEnv(t *testing.T, port int) (home string) {
	t.Helper()
	home = t.TempDir()
	cfgDir := filepath.Join(home, ".config", "pdx")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf("bind = \"127.0.0.1\"\nport = %d\n", port)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// daemonThatAnswers is a daemon: no relay under way, and the setup of the hooks done.
func daemonThatAnswers(t *testing.T) (port int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/team/inflight":
			_, _ = w.Write([]byte(`{"approvals_open":0,"relays_active":0,"relays":[]}`))
		case "/api/hooks/cc/setup":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			t.Errorf("the setup asked %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

// runPdxSetup runs `pdx setup <args>` with HOME = home and returns its exit code and streams.
func runPdxSetup(t *testing.T, home string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestSetupHelperProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "HOME="+home, "PDX_SETUP_HOME="+home, "PDX_SETUP_HELPER=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return code, out.String(), errb.String()
}
