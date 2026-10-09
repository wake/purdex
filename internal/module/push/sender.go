package push

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/push/apns"
)

const (
	queueCap   = 256
	retryAfter = 2 * time.Second
	expiresIn  = time.Hour
)

// deviceBook is the sender's view of the registered devices; the module implements it over its cache and store.
type deviceBook interface {
	Get(deviceID string) (push.Device, bool)
	Remove(deviceID string)
	MarkSent(deviceID string, at int64)
	MarkError(deviceID, reason string)
}

// apnsClient is the sender's view of the APNs client.
type apnsClient interface {
	Send(ctx context.Context, env, token string, h apns.Headers, payload []byte) apns.Result
	Invalidate()
}

// Job is one thing to tell some devices about. Make builds the content for one device (its host label and locale are the
// device's) at send time, from the device as it is then; false means "nothing for this one".
type Job struct {
	DeviceIDs []string
	Make      func(push.Device) (push.Content, bool)
}

// sender is the push module's one sending goroutine (spec §7). Triggers call Enqueue and never wait; everything that can
// block - the HTTP request, the retry pause, the store - happens here, never on a hub's or a handler's goroutine.
type sender struct {
	book   deviceBook
	apns   apnsClient
	hostID string
	topic  string

	openCount func() (int, bool) // the open-approval count each payload carries (spec §6); nil or !ok = unknown, left out

	queue   chan Job
	dropped atomic.Int64
	lastLog atomic.Int64 // unix ms of the last "queue full" log line

	sleep func(ctx context.Context, d time.Duration)
	now   func() time.Time

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newSender(book deviceBook, a apnsClient, hostID, topic string) *sender {
	return &sender{
		book: book, apns: a, hostID: hostID, topic: topic,
		queue: make(chan Job, queueCap),
		sleep: func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
			case <-ctx.Done():
			}
		},
		now: time.Now,
	}
}

// Enqueue hands a job over without ever blocking: a full queue drops it (counted, logged at most once a minute).
func (s *sender) Enqueue(j Job) bool {
	select {
	case s.queue <- j:
		return true
	default:
		s.dropped.Add(1)
		nowMs := s.now().UnixMilli()
		if last := s.lastLog.Load(); nowMs-last > 60_000 && s.lastLog.CompareAndSwap(last, nowMs) {
			log.Printf("[push] send queue full: %d notification(s) dropped so far", s.dropped.Load())
		}
		return false
	}
}

// Dropped is how many jobs the full queue refused.
func (s *sender) Dropped() int64 { return s.dropped.Load() }

// Start runs the sending goroutine until ctx ends or Stop is called.
func (s *sender) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case j := <-s.queue:
				s.process(ctx, j)
			}
		}
	}()
}

// Stop cancels the in-flight request and waits for the goroutine.
func (s *sender) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

func (s *sender) process(ctx context.Context, j Job) {
	for _, id := range j.DeviceIDs {
		if ctx.Err() != nil {
			return
		}
		d, ok := s.book.Get(id)
		if !ok {
			continue
		}
		content, ok := j.Make(d)
		if !ok {
			continue
		}
		if s.openCount != nil {
			if n, ok := s.openCount(); ok {
				content.OpenApprovals = &n
			}
		}
		payload, err := content.Payload(s.hostID)
		if err != nil {
			s.book.MarkError(id, "payload")
			continue
		}
		h := apns.Headers{Topic: s.topic, CollapseID: content.CollapseID, Expiration: s.now().Add(expiresIn)}
		s.finish(ctx, d, s.sendWithRetry(ctx, d, h, payload))
	}
}

// sendWithRetry is one send plus the single retry the spec allows: a renewed provider token after a 403 about it, or one
// pause of 2 s after a 429 / 5xx / network error.
func (s *sender) sendWithRetry(ctx context.Context, d push.Device, h apns.Headers, payload []byte) apns.Result {
	res := s.apns.Send(ctx, d.Env, d.Token, h, payload)
	// A registration dropped meanwhile (its phone was revoked) is not sent the retry. The one request already in flight when
	// the revoke lands cannot be recalled.
	stillRegistered := func() bool { _, ok := s.book.Get(d.DeviceID); return ok }
	switch res.Class {
	case apns.JWTRejected:
		s.apns.Invalidate()
		if !stillRegistered() {
			return res
		}
		return s.apns.Send(ctx, d.Env, d.Token, h, payload)
	case apns.RetryLater:
		s.sleep(ctx, retryAfter)
		if ctx.Err() != nil || !stillRegistered() {
			return res
		}
		return s.apns.Send(ctx, d.Env, d.Token, h, payload)
	}
	return res
}

func (s *sender) finish(ctx context.Context, d push.Device, res apns.Result) {
	masked := push.MaskToken(d.Token)
	switch res.Class {
	case apns.OK:
		s.book.MarkSent(d.DeviceID, s.now().UnixMilli())
		log.Printf("[push] sent to %s apns-id=%s", masked, res.APNsID)
	case apns.Remove:
		s.book.Remove(d.DeviceID)
		log.Printf("[push] device %s is gone (%s): removed apns-id=%s", masked, reasonOf(res), res.APNsID)
	default:
		if ctx.Err() != nil {
			return // shutting down: not the device's fault
		}
		why := reasonOf(res)
		s.book.MarkError(d.DeviceID, why)
		log.Printf("[push] send to %s failed: %s apns-id=%s", masked, why, res.APNsID)
	}
}

func reasonOf(r apns.Result) string {
	switch {
	case r.Reason != "":
		return r.Reason
	case r.Err != "":
		return r.Err
	default:
		return fmt.Sprintf("http %d", r.Status)
	}
}
