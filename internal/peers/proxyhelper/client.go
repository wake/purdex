package proxyhelper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
)

const (
	// maxStdoutLine bounds one helper stdout line: a 1 MiB socket frame
	// (ccuds' cap) JSON-escaped can double, plus the envelope.
	maxStdoutLine = 4 * 1024 * 1024
	// framesBuffer is the Handle's Frames depth; the pump selects on done
	// so the depth is a courtesy, not a correctness knob.
	framesBuffer = 16
	// dialProbeTimeout bounds the "is anybody listening" probe that decides
	// whether a dead helper's socket may be unlinked.
	dialProbeTimeout = 500 * time.Millisecond
	// execWaitDelay bounds how long exec.Cmd.Wait waits for the stderr copy
	// after the process has exited (a grandchild holding the pipe).
	execWaitDelay = time.Second
)

// ErrNotReady is returned (wrapped) by Spawn when the helper did not
// answer ready:true in time.
var ErrNotReady = errors.New("helper did not become ready")

// Logf receives the client's diagnostics (a malformed stdout line, a pump
// that ended on an error). It defaults to the standard logger; the daemon
// or a test may replace it.
var Logf = log.Printf

// Proc is a started helper process as the client sees it.
type Proc interface {
	PID() int
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Signal(os.Signal) error
	Wait() error // returns once exited; safe to call once
}

// Starter starts a helper process. ctx is the PROCESS lifetime — the
// caller passes a long-lived context, never a request one.
type Starter func(ctx context.Context) (Proc, error)

// ExecStarter runs `exe peer-proxy` with an environment of PATH and HOME
// only, in its own process group, with stdin/stdout pipes and every
// stderr line forwarded to stderr with a "peer-proxy[<pid>]: " prefix.
func ExecStarter(exe string, stderr io.Writer) Starter {
	return func(ctx context.Context) (Proc, error) {
		cmd := exec.CommandContext(ctx, exe, "peer-proxy")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.WaitDelay = execWaitDelay
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("proxyhelper: stdin pipe: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("proxyhelper: stdout pipe: %w", err)
		}
		pw := &prefixWriter{dst: stderr}
		cmd.Stderr = pw
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("proxyhelper: start %s: %w", exe, err)
		}
		pw.setPrefix("peer-proxy[" + strconv.Itoa(cmd.Process.Pid) + "]: ")
		return &execProc{cmd: cmd, stdin: stdin, stdout: stdout, stderr: pw}, nil
	}
}

// execProc is Proc over exec.Cmd.
type execProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *prefixWriter

	waitOnce sync.Once
	waitErr  error
}

func (p *execProc) PID() int                 { return p.cmd.Process.Pid }
func (p *execProc) Stdin() io.WriteCloser    { return p.stdin }
func (p *execProc) Stdout() io.Reader        { return p.stdout }
func (p *execProc) Signal(s os.Signal) error { return p.cmd.Process.Signal(s) }

// Wait reaps the process. exec.Cmd.Wait also closes the parent's ends of
// the stdin/stdout pipes and joins the stderr copy, so a pump blocked in
// Read on stdout is released by it.
func (p *execProc) Wait() error {
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
		p.stderr.flush()
	})
	return p.waitErr
}

// prefixWriter prefixes every complete line written through it. The
// prefix is set once the pid is known; exec's copy goroutine only writes
// after Start, so no line is ever emitted without it.
type prefixWriter struct {
	mu      sync.Mutex
	dst     io.Writer
	prefix  string
	partial []byte
}

func (w *prefixWriter) setPrefix(p string) {
	w.mu.Lock()
	w.prefix = p
	w.mu.Unlock()
}

func (w *prefixWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial = append(w.partial, b...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			return len(b), nil
		}
		line := w.partial[:i+1]
		if _, err := io.WriteString(w.dst, w.prefix+string(line)); err != nil {
			return len(b), err
		}
		w.partial = w.partial[i+1:]
	}
}

// flush emits a trailing line that had no newline.
func (w *prefixWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.partial) > 0 {
		io.WriteString(w.dst, w.prefix+string(w.partial)+"\n")
		w.partial = nil
	}
}

