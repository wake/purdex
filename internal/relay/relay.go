// internal/relay/relay.go
package relay

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// Relay bridges a subprocess (typically claude -p stream-json) to the daemon
// via WebSocket. Subprocess stdout is read line-by-line to preserve NDJSON
// boundaries and tee'd to stderr for terminal visibility.
type Relay struct {
	SessionCode string
	DaemonURL   string
	Token       string
	TokenFile   string // If set and Token is empty, read token from this file then delete it.
	Command     []string
}

// Run connects to the daemon WebSocket, starts the subprocess, and bridges
// data between them until the context is cancelled or the subprocess exits.
func (r *Relay) Run(ctx context.Context) error {
	if len(r.Command) == 0 {
		return fmt.Errorf("no command specified")
	}

	// Resolve token: prefer Token field, fallback to TokenFile.
	token := r.Token
	if token == "" && r.TokenFile != "" {
		data, err := os.ReadFile(r.TokenFile)
		if err != nil {
			return fmt.Errorf("read token file: %w", err)
		}
		token = string(data)
		// Remove token file after reading for security.
		os.Remove(r.TokenFile)
	}

	// Connect to daemon WebSocket
	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, r.DaemonURL, header)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	// Start subprocess — use plain exec.Command (NOT CommandContext) to avoid
	// SIGKILL on context cancel. We handle graceful shutdown manually below.
	cmd := exec.Command(r.Command[0], r.Command[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start command: %w", err)
	}

	// Graceful shutdown: on context cancel, send SIGTERM then SIGKILL after 5s.
	go func() {
		<-ctx.Done()
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
			time.AfterFunc(5*time.Second, func() {
				if cmd.Process != nil {
					cmd.Process.Kill()
				}
			})
		}
	}()

	var wg sync.WaitGroup

	// stdoutDone is closed once the stdout goroutine has fully drained the pipe
	// and stopped writing to the WS. Only then may the main goroutine reap the
	// process and emit the terminal frame + close, keeping conn writes
	// single-threaded (gorilla/websocket forbids concurrent writers).
	stdoutDone := make(chan struct{})

	// Subprocess stdout → line-buffered tee to stderr + send to daemon WS
	// IMPORTANT: use bufio.Scanner for line-based reading to preserve NDJSON boundaries
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stdoutDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB max line
		for scanner.Scan() {
			// I2 fix: scanner.Text() returns a string copy, avoiding buffer aliasing.
			line := scanner.Text()
			os.Stderr.WriteString(line)
			os.Stderr.WriteString("\n")
			// I1 fix: break on write error.
			if err := conn.WriteMessage(websocket.TextMessage, []byte(line)); err != nil {
				return
			}
		}
	}()

	// Daemon WS → subprocess stdin
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer stdin.Close()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				// WS disconnected — send SIGTERM to subprocess
				if cmd.Process != nil {
					cmd.Process.Signal(syscall.SIGTERM)
				}
				return
			}
			// Check for shutdown signal
			if string(msg) == `{"type":"shutdown"}` {
				if cmd.Process != nil {
					cmd.Process.Signal(os.Interrupt)
				}
				return
			}
			// I1 fix: break on write error.
			if _, err := stdin.Write(msg); err != nil {
				return
			}
			if _, err := stdin.Write([]byte("\n")); err != nil {
				return
			}
		}
	}()

	// Reap the subprocess only after stdout is fully drained. Per the
	// exec.StdoutPipe contract, Wait must not be called until all reads from the
	// pipe have completed. Waiting on stdoutDone first also guarantees the stdout
	// goroutine is no longer writing to conn, so the terminal frame + close below
	// are emitted by a single writer.
	<-stdoutDone
	cmdErr := cmd.Wait()

	// Emit the authoritative process-exit signal as an out-of-band binary frame
	// (spec §5.3): terminal timing is decided by process exit, carrying the exit
	// code / signal so the daemon's execution layer can act on it. This is
	// additive — the line-by-line stream above is unchanged. Best-effort: if the
	// WS is already broken the daemon falls back to §5.4 reconcile.
	ev := terminalEventFromState(cmd.ProcessState)
	if data, mErr := MarshalTerminalFrame(ev); mErr == nil {
		conn.WriteMessage(websocket.BinaryMessage, data)
	}
	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "subprocess exited"))

	wg.Wait()

	if cmdErr != nil {
		fmt.Fprintf(os.Stderr, "pdx relay: subprocess exited: %v\n", cmdErr)
	}
	return cmdErr
}

// terminalEventFromState builds the structured terminal event from the reaped
// process state. ExitCode is -1 when the process did not exit normally; when it
// was terminated by a signal, the signal number is recorded.
func terminalEventFromState(ps *os.ProcessState) TerminalEvent {
	ev := TerminalEvent{Type: TerminalEventType, ExitCode: -1}
	if ps == nil {
		return ev
	}
	ev.ExitCode = ps.ExitCode()
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		ev.Signaled = true
		ev.Signal = int(ws.Signal())
	}
	return ev
}
