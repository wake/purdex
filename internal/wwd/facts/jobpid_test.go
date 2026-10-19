package facts

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestJobPIDHelper is not a test: it is the process the tests below start (the test binary itself, which is not a hardened Apple
// binary, so the kernel shows its environment), and it only waits to be killed.
func TestJobPIDHelper(t *testing.T) {
	if os.Getenv("FACTS_JOBPID_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

func startHelper(t *testing.T, env ...string) *exec.Cmd {
	t.Helper()
	c := exec.Command(os.Args[0], "-test.run=^TestJobPIDHelper$")
	c.Env = append([]string{"PATH=" + os.Getenv("PATH"), "FACTS_JOBPID_HELPER=1"}, env...)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	return c
}

// waitJob polls for pid's mark: a process that has only just started shows its environment once it has exec'd.
func waitJob(pid int, want string) (string, bool) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		id, ok := JobOfPID(pid)
		if (ok && id == want) || time.Now().After(deadline) {
			return id, ok
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// JobOfPID is the watcher's way to the one shared check: it reads the process's environment and answers JobFromEnv. A process started
// with the mark carries it; one without does not; a value that does not look like a job id is not a mark; a pid that is not a process
// answers no.
func TestJobOfPIDReadsTheMarkOfAProcess(t *testing.T) {
	marked := startHelper(t, "WWD_JOB=j_abc123")
	plain := startHelper(t, "FOO=bar")
	badMark := startHelper(t, "WWD_JOB=not-a-job-id")

	if id, ok := waitJob(marked.Process.Pid, "j_abc123"); !ok || id != "j_abc123" {
		t.Fatalf("marked process: %q %v", id, ok)
	}
	// give the others the same time to be exec'd before saying they have no mark
	if id, ok := waitJob(plain.Process.Pid, "never"); ok {
		t.Fatalf("a process without the mark answered %q", id)
	}
	if id, ok := JobOfPID(badMark.Process.Pid); ok {
		t.Fatalf("a value that is not a job id is not a mark: %q", id)
	}
	if _, ok := JobOfPID(0); ok {
		t.Fatal("pid 0 answered")
	}
	if _, ok := JobOfPID(-5); ok {
		t.Fatal("a negative pid answered")
	}
}