// Handle is a ready helper.
type Handle interface {
	PID() int
	Sock() string
	Files() []string
	// Frames delivers every inbound socket line. It is closed by the pump
	// after the helper's stdout reaches EOF or after Stop joined the pump
	// — never before the pump goroutine has exited.
	Frames() <-chan string
	// Err is the pump's terminal error once Frames is closed: nil after a
	// clean EOF or a Stop, otherwise why reading stdout stopped early (an
	// over-long line is bufio.ErrTooLong). Nil while the pump runs.
	Err() error
	// Stop (R2-M5): close stdin (the helper's EOF signal); wait ≤ grace
	// for exit; SIGKILL; Wait; close the stdout pipe's read end (unblocks
	// a pump stuck in Read); close the pump's done channel (unblocks a
	// pump stuck in a Frames send); join the pump; Frames is closed.
	// Returns the exit error. Idempotent and safe to call concurrently.
	Stop(grace time.Duration) error
	Signal(os.Signal) error
}

// handle is the Handle implementation over a Proc.
type handle struct {
	proc  Proc
	pid   int
	sock  string
	files []string

	frames   chan string
	done     chan struct{} // closed by Stop: releases a pump blocked in a Frames send
	pumpDone chan struct{} // closed by the pump when it exits (after closing frames)

	stopping atomic.Bool // set first thing in Stop: a read error after this is self-inflicted
	stopOnce sync.Once
	stopErr  error

	errMu   sync.Mutex
	pumpErr error
}

func (h *handle) PID() int                 { return h.pid }
func (h *handle) Sock() string             { return h.sock }
func (h *handle) Files() []string          { return append([]string(nil), h.files...) }
func (h *handle) Frames() <-chan string    { return h.frames }
func (h *handle) Signal(s os.Signal) error { return h.proc.Signal(s) }

func (h *handle) Err() error {
	h.errMu.Lock()
	defer h.errMu.Unlock()
	return h.pumpErr
}

func (h *handle) Stop(grace time.Duration) error {
	h.stopOnce.Do(func() {
		h.stopping.Store(true)
		h.proc.Stdin().Close()
		exited := make(chan error, 1)
		go func() { exited <- h.proc.Wait() }()
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case h.stopErr = <-exited:
		case <-timer.C:
			h.proc.Signal(syscall.SIGKILL)
			h.stopErr = <-exited
		}
		closeReader(h.proc.Stdout())
		close(h.done)
		<-h.pumpDone
	})
	return h.stopErr
}

// pump copies stdout lines into Frames until stdout ends (EOF, read error,
// or the read end closed by Stop) or done closes; on exit it closes Frames.
// A scanner error that Stop did not cause is recorded for Err and logged.
func (h *handle) pump(sc *bufio.Scanner) {
	defer close(h.pumpDone)
	defer close(h.frames)
	for sc.Scan() {
		var fl struct {
			Frame *string `json:"frame"`
		}
		if err := json.Unmarshal(sc.Bytes(), &fl); err != nil || fl.Frame == nil {
			Logf("proxyhelper: pid %d: skipping malformed stdout line %q", h.pid, truncate(sc.Text(), 200))
			continue
		}
		select {
		case h.frames <- *fl.Frame:
		case <-h.done:
			return
		}
	}
	if err := sc.Err(); err != nil && !h.stopping.Load() {
		h.errMu.Lock()
		h.pumpErr = err
		h.errMu.Unlock()
		Logf("proxyhelper: pid %d: stdout pump ended: %v", h.pid, err)
	}
}

