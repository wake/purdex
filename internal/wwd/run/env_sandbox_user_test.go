package run

import (
	"context"
	"strings"
	"testing"
)

// The account a job's USER and LOGNAME name is the one the command runs as: with a sandbox that is `systemd-run --uid=<Sandbox.User>`, not
// the account of the daemon that builds the environment (#3919, review of #3924). Here the two differ.
func TestSandboxedJobCarriesTheSandboxAccountNotTheDaemons(t *testing.T) {
	withUser(t, "daemon-acct")
	w := newWorld(t)
	w.sb.User = "sandbox-acct"
	r := Run(context.Background(), sandboxSpec(t, w, "sh", "-c", "true"))
	if r.Outcome != Exited || r.ExitCode != 0 {
		t.Fatalf("result = %+v", r)
	}
	var run string
	for _, c := range w.calls(t) {
		if strings.Contains(c, "systemd-run") {
			run = c
			break
		}
	}
	if run == "" {
		t.Fatalf("no systemd-run call in %q", w.calls(t))
	}
	if !strings.Contains(run, "--uid=sandbox-acct") {
		t.Fatalf("the unit does not run as the sandbox account: %q", run)
	}
	for _, k := range []string{"USER", "LOGNAME"} {
		if !strings.Contains(run, " "+k+"=sandbox-acct ") {
			t.Errorf("%s is not sandbox-acct in %q", k, run)
		}
		if strings.Contains(run, k+"=daemon-acct") {
			t.Errorf("%s carries the daemon's account in %q", k, run)
		}
	}
}

// Without a sandbox the command runs as the account of this process, so that is the one named.
func TestPlainJobCarriesTheDaemonsAccount(t *testing.T) {
	withUser(t, "daemon-acct")
	env, err := buildEnv("daemon-acct", "j_a1", "", "/ws", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"USER", "LOGNAME"} {
		if v, _ := envValue(env, k); v != "daemon-acct" {
			t.Errorf("%s = %q", k, v)
		}
	}
	if got := accountOf(Spec{}); got != "daemon-acct" {
		t.Errorf("accountOf(no sandbox) = %q, want the process's account", got)
	}
	if got := accountOf(Spec{Sandbox: &Sandbox{User: "sandbox-acct"}}); got != "sandbox-acct" {
		t.Errorf("accountOf(sandbox) = %q, want the sandbox's account", got)
	}
}
