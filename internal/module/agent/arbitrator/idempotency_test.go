package arbitrator

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent/observation"
)

func baseObs() observation.Observation {
	return observation.Observation{
		SessionID:          "s1",
		ObservedGeneration: 1,
		SourceKind:         observation.SourceHook,
		Action:             "session_start",
		Phase:              observation.PhaseCommitted,
		Proposal: observation.StateProposal{
			ActorKey: observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"},
		},
		Evidence: []observation.EvidenceRef{
			{Key: "pid", Value: 42},
			{Key: "cmd", Value: "bash"},
		},
	}
}

func TestIdemKey_Determinism_SameInputs_SameKey(t *testing.T) {
	a, err := makeIdemKey(baseObs())
	if err != nil {
		t.Fatalf("makeIdemKey err: %v", err)
	}
	b, err := makeIdemKey(baseObs())
	if err != nil {
		t.Fatalf("makeIdemKey err: %v", err)
	}
	if a != b {
		t.Errorf("same input produced different keys:\n a=%v\n b=%v", a, b)
	}
}

func TestIdemKey_EvidenceOrderInvariant_SortedBeforeSerialization(t *testing.T) {
	o1 := baseObs()
	o1.Evidence = []observation.EvidenceRef{
		{Key: "a", Value: 1},
		{Key: "b", Value: 2},
	}
	o2 := baseObs()
	o2.Evidence = []observation.EvidenceRef{
		{Key: "b", Value: 2},
		{Key: "a", Value: 1},
	}
	k1, err := makeIdemKey(o1)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := makeIdemKey(o2)
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 {
		t.Errorf("evidence ordering must not affect key:\n k1=%v\n k2=%v", k1, k2)
	}
}

func TestIdemKey_EvidenceDuplicateKeyValue_NotDeduped(t *testing.T) {
	o := baseObs()
	o.Evidence = []observation.EvidenceRef{
		{Key: "pid", Value: 1},
		{Key: "pid", Value: 1},
	}
	oSingle := baseObs()
	oSingle.Evidence = []observation.EvidenceRef{
		{Key: "pid", Value: 1},
	}

	kDup, err := makeIdemKey(o)
	if err != nil {
		t.Fatal(err)
	}
	kSingle, err := makeIdemKey(oSingle)
	if err != nil {
		t.Fatal(err)
	}
	if kDup == kSingle {
		t.Error("duplicate evidence entries must NOT be de-duplicated (key should differ from single-entry canonical)")
	}

	// Sanity: same duplicated-evidence obs produces a stable key.
	kDup2, err := makeIdemKey(o)
	if err != nil {
		t.Fatal(err)
	}
	if kDup != kDup2 {
		t.Error("duplicated-evidence key should be stable across calls")
	}
}

func TestIdemKey_ActorKeyEvidence_ShapeIncluded(t *testing.T) {
	ak := observation.ActorKey{SessionID: "s-foo", Generation: 7, ActorID: "a-bar"}
	o := baseObs()
	o.Evidence = []observation.EvidenceRef{
		{Key: "target", Value: ak},
	}
	k, err := makeIdemKey(o)
	if err != nil {
		t.Fatal(err)
	}
	canon := k.canonical
	for _, needle := range []string{
		`"session_id":"s-foo"`,
		`"generation":7`,
		`"actor_id":"a-bar"`,
	} {
		if !strings.Contains(canon, needle) {
			t.Errorf("canonical missing %q; got %s", needle, canon)
		}
	}
}

func TestIdemKey_UnmarshalableEvidence_ReturnsSentinelError(t *testing.T) {
	ch := make(chan int)
	o := baseObs()
	o.Evidence = []observation.EvidenceRef{
		{Key: "ch", Value: ch},
	}
	_, err := makeIdemKey(o)
	if err == nil {
		t.Fatal("expected error for non-marshalable evidence")
	}
	if !errors.Is(err, ErrEvidenceUnmarshalable) {
		t.Errorf("want ErrEvidenceUnmarshalable, got %v", err)
	}
}

func TestIdemKey_CanonicalBytesAreMapKey_NoHashCollisionFalseDup(t *testing.T) {
	// Two observations with different action strings must yield different keys
	// and not collide in the idemCache map.
	o1 := baseObs()
	o1.Action = "action-A"
	o2 := baseObs()
	o2.Action = "action-B"

	k1, err := makeIdemKey(o1)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := makeIdemKey(o2)
	if err != nil {
		t.Fatal(err)
	}
	if k1 == k2 {
		t.Error("different actions must not share a canonical key")
	}

	now := time.Unix(0, 0)
	cache := newIdemCache(func() time.Time { return now })
	acc1, _ := cache.Check(o1, 1, now)
	acc2, _ := cache.Check(o2, 1, now)
	if !acc1 || !acc2 {
		t.Errorf("first-seen keys both should be accepted; got %v %v", acc1, acc2)
	}
}

