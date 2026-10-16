package fleet

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/wake/purdex/internal/execstat"
)

// Status is how a probe went (spec §5).
type Status string

const (
	StatusOK      Status = "ok"
	StatusOffline Status = "offline"
	StatusError   Status = "error"
)

// Code is the reason behind a non-ok status. It is the only failure detail that leaves the daemon: SSH's stderr never does.
type Code string

const (
	CodeNone        Code = ""
	CodeTimeout     Code = "timeout"
	CodeUnreachable Code = "unreachable"
	CodeHostKey     Code = "host_key"
	CodeAuth        Code = "auth"
	CodeRemoteExit  Code = "remote_exit"
	CodeParse       Code = "parse"
	CodeAliasGone   Code = "alias_gone"
)

const (
	// DefaultTimeout is how long one call may take before ssh is killed.
	DefaultTimeout = 10 * time.Second
	// MaxStdout is the most output read from a host; more is a `parse` error.
	MaxStdout = 64 << 10
	// MaxStderr is how much of ssh's stderr is kept, for the daemon log only.
	MaxStderr = 4 << 10
)

// Result is one probe. Stderr is for the daemon's own log; it must never be put in an API payload or in Code.
type Result struct {
	Reading Reading
	Status  Status
	Code    Code
	Stderr  string
}

// Runner runs the fixed probe over ssh.
type Runner struct {
	// LookPath finds the ssh binary; nil means exec.LookPath. Tests inject a fake.
	LookPath func(file string) (string, error)
	// Timeout bounds one call; zero means DefaultTimeout.
	Timeout time.Duration
}

// Args is the whole argv after the program name: fixed options, the alias after `--` (so it can never be read as an option)
// and the constant script. Nothing here goes through a local shell.
func Args(alias string) []string {
	return []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=yes",
		// An alias with its own RemoteCommand would make ssh refuse a command line ("Cannot execute command-line and
		// remote command"); none lets the probe script replace it.
		"-o", "RemoteCommand=none",
		"--", alias, Script,
	}
}

// Run probes alias. The alias is shape-checked again here; whether it is still in the SSH config is the caller's check
// (ValidAlias), made right before the call. A cancelled ctx reads as `offline/unreachable`: nothing could be learned.
func (r Runner) Run(ctx context.Context, alias string) Result {
	if !aliasRE.MatchString(alias) {
		return Result{Status: StatusError, Code: CodeAliasGone}
	}
	look := r.LookPath
	if look == nil {
		look = exec.LookPath
	}
	ssh, err := look("ssh")
	if err != nil {
		return Result{Status: StatusOffline, Code: CodeUnreachable}
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout := &capped{max: MaxStdout, onFull: cancel}
	stderr := &headTail{max: MaxStderr}
	cmd := execstat.CommandContext(cctx, "fleet.ssh", ssh, Args(alias)...)
	cmd.Stdin = nil // /dev/null
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = time.Second
	runErr := cmd.Run()
	res := Result{Stderr: stderr.String()}

	switch {
	case errors.Is(cctx.Err(), context.DeadlineExceeded) && !stdout.full:
		res.Status, res.Code = StatusOffline, CodeTimeout
	case stdout.full:
		res.Status, res.Code = StatusError, CodeParse
	case ctx.Err() != nil:
		res.Status, res.Code = StatusOffline, CodeUnreachable
	case runErr == nil:
		reading, perr := Parse(stdout.String())
		if perr != nil {
			res.Status, res.Code = StatusError, CodeParse
			return res
		}
		res.Reading, res.Status = reading, StatusOK
	default:
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			res.Status, res.Code = StatusOffline, CodeUnreachable
			return res
		}
		if ee.ExitCode() == 255 {
			res.Status, res.Code = classify255(res.Stderr)
		} else {
			res.Status, res.Code = StatusError, CodeRemoteExit
		}
	}
	return res
}

// Classify255 is classify255 for the long-lived sessions of the fleet module: the verdict for an ssh that ended with exit
// status 255, from its stderr (which never leaves the daemon).
func Classify255(stderr string) (Status, Code) { return classify255(stderr) }

// classify255 tells apart the exit-255 cases by reading stderr; only the verdict leaves this function.
func classify255(stderr string) (Status, Code) {
	switch {
	case strings.Contains(stderr, "Host key verification failed"),
		strings.Contains(stderr, "REMOTE HOST IDENTIFICATION HAS CHANGED"):
		return StatusError, CodeHostKey
	case strings.Contains(stderr, "Permission denied"):
		return StatusError, CodeAuth
	}
	return StatusOffline, CodeUnreachable
}

// capped keeps the first max bytes and drops the rest without ever failing a write (a failing write could leave the child
// blocked on a full pipe). When the limit is passed, full is set and onFull is called.
type capped struct {
	max    int
	buf    []byte
	full   bool
	onFull func()
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - len(c.buf); len(p) <= room {
		c.buf = append(c.buf, p...)
	} else {
		if room > 0 {
			c.buf = append(c.buf, p[:room]...)
		}
		if !c.full {
			c.full = true
			if c.onFull != nil {
				c.onFull()
			}
		}
	}
	return len(p), nil
}

func (c *capped) String() string { return string(c.buf) }

// headTail keeps the first max/2 and the last max/2 bytes of what is written. ssh prints its decisive message (host key,
// permission denied) last, after any amount of earlier diagnostics, so the classifier must see the end as well as the start.
// Writes never fail, for the same reason as capped.
type headTail struct {
	max       int
	head      []byte
	tail      []byte // the most recent max/2 bytes after the head is full
	truncated bool
}

func (h *headTail) Write(p []byte) (int, error) {
	n := len(p)
	half := h.max / 2
	if room := half - len(h.head); room > 0 {
		take := min(room, len(p))
		h.head = append(h.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		h.tail = append(h.tail, p...)
		if len(h.tail) > half {
			h.truncated = true
			h.tail = append(h.tail[:0], h.tail[len(h.tail)-half:]...)
		}
	}
	return n, nil
}

func (h *headTail) String() string {
	if h.truncated {
		return string(h.head) + "\n...\n" + string(h.tail)
	}
	return string(h.head) + string(h.tail)
}
