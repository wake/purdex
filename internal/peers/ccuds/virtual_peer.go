package ccuds

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/peers"
)

const (
	// DefaultSockDir is where Claude Code binds <pid>.sock (spec §3.1).
	DefaultSockDir = "/tmp/cc-socks"
	// maxFrameLine bounds one inbound NDJSON line; a longer one ends the
	// connection.
	maxFrameLine = 1 << 20
	// framesBuffer is the Frames channel depth; the reader still selects
	// on done so the depth is a courtesy, not a correctness knob.
	framesBuffer = 16
	// staleDialTimeout bounds the probe that decides whether an existing
	// <pid>.sock is live or an orphan.
	staleDialTimeout = 500 * time.Millisecond
)

// VirtualPeerOptions configures StartVirtualPeer. Zero values take the
// documented defaults; tests inject PID, dirs and ProcStart so nothing
// touches the real registry or forks ps.
type VirtualPeerOptions struct {
	PID          int    // own pid; 0 ⇒ os.Getpid()
	SockDir      string // "" ⇒ DefaultSockDir
	RegistryDir  string // "" ⇒ ~/.claude/sessions
	Name         string
	SessionID    string // "" ⇒ random UUID v4
	Cwd          string
	Version      string
	PidDomain    string // "" ⇒ runtime.GOOS
	PeerFeatures []string
	ProcStart    func(pid int) (string, error) // nil ⇒ DefaultProcStart
}

// VirtualPeer is a process impersonating one Claude Code peer: it owns
// <SockDir>/<pid>.sock plus the two registry files, and surfaces every
// NDJSON line written to the socket on Frames.
type VirtualPeer struct {
	sockPath string
	files    []string
	ln       net.Listener
	frames   chan string
	done     chan struct{}

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool

	wg        sync.WaitGroup
	closeOnce sync.Once
}

// DefaultProcStart runs `TZ=UTC ps -p <pid> -o lstart=` and returns the
// trimmed line — the exact string the harness compares byte-for-byte
// against its own ps invocation.
func DefaultProcStart(pid int) (string, error) {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TZ=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "TZ=UTC")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ps -p %d: %w", pid, err)
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", fmt.Errorf("ps -p %d: no such process", pid)
	}
	if _, err := peers.ParseProcStart(s); err != nil {
		return "", fmt.Errorf("ps -p %d: unexpected lstart %q: %w", pid, s, err)
	}
	return s, nil
}

// StartVirtualPeer binds <SockDir>/<pid>.sock (creating SockDir 0700 if
// missing and replacing a stale socket only when connecting to it is
// refused), chmods it 0600, writes the registry files, and starts
// accepting. Every failure undoes what was already done.
func StartVirtualPeer(o VirtualPeerOptions) (*VirtualPeer, error) {
	if o.PID == 0 {
		o.PID = os.Getpid()
	}
	if o.SockDir == "" {
		o.SockDir = DefaultSockDir
	}
	if o.RegistryDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("virtual peer: registry dir: %w", err)
		}
		o.RegistryDir = filepath.Join(home, ".claude", "sessions")
	}
	if o.PidDomain == "" {
		o.PidDomain = runtime.GOOS
	}
	if o.ProcStart == nil {
		o.ProcStart = DefaultProcStart
	}
	if o.SessionID == "" {
		id, err := randomUUID()
		if err != nil {
			return nil, err
		}
		o.SessionID = id
	}
	procStart, err := o.ProcStart(o.PID)
	if err != nil {
		return nil, fmt.Errorf("virtual peer: procStart: %w", err)
	}
	peerToken, err := randomHex(16)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(o.SockDir, 0o700); err != nil {
		return nil, fmt.Errorf("virtual peer: sock dir: %w", err)
	}
	sockPath := filepath.Join(o.SockDir, strconv.Itoa(o.PID)+".sock")
	if err := removeIfStale(sockPath); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("virtual peer: listen: %w", err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("virtual peer: chmod socket: %w", err)
	}
	files, err := WriteRegistry(o.RegistryDir, RegistryEntry{
		PID:          o.PID,
		SessionID:    o.SessionID,
		Name:         o.Name,
		Cwd:          o.Cwd,
		ProcStart:    procStart,
		Version:      o.Version,
		Inbox:        sockPath,
		PidDomain:    o.PidDomain,
		PeerFeatures: o.PeerFeatures,
	}, peerToken)
	if err != nil {
		ln.Close() // unlinks the socket (Listen-created listeners unlink on close)
		return nil, fmt.Errorf("virtual peer: registry: %w", err)
	}

	v := &VirtualPeer{
		sockPath: sockPath,
		files:    files,
		ln:       ln,
		frames:   make(chan string, framesBuffer),
		done:     make(chan struct{}),
		conns:    make(map[net.Conn]struct{}),
	}
	v.wg.Add(1)
	go v.acceptLoop()
	return v, nil
}

