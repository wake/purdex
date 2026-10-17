package run

import (
	"io"
	"os"
	"sync"
)

const (
	// DefaultMaxLog caps each captured file; output past it is counted and dropped, the command is never blocked on it.
	DefaultMaxLog = 64 << 20
	// tailBytes is how much of the end of the log is kept in memory for the summary's tail.
	tailBytes = 64 << 10

	truncatedNote = "\n[wwd: log truncated, the command printed more than the capture limit]\n"
)

// capture writes what a command prints to the log file (stdout and stderr in the order they arrive), the stdout file, and an
// in-memory tail of the log.
type capture struct {
	mu        sync.Mutex
	log       *os.File
	stdout    *os.File
	max       int64
	logN      int64
	outN      int64
	logCut    bool
	outCut    bool
	tail      []byte
	closed    bool
	lastError error
}

func newCapture(logPath, stdoutPath string, max int64) (*capture, error) {
	if max <= 0 {
		max = DefaultMaxLog
	}
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	sf, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		lf.Close()
		return nil, err
	}
	return &capture{log: lf, stdout: sf, max: max}, nil
}

func (c *capture) writeLog(p []byte) {
	if c.logN < c.max {
		room := c.max - c.logN
		w := p
		if int64(len(w)) > room {
			w = w[:room]
		}
		if _, err := c.log.Write(w); err != nil {
			c.lastError = err
		}
		c.logN += int64(len(w))
		if int64(len(p)) > room && !c.logCut {
			c.logCut = true
			c.log.WriteString(truncatedNote)
		}
	} else if !c.logCut {
		c.logCut = true
		c.log.WriteString(truncatedNote)
	}
	c.tail = append(c.tail, p...)
	if len(c.tail) > 2*tailBytes {
		c.tail = append(c.tail[:0], c.tail[len(c.tail)-tailBytes:]...)
	}
}

// Stdout is the writer for the command's standard output.
func (c *capture) Stdout() io.Writer {
	return writerFunc(func(p []byte) (int, error) { return c.write(p, true) })
}

// Stderr is the writer for the command's standard error.
func (c *capture) Stderr() io.Writer {
	return writerFunc(func(p []byte) (int, error) { return c.write(p, false) })
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// write never fails and never short-writes: a full disk or the cap must not stall the command.
func (c *capture) write(p []byte, stdout bool) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return len(p), nil
	}
	c.writeLog(p)
	if stdout {
		if c.outN < c.max {
			room := c.max - c.outN
			w := p
			if int64(len(w)) > room {
				w = w[:room]
			}
			if _, err := c.stdout.Write(w); err != nil {
				c.lastError = err
			}
			c.outN += int64(len(w))
			if int64(len(p)) > room {
				c.outCut = true
			}
		} else {
			c.outCut = true
		}
	}
	return len(p), nil
}

// note adds a line of the runner's own to the log and the tail.
func (c *capture) note(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	// the runner's own line is not the command's output: it is written whatever the capture cap says
	if _, err := c.log.WriteString(s); err != nil {
		c.lastError = err
	}
	c.tail = append(c.tail, s...)
}

func (c *capture) close() (tail []byte, logCut bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.log.Close()
	c.stdout.Close()
	t := c.tail
	if len(t) > tailBytes {
		t = t[len(t)-tailBytes:]
	}
	return append([]byte(nil), t...), c.logCut, c.lastError
}
