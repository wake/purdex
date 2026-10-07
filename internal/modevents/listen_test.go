package modevents

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// shortDir is a 0700 directory short enough for a socket path (t.TempDir
// on macOS is not).
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pdxm-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o700)
		_ = os.RemoveAll(dir)
	})
	return dir
}

func sockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(shortDir(t), SocketName)
}

// staleSocket leaves a socket file nobody listens on: bound, listening,
// then its fd closed without an unlink (what an exec-self that skipped
// Stop leaves behind; the kernel closes close-on-exec fds).
func staleSocket(t *testing.T, path string) {
	t.Helper()
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		t.Fatal(err)
	}
	if err := unix.Listen(fd, 1); err != nil {
		t.Fatal(err)
	}
	if err := unix.Close(fd); err != nil {
		t.Fatal(err)
	}
}

func unixClient(t *testing.T, path string) *http.Client {
	t.Helper()
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

// serve runs h on l until the test ends.
func serve(t *testing.T, l net.Listener, h http.Handler) {
	t.Helper()
	srv := NewServer(h)
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(l)
		close(done)
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		<-done
	})
}

func mustListen(t *testing.T, path string) net.Listener {
	t.Helper()
	l, st := Listen(path)
	if !st.Enabled || l == nil {
		t.Fatalf("Listen(%s) = %+v", path, st)
	}
	return l
}

func isSocket(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSocket != 0
}

func dialable(path string) bool {
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func TestListen_CreatesSocket0600(t *testing.T) {
	p := sockPath(t)
	l := mustListen(t, p)
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want a 0600 socket", fi.Mode())
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("a second Close must be a no-op: %v", err)
	}
	if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Close must unlink the socket: %v", err)
	}
}

func TestListen_RemovesStaleSocket(t *testing.T) {
	p := sockPath(t)
	ln, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if !isSocket(p) || dialable(p) {
		t.Fatal("setup: want a dead socket file")
	}
	l := mustListen(t, p)
	defer l.Close()
	if !dialable(p) {
		t.Fatal("the stale socket must be replaced by a live one")
	}
}

func TestListen_StaleAfterUnclosedListener(t *testing.T) {
	p := sockPath(t)
	staleSocket(t, p)
	if !isSocket(p) || dialable(p) {
		t.Fatal("setup: want a dead socket file")
	}
	l := mustListen(t, p)
	defer l.Close()
	if !dialable(p) {
		t.Fatal("the file a closed fd left must be replaced")
	}

	// That is the only thing an exec-self that skipped Stop can leave:
	// the listener's fd is close-on-exec, so the new image holds nothing.
	rc, err := l.(*peerListener).Listener.(*net.UnixListener).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var flags int
	var ferr error
	if err := rc.Control(func(fd uintptr) { flags, ferr = unix.FcntlInt(fd, unix.F_GETFD, 0) }); err != nil || ferr != nil {
		t.Fatal(err, ferr)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("the listener fd must be close-on-exec")
	}
}

func TestListen_InUseLeavesItAlone(t *testing.T) {
	p := sockPath(t)
	first := mustListen(t, p)
	defer first.Close()
	l, st := Listen(p)
	if l != nil || st.Enabled || st.Reason != ReasonInUse {
		t.Fatalf("second Listen = %v, %+v; want in_use", l, st)
	}
	if !dialable(p) {
		t.Fatal("the live socket must be left alone")
	}
}