// removeIfStale unlinks path only when it exists and connecting to it is
// refused (nobody is listening). A live listener is someone else's and is
// an error; any other probe failure is reported rather than guessed at.
func removeIfStale(path string) error {
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("virtual peer: stat %s: %w", path, err)
	}
	conn, err := net.DialTimeout("unix", path, staleDialTimeout)
	if err == nil {
		conn.Close()
		return fmt.Errorf("virtual peer: %s already has a live listener", path)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("virtual peer: probe %s: %w", path, err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("virtual peer: remove stale %s: %w", path, err)
	}
	return nil
}

// SockPath is the bound socket path (the peer's messagingSocketPath).
func (v *VirtualPeer) SockPath() string { return v.sockPath }

// Files are the registry paths created for this peer, [json, key].
func (v *VirtualPeer) Files() []string { return append([]string(nil), v.files...) }

// Frames delivers every non-empty line written to the socket, without its
// newline. It is closed by Close after every reader goroutine has exited.
func (v *VirtualPeer) Frames() <-chan string { return v.frames }

func (v *VirtualPeer) acceptLoop() {
	defer v.wg.Done()
	for {
		c, err := v.ln.Accept()
		if err != nil {
			return // listener closed by Close (or unrecoverable)
		}
		if !v.track(c) {
			c.Close()
			return
		}
		v.wg.Add(1)
		go v.readConn(c)
	}
}

// track records c so Close can close it; false once Close has begun.
func (v *VirtualPeer) track(c net.Conn) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return false
	}
	v.conns[c] = struct{}{}
	return true
}

func (v *VirtualPeer) untrack(c net.Conn) {
	v.mu.Lock()
	delete(v.conns, c)
	v.mu.Unlock()
	c.Close()
}

// readConn scans c line by line and forwards each non-empty line. The
// send selects on done so a consumer that stopped reading Frames never
// wedges Close; a conn that never sends a newline sits in Scan until
// Close closes it.
func (v *VirtualPeer) readConn(c net.Conn) {
	defer v.wg.Done()
	defer v.untrack(c)
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64*1024), maxFrameLine)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		select {
		case v.frames <- line:
		case <-v.done:
			return
		}
	}
}

// Close stops accepting, closes every live connection, waits for the
// accept and reader goroutines, closes Frames, and unlinks the socket and
// registry files. Idempotent: the first call returns the first unlink
// error (if any); later calls return nil.
func (v *VirtualPeer) Close() error {
	var err error
	v.closeOnce.Do(func() {
		close(v.done)
		v.ln.Close()
		v.mu.Lock()
		v.closed = true
		conns := make([]net.Conn, 0, len(v.conns))
		for c := range v.conns {
			conns = append(conns, c)
		}
		v.mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
		v.wg.Wait()
		close(v.frames)

		if rmErr := os.Remove(v.sockPath); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			err = rmErr
		}
		if rmErr := RemoveRegistry(v.files); rmErr != nil && err == nil {
			err = rmErr
		}
	})
	return err
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("virtual peer: random: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// randomUUID returns a v4 UUID in canonical 8-4-4-4-12 form.
func randomUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("virtual peer: random: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}
