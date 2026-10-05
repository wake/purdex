package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// These tests own their fds as raw ints (syscall.Open) and close them once
// themselves: an fd wrapped by two *os.File values is closed twice by their
// finalizers, which turns the suite flaky.

func rawLockedPidFile(t *testing.T) (path string, fd int) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "pdx.pid")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	syscall.CloseOnExec(fd)
	return path, fd
}

func getFD(t *testing.T, fd int) uintptr {
	t.Helper()
	r, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	return r
}

func TestPidLockEnvEntry_NamesFDWithoutTouchingFlags(t *testing.T) {
	_, fd := rawLockedPidFile(t)
	f := os.NewFile(uintptr(fd), "pid") // sole owner; its Close releases the fd
	defer f.Close()
	if want := pidLockFDEnv + "=" + strconv.Itoa(fd); pidLockEnvEntry(f) != want {
		t.Fatalf("entry = %q, want %q", pidLockEnvEntry(f), want)
	}
	if got := getFD(t, fd); got&syscall.FD_CLOEXEC == 0 {
		t.Fatal("pidLockEnvEntry cleared close-on-exec: it must be side-effect free")
	}
}

func TestClearCloseOnExec_LeavesFlagsZero(t *testing.T) {
	_, fd := rawLockedPidFile(t)
	f := os.NewFile(uintptr(fd), "pid") // sole owner
	defer f.Close()
	if err := clearCloseOnExec(f); err != nil {
		t.Fatal(err)
	}
	if got := getFD(t, fd); got != 0 {
		t.Fatalf("F_GETFD = %d, want 0: the fd would not survive exec", got)
	}
}

// H2: the lock is held by a different open file description, so adopt's
// LOCK_EX|LOCK_NB must fail with EWOULDBLOCK; it closes the fd it wrapped
// and leaves the holder's lock alone.
func TestAdoptPidLock_RejectsLockHeldByAnotherDescription(t *testing.T) {
	path, holder := rawLockedPidFile(t)
	defer syscall.Close(holder)
	other, err := syscall.Open(path, syscall.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	// adoptPidLock owns other from here on, success or not.
	if f, err := adoptPidLock(strconv.Itoa(other), path, 1); err == nil {
		f.Close()
		t.Fatal("adopted a lock held by another open file description")
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(other), syscall.F_GETFD, 0); errno != syscall.EBADF {
		t.Fatalf("rejected fd still open (errno %v)", errno)
	}
	if running, _ := isDaemonRunning(path); !running {
		t.Fatal("holder's lock was lost")
	}
}

func TestAdoptPidLock_AdoptsHeldLock(t *testing.T) {
	path, fd := rawLockedPidFile(t)
	f, err := adoptPidLock(strconv.Itoa(fd), path, 4242)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() // f owns fd now
	if got := getFD(t, int(f.Fd())); got&syscall.FD_CLOEXEC == 0 {
		t.Fatal("FD_CLOEXEC not set again: children would inherit the lock")
	}
	data, _ := os.ReadFile(path)
	if strings.TrimSpace(string(data)) != "4242" {
		t.Fatalf("pid file = %q, want 4242", data)
	}
	if running, _ := isDaemonRunning(path); !running {
		t.Fatal("adopted lock is not held: isDaemonRunning says stopped")
	}
}

func TestAdoptPidLock_Rejects(t *testing.T) {
	path, fd := rawLockedPidFile(t)
	defer syscall.Close(fd)

	for name, v := range map[string]string{
		"non-numeric": "abc",
		"empty":       "",
		"negative":    "-1",
		"stdio fd":    "2",
		"fd not open": "9999",
	} {
		t.Run(name, func(t *testing.T) {
			if f, err := adoptPidLock(v, path, 1); err == nil {
				f.Close()
				t.Fatalf("adoptPidLock(%q) succeeded, want an error", v)
			}
		})
	}

	t.Run("other file", func(t *testing.T) {
		otherFD, err := syscall.Open(filepath.Join(t.TempDir(), "other.pid"), syscall.O_CREAT|syscall.O_RDWR, 0644)
		if err != nil {
			t.Fatal(err)
		}
		// A rejected fd is closed by adoptPidLock; do not close it here too.
		if f, err := adoptPidLock(strconv.Itoa(otherFD), path, 1); err == nil {
			f.Close()
			t.Fatal("adopted an fd open on a different file")
		}
		if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(otherFD), syscall.F_GETFD, 0); errno != syscall.EBADF {
			t.Fatalf("rejected fd still open (errno %v)", errno)
		}
	})
}

func TestCaptureReexecPlan_DropsPidLockFD(t *testing.T) {
	env := []string{"A=1", "PDX_PIDLOCK_FD=7", "B=2"}
	p, err := captureReexecPlan(func() (string, error) { return "/opt/pdx", nil }, []string{"pdx"}, env)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"A=1", "B=2"}; !reflect.DeepEqual(p.env, want) {
		t.Fatalf("env = %v, want %v", p.env, want)
	}
}
