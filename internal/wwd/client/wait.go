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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://wwd/v1/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	// no call timeout: the stream is long-lived and the context ends it
	resp, err := c.http.Do(req)
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
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if len(data) == 0 {
				continue
			}
			ev, err := wire.Decode[wire.Event]([]byte(strings.Join(data, "\n")))
			data = data[:0]
			if err != nil {
				return fmt.Errorf("%w: %v", ErrBadResponse, err)
			}
			if fn(ev) {
				return nil
			}
		case strings.HasPrefix(line, ":"): // a comment: the keep-alive
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
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

// pollEvery is how often WaitJob asks for the job while it has no event stream.
var pollEvery = 2 * time.Second

// reconnectAfter is the pause before WaitJob opens the event stream again.
var reconnectAfter = 500 * time.Millisecond

// WaitJob waits until the job is in a terminal state and returns it. It listens to the event stream and, because the stream can
// drop and a state change can come between two reads, it reads the job after the stream is open and again whenever the stream
// breaks, polling meanwhile. onUpdate, when not nil, sees every state change seen (not every event). A cancelled context ends the
// wait with its error; an unreachable wwd with ErrUnavailable once it has failed three reads in a row.
func (c *Client) WaitJob(ctx context.Context, id string, onUpdate func(wire.Job)) (wire.Job, error) {
	var (
		mu   sync.Mutex // seen runs on the event goroutine and on this one
		last wire.JobState
	)
	seen := func(j wire.Job) bool {
		mu.Lock()
		defer mu.Unlock()
		if j.State != last {
			last = j.State
			if onUpdate != nil {
				onUpdate(j)
			}
		}
		return j.State.Terminal()
	}
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
			if j, ok, rerr := c.readOnce(ctx, id, seen, &failures); ok {
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
		if j, ok, rerr := c.readOnce(ctx, id, seen, &failures); ok {
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

// readOnce reads the job; ok is true when it is terminal. Three failed reads in a row end the wait with the error.
func (c *Client) readOnce(ctx context.Context, id string, seen func(wire.Job) bool, failures *int) (wire.Job, bool, error) {
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
	return j, seen(j), nil
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
