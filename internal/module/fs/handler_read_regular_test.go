package fs

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// postReadWithTimeout runs handleRead in a goroutine so a blocking handler
// fails the test instead of hanging it. A goroutine stuck in open(2) on a FIFO
// is released by unblock (opening the FIFO O_RDWR) so it does not leak.
func postReadWithTimeout(t *testing.T, m *FsModule, path string, unblock func()) *httptest.ResponseRecorder {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- postJSON(m.handleRead, map[string]string{"path": path}) }()
	select {
	case w := <-done:
		return w
	case <-time.After(2 * time.Second):
		if unblock != nil {
			unblock()
		}
		t.Fatalf("handleRead blocked on %s (>2s)", path)
		return nil
	}
}

func unblockFifo(path string) func() {
	return func() {
		if f, err := os.OpenFile(path, os.O_RDWR, 0); err == nil {
			f.Close()
		}
	}
}

func TestHandleReadRejectsFifo(t *testing.T) {
	m, dir := setupTestModule(t)
	p := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	w := postReadWithTimeout(t, m, p, unblockFifo(p))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleReadRejectsSymlinkToFifo(t *testing.T) {
	m, dir := setupTestModule(t)
	p := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	link := filepath.Join(dir, "pic.png")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	w := postReadWithTimeout(t, m, link, unblockFifo(p))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleReadRejectsCharDevice(t *testing.T) {
	m, _ := setupTestModule(t)
	w := postReadWithTimeout(t, m, "/dev/null", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleReadRegularEmptyMissingRelative(t *testing.T) {
	m, dir := setupTestModule(t)
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := postReadWithTimeout(t, m, p, nil)
	if w.Code != http.StatusOK || w.Body.String() != "hello" {
		t.Fatalf("regular: %d %q", w.Code, w.Body.String())
	}
	e := filepath.Join(dir, "empty")
	if err := os.WriteFile(e, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	w = postReadWithTimeout(t, m, e, nil)
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("empty: %d %q", w.Code, w.Body.String())
	}
	w = postReadWithTimeout(t, m, filepath.Join(dir, "nope"), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", w.Code)
	}
	w = postReadWithTimeout(t, m, "relative/x", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("relative: %d", w.Code)
	}
}
