package ccuds

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Frame is one NDJSON line on a Claude Code messaging socket (spec §3.2).
// Only frames with Type "user" are delivered by the harness.
type Frame struct {
	MsgV     int    `json:"msgV"`
	MsgID    string `json:"msg_id"`
	Type     string `json:"type"`
	Priority string `json:"priority"`
	From     string `json:"from,omitempty"` // "uds:<sock>" reply address; "" ⇒ no reply address
	Message  struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
}

// udsPrefix is the scheme the harness expects on From for a reply address.
const udsPrefix = "uds:"

// BuildFrame returns exactly one NDJSON line (with its trailing "\n") for
// the harness: msgV 1, type "user", priority "next", from "uds:"+fromSock,
// role "user", content w.Format(). HTML-significant characters are not
// \u-escaped so the line matches what Claude Code itself emits.
func BuildFrame(msgID, fromSock string, w Wrapper) ([]byte, error) {
	var f Frame
	f.MsgV = 1
	f.MsgID = msgID
	f.Type = "user"
	f.Priority = "next"
	f.From = udsPrefix + fromSock
	f.Message.Role = "user"
	f.Message.Content = w.Format()

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil // Encode appends the "\n"
}

// rawFrame is Frame with the content left undecoded, because the harness
// may send it either as a string or as an array of content blocks.
type rawFrame struct {
	MsgV     int    `json:"msgV"`
	MsgID    string `json:"msg_id"`
	Type     string `json:"type"`
	Priority string `json:"priority"`
	From     string `json:"from"`
	Message  struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// ParseFrame decodes one line. message.content may be a JSON string or an
// array of {"type":"text","text":…} blocks, whose text is concatenated in
// order (blocks of another type are skipped); any other shape is an error.
// Unknown top-level fields are ignored.
func ParseFrame(line []byte) (Frame, error) {
	var raw rawFrame
	if err := json.Unmarshal(line, &raw); err != nil {
		return Frame{}, fmt.Errorf("frame: %w", err)
	}
	content, err := decodeContent(raw.Message.Content)
	if err != nil {
		return Frame{}, err
	}
	var f Frame
	f.MsgV = raw.MsgV
	f.MsgID = raw.MsgID
	f.Type = raw.Type
	f.Priority = raw.Priority
	f.From = raw.From
	f.Message.Role = raw.Message.Role
	f.Message.Content = content
	return f, nil
}

func decodeContent(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", errors.New("frame: message.content missing")
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", fmt.Errorf("frame: content: %w", err)
		}
		return s, nil
	case '[':
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(trimmed, &blocks); err != nil {
			return "", fmt.Errorf("frame: content blocks: %w", err)
		}
		var b strings.Builder
		for _, blk := range blocks {
			if blk.Type == "text" {
				b.WriteString(blk.Text)
			}
		}
		return b.String(), nil
	default:
		return "", errors.New("frame: message.content is neither a string nor a block array")
	}
}

// FromSocket strips the "uds:" scheme from a frame's From field. ok is
// false when the prefix is absent or nothing follows it: the harness
// attaches no reply address in either case.
func FromSocket(from string) (path string, ok bool) {
	path, ok = strings.CutPrefix(from, udsPrefix)
	if !ok || path == "" {
		return "", false
	}
	return path, true
}

var (
	// ErrWriteIncomplete: the line was not fully written before the
	// deadline (the peer stopped reading, or the connection broke).
	ErrWriteIncomplete = errors.New("frame not fully written")
	// ErrPostWriteTimeout: the full line was written and the write side
	// half-closed, but the peer did not close its end before the deadline.
	ErrPostWriteTimeout = errors.New("timed out after write")
)

// dialUnix is WriteFrame's connect step: net.Dialer.DialContext in
// production, a stalled fake in tests (a receiver that stopped accepting
// with a full backlog cannot be staged deterministically).
var dialUnix = func(ctx context.Context, d *net.Dialer, sockPath string) (net.Conn, error) {
	return d.DialContext(ctx, "unix", sockPath)
}

// WriteFrame dials sockPath, writes line, half-closes the write side and
// waits for the peer's EOF — all under ONE absolute deadline of
// now+timeout, computed before the dial: a receiver that stopped
// accepting (full backlog) cannot hold the connect open past it, and the
// same instant bounds the write and the EOF wait. It returns nil when
// everything completed; a wrapped ErrWriteIncomplete when the dial timed
// out or the write failed or timed out (nothing, or not everything, was
// written); a wrapped ErrPostWriteTimeout when the full line was written
// but the EOF wait hit the deadline. Other dial errors (no listener,
// refused) are returned as-is. Cancelling ctx aborts the dial, the write
// or the wait and returns ctx.Err() (wrapped).
func WriteFrame(ctx context.Context, sockPath string, line []byte, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ctxErr := func(op string, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("frame: %s: %w", op, cerr)
		}
		return nil
	}

	conn, err := dialUnix(ctx, &net.Dialer{Deadline: deadline}, sockPath)
	if err != nil {
		if cerr := ctxErr("dial", err); cerr != nil {
			return cerr
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return fmt.Errorf("%w: dial: %v", ErrWriteIncomplete, err)
		}
		return err
	}
	defer conn.Close()
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("frame: unexpected conn type %T", conn)
	}

	if err := uc.SetDeadline(deadline); err != nil {
		return fmt.Errorf("%w: set deadline: %v", ErrWriteIncomplete, err)
	}
	// A cancelled ctx moves the deadline into the past, which wakes any
	// blocked Write/Read with a timeout error; ctxErr turns that into
	// ctx.Err().
	stop := context.AfterFunc(ctx, func() { uc.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	n, err := uc.Write(line)
	if err != nil || n != len(line) {
		if cerr := ctxErr("write", err); cerr != nil {
			return cerr
		}
		return fmt.Errorf("%w: %d/%d bytes: %v", ErrWriteIncomplete, n, len(line), err)
	}
	if err := uc.CloseWrite(); err != nil {
		return fmt.Errorf("%w: close write: %v", ErrWriteIncomplete, err)
	}

	// Wait for the peer to close; anything it sends back is discarded.
	if _, err := io.Copy(io.Discard, uc); err != nil {
		if cerr := ctxErr("wait for peer close", err); cerr != nil {
			return cerr
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return fmt.Errorf("%w: %v", ErrPostWriteTimeout, err)
		}
		return err
	}
	return nil
}
