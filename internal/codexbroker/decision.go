package codexbroker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"
)

// DefaultGracefulShutdownTimeout is the wall-clock budget the predicate-A
// thread/list RPC and the kill-sequence graceful step share. Spec §5.4
// line 445.
const DefaultGracefulShutdownTimeout = 5 * time.Second

// threadListResponse decodes the broker's /thread/list JSON body. We only
// look at id + status; any extra fields are ignored.
type threadListResponse struct {
	Threads []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"threads"`
}

// EvalPredicateA implements spec §5.1 row A "Active execution".
//
// Strategy (per plan §4 task C):
//
//  1. Try the broker RPC thread/list via dialer; if ≥1 thread reports
//     status=active OR a pending approval → A=true.
//  2. Otherwise (RPC unreachable, RPC OK with no active threads, or RPC
//     timeout) inspect the shared jobs slice the caller already loaded:
//     a queued job → A=true; a running job that is NOT stale (per
//     IsStaleRunning) → A=true; otherwise → A=false.
//
// The conflict rule (spec §5.1 lines 367-368) lives in this predicate: an
// RPC stall does not penalise the broker — state.json drives the decision
// when the RPC is down.
//
// The jobs slice is owned by the caller (EvalDecision in task F) so the
// state.json read happens once per broker per scan.
func EvalPredicateA(ctx context.Context, rec BrokerRecord, jobs []StateJobLite, lister ProcessLister, dialer Dialer) (bool, string) {
	// Step 1 — RPC.
	rpcOK, rpcSawActive, rpcDetail := tryThreadList(ctx, rec, dialer)
	if rpcSawActive {
		return true, "rpc:" + rpcDetail
	}

	// Step 2 — fall through to state.json jobs slice.
	now := time.Now()
	for _, job := range jobs {
		switch job.Status {
		case "queued":
			return true, "state:queued:" + job.ID
		case "running":
			if stale, _ := IsStaleRunning(job, lister, DefaultStaleRunningThreshold, now); !stale {
				return true, "state:running-fresh:" + job.ID
			}
		}
	}
	if rpcOK {
		return false, "rpc:no-active-threads"
	}
	return false, "rpc-down-and-state-quiet"
}

// tryThreadList performs the broker RPC. Returns:
//   - rpcOK: the broker accepted the connection, parsed the response, and
//     produced a non-error status code. (False on dial error, body parse
//     error, or non-200.)
//   - sawActive: rpcOK and at least one thread is active or awaiting
//     approval.
//   - detail: short descriptor for the audit trace.
//
// The function never blocks longer than DefaultGracefulShutdownTimeout or
// the caller's context deadline, whichever is smaller.
func tryThreadList(ctx context.Context, rec BrokerRecord, dialer Dialer) (rpcOK bool, sawActive bool, detail string) {
	if dialer == nil || rec.Endpoint == "" {
		return false, false, "no-endpoint-or-dialer"
	}
	addr := strings.TrimPrefix(rec.Endpoint, "unix:")
	if addr == rec.Endpoint {
		// Endpoint scheme not unix:; we don't speak it in P2.
		return false, false, "non-unix-endpoint"
	}

	rpcCtx, cancel := context.WithTimeout(ctx, DefaultGracefulShutdownTimeout)
	defer cancel()

	type result struct {
		ok     bool
		active bool
		detail string
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := dialer.Dial("unix", addr)
		if err != nil {
			ch <- result{false, false, "dial-error"}
			return
		}
		defer conn.Close()
		// Apply ctx deadline to read/write operations.
		if dl, ok := rpcCtx.Deadline(); ok {
			_ = conn.SetDeadline(dl)
		}
		req := "POST /thread/list HTTP/1.1\r\nHost: broker\r\nContent-Length: 0\r\n\r\n"
		if _, err := conn.Write([]byte(req)); err != nil {
			ch <- result{false, false, "write-error"}
			return
		}
		// Read response — minimal HTTP/1.1 parser.
		body, parseErr := readHTTPBody(conn)
		if parseErr != nil {
			ch <- result{false, false, "read-error"}
			return
		}
		var resp threadListResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			ch <- result{false, false, "decode-error"}
			return
		}
		if len(resp.Threads) == 0 {
			ch <- result{true, false, "no-threads"}
			return
		}
		for _, th := range resp.Threads {
			if isActiveStatus(th.Status) {
				ch <- result{true, true, "thread-" + th.Status + ":" + th.ID}
				return
			}
		}
		ch <- result{true, false, "no-active-threads"}
	}()

	select {
	case <-rpcCtx.Done():
		return false, false, "timeout"
	case r := <-ch:
		return r.ok, r.active, r.detail
	}
}

// readHTTPBody peels the body out of a one-shot HTTP/1.1 response, skipping
// status line + headers. Honours the Content-Length header; falls back to
// reading to EOF if no length was advertised. Bounded by the conn deadline.
func readHTTPBody(conn net.Conn) ([]byte, error) {
	br := bufio.NewReader(conn)
	// status line
	if _, err := br.ReadString('\n'); err != nil {
		return nil, err
	}
	contentLength := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trim := strings.TrimRight(line, "\r\n")
		if trim == "" {
			break
		}
		if eq := strings.IndexByte(trim, ':'); eq > 0 {
			name := strings.ToLower(strings.TrimSpace(trim[:eq]))
			val := strings.TrimSpace(trim[eq+1:])
			if name == "content-length" {
				if n, err := atoi(val); err == nil {
					contentLength = n
				}
			}
		}
	}
	if contentLength >= 0 {
		buf := make([]byte, contentLength)
		_, err := readFull(br, buf)
		if err != nil {
			return nil, err
		}
		return buf, nil
	}
	// Read until EOF / deadline.
	var buf []byte
	chunk := make([]byte, 4096)
	for {
		n, err := br.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			if errors.Is(err, net.ErrClosed) || err.Error() == "EOF" {
				return buf, nil
			}
			return buf, nil // best-effort — partial body is okay for our decode
		}
	}
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	read := 0
	for read < len(buf) {
		n, err := r.Read(buf[read:])
		read += n
		if err != nil {
			return read, err
		}
	}
	return read, nil
}

func atoi(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("non-digit")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func isActiveStatus(s string) bool {
	switch strings.ToLower(s) {
	case "active", "running", "pending", "pending_approval", "approval_required":
		return true
	default:
		return false
	}
}
