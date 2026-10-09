// internal/module/team/remote_view_attack_test.go
package teammod

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// Two reads that start in the SAME millisecond: the one that started later still wins (generation, not time).
func TestRemoteView_SameMillisecondOlderReadDoesNotOverwrite(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("a1", "hostM", "mk2", rowActive)
	slow := make(chan struct{})
	var n atomic.Int32
	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) {
		if n.Add(1) == 1 {
			<-slow
			return nil, errors.New("stale failure")
		}
		return []ipeers.PeerRecord{remoteRecord("sid-a1", 20, "m", "")}, nil
	}
	first := make(chan struct{})
	go func() { defer close(first); f.m.readRemoteHost(context.Background(), "hostM") }()
	waitFor(t, func() bool { return n.Load() == 1 })
	f.m.readRemoteHost(context.Background(), "hostM") // same clock value
	close(slow)
	<-first
	if c, unavailable := f.m.remoteContextOf("hostM", "sid-a1"); c == nil || unavailable {
		t.Fatalf("the older read overwrote the newer: %+v unavailable=%v", c, unavailable)
	}
}

// A host that holds only finished rows (released / killed / gone / failed) is not asked: a dead old host must not make
// every view wait.
func TestRemoteView_FinishedRowsDoNotAskTheirHost(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("x1", "hostM", "mk1", string(team.MemberReleased))
	f.remoteRow("x2", "hostM", "mk2", string(team.MemberGone))
	f.remoteRow("x3", "hostN", "mk3", string(team.MemberFailed))
	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) {
		t.Error("a host with no member in play was asked")
		return nil, nil
	}
	f.leadTeamView()
	// And one in play still is.
	f.remoteRow("a1", "hostN", "mk4", rowActive)
	var asked []string
	f.m.peerRecords = func(_ context.Context, h string) ([]ipeers.PeerRecord, error) {
		asked = append(asked, h)
		return nil, nil
	}
	f.leadTeamView()
	if len(asked) != 1 || asked[0] != "hostN" {
		t.Fatalf("asked %v, want only hostN", asked)
	}
}