// Spawn starts a helper, hands it cfg, waits at most readyTimeout (a
// timer, never ctx) for ready:true and returns the Handle with its pump
// running. ready:false, a timeout, EOF or an undecodable first line ⇒
// the process is SIGKILLed and reaped, whatever the dead helper may have
// left behind that it can prove is its own is removed (R2-M8), and a
// wrapped ErrNotReady is returned. ctx is the PROCESS lifetime.
func Spawn(ctx context.Context, start Starter, cfg Config, readyTimeout time.Duration) (Handle, error) {
	p, err := start(ctx)
	if err != nil {
		return nil, fmt.Errorf("proxyhelper: start: %w", err)
	}
	line, err := json.Marshal(cfg)
	if err != nil {
		return nil, abort(p, cfg, fmt.Errorf("proxyhelper: encode config: %w", err))
	}
	if _, err := p.Stdin().Write(append(line, '\n')); err != nil {
		return nil, abort(p, cfg, fmt.Errorf("%w: write config: %v", ErrNotReady, err))
	}

	sc := bufio.NewScanner(p.Stdout())
	sc.Buffer(make([]byte, 64*1024), maxStdoutLine)
	type first struct {
		ok   bool
		line string
		err  error
	}
	ch := make(chan first, 1)
	go func() {
		ok := sc.Scan()
		ch <- first{ok, sc.Text(), sc.Err()}
	}()
	timer := time.NewTimer(readyTimeout)
	defer timer.Stop()
	var f first
	select {
	case f = <-ch:
	case <-timer.C:
		return nil, abort(p, cfg, fmt.Errorf("%w: no ready line within %v", ErrNotReady, readyTimeout))
	}
	if !f.ok {
		if f.err == nil {
			f.err = io.EOF
		}
		return nil, abort(p, cfg, fmt.Errorf("%w: stdout ended before the ready line: %v", ErrNotReady, f.err))
	}
	var ready readyLine
	if err := json.Unmarshal([]byte(f.line), &ready); err != nil {
		return nil, abort(p, cfg, fmt.Errorf("%w: undecodable ready line %q: %v", ErrNotReady, truncate(f.line, 200), err))
	}
	if !ready.Ready {
		return nil, abort(p, cfg, fmt.Errorf("%w: %s", ErrNotReady, ready.Error))
	}

	h := &handle{
		proc:     p,
		pid:      p.PID(),
		sock:     ready.Sock,
		files:    ready.Files,
		frames:   make(chan string, framesBuffer),
		done:     make(chan struct{}),
		pumpDone: make(chan struct{}),
	}
	go h.pump(sc)
	return h, nil
}

// abort kills and reaps p, releases its pipes, removes the leftovers the
// dead helper provably owned, and returns cause.
func abort(p Proc, cfg Config, cause error) error {
	p.Stdin().Close()
	p.Signal(syscall.SIGKILL)
	p.Wait()
	closeReader(p.Stdout())
	removeLeftovers(cfg, p.PID())
	return cause
}

// removeLeftovers unlinks only what the dead helper pid can be proven to
// own: <registry_dir>/<pid>.json iff its sessionId equals cfg.SessionID;
// every <registry_dir>/<pid>.*.key whose procStart equals that json's;
// <sock_dir>/<pid>.sock iff nobody is listening on it.
func removeLeftovers(cfg Config, pid int) {
	registryDir, sockDir := cfg.RegistryDir, cfg.SockDir
	// Mirror the defaults ccuds.StartVirtualPeer applies for empty dirs, so
	// the probe looks where the helper actually wrote.
	if registryDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		registryDir = filepath.Join(home, ".claude", "sessions")
	}
	if sockDir == "" {
		sockDir = ccuds.DefaultSockDir
	}
	jsonPath := filepath.Join(registryDir, strconv.Itoa(pid)+".json")
	if data, ok := peers.ReadRegistryCandidate(jsonPath); ok {
		var wire struct {
			SessionID string `json:"sessionId"`
			ProcStart string `json:"procStart"`
		}
		if json.Unmarshal(data, &wire) == nil && wire.SessionID != "" && wire.SessionID == cfg.SessionID {
			os.Remove(jsonPath)
			if wire.ProcStart != "" {
				keys, _ := filepath.Glob(filepath.Join(registryDir, strconv.Itoa(pid)+".*.key"))
				for _, k := range keys {
					if ccuds.RegistryProcStart(k) == wire.ProcStart {
						os.Remove(k)
					}
				}
			}
		}
	}
	sock := filepath.Join(sockDir, strconv.Itoa(pid)+".sock")
	if dialRefused(sock) {
		os.Remove(sock)
	}
}

// dialRefused reports whether connecting to sock fails because nobody
// listens (ECONNREFUSED) or the path is gone (ENOENT). A live listener or
// any other failure is not a licence to unlink.
func dialRefused(sock string) bool {
	c, err := net.DialTimeout("unix", sock, dialProbeTimeout)
	if err == nil {
		c.Close()
		return false
	}
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOENT)
}

// closeReader closes r when it can be closed (a pipe read end).
func closeReader(r io.Reader) {
	if c, ok := r.(io.Closer); ok {
		c.Close()
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
