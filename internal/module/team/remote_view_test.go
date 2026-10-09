// internal/module/team/remote_view_test.go
package teammod

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// X5 (cross-host team spec §8): a remote member is addressed by its host's alias, shows that host's state, and its
// context / model come from the member host's GET /api/peers.

const leadTeamURL = "/api/team?origin_inbox=%2Ftmp%2F10.sock"

func remoteRecord(sessionID string, pct float64, model, effort string) ipeers.PeerRecord {
	return ipeers.PeerRecord{RowKind: "session", Agent: &ipeers.AgentInfo{Type: "cc", SessionID: sessionID,
		Context: &ipeers.ContextInfo{UsedPercentage: &pct, Window: 200000, At: 7, ModelID: model, Effort: effort}}}
}

func (f *fixture) leadTeamView() team.TeamView {
	f.t.Helper()
	code, v, e := call[team.TeamView](f, http.MethodGet, leadTeamURL, nil)
	if code != http.StatusOK {
		f.t.Fatalf("GET /api/team = %d %+v", code, e)
	}
	return v
}

func memberOf(t *testing.T, v team.TeamView, sessionID string) team.Member {
	t.Helper()
	for _, m := range v.Members {
		if m.SessionID == sessionID {
			return m
		}
	}
	t.Fatalf("no member %s in %+v", sessionID, v.Members)
	return team.Member{}
}

func TestRemoteView_AddressStateAndContextOfARemoteMember(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowJoining)
	f.m.peerRecords = func(_ context.Context, host string) ([]ipeers.PeerRecord, error) {
		if host != "hostM" {
			t.Errorf("asked %s", host)
		}
		return []ipeers.PeerRecord{remoteRecord("sid-abc12", 41, "claude-opus-5-5", "high"), remoteRecord("someone-else", 99, "x", "y")}, nil
	}
	m := memberOf(t, f.leadTeamView(), "sid-abc12")
	if m.Address != "air26/"+remoteRef || m.HostAlias != "air26" || m.HostID != "hostM" || m.State != team.MemberJoining {
		t.Fatalf("member = %+v", m)
	}
	if c := m.Context; c == nil || c.UsedPercentage == nil || *c.UsedPercentage != 41 || c.ModelID != "claude-opus-5-5" || c.Effort != "high" {
		t.Fatalf("context = %+v", m.Context)
	}
	if m.ContextUnavailable {
		t.Fatal("flagged unavailable though the host answered")
	}
}

func TestRemoteView_UnreachableHostIsBlankAndFlagged(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) { return nil, errors.New("timeout") }
	m := memberOf(t, f.leadTeamView(), "sid-abc12")
	if m.Context != nil || !m.ContextUnavailable {
		t.Fatalf("member = %+v", m)
	}
	if m.Address != "air26/"+remoteRef {
		t.Fatalf("address = %q", m.Address)
	}
}

// A reading is fresh for a while: two views inside it ask the host once; after it, again.
func TestRemoteView_ReadingsAreCached(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	var n atomic.Int32
	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) {
		n.Add(1)
		return []ipeers.PeerRecord{remoteRecord("sid-abc12", 10, "m", "")}, nil
	}
	f.leadTeamView()
	f.leadTeamView()
	if n.Load() != 1 {
		t.Fatalf("asked %d times inside the fresh window", n.Load())
	}
	f.clock.Add(remoteReadingFreshMs)
	f.leadTeamView()
	if n.Load() != 2 {
		t.Fatalf("asked %d times after it", n.Load())
	}
}

// A member on the lead's own host is untouched: no alias, no host call.
func TestRemoteView_LocalMembersAskNoHost(t *testing.T) {
	f, _ := remoteFixture(t)
	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) {
		t.Error("a host was asked for a team with no remote member")
		return nil, nil
	}
	f.leadTeamView()
}

// The roster never waits on a remote call: a build returns while the host's answer is still pending, then the answer
// lands in the cache and the roster is signalled.
func TestRemoteView_RosterDoesNotWaitOnTheHost(t *testing.T) {
	f, _ := remoteFixture(t)
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	release := make(chan struct{})
	f.m.peerRecords = func(ctx context.Context, _ string) ([]ipeers.PeerRecord, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return []ipeers.PeerRecord{remoteRecord("sid-abc12", 55, "m", "")}, nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := f.m.buildRoster(); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the roster build waited on the remote host")
	}
	close(release)
	waitFor(t, func() bool {
		c, _ := f.m.remoteContextOf("hostM", "sid-abc12")
		return c != nil
	})
}
