// Package proxyhelper is the `pdx peer-proxy` helper — a tiny long-lived
// process the daemon spawns per remote sender to impersonate one Claude
// Code peer — and the client that starts and talks to it.
//
// The helper holds no credential and talks to nothing but stdin/stdout
// (plan D8):
//
//	daemon → helper (stdin, line 1):  Config as one JSON line
//	helper → daemon (stdout, line 1): {"ready":true,"pid":N,"sock":"…","files":["…","…"]}
//	                              or: {"ready":false,"error":"…"}   then exit 1
//	helper → daemon (stdout, after):  {"frame":"<raw NDJSON line from the socket>"}
//	daemon → helper: stdin EOF ⇒ the helper closes the peer and exits 0
//	SIGTERM/SIGINT ⇒ same cleanup, exit 0
package proxyhelper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/wake/purdex/internal/peers/ccuds"
)

// maxConfigLine bounds the stdin config line.
const maxConfigLine = 64 * 1024

// Config is the stdin line: everything the helper needs to impersonate one
// peer. The daemon chooses SessionID so that, should the helper die before
// ready, the spawner can prove ownership of <pid>.json and remove it.
type Config struct {
	Name         string   `json:"name"`
	RegistryDir  string   `json:"registry_dir"`
	SockDir      string   `json:"sock_dir"`
	Version      string   `json:"version"`
	Cwd          string   `json:"cwd"`
	SessionID    string   `json:"session_id"`
	PeerFeatures []string `json:"peer_features"`
}

// Options are the process-level knobs of Run. Tests inject every field so
// nothing forks ps or installs a signal handler in the test binary.
type Options struct {
	PID       int                           // 0 ⇒ os.Getpid()
	ProcStart func(pid int) (string, error) // nil ⇒ ccuds.DefaultProcStart
	Signals   <-chan os.Signal              // nil ⇒ Run installs SIGTERM/SIGINT notify itself
}

// readyLine is stdout line 1.
type readyLine struct {
	Ready bool     `json:"ready"`
	PID   int      `json:"pid,omitempty"`
	Sock  string   `json:"sock,omitempty"`
	Files []string `json:"files,omitempty"`
	Error string   `json:"error,omitempty"`
}

// frameLine is every later stdout line.
type frameLine struct {
	Frame string `json:"frame"`
}

// stdoutWriter serialises every stdout line behind one mutex and encodes
// without HTML escaping so the frame text reaches the daemon unchanged.
type stdoutWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *stdoutWriter) writeLine(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	enc := json.NewEncoder(s.w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v) // appends "\n"
}

// Run reads the config line, starts a ccuds.VirtualPeer, writes the ready
// line, then pumps the peer's Frames to stdout as {"frame":…} until stdin
// EOF, a signal, or ctx.Done(); then it Close()s the peer (which unlinks
// the socket and both registry files). A stdout write error (the daemon
// is gone) is treated like stdin EOF. Returns nil on a clean shutdown;
// after a ready:false line, the error that caused it.
func Run(ctx context.Context, stdin io.Reader, stdout io.Writer, o Options) error {
	out := &stdoutWriter{w: stdout}
	in := bufio.NewReaderSize(stdin, 16*1024)

	// Subscribe before anything is created so SIGTERM ⇒ cleanup holds for
	// the helper's whole life; a signal that lands during startup is
	// buffered and acted on as soon as the loop starts.
	sigs := o.Signals
	if sigs == nil {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
		defer signal.Stop(ch)
		sigs = ch
	}

	cfg, err := readConfig(in)
	if err != nil {
		out.writeLine(readyLine{Ready: false, Error: err.Error()})
		return err
	}

	if o.PID == 0 {
		o.PID = os.Getpid()
	}
	peer, err := ccuds.StartVirtualPeer(ccuds.VirtualPeerOptions{
		PID:          o.PID,
		SockDir:      cfg.SockDir,
		RegistryDir:  cfg.RegistryDir,
		Name:         cfg.Name,
		SessionID:    cfg.SessionID,
		Cwd:          cfg.Cwd,
		Version:      cfg.Version,
		PeerFeatures: cfg.PeerFeatures,
		ProcStart:    o.ProcStart,
	})
	if err != nil {
		out.writeLine(readyLine{Ready: false, Error: err.Error()})
		return err
	}
	// Socket bound and both registry files on disk: only now say ready.
	if err := out.writeLine(readyLine{Ready: true, PID: o.PID, Sock: peer.SockPath(), Files: peer.Files()}); err != nil {
		return peer.Close()
	}

	// Nothing but EOF ever follows the config line on stdin (D8).
	stdinDone := make(chan struct{})
	go func() {
		defer close(stdinDone)
		io.Copy(io.Discard, in)
	}()

	for {
		select {
		case line := <-peer.Frames():
			if err := out.writeLine(frameLine{Frame: line}); err != nil {
				return peer.Close() // daemon gone
			}
		case <-stdinDone:
			return peer.Close()
		case <-sigs:
			return peer.Close()
		case <-ctx.Done():
			return peer.Close()
		}
	}
}

// readConfig reads and decodes the one config line. EOF before any line,
// an over-long line and malformed JSON are all config errors.
func readConfig(in *bufio.Reader) (Config, error) {
	var line []byte
	for {
		chunk, isPrefix, err := in.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) == 0 {
				return Config{}, fmt.Errorf("config: stdin closed before the config line: %w", err)
			}
			return Config{}, fmt.Errorf("config: read: %w", err)
		}
		line = append(line, chunk...)
		if len(line) > maxConfigLine {
			return Config{}, fmt.Errorf("config: line exceeds %d bytes", maxConfigLine)
		}
		if !isPrefix {
			break
		}
	}
	var cfg Config
	if err := json.Unmarshal(line, &cfg); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
