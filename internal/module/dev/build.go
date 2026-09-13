package dev

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// buildTarget names a GOOS/GOARCH pair. The zero value means "the host".
type buildTarget struct {
	GOOS, GOARCH string
}

func (t buildTarget) isHost() bool { return t.GOOS == "" && t.GOARCH == "" }

// lineWriter is an io.Writer that splits whatever exec.Cmd writes to it into
// newline-delimited lines and calls sink for each complete one. It is meant
// to be driven solely by Cmd's own internal copy goroutine (via cmd.Stdout /
// cmd.Stderr) — see buildBinary for why that shape matters — so a single
// instance is never written to concurrently; sink itself may still be
// invoked concurrently by the stdout and stderr instances, so sink is
// expected to serialize on its own (buildBinary's emit does).
//
// A final line without a trailing newline is intentionally dropped rather
// than flushed: on a forced WaitDelay close (see buildBinary), the copy can
// be cut off mid-line, and there is no way to tell that apart here from a
// genuinely unterminated last line on a clean exit. Real `go build` output
// always newline-terminates every diagnostic, so this only affects
// pathological input, and dropping is safer than ever emitting a torn line.
type lineWriter struct {
	sink func(string)
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSuffix(w.buf[:i], []byte("\r"))
		w.sink(string(line))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

// buildBinary runs `go build ./cmd/pdx` in the repo root, writing the binary
// to out and streaming every compiler line to sink. hash/version are baked
// into internal/buildinfo. Cross builds set GOOS/GOARCH and CGO_ENABLED=0
// (modernc sqlite is pure Go, so the host toolchain's C SDK is irrelevant).
// It does not consult git: callers pass the identity they already captured.
// sink is never called concurrently.
func (m *DevModule) buildBinary(ctx context.Context, t buildTarget, hash, version, out string, sink func(line string)) error {
	ldflags := rebuildLdflags(hash, version)
	var sinkMu sync.Mutex
	emit := func(line string) {
		sinkMu.Lock()
		defer sinkMu.Unlock()
		sink(line)
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-ldflags", ldflags, "-o", out, "./cmd/pdx")
	cmd.Dir = m.repoRoot
	// Inherit env so GOCACHE / PATH / HOME work; do not scrub.
	cmd.Env = os.Environ()
	// On ctx cancel, CommandContext's default Cancel kills only this "go"
	// process; its compile/link children inherit the stdout/stderr pipes
	// and, being separate processes, are not killed with it. If one of them
	// is still running, it keeps the pipes open, so Cmd's internal copy
	// goroutines feeding cmd.Stdout/cmd.Stderr — and therefore
	// daemonRebuildMu, held by every caller of buildBinary — would block
	// until it exits on its own. WaitDelay bounds that: once the grace
	// period elapses after cancellation, Cmd force-closes the pipes to
	// unblock the copy goroutines so Wait returns and the 5-/6-minute
	// budgets in daemon.go / download.go actually hold.
	//
	// This mechanism (os/exec's awaitGoroutines, closing what it tracks as
	// parentIOPipes) only instruments pipes Cmd manages itself internally,
	// which requires cmd.Stdout/cmd.Stderr to be plain io.Writers — hence
	// lineWriter below instead of cmd.StdoutPipe()/StderrPipe(), whose
	// pipes the caller drains and closes by hand and which WaitDelay does
	// not reach.
	cmd.WaitDelay = 10 * time.Second
	if !t.isHost() {
		cmd.Env = append(cmd.Env, "GOOS="+t.GOOS, "GOARCH="+t.GOARCH, "CGO_ENABLED=0")
	}
	cmd.Stdout = &lineWriter{sink: emit}
	cmd.Stderr = &lineWriter{sink: emit}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build: %w", err)
	}
	return nil
}
