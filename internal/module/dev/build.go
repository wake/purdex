package dev

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// buildTarget names a GOOS/GOARCH pair. The zero value means "the host".
type buildTarget struct {
	GOOS, GOARCH string
}

func (t buildTarget) isHost() bool { return t.GOOS == "" && t.GOARCH == "" }

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
	if !t.isHost() {
		cmd.Env = append(cmd.Env, "GOOS="+t.GOOS, "GOARCH="+t.GOARCH, "CGO_ENABLED=0")
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdout.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		return err
	}
	stream := func(src io.Reader, done chan<- struct{}) {
		defer close(done)
		sc := bufio.NewScanner(src)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			emit(sc.Text())
		}
	}
	doneOut, doneErr := make(chan struct{}), make(chan struct{})
	go stream(stdout, doneOut)
	go stream(stderr, doneErr)
	<-doneOut
	<-doneErr
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("go build: %w", err)
	}
	return nil
}
