package teammod

import (
	"fmt"
	"sync/atomic"
	"testing"

	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// snapOrigins is a resolver that can hand out a per-pass presence reader, as the peers OriginResolver does.
type snapOrigins struct {
	*fakeOrigins
	snapshots atomic.Int32 // PresenceSnapshot calls: one registry read each, in the real resolver
	direct    atomic.Int32 // LeadPresence asked of the resolver itself: one registry read each
	inSnap    atomic.Int32 // LeadPresence asked of a snapshot
}

func (s *snapOrigins) LeadPresence(sid string, pid int, procStart string) peersmod.Presence {
	s.direct.Add(1)
	return s.fakeOrigins.LeadPresence(sid, pid, procStart)
}

type snapReader struct{ s *snapOrigins }

func (r snapReader) LeadPresence(sid string, pid int, procStart string) peersmod.Presence {
	r.s.inSnap.Add(1)
	return r.s.fakeOrigins.LeadPresence(sid, pid, procStart)
}

func (s *snapOrigins) PresenceSnapshot() peersmod.PresenceReader {
	s.snapshots.Add(1)
	return snapReader{s}
}

// #3092: a liveness pass reads the peers registry once, however many leads and members it asks about.
func TestTick_LivenessPassTakesOnePresenceSnapshot(t *testing.T) {
	f := newFixture(t)
	so := &snapOrigins{fakeOrigins: f.origins}
	f.m.origins = so
	for i := 1; i <= 4; i++ {
		seedTeam(t, f.m.store, uid(i), fmt.Sprintf("sid-lead-%d", i), f.clock.Load())
		for j := 1; j <= 3; j++ {
			seedMember(t, f.m.store, fmt.Sprintf("op-%d-%d", i, j), uid(i), fmt.Sprintf("sid-m-%d-%d", i, j), 1)
		}
	}
	livenessTick(f)
	if got := so.snapshots.Load(); got != 1 {
		t.Fatalf("the pass took %d presence snapshots, want 1", got)
	}
	if got := so.direct.Load(); got != 0 {
		t.Fatalf("%d presence questions went to the resolver itself (one registry read each), want 0", got)
	}
	if got := so.inSnap.Load(); got < 12 {
		t.Fatalf("only %d questions were answered from the snapshot, want at least the 12 members", got)
	}
}

// A stuck kill claim is given back on "still there": that answer is never taken from the pass's earlier registry read,
// or a process that died after the read would be given back as active.
func TestSettleStuckKilling_IgnoresThePassSnapshot(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	sid := memberBySpawn(t, f.m.store, key).SessionID
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing', updated_at = ? WHERE spawn_op = ?`, f.m.now()-killClaimStuckAfter.Milliseconds()-1, key); err != nil {
		t.Fatal(err)
	}
	f.m.bootAt = f.m.now() - (team.BootGraceS+1)*1000
	so := &snapOrigins{fakeOrigins: f.origins}
	f.m.origins = so
	f.m.passPresence.Store(&presencePass{liveReader{}}) // the snapshot read it live, before it died
	f.origins.markDead(sid)
	f.m.settleStuckKillingMembers()
	if got := f.killRowState(key); got != team.MemberGone {
		t.Fatalf("a claim whose process died after the pass's read = %s, want gone", got)
	}
}

type liveReader struct{}

func (liveReader) LeadPresence(string, int, string) peersmod.Presence { return peersmod.PresenceLive }

// Between passes (and for a resolver with no snapshot) a question goes to the resolver itself.
func TestLeadPresence_WithoutAPassAsksTheResolver(t *testing.T) {
	f := newFixture(t)
	so := &snapOrigins{fakeOrigins: f.origins}
	f.m.origins = so
	f.m.leadPresence("sid-x", 1, "")
	if so.direct.Load() != 1 || so.snapshots.Load() != 0 {
		t.Fatalf("direct=%d snapshots=%d, want 1 and 0", so.direct.Load(), so.snapshots.Load())
	}
	if f.m.passPresence.Load() != nil {
		t.Fatal("a snapshot is still held after the pass")
	}
	livenessTick(f)
	if f.m.passPresence.Load() != nil {
		t.Fatal("a snapshot is still held after the pass")
	}
}
