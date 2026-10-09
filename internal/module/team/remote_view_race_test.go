// internal/module/team/remote_view_race_test.go
package teammod

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
)

// A session the host lists but that has no statusline reading yet is live (blank context), not offline.
func TestRemoteRoster_ListedSessionWithoutContextIsLive(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("a1", "hostM", "mk2", rowActive)
	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) {
		return []ipeers.PeerRecord{{RowKind: "session", Agent: &ipeers.AgentInfo{Type: "cc", SessionID: "sid-a1"}}}, nil
	}
	f.m.readRemoteHost(context.Background(), "hostM")
	r, err := f.m.buildRoster()
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := rosterMemberOf(t, r, "sid-a1"); !m.Live || m.Context != nil || m.ContextUnavailable {
		t.Fatalf("a1 = %+v", m)
	}
}

// Two overlapping reads of one host: the one that started later wins even when it finishes first.
func TestRemoteView_OlderOverlappingReadDoesNotOverwrite(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("a1", "hostM", "mk2", rowActive)
	slow := make(chan struct{})
	answers := []func() ([]ipeers.PeerRecord, error){
		func() ([]ipeers.PeerRecord, error) { <-slow; return nil, errors.New("stale failure") }, // started first
		func() ([]ipeers.PeerRecord, error) { return []ipeers.PeerRecord{remoteRecord("sid-a1", 20, "m", "")}, nil },
	}
	var n atomic.Int32
	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) {
		return answers[n.Add(1)-1]()
	}
	first := make(chan struct{})
	go func() { defer close(first); f.m.readRemoteHost(context.Background(), "hostM") }()
	waitFor(t, func() bool { return n.Load() == 1 })
	f.clock.Add(5) // the second read starts later
	f.m.readRemoteHost(context.Background(), "hostM")
	close(slow)
	<-first
	if c, unavailable := f.m.remoteContextOf("hostM", "sid-a1"); c == nil || unavailable {
		t.Fatalf("the older read overwrote the newer: %+v unavailable=%v", c, unavailable)
	}
}
