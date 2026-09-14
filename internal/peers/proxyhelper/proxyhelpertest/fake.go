// Package proxyhelpertest is an in-process fake of the `pdx peer-proxy`
// helper for tests of everything that spawns one (the proxyhelper client,
// the daemon's helper manager, `pdx msg selftest`). A fake process runs
// proxyhelper.Run — or one of its failure variants — in a goroutine over
// io.Pipes, with a pid from a counter starting at 900000 and a fake
// procStart, so nothing forks, nothing installs a signal handler, and the
// registry and socket dirs are whatever the Config says.
//
// Every variant exits and becomes Wait-able when its context is
// cancelled, when it is signalled, or when its stdin is closed, so a
// failing test never hangs on Wait.
package proxyhelpertest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
	"github.com/wake/purdex/internal/peers/proxyhelper"
)

// firstPID is the first pid handed out; the counter is package-global so
// two Fakes sharing one registry dir never collide.
const firstPID = 900000

var nextPID atomic.Int64

func init() { nextPID.Store(firstPID) }

// PeekPID returns the pid the next spawned fake process will get. Only
// meaningful while no other goroutine is spawning.
func PeekPID() int { return int(nextPID.Load()) }

// ProcStart is the fake `ps -o lstart=` every fake process reports: a
// valid lstart string that differs per pid. Tests that need a procStart
// oracle for fake pids (the manager's Sweep, the selftest) plug this in.
func ProcStart(pid int) (string, error) {
	base := time.Date(2026, time.September, 14, 0, 0, 0, 0, time.UTC)
	return base.Add(time.Duration(pid) * time.Second).Format(peers.ProcStartLayout), nil
}

// Variant selects how a fake helper behaves before ready.
type Variant int

const (
	// Normal runs proxyhelper.Run faithfully.
	Normal Variant = iota
	// Broken reads the config, writes nothing to stdout or disk, and never
	// becomes ready.
	Broken
	// BrokenRegistered reads the config, writes both registry files and
	// binds the socket like a real helper would, then never writes ready.
	// When killed it leaves the files and the (now dead) socket path on
	// disk — exactly what a SIGKILLed helper leaves for Spawn to clean up.
	BrokenRegistered
	// Refusing answers {"ready":false,"error":"refusing"} and exits 1.
	Refusing
	// Barrier reads the config and blocks until Release() is called, then
	// behaves like Normal.
	Barrier
)

// Options configures a Fake. The zero value is a faithful helper.
type Options struct {
	Variant Variant
}

// Fake is a helper factory: a Starter plus its controls and counters.
type Fake struct {
	opts    Options
	barrier chan struct{}
	release sync.Once

	mu       sync.Mutex
	spawns   int
	stops    int
	signals  int
	lastPID  int
	holdStop <-chan struct{}
	procs    []*Proc
}

// New returns a Fake with opts.
func New(opts Options) *Fake {
	return &Fake{opts: opts, barrier: make(chan struct{})}
}

// Starter returns the proxyhelper.Starter that spawns fake processes.
func (f *Fake) Starter() proxyhelper.Starter { return f.start }

// Release opens the Barrier for every current and future process of this
// Fake. Idempotent; a no-op for other variants.
func (f *Fake) Release() { f.release.Do(func() { close(f.barrier) }) }

// HoldStop makes every process's Wait block until ch is closed (in
// addition to the process having exited), so a Handle.Stop — which Waits
// — cannot return before the test lets it. Set it before spawning.
func (f *Fake) HoldStop(ch <-chan struct{}) {
	f.mu.Lock()
	f.holdStop = ch
	f.mu.Unlock()
}

