package arbitrator

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent/observation"
)

func TestFrameState_ZeroSession_Generation0(t *testing.T) {
	fs := newFrameState()
	sess := fs.getOrCreateSession("s1")
	if sess == nil {
		t.Fatal("getOrCreateSession returned nil")
	}
	if sess.Generation != 0 {
		t.Errorf("Generation = %d, want 0", sess.Generation)
	}
	if sess.Actors == nil {
		t.Error("Actors map is nil")
	}
}

func TestFrameState_GetOrCreateSession_ReturnsSameInstance(t *testing.T) {
	fs := newFrameState()
	a := fs.getOrCreateSession("s1")
	b := fs.getOrCreateSession("s1")
	if a != b {
		t.Error("getOrCreateSession returned different instances for same key")
	}
}

func TestFrameState_ForceEndOldGenActors_SetsEndedAt_SessionRestart(t *testing.T) {
	fs := newFrameState()
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)

	sess := fs.getOrCreateSession("s1")
	sess.Generation = 1

	k1 := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	k2 := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a2"}
	kOther := observation.ActorKey{SessionID: "s1", Generation: 2, ActorID: "b1"}
	fs.upsertActor(k1, func(a *actorSummary) { a.Status = "active"; a.LastActivity = now })
	fs.upsertActor(k2, func(a *actorSummary) { a.Status = "waiting"; a.LastActivity = now })
	fs.upsertActor(kOther, func(a *actorSummary) { a.Status = "active"; a.LastActivity = now })

	ended := fs.forceEndOldGenActors("s1", 1, now)

	if len(ended) != 2 {
		t.Fatalf("ended = %v, want 2 keys", ended)
	}
	a1, _ := fs.actor(k1)
	if a1.EndedAt == nil || !a1.EndedAt.Equal(now) {
		t.Errorf("k1.EndedAt = %v, want %v", a1.EndedAt, now)
	}
	if a1.EndedReason != "session_restart" {
		t.Errorf("k1.EndedReason = %q, want session_restart", a1.EndedReason)
	}
	if a1.Status != "ended" {
		t.Errorf("k1.Status = %q, want ended", a1.Status)
	}
	other, _ := fs.actor(kOther)
	if other.EndedAt != nil {
		t.Errorf("kOther should not be ended, got EndedAt = %v", other.EndedAt)
	}
}

func TestFrameState_ForceEndOldGenActors_SkipsAlreadyEnded(t *testing.T) {
	fs := newFrameState()
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	earlier := now.Add(-time.Hour)

	sess := fs.getOrCreateSession("s1")
	sess.Generation = 1
	k := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	fs.upsertActor(k, func(a *actorSummary) {
		a.EndedAt = &earlier
		a.EndedReason = "prior"
		a.Status = "ended"
	})

	ended := fs.forceEndOldGenActors("s1", 1, now)

	if len(ended) != 0 {
		t.Errorf("already-ended actor should not be re-ended, got %v", ended)
	}
	a, _ := fs.actor(k)
	if !a.EndedAt.Equal(earlier) {
		t.Errorf("EndedAt changed: got %v, want %v", a.EndedAt, earlier)
	}
	if a.EndedReason != "prior" {
		t.Errorf("EndedReason changed: got %q", a.EndedReason)
	}
}

func TestFrameState_ActorLifecycle_Track(t *testing.T) {
	fs := newFrameState()
	t0 := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Second)
	t2 := t0.Add(2 * time.Second)

	k := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	fs.upsertActor(k, func(a *actorSummary) {
		a.Status = "active"
		a.LastActivity = t0
	})
	a, ok := fs.actor(k)
	if !ok {
		t.Fatal("actor not found after upsert")
	}
	if a.Status != "active" || !a.LastActivity.Equal(t0) {
		t.Errorf("initial state mismatch: %+v", a)
	}
	if a.WatcherTokens == nil {
		t.Error("WatcherTokens should be initialized to non-nil map on insert")
	}

	fs.upsertActor(k, func(a *actorSummary) { a.LastActivity = t1 })
	a, _ = fs.actor(k)
	if !a.LastActivity.Equal(t1) {
		t.Errorf("LastActivity = %v, want %v", a.LastActivity, t1)
	}
	if a.Status != "active" {
		t.Errorf("Status regressed: %q", a.Status)
	}

	fs.upsertActor(k, func(a *actorSummary) {
		a.EndedAt = &t2
		a.EndedReason = "completed"
		a.Status = "ended"
	})
	a, _ = fs.actor(k)
	if a.EndedAt == nil || !a.EndedAt.Equal(t2) {
		t.Errorf("EndedAt = %v, want %v", a.EndedAt, t2)
	}
}

