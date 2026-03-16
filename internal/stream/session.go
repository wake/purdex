// internal/stream/session.go
package stream

import (
	"bufio"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// StreamSession manages a single claude -p subprocess.
type StreamSession struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu          sync.RWMutex
	subscribers map[chan []byte]struct{}
	running     bool
	done        chan struct{}
}

// NewSession starts a subprocess and begins reading its stdout.
func NewSession(command string, args []string, cwd string) (*StreamSession, error) {
	cmd := exec.Command(command, args...)
	cmd.Dir = cwd

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, err
	}

	s := &StreamSession{
		cmd:         cmd,
		stdin:       stdin,
		stdout:      stdout,
		subscribers: make(map[chan []byte]struct{}),
		running:     true,
		done:        make(chan struct{}),
	}

	go s.readLoop()
	return s, nil
}

func (s *StreamSession) readLoop() {
	defer func() {
		s.mu.Lock()
		s.running = false
		close(s.done)
		// Close all subscriber channels
		for ch := range s.subscribers {
			close(ch)
			delete(s.subscribers, ch)
		}
		s.mu.Unlock()
	}()

	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer for large JSON
	for scanner.Scan() {
		line := make([]byte, len(scanner.Bytes()))
		copy(line, scanner.Bytes())

		s.mu.RLock()
		for ch := range s.subscribers {
			select {
			case ch <- line:
			default:
				// Drop if subscriber can't keep up
			}
		}
		s.mu.RUnlock()
	}
}

// Send writes a JSON line to the subprocess stdin.
func (s *StreamSession) Send(data []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.running {
		return io.ErrClosedPipe
	}
	// Append newline if not present
	if len(data) == 0 || data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	_, err := s.stdin.Write(data)
	return err
}

// Subscribe returns a channel that receives stdout lines.
func (s *StreamSession) Subscribe() <-chan []byte {
	ch := make(chan []byte, 64)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel.
func (s *StreamSession) Unsubscribe(ch <-chan []byte) {
	s.mu.Lock()
	// Type assertion to get the writable channel
	for c := range s.subscribers {
		if c == ch {
			delete(s.subscribers, c)
			break
		}
	}
	s.mu.Unlock()
}

// Running returns whether the subprocess is still alive.
func (s *StreamSession) Running() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Stop gracefully terminates the subprocess.
func (s *StreamSession) Stop() {
	s.stdin.Close()
	s.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		s.cmd.Process.Kill()
		<-s.done
	}
	s.cmd.Wait()
}

// Done returns a channel that closes when the subprocess exits.
func (s *StreamSession) Done() <-chan struct{} {
	return s.done
}
