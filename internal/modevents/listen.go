package modevents

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Status says whether the channel is listening and, when not, why.
type Status struct {
	Enabled bool
	Reason  string
}

// Why the channel is disabled (spec §6.1); the read API reports them.
const (
	ReasonPathTooLong  = "path_too_long" // the socket path is longer than MaxSocketPath
	ReasonUnsafeDir    = "unsafe_dir"    // the directory is not ours alone
	ReasonInUse        = "in_use"        // another daemon answers on the socket
	ReasonNotASocket   = "not_a_socket"  // something else is at the path; never removed
	ReasonListenFailed = "listen_failed" // the directory is missing, or bind/chmod failed
)

// staleProbe is how long Listen waits for a socket already at the path to
// accept a connection before treating it as stale.
const staleProbe = 200 * time.Millisecond

// peerUID returns the uid of the process at the other end of a Unix
// socket connection (peercred_*.go). A var so tests can stand in for
// another user; Listen captures it.
var peerUID = connPeerUID

// Listen binds the mod socket at path. Its directory is resolved with
// EvalSymlinks and the socket is bound in the resolved directory. That
// directory must be owned by the effective uid and not group- or
// other-writable, and every ancestor up to / must be owned by that uid or
// by root and be either not group- or other-writable or sticky (spec
// §6.1): no other user can rename or replace any component of the path, so
// only this user can have put anything at it. A dead socket left there is
// removed; a live one, or anything that is not a socket, is left alone and
// the channel stays disabled. The socket is chmod 0600.
//
// The returned listener only hands out connections whose peer uid is the
// daemon's effective uid; others are closed before a byte is read. That
// check is the gate, the file mode is defence in depth. Closing the
// listener unlinks the socket at the resolved path it was bound at; a
// second Close is a no-op.
func Listen(path string) (net.Listener, Status) {
	if len(path) > MaxSocketPath {
		return nil, Status{Reason: ReasonPathTooLong}
	}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err == nil {
		dir, err = filepath.EvalSymlinks(dir)
	}
	if err != nil {
		log.Printf("[modevents] socket dir %s: %v", filepath.Dir(path), err)
		return nil, Status{Reason: ReasonListenFailed}
	}
	path = filepath.Join(dir, filepath.Base(path))
	if len(path) > MaxSocketPath {
		log.Printf("[modevents] resolved socket path %s is longer than %d bytes", path, MaxSocketPath)
		return nil, Status{Reason: ReasonPathTooLong}
	}
	euid := os.Geteuid()
	if err := securePath(dir, euid); err != nil {
		log.Printf("[modevents] socket dir %s is unsafe: %v", dir, err)
		return nil, Status{Reason: ReasonUnsafeDir}
	}

	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&fs.ModeSocket == 0 {
			return nil, Status{Reason: ReasonNotASocket}
		}
		if c, err := net.DialTimeout("unix", path, staleProbe); err == nil {
			c.Close()
			return nil, Status{Reason: ReasonInUse}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Printf("[modevents] remove stale socket %s: %v", path, err)
			return nil, Status{Reason: ReasonListenFailed}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		log.Printf("[modevents] stat %s: %v", path, err)
		return nil, Status{Reason: ReasonListenFailed}
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		log.Printf("[modevents] listen %s: %v", path, err)
		return nil, Status{Reason: ReasonListenFailed}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		log.Printf("[modevents] chmod %s: %v", path, err)
		ln.Close()
		return nil, Status{Reason: ReasonListenFailed}
	}
	return &peerListener{Listener: ln, euid: uint32(euid), peerUID: peerUID}, Status{Enabled: true}
}

// securePath checks dir, an absolute path with its symlinks resolved, the
// way OpenSSH's secure-path check does: dir itself is a directory owned by
// euid and closed to group and others; every ancestor up to / is a
// directory owned by euid or root, and either closed to group and others
// or sticky (only an entry's owner may then rename or remove it). The
// error names the first component that fails. It uses Lstat, so a
// component swapped for a symlink after EvalSymlinks fails as not a
// directory.
func securePath(dir string, euid int) error {
	for d := dir; ; d = filepath.Dir(d) {
		fi, err := os.Lstat(d)
		if err != nil {
			return err
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || !fi.IsDir() {
			return fmt.Errorf("%s is not a directory (%v)", d, fi.Mode())
		}
		uid := int(st.Uid)
		writable := fi.Mode().Perm()&0o022 != 0
		if d == dir {
			if uid != euid || writable {
				return fmt.Errorf("%s (uid %d, %v) must be owned by uid %d and closed to group and others", d, uid, fi.Mode(), euid)
			}
		} else if (uid != euid && uid != 0) || (writable && fi.Mode()&os.ModeSticky == 0) {
			return fmt.Errorf("ancestor %s (uid %d, %v) must be owned by uid %d or root, and closed to group and others or sticky", d, uid, fi.Mode(), euid)
		}
		if d == filepath.Dir(d) {
			return nil
		}
	}
}

// peerListener closes every accepted connection whose peer is not euid.
type peerListener struct {
	net.Listener
	euid    uint32
	peerUID func(net.Conn) (uint32, error)

	closeOnce sync.Once
	closeErr  error
}

func (l *peerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if uid, err := l.peerUID(c); err == nil && uid == l.euid {
			return c, nil
		}
		c.Close()
	}
}

// Close closes (and unlinks) once: the module closes the listener before
// http.Server.Shutdown, which closes it again.
func (l *peerListener) Close() error {
	l.closeOnce.Do(func() { l.closeErr = l.Listener.Close() })
	return l.closeErr
}

// rawFD runs f with the connection's file descriptor.
func rawFD(c net.Conn, f func(fd int) error) error {
	sc, ok := c.(syscall.Conn)
	if !ok {
		return errors.New("connection has no file descriptor")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := raw.Control(func(fd uintptr) { ferr = f(int(fd)) }); err != nil {
		return err
	}
	return ferr
}