func TestListen_RefusesNonSocket(t *testing.T) {
	p := sockPath(t)
	if err := os.WriteFile(p, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, st := Listen(p)
	if l != nil || st.Enabled || st.Reason != ReasonNotASocket {
		t.Fatalf("Listen = %v, %+v; want not_a_socket", l, st)
	}
	if b, err := os.ReadFile(p); err != nil || string(b) != "keep me" {
		t.Fatalf("a regular file at the path must never be removed: %q, %v", b, err)
	}

	// A symlink, even to a dead socket, is not a socket either.
	q := filepath.Join(filepath.Dir(p), "other.sock")
	staleSocket(t, q)
	link := filepath.Join(filepath.Dir(p), "link.sock")
	if err := os.Symlink(q, link); err != nil {
		t.Fatal(err)
	}
	if _, st := Listen(link); st.Reason != ReasonNotASocket {
		t.Fatalf("symlink: %+v", st)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("the symlink must stay")
	}
}

func TestListen_UnsafeDir(t *testing.T) {
	for _, mode := range []os.FileMode{0o777, 0o720, 0o702} {
		p := sockPath(t)
		dir := filepath.Dir(p)
		staleSocket(t, p)
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		l, st := Listen(p)
		if l != nil || st.Enabled || st.Reason != ReasonUnsafeDir {
			t.Fatalf("dir %v: Listen = %v, %+v; want unsafe_dir", mode, l, st)
		}
		if !isSocket(p) || dialable(p) {
			t.Fatalf("dir %v: nothing may be removed or created", mode)
		}
		ents, _ := os.ReadDir(dir)
		if len(ents) != 1 {
			t.Fatalf("dir %v: %d entries", mode, len(ents))
		}
	}

	// A symlinked data dir is checked where it points.
	target := shortDir(t)
	if err := os.Chmod(target, 0o777); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(shortDir(t), "data")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, st := Listen(filepath.Join(link, SocketName)); st.Reason != ReasonUnsafeDir {
		t.Fatalf("symlink to an unsafe dir: %+v", st)
	}

	// A missing dir cannot be listened in.
	if _, st := Listen(filepath.Join(shortDir(t), "missing", SocketName)); st.Reason != ReasonListenFailed {
		t.Fatalf("missing dir: %+v", st)
	}
	// Too long a path is refused before anything is looked at.
	if _, st := Listen("/tmp/" + strings.Repeat("a", 120) + "/mod.sock"); st.Reason != ReasonPathTooLong {
		t.Fatalf("long path: %+v", st)
	}
	// So is a short path whose resolved form is too long.
	long := filepath.Join(shortDir(t), strings.Repeat("d", 90))
	if err := os.Mkdir(long, 0o700); err != nil {
		t.Fatal(err)
	}
	short := filepath.Join(shortDir(t), "l")
	if err := os.Symlink(long, short); err != nil {
		t.Fatal(err)
	}
	if p := filepath.Join(short, SocketName); len(p) > MaxSocketPath {
		t.Fatalf("setup: %s is already too long", p)
	}
	if _, st := Listen(filepath.Join(short, SocketName)); st.Reason != ReasonPathTooLong {
		t.Fatalf("long resolved path: %+v", st)
	}
	if ents, _ := os.ReadDir(long); len(ents) != 0 {
		t.Fatalf("long resolved path: %d entries", len(ents))
	}
}

// nestedDir makes <shortDir>/a/b, with b (0700) the socket dir and a set
// to aMode, and returns b.
func nestedDir(t *testing.T, aMode os.FileMode) string {
	t.Helper()
	a := filepath.Join(shortDir(t), "a")
	b := filepath.Join(a, "b")
	if err := os.MkdirAll(b, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(a, aMode); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(a)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode() & (os.ModePerm | os.ModeSticky); got != aMode {
		t.Fatalf("setup: %s is %v, want %v", a, got, aMode)
	}
	return b
}

func TestListen_UnsafeAncestor(t *testing.T) {
	// Whoever can write to a can rename b away and put their own in its
	// place, however safe b itself is.
	b := nestedDir(t, 0o777)
	l, st := Listen(filepath.Join(b, SocketName))
	if l != nil || st.Enabled || st.Reason != ReasonUnsafeDir {
		t.Fatalf("Listen = %v, %+v; want unsafe_dir", l, st)
	}
	if ents, _ := os.ReadDir(b); len(ents) != 0 {
		t.Fatalf("nothing may be created: %d entries", len(ents))
	}
}

func TestListen_StickyAncestorIsFine(t *testing.T) {
	// A sticky ancestor (like /tmp itself) only lets b's owner rename it.
	b := nestedDir(t, 0o777|os.ModeSticky)
	p := filepath.Join(b, SocketName)
	l := mustListen(t, p)
	defer l.Close()
	if !dialable(p) {
		t.Fatal("the socket must be live")
	}
}

func TestListen_SymlinkedDirIsResolved(t *testing.T) {
	target := shortDir(t)
	link := filepath.Join(shortDir(t), "data")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(resolved, SocketName)

	l := mustListen(t, filepath.Join(link, SocketName))
	if !isSocket(want) {
		t.Fatalf("no socket at %s", want)
	}
	if got := l.Addr().String(); got != want {
		t.Fatalf("bound at %s, want %s", got, want)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink must stay: %v, %v", fi, err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(want); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Close must unlink the socket in the resolved dir: %v", err)
	}
}

// expectRefused dials path and checks the daemon closes the connection
// without answering.
func expectRefused(t *testing.T, path string) {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.Write([]byte("POST /mod/v1/events HTTP/1.1\r\nHost: pdx\r\nContent-Length: 2\r\n\r\n{}"))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	var ne net.Error
	if n != 0 || err == nil || (errors.As(err, &ne) && ne.Timeout()) {
		t.Fatalf("read = %d %q, %v; want the connection closed unanswered", n, buf[:n], err)
	}
}

func TestListen_RejectsOtherUID(t *testing.T) {
	euid := uint32(os.Geteuid())
	var uid atomic.Uint32
	var credErr atomic.Bool
	uid.Store(euid + 1)
	orig := peerUID
	peerUID = func(net.Conn) (uint32, error) {
		if credErr.Load() {
			return 0, errors.New("no credentials")
		}
		return uid.Load(), nil
	}
	t.Cleanup(func() { peerUID = orig })

	p := sockPath(t)
	l := mustListen(t, p)
	var calls atomic.Int32
	serve(t, l, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("served"))
	}))

	expectRefused(t, p) // another uid
	credErr.Store(true)
	uid.Store(euid)
	expectRefused(t, p) // credentials unreadable
	if n := calls.Load(); n != 0 {
		t.Fatalf("the handler ran %d times for refused peers", n)
	}

	credErr.Store(false)
	res, err := unixClient(t, p).Post("http://pdx/x", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("the same uid must be served: %d, calls %d", res.StatusCode, calls.Load())
	}
}