func TestIdemCache_FirstSeen_Accepts(t *testing.T) {
	now := time.Unix(0, 0)
	c := newIdemCache(func() time.Time { return now })
	acc, reason := c.Check(baseObs(), 1, now)
	if !acc {
		t.Errorf("first-seen should accept; got reason=%q", reason)
	}
	if reason != "" {
		t.Errorf("accepted entries should have empty reason, got %q", reason)
	}
}

func TestIdemCache_DuplicateLowerSeq_Rejected(t *testing.T) {
	now := time.Unix(0, 0)
	c := newIdemCache(func() time.Time { return now })
	if acc, _ := c.Check(baseObs(), 5, now); !acc {
		t.Fatal("first should accept")
	}
	acc, reason := c.Check(baseObs(), 3, now)
	if acc {
		t.Error("lower seq must be rejected")
	}
	if reason != "duplicate_lower_seq" {
		t.Errorf("reason = %q, want duplicate_lower_seq", reason)
	}
}

func TestIdemCache_DuplicateEqualSeq_Rejected(t *testing.T) {
	now := time.Unix(0, 0)
	c := newIdemCache(func() time.Time { return now })
	if acc, _ := c.Check(baseObs(), 5, now); !acc {
		t.Fatal("first should accept")
	}
	acc, reason := c.Check(baseObs(), 5, now)
	if acc {
		t.Error("equal seq must be rejected")
	}
	if reason != "duplicate_equal_seq" {
		t.Errorf("reason = %q, want duplicate_equal_seq", reason)
	}
}

func TestIdemCache_HigherSeq_Accepts_UpdatesWatermark(t *testing.T) {
	now := time.Unix(0, 0)
	c := newIdemCache(func() time.Time { return now })
	if acc, _ := c.Check(baseObs(), 1, now); !acc {
		t.Fatal("seq=1 must accept")
	}
	if acc, _ := c.Check(baseObs(), 7, now); !acc {
		t.Fatal("seq=7 must accept (watermark jump)")
	}
	// Now anything ≤ 7 should reject.
	if acc, _ := c.Check(baseObs(), 7, now); acc {
		t.Error("seq=7 duplicate should reject")
	}
	if acc, _ := c.Check(baseObs(), 6, now); acc {
		t.Error("seq=6 (below new watermark) should reject")
	}
	// seq=8 accepts.
	if acc, _ := c.Check(baseObs(), 8, now); !acc {
		t.Error("seq=8 must accept")
	}
}

func TestIdemCache_PruneStale_5Min(t *testing.T) {
	t0 := time.Unix(0, 0)
	clk := t0
	c := newIdemCache(func() time.Time { return clk })

	// Two entries at t0.
	o1 := baseObs()
	o1.Action = "a1"
	o2 := baseObs()
	o2.Action = "a2"
	if acc, _ := c.Check(o1, 1, clk); !acc {
		t.Fatal()
	}
	if acc, _ := c.Check(o2, 1, clk); !acc {
		t.Fatal()
	}

	// Advance t to t0+4m — nothing should be pruned yet.
	clk = t0.Add(4 * time.Minute)
	if n := c.Prune(clk); n != 0 {
		t.Errorf("Prune at 4m = %d, want 0", n)
	}

	// Touch o1 at t0+4m (higher seq) — LastTouched updated.
	if acc, _ := c.Check(o1, 10, clk); !acc {
		t.Fatal("higher seq should accept")
	}

	// Advance to t0+6m. o2 (last touched t0) is > 5m stale → pruned; o1 (touched at 4m, now 2m old) survives.
	clk = t0.Add(6 * time.Minute)
	n := c.Prune(clk)
	if n != 1 {
		t.Errorf("Prune at 6m = %d, want 1", n)
	}

	// o2 pruned: first-seen again at seq=1 must accept.
	if acc, _ := c.Check(o2, 1, clk); !acc {
		t.Error("pruned entry should behave as first-seen on re-submit")
	}
	// o1 survives: seq=10 duplicate should reject.
	if acc, _ := c.Check(o1, 10, clk); acc {
		t.Error("o1 watermark lost — Prune over-pruned")
	}
}
