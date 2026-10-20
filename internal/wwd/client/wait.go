package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ErrStreamEnded: the event stream closed (wwd restarted, or it dropped a reader that fell behind). Watch callers reconnect.
var ErrStreamEnded = errors.New("wwd event stream ended")

// maxFrame caps one SSE frame (a job event carries a job with its summary).
const maxFrame = 4 << 20

// Watch opens GET /v1/events and calls fn for every event until fn returns true, the context ends, or the stream does
// (ErrStreamEnded). ready, when not nil, is closed once the stream is open, so a caller that must not miss an event can read
// state after it. Keep-alive comments are skipped; a frame that is not a valid event is an ErrBadResponse.
func (c *Client) Watch(ctx context.Context, ready chan<- struct{}, fn func(wire.Event) (done bool)) error {
	sctx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	req, err := http.NewRequestWithContext(sctx, http.MethodGet, "http://wwd/v1/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	// no call timeout: the stream is long-lived and the context ends it
	g := &connectGuard{cancel: cancelStream}
	t := time.AfterFunc(c.timeout(), g.expire) // connecting and the answer's headers are bounded; the body is the context's
	resp, err := c.http.Do(req)
	g.connected()
	t.Stop()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return &StatusError{Code: resp.StatusCode, Body: string(b)}
	}
	if ready != nil {
		close(ready)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), maxFrame)
	var data []string
	size := 0
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if len(data) == 0 {
				continue
			}
			ev, err := wire.DecodeLenient[wire.Event]([]byte(strings.Join(data, "\n")))
			size = 0
			data = data[:0]
			if err != nil {
				return fmt.Errorf("%w: %v", ErrBadResponse, err)
			}
			if fn(ev) {
				return nil
			}
		case strings.HasPrefix(line, ":"): // a comment: the keep-alive
		case strings.HasPrefix(line, "data:"):
			part := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			if size += len(part) + 1; size > maxFrame {
				return fmt.Errorf("%w: an event frame is over %d bytes", ErrBadResponse, maxFrame)
			}
			data = append(data, part)
		default: // event:, id:, retry: carry nothing the envelope does not
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrStreamEnded, err)
	}
	return ErrStreamEnded
}

// connectGuard hands the stream over from the connect timeout to the caller's context: once connected has been called, the
// timer's callback can no longer cancel anything, whatever the timing of the two.
type connectGuard struct {
	mu     sync.Mutex
	done   bool
	cancel context.CancelFunc
}

func (g *connectGuard) connected() {
	g.mu.Lock()
	g.done = true
	g.mu.Unlock()
}

func (g *connectGuard) expire() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.done {
		g.cancel()
	}
}

// pollEvery is how often WaitJob asks for the job while it has no event stream.
var pollEvery = 2 * time.Second

// reconnectAfter is the pause before WaitJob opens the event stream again.
var reconnectAfter = 500 * time.Millisecond

// WaitJob waits until the job is in a terminal state and returns it. It listens to the event stream and, because the stream can
// drop and a state change can come between two reads, it reads the job after the stream is open and again whenever the stream
// breaks, polling meanwhile. onUpdate, when not nil, sees every state change seen (not every event). A cancelled context ends the
// wait with its error; an unreachable wwd with ErrUnavailable once it has failed three reads in a row.
func (c *Client) WaitJob(ctx context.Context, id string, onUpdate func(wire.Job)) (wire.Job, error) {
	tr := &tracker{onUpdate: onUpdate}
	seen := tr.seen
	failures := 0
	for {
		if err := ctx.Err(); err != nil {
			return wire.Job{}, err
		}
		streamCtx, stop := context.WithCancel(ctx)
		ready := make(chan struct{})
		var final *wire.Job
		watchErr := make(chan error, 1)
		go func() {
			watchErr <- c.Watch(streamCtx, ready, func(e wire.Event) bool {
				if e.Type != wire.EventJob {
					return false
				}
				d, err := e.DecodeData()
				if err != nil {
					return false
				}
				je, ok := d.(wire.JobEvent)
				if !ok || je.Job.ID != id {
					return false
				}
				if seen(je.Job) {
					j := je.Job
					final = &j
					return true
				}
				return false
			})
		}()
		// Read the job once the stream is open (or has failed to open), so a change from before is not missed.
		select {
		case <-ready:
		case err := <-watchErr:
			stop()
			if err2 := ctx.Err(); err2 != nil {
				return wire.Job{}, err2
			}
			if j, ok, rerr := c.readOnce(ctx, id, tr, &failures); ok {
				return j, nil
			} else if rerr != nil {
				return wire.Job{}, rerr
			}
			_ = err
			if !sleepCtx(ctx, pollEvery) {
				return wire.Job{}, ctx.Err()
			}
			continue
		}
		if j, ok, rerr := c.readOnce(ctx, id, tr, &failures); ok {
			stop()
			<-watchErr
			return j, nil
		} else if rerr != nil {
			stop()
			<-watchErr
			return wire.Job{}, rerr
		}
		err := <-watchErr
		stop()
		if final != nil {
			return *final, nil
		}
		if ctx.Err() != nil {
			return wire.Job{}, ctx.Err()
		}
		_ = err // the stream ended or broke: read again and reconnect
		if !sleepCtx(ctx, reconnectAfter) {
			return wire.Job{}, ctx.Err()
		}
	}
}

// tracker reports state changes to onUpdate once each, from the event goroutine and the reading goroutine alike. A terminal
// state is final: a snapshot read that was in flight when the terminal event arrived must not report an older state after it.
type tracker struct {
	mu       sync.Mutex
	last     wire.JobState
	done     bool
	final    wire.Job // the first terminal job seen; valid once done (read after seen returned true)
	onUpdate func(wire.Job)
}

// seen records j and says whether the job is terminal.
func (t *tracker) seen(j wire.Job) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return true
	}
	if j.State != t.last {
		t.last = j.State
		if t.onUpdate != nil {
			t.onUpdate(j)
		}
	}
	if j.State.Terminal() {
		t.done, t.final = true, j
	}
	return t.done
}

// readOnce reads the job; ok is true when it is terminal. Three failed reads in a row end the wait with the error.
func (c *Client) readOnce(ctx context.Context, id string, tr *tracker, failures *int) (wire.Job, bool, error) {
	j, err := c.Job(ctx, id)
	if err != nil {
		if ctx.Err() != nil {
			return wire.Job{}, false, ctx.Err()
		}
		var se *StatusError
		if errors.As(err, &se) && se.Code == http.StatusNotFound {
			return wire.Job{}, false, err // there is no such job; waiting would never end
		}
		if *failures++; *failures >= 3 {
			return wire.Job{}, false, err
		}
		return wire.Job{}, false, nil
	}
	*failures = 0
	if tr.seen(j) {
		return tr.final, true, nil // the first terminal job seen, not an older snapshot that was in flight
	}
	return j, false, nil
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