func TestFrameState_WatcherToken_Rotation(t *testing.T) {
	fs := newFrameState()
	k := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	fs.upsertActor(k, func(a *actorSummary) { a.Status = "active" })

	fs.rotateWatcherToken(k, "probe-1", "tok-1")
	a, _ := fs.actor(k)
	if a.WatcherTokens["probe-1"] != "tok-1" {
		t.Errorf("got %q, want tok-1", a.WatcherTokens["probe-1"])
	}

	fs.rotateWatcherToken(k, "probe-1", "tok-2")
	a, _ = fs.actor(k)
	if a.WatcherTokens["probe-1"] != "tok-2" {
		t.Errorf("got %q, want tok-2", a.WatcherTokens["probe-1"])
	}

	fs.rotateWatcherToken(k, "probe-2", "tok-P2")
	a, _ = fs.actor(k)
	if a.WatcherTokens["probe-1"] != "tok-2" || a.WatcherTokens["probe-2"] != "tok-P2" {
		t.Errorf("per-probe isolation broken: %+v", a.WatcherTokens)
	}
}

func TestFrameState_WatcherToken_ClearOnSessionStart(t *testing.T) {
	fs := newFrameState()
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)

	k := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	fs.upsertActor(k, func(a *actorSummary) { a.Status = "active"; a.LastActivity = now })
	fs.rotateWatcherToken(k, "probe-1", "tok-old")

	fs.forceEndOldGenActors("s1", 1, now)

	a, _ := fs.actor(k)
	if len(a.WatcherTokens) != 0 {
		t.Errorf("WatcherTokens not cleared on session restart: %+v", a.WatcherTokens)
	}
	if a.EndedAt == nil {
		t.Error("actor not ended by forceEndOldGenActors")
	}
}

func TestFrameState_IsPrimary_Transfer(t *testing.T) {
	fs := newFrameState()
	t0 := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Second)

	k1 := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	k2 := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a2"}
	fs.upsertActor(k1, func(a *actorSummary) { a.Status = "active"; a.LastActivity = t0 })
	fs.upsertActor(k2, func(a *actorSummary) { a.Status = "active"; a.LastActivity = t0 })

	fs.setPrimary(k1, t0)
	a1, _ := fs.actor(k1)
	if !a1.IsPrimary {
		t.Error("k1 should be primary after setPrimary")
	}

	fs.setPrimary(k2, t1)
	a1, _ = fs.actor(k1)
	a2, _ := fs.actor(k2)
	if a1.IsPrimary {
		t.Error("k1 should no longer be primary after transfer")
	}
	if a1.EndedAt == nil || !a1.EndedAt.Equal(t1) {
		t.Errorf("k1.EndedAt = %v, want %v", a1.EndedAt, t1)
	}
	if a1.EndedReason != "replaced_by_new_primary" {
		t.Errorf("k1.EndedReason = %q, want replaced_by_new_primary", a1.EndedReason)
	}
	if !a2.IsPrimary {
		t.Error("k2 should be primary after transfer")
	}
}

func TestFrameState_AllActiveActors_FiltersEnded(t *testing.T) {
	fs := newFrameState()
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)

	sess := fs.getOrCreateSession("s1")
	sess.Generation = 1
	kAlive := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "alive"}
	kEnded := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "ended"}
	fs.upsertActor(kAlive, func(a *actorSummary) { a.Status = "active"; a.LastActivity = now })
	fs.upsertActor(kEnded, func(a *actorSummary) {
		a.Status = "ended"
		a.EndedAt = &now
		a.EndedReason = "done"
	})

	active := fs.allActiveActors()
	if _, ok := active[kAlive]; !ok {
		t.Error("alive actor missing from allActiveActors")
	}
	if _, ok := active[kEnded]; ok {
		t.Error("ended actor should not appear in allActiveActors")
	}
}

func TestFrameState_AllActiveActors_FiltersOldGeneration(t *testing.T) {
	fs := newFrameState()
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)

	sess := fs.getOrCreateSession("s1")
	sess.Generation = 2

	kOld := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "old"}
	kCur := observation.ActorKey{SessionID: "s1", Generation: 2, ActorID: "cur"}
	fs.upsertActor(kOld, func(a *actorSummary) { a.Status = "active"; a.LastActivity = now })
	fs.upsertActor(kCur, func(a *actorSummary) { a.Status = "active"; a.LastActivity = now })

	active := fs.allActiveActors()
	if _, ok := active[kOld]; ok {
		t.Errorf("old-generation actor should be filtered; active = %v", active)
	}
	if _, ok := active[kCur]; !ok {
		t.Error("current-generation actor missing")
	}
}

func TestFrameState_Actor_NotFound(t *testing.T) {
	fs := newFrameState()
	k := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "nope"}
	if _, ok := fs.actor(k); ok {
		t.Error("actor(nonexistent) should return ok=false")
	}
}
