package agent_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/agent"
)

func testProcessInfoReadsCurrentProcess(t *testing.T) {
	t.Helper()

	info, err := agent.ReadProcessInfo(os.Getpid())
	if err != nil {
		t.Fatalf("ReadProcessInfo(current): %v", err)
	}
	if info.PID != os.Getpid() {
		t.Fatalf("PID = %d, want %d", info.PID, os.Getpid())
	}
	if info.PPID <= 0 {
		t.Fatalf("PPID = %d, want > 0", info.PPID)
	}
	if info.ExePath == "" {
		t.Fatal("ExePath should not be empty")
	}
	if !filepath.IsAbs(info.ExePath) {
		t.Fatalf("ExePath = %q, want absolute path", info.ExePath)
	}
	if len(info.Argv) == 0 {
		t.Fatal("Argv should not be empty")
	}
	if info.StartTime.IsZero() {
		t.Fatal("StartTime should not be zero")
	}
}

func testProcessInfoPreservesSymlinkInvocationPath(t *testing.T) {
	t.Helper()

	if os.Getenv("GO_WANT_PROCESS_INFO_HELPER") == "1" {
		time.Sleep(5 * time.Second)
		os.Exit(0)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	linkPath := filepath.Join(t.TempDir(), "process-info-helper")
	if err := os.Symlink(exe, linkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	cmd := exec.Command(linkPath, "-test.run=TestProcessInfoSymlinkHelper")
	cmd.Env = append(os.Environ(), "GO_WANT_PROCESS_INFO_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start helper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		info, err := agent.ReadProcessInfo(cmd.Process.Pid)
		if err == nil {
			if info.ExePath != linkPath {
				t.Fatalf("ExePath = %q, want %q (symlink invocation path preserved)", info.ExePath, linkPath)
			}
			if len(info.Argv) == 0 {
				t.Fatal("Argv should not be empty for helper process")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ReadProcessInfo(helper): %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestProcessInfoSymlinkHelper(t *testing.T) {
	if os.Getenv("GO_WANT_PROCESS_INFO_HELPER") != "1" {
		return
	}
	time.Sleep(5 * time.Second)
}

func testProcessInfoMissingProcess(t *testing.T) {
	t.Helper()

	if _, err := agent.ReadProcessInfo(999999); err == nil {
		t.Fatal("ReadProcessInfo should fail for missing process")
	}
}