// ExitOnItsOwn makes every live fake process die as if it had crashed:
// its stdout reaches EOF on the client side, it becomes Wait-able and
// Wait reports ErrKilled. Unlike a real crash, a Normal fake still runs
// the helper's own cleanup (its ctx is cancelled), so its registry files
// and socket are removed; a test that needs a dead helper's leftovers on
// disk uses the BrokenRegistered variant.
func (f *Fake) ExitOnItsOwn() {
	f.mu.Lock()
	procs := append([]*Proc(nil), f.procs...)
	f.mu.Unlock()
	for _, p := range procs {
		p.kill()
	}
}

// Spawns is how many processes were started.
func (f *Fake) Spawns() int { f.mu.Lock(); defer f.mu.Unlock(); return f.spawns }

// Stops is how many times a process was reaped with Wait — once per
// Handle.Stop (idempotent) and once per Spawn failure.
func (f *Fake) Stops() int { f.mu.Lock(); defer f.mu.Unlock(); return f.stops }

// Signals is how many signals were delivered to processes.
func (f *Fake) Signals() int { f.mu.Lock(); defer f.mu.Unlock(); return f.signals }

// LastPID is the pid of the most recently started process (0 if none).
func (f *Fake) LastPID() int { f.mu.Lock(); defer f.mu.Unlock(); return f.lastPID }

func (f *Fake) start(ctx context.Context) (proxyhelper.Proc, error) {
	pid := int(nextPID.Add(1) - 1)
	ctx, cancel := context.WithCancel(ctx)
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	p := &Proc{
		f: f, pid: pid, ctx: ctx, cancel: cancel,
		stdinR: stdinR, stdinW: stdinW, stdoutR: stdoutR, stdoutW: stdoutW,
		done: make(chan struct{}),
	}
	f.mu.Lock()
	f.spawns++
	f.lastPID = pid
	f.procs = append(f.procs, p)
	f.mu.Unlock()
	// A cancelled context ends the process even while it is still waiting
	// for its config line: close its stdin so that read returns.
	go func() {
		<-ctx.Done()
		stdinR.Close()
	}()
	go p.run()
	return p, nil
}

// Proc is one fake helper process.
type Proc struct {
	f      *Fake
	pid    int
	ctx    context.Context
	cancel context.CancelFunc

	stdinR  *io.PipeReader // the process's end
	stdinW  *io.PipeWriter // the client's end
	stdoutR *io.PipeReader // the client's end
	stdoutW *io.PipeWriter // the process's end

	done   chan struct{} // closed when the process goroutine has exited
	err    error
	killed atomic.Bool
}

// ErrKilled is what Wait returns for a process that was signalled or
// made to ExitOnItsOwn, mirroring exec's "signal: killed".
var ErrKilled = errors.New("signal: killed")

func (p *Proc) PID() int              { return p.pid }
func (p *Proc) Stdin() io.WriteCloser { return p.stdinW }
func (p *Proc) Stdout() io.Reader     { return p.stdoutR }

// Signal delivers a fatal signal: the process's context is cancelled and,
// as with a real SIGKILL, its ends of both pipes are closed so a body
// blocked in a stdout write (nobody reading) still dies; Wait then
// reports ErrKilled. A Normal fake still runs the helper's cleanup on
// the way out (see ExitOnItsOwn); BrokenRegistered leaves files behind.
func (p *Proc) Signal(os.Signal) error {
	p.f.mu.Lock()
	p.f.signals++
	p.f.mu.Unlock()
	p.kill()
	return nil
}

func (p *Proc) kill() {
	p.killed.Store(true)
	p.cancel()
	p.stdoutW.Close()
	p.stdinR.Close()
}

// Wait joins the process goroutine and returns its exit error: ErrKilled
// after Signal/ExitOnItsOwn, the variant's own exit error otherwise (nil
// for a clean exit). It honours HoldStop.
func (p *Proc) Wait() error {
	p.f.mu.Lock()
	p.f.stops++
	hold := p.f.holdStop
	p.f.mu.Unlock()
	<-p.done
	if hold != nil {
		<-hold
	}
	if p.killed.Load() {
		return ErrKilled
	}
	return p.err
}

