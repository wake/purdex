package modevents

import (
	"errors"
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

// Listen binds the mod socket at path. The directory must be a real
// directory owned by the effective uid and not group- or other-writable,
// so only this user can have put anything at the path. A dead socket left
// there is removed; a live one, or anything that is not a socket, is left
// alone and the channel stays disabled. The socket is chmod 0600.
//
// The returned listener only hands out connections whose peer uid is the
// daemon's effective uid; others are closed before a byte is read. That
// check is the gate, the file mode is defence in depth. Closing the
// listener unlinks the socket; a second Close is a no-op.
func Listen(path string) (net.Listener, Status) {
	if len(path) > MaxSocketPath {
		return nil, Status{Reason: ReasonPathTooLong}
	}
	euid := os.Geteuid()
	dir := filepath.Dir(path)
	fi, err := os.Lstat(dir)
	if err != nil {
		log.Printf("[modevents] socket dir %s: %v", dir, err)
		return nil, Status{Reason: ReasonListenFailed}
	}
	if !safeDir(fi, euid) {
		log.Printf("[modevents] socket dir %s is not a directory owned by uid %d and closed to group and others (%v)", dir, euid, fi.Mode())
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

func safeDir(fi fs.FileInfo, euid int) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && fi.IsDir() && int(st.Uid) == euid && fi.Mode().Perm()&0o022 == 0
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
