package arbitrator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/wake/purdex/internal/module/agent/observation"
)

// ErrEvidenceUnmarshalable is returned by makeIdemKey when any Evidence entry
// carries a Value that json.Marshal cannot serialize (e.g. chan, func,
// complex types). Callers should log and bypass idempotency for such
// observations — the cache itself never stores a partial key.
var ErrEvidenceUnmarshalable = errors.New("evidence value not JSON-marshalable")

// idemKey wraps a canonical JSON string derived from an Observation's
// idempotency-relevant fields. Two observations produce equal idemKeys iff
// their canonical strings are byte-equal; Go map lookup uses that canonical
// string directly as the map key, so hash collisions never cause false
// duplicates (collisions only trigger a full key compare).
type idemKey struct {
	canonical string
}

// idemEntry is the per-key watermark tracked by the idempotency cache.
type idemEntry struct {
	Seq         int64
	LastTouched time.Time
}

// idemCache is an in-memory, single-owner idempotency table: duplicate
// observations (same canonical key, seq ≤ watermark) are rejected.
//
// pruneAfter defaults to 5 minutes — entries untouched for that long are
// removed on the next Prune call.
type idemCache struct {
	now        func() time.Time
	entries    map[idemKey]idemEntry
	pruneAfter time.Duration
}

// defaultIdemPruneAfter is the default staleness threshold applied by
// newIdemCache. Callers can override by assigning pruneAfter directly after
// construction.
const defaultIdemPruneAfter = 5 * time.Minute

// newIdemCache returns an empty idempotency cache. The now clock seam is
// retained only for future use (Check/Prune accept an explicit now from the
// caller, keeping test assertions hermetic).
func newIdemCache(now func() time.Time) *idemCache {
	return &idemCache{
		now:        now,
		entries:    make(map[idemKey]idemEntry),
		pruneAfter: defaultIdemPruneAfter,
	}
}

// canonicalObs is the internal shape that makeIdemKey serializes. Field
// tags fix the JSON key order (Go encoding/json writes struct fields in
// declaration order); evidence is sorted before marshaling so the result is
// order-invariant across equivalent inputs.
type canonicalObs struct {
	ActorKey   observation.ActorKey   `json:"actor_key"`
	SourceKind observation.SourceKind `json:"source_kind"`
	Action     string                 `json:"action"`
	Evidence   []canonicalEvidence    `json:"evidence"`
}

type canonicalEvidence struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// makeIdemKey builds a stable canonical-JSON key for obs. Evidence order is
// normalized (by Key, then by marshaled-value string) before serialization,
// but duplicate (Key,Value) pairs are preserved — two identical entries do
// not collapse to one.
//
// Returns ErrEvidenceUnmarshalable (wrapped with context) when any Evidence
// Value cannot be json.Marshal'd.
func makeIdemKey(obs observation.Observation) (idemKey, error) {
	ev := make([]canonicalEvidence, len(obs.Evidence))
	for i, e := range obs.Evidence {
		raw, err := json.Marshal(e.Value)
		if err != nil {
			return idemKey{}, fmt.Errorf("%w: key=%q: %v", ErrEvidenceUnmarshalable, e.Key, err)
		}
		ev[i] = canonicalEvidence{Key: e.Key, Value: raw}
	}
	sort.SliceStable(ev, func(i, j int) bool {
		if ev[i].Key != ev[j].Key {
			return ev[i].Key < ev[j].Key
		}
		return bytes.Compare(ev[i].Value, ev[j].Value) < 0
	})

	payload := canonicalObs{
		ActorKey:   obs.Proposal.ActorKey,
		SourceKind: obs.SourceKind,
		Action:     obs.Action,
		Evidence:   ev,
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		// Should not happen — ActorKey / SourceKind / Action / Evidence are
		// all marshalable primitives. Defensive return keeps the contract
		// explicit.
		return idemKey{}, fmt.Errorf("marshal canonical observation: %w", err)
	}
	return idemKey{canonical: string(buf)}, nil
}

// Check records the observation's seq against the per-key watermark. It
// accepts (and updates the watermark) when seq strictly exceeds the stored
// value; equal-or-lower seqs are rejected with a diagnostic reason string.
//
// First-seen observations always accept. Reasons:
//   - "duplicate_lower_seq" : seq < stored watermark
//   - "duplicate_equal_seq" : seq == stored watermark
//   - ""                    : accepted
func (c *idemCache) Check(obs observation.Observation, seq int64, now time.Time) (bool, string) {
	key, err := makeIdemKey(obs)
	if err != nil {
		// Unmarshalable evidence — caller (apply pipeline) decides policy.
		// idemCache contract keeps this path a no-op: do not mutate state,
		// do not accept the observation via this layer.
		return false, ""
	}
	if entry, ok := c.entries[key]; ok {
		if seq < entry.Seq {
			return false, "duplicate_lower_seq"
		}
		if seq == entry.Seq {
			return false, "duplicate_equal_seq"
		}
	}
	c.entries[key] = idemEntry{Seq: seq, LastTouched: now}
	return true, ""
}

// Prune removes every entry whose LastTouched + pruneAfter is strictly
// before now. Returns the number of entries removed.
func (c *idemCache) Prune(now time.Time) int {
	removed := 0
	for key, entry := range c.entries {
		if entry.LastTouched.Add(c.pruneAfter).Before(now) {
			delete(c.entries, key)
			removed++
		}
	}
	return removed
}