// run is the process body: it exits (closing its ends of the pipes, as a
// real process exit would) when the variant's work is done.
func (p *Proc) run() {
	defer close(p.done)
	defer p.cancel() // releases the context watcher
	defer p.stdoutW.Close()
	defer p.stdinR.Close()
	switch p.f.opts.Variant {
	case Normal:
		p.err = p.runHelper(p.stdinR)
	case Refusing:
		// Read the config first: io.Pipe has no kernel buffer, so a client
		// whose config write has not been consumed would never reach its
		// stdout read.
		p.readConfig()
		io.WriteString(p.stdoutW, `{"ready":false,"error":"refusing"}`+"\n")
		p.err = errors.New("exit status 1")
	case Broken:
		_, stdinEOF := p.readConfig()
		p.err = p.stall(stdinEOF)
	case BrokenRegistered:
		line, stdinEOF := p.readConfig()
		ln := p.registerOnly(line)
		p.err = p.stall(stdinEOF)
		if ln != nil {
			ln.Close() // the socket path stays on disk, nobody listens
		}
	case Barrier:
		line, stdinEOF := p.readConfig()
		select {
		case <-p.f.barrier:
		case <-p.ctx.Done():
			p.err = ErrKilled
			return
		case <-stdinEOF:
			return
		}
		p.err = p.runHelper(io.MultiReader(bytes.NewReader(line), eofAfter{stdinEOF}))
	}
}

// runHelper runs the real helper body with a never-firing Signals
// channel (no signal.Notify in the test binary).
func (p *Proc) runHelper(stdin io.Reader) error {
	return proxyhelper.Run(p.ctx, stdin, p.stdoutW, proxyhelper.Options{
		PID:       p.pid,
		ProcStart: ProcStart,
		Signals:   make(chan os.Signal),
	})
}

// readConfig reads the config line and keeps draining stdin on a
// goroutine; stdinEOF closes when stdin ends.
func (p *Proc) readConfig() (line []byte, stdinEOF <-chan struct{}) {
	br := bufio.NewReader(p.stdinR)
	line, _ = br.ReadBytes('\n')
	eof := make(chan struct{})
	go func() {
		defer close(eof)
		io.Copy(io.Discard, br)
	}()
	return line, eof
}

// stall blocks until the process is killed or stdin ends.
func (p *Proc) stall(stdinEOF <-chan struct{}) error {
	select {
	case <-p.ctx.Done():
		return ErrKilled
	case <-stdinEOF:
		return nil
	}
}

// registerOnly writes the registry files and binds the socket for cfg the
// way ccuds.StartVirtualPeer would, without ever accepting or cleaning
// up. The listener is returned so the caller can close it on exit while
// leaving the socket path behind. Failures are ignored: a helper that
// could not register still stalls.
func (p *Proc) registerOnly(line []byte) *net.UnixListener {
	var cfg proxyhelper.Config
	if json.Unmarshal(line, &cfg) != nil {
		return nil
	}
	procStart, _ := ProcStart(p.pid)
	os.MkdirAll(cfg.SockDir, 0o700)
	sock := filepath.Join(cfg.SockDir, strconv.Itoa(p.pid)+".sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		return nil
	}
	ln.SetUnlinkOnClose(false)
	ccuds.WriteRegistry(cfg.RegistryDir, ccuds.RegistryEntry{
		PID: p.pid, SessionID: cfg.SessionID, Name: cfg.Name, Cwd: cfg.Cwd,
		ProcStart: procStart, Version: cfg.Version, Inbox: sock, PeerFeatures: cfg.PeerFeatures,
	}, "fake-peer-token-"+strconv.Itoa(p.pid))
	return ln
}

// eofAfter is a reader that blocks until ch closes, then reports EOF —
// the tail of a stdin whose only remaining event is EOF.
type eofAfter struct{ ch <-chan struct{} }

func (e eofAfter) Read([]byte) (int, error) {
	<-e.ch
	return 0, io.EOF
}
