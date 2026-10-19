package daemon

import (
	"context"
	"testing"
	"time"
)

func TestPoolCountsSilenceFromTheFirstFailureAndResetsOnAnAnswer(t *testing.T) {
	_, srv := peerDaemon(t, "mserv", testToken)
	clock := s0
	p := NewPoolPoller([]PeerAddr{{"mserv", addr(srv)}, {"air19", "127.0.0.1:1"}}, testToken, nil)
	p.now = func() time.Time { return clock }
	p.downSince[0], p.downSince[1] = s0, s0 // created at s0 (the clock was swapped after construction)
	// an answering peer is not silent; one never reached is silent since the poller began
	p.PollOnce(context.Background())
	d := p.Downs()
	if !d[0].Since.IsZero() || d[1].Since != s0 || d[1].Alias != "air19" {
		t.Fatalf("after the first poll: %+v", d)
	}
	// still down later: the start does not move
	clock = s0.Add(4 * time.Minute)
	p.PollOnce(context.Background())
	if got := p.Downs()[1].Since; got != s0 {
		t.Fatalf("the silence start moved to %v", got)
	}
	// a peer that answered and then goes away: silent since the first failed poll, not the latest
	srv.Close()
	clock = s0.Add(5 * time.Minute)
	p.PollOnce(context.Background())
	clock = s0.Add(7 * time.Minute)
	p.PollOnce(context.Background())
	if got := p.Downs()[0].Since; got != s0.Add(5*time.Minute) {
		t.Fatalf("silent since %v, want the first failure", got)
	}
}
