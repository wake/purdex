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

	"github.com/gorilla/websocket"
)

// Relay bridges a subprocess (typically claude -p stream-json) to the daemon
// via WebSocket. Subprocess stdout is read line-by-line to preserve NDJSON
// boundaries and tee'd to stderr for terminal visibility.
type Relay struct {
	SessionName string
	DaemonURL   string
	Token       string
	Command     []string
}

// Run connects to the daemon WebSocket, starts the subprocess, and bridges
// data between them until the context is cancelled or the subprocess exits.
func (r *Relay) Run(ctx context.Context) error {
	if len(r.Command) == 0 {
		return fmt.Errorf("no command specified")
	}

	// Connect to daemon WebSocket
	header := http.Header{}
	if r.Token != "" {
		header.Set("Authorization", "Bearer "+r.Token)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, r.DaemonURL, header)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	// Start subprocess
	cmd := exec.CommandContext(ctx, r.Command[0], r.Command[1:]...)
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

	var wg sync.WaitGroup

	// Subprocess stdout → line-buffered tee to stderr + send to daemon WS
	// IMPORTANT: use bufio.Scanner for line-based reading to preserve NDJSON boundaries
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "subprocess exited"))
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB max line
		for scanner.Scan() {
			line := scanner.Bytes()
			os.Stderr.Write(line)
			os.Stderr.Write([]byte("\n"))
			conn.WriteMessage(websocket.TextMessage, line)
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
			stdin.Write(msg)
			stdin.Write([]byte("\n"))
		}
	}()

	cmdErr := cmd.Wait()
	wg.Wait()

	if cmdErr != nil {
		fmt.Fprintf(os.Stderr, "tbox relay: subprocess exited: %v\n", cmdErr)
	}
	return cmdErr
}
