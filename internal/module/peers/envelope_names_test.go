package peers

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// envelopeHistory is a name history that answers Familiar with fixed codes (or an error) and records the limit it was asked for.
type envelopeHistory struct {
	codes []string
	err   error
	limit int
	// left is how long before its deadline the Familiar call's context had (0: it had none)
	left time.Duration
}

func (h *envelopeHistory) Leads(context.Context) ([]store.NameLead, error) { return nil, nil }
func (h *envelopeHistory) Pairs(context.Context) ([]store.NamePair, error) { return nil, nil }
func (h *envelopeHistory) Familiar(ctx context.Context, limit int) ([]string, error) {
	h.limit = limit
	if d, ok := ctx.Deadline(); ok {
		h.left = time.Until(d)
	}
	return h.codes, h.err
}

func envelopeWithHistory(t *testing.T, h NameHistoryReader) ipeers.Envelope {
	t.Helper()
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, &fakeSessions{}, &fakeOwners{}, t.TempDir(), allLiveLiveness(fixture76973ProcStart), &fakeClock{times: []time.Time{time.Unix(0, 0)}}, time.Second)
	if h != nil {
		m.WithNameHistory(h)
	}
	env := m.localEnvelope(t.Context(), "mlab:abc123", "mlab")
	if !env.OK {
		t.Fatalf("envelope not ok: %+v", env)
	}
	return env
}

// The local envelope publishes the host's familiar set: the last 64 codes of the name history (spec §6.1). Mutation gate: do not
// publish → red; ask for another limit → red.
func TestLocalEnvelope_PublishesTheFamiliarSet(t *testing.T) {
	h := &envelopeHistory{codes: []string{"hiro", "freya"}}
	env := envelopeWithHistory(t, h)
	if env.Names == nil || !reflect.DeepEqual(env.Names.Familiar, []string{"hiro", "freya"}) {
		t.Fatalf("names = %+v, want familiar [hiro freya]", env.Names)
	}
	if h.limit != ipeers.MaxFamiliar || ipeers.MaxFamiliar != 64 {
		t.Fatalf("asked the history for %d codes, want 64", h.limit)
	}
}

// The familiar read has a bound of its own and does not borrow what is left of the inventory's budget: an inventory that used its
// budget up would otherwise lose the read, publish no names, and the readers (who take no names for an older daemon's empty set)
// would drop this host's valid set. Mutation gate: read under the inventory's context → red.
func TestLocalEnvelope_TheFamiliarReadHasItsOwnBound(t *testing.T) {
	h := &envelopeHistory{codes: []string{"hiro"}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, &fakeSessions{}, &fakeOwners{}, t.TempDir(), allLiveLiveness(fixture76973ProcStart), &fakeClock{times: []time.Time{time.Unix(0, 0)}}, time.Second)
	m.budget = 400 * time.Millisecond // shorter than the name store's bound: a read under the inventory's context would have less
	m.WithNameHistory(h)
	env := m.localEnvelope(t.Context(), "mlab:abc123", "mlab")
	if !env.OK || env.Names == nil {
		t.Fatalf("envelope = %+v, want ok with names", env)
	}
	if h.left < 700*time.Millisecond || h.left > namerTimeout {
		t.Fatalf("the read had %s left, want a bound of its own near %s, more than the inventory's 400ms budget leaves", h.left, namerTimeout)
	}
}

// A history that knows nobody still publishes names, with an empty array (never null): "I read mine and it is empty" is not the
// same as an older daemon's silence on the wire.
func TestLocalEnvelope_AnEmptyHistoryPublishesAnEmptyArray(t *testing.T) {
	env := envelopeWithHistory(t, &envelopeHistory{})
	if env.Names == nil || env.Names.Familiar == nil || len(env.Names.Familiar) != 0 {
		t.Fatalf("names = %+v, want an empty non-nil familiar", env.Names)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"names":{"familiar":[]}`) {
		t.Fatalf("envelope json = %s, want names with an empty familiar array", raw)
	}
}

// No history wired publishes no names (a reader takes that as an older daemon's empty set), and costs the envelope nothing: it is
// still ok and not partial.
func TestLocalEnvelope_NoNamesWithoutAHistory(t *testing.T) {
	env := envelopeWithHistory(t, nil)
	if env.Names != nil || env.Partial {
		t.Fatalf("names = %+v partial = %v, want no names and not partial", env.Names, env.Partial)
	}
	raw, _ := json.Marshal(env)
	if strings.Contains(string(raw), `"names"`) {
		t.Fatalf("envelope json carries names: %s", raw)
	}
}

// A history that cannot be read says so on the wire (names.unavailable), so a reader keeps what this host published before instead of
// taking the silence for an older daemon's empty set. The envelope itself is still ok and not partial: the rows owe the history
// nothing. Mutation gate: publish nothing, or an empty set without the flag → red.
func TestLocalEnvelope_AHistoryThatCannotBeReadSaysUnavailable(t *testing.T) {
	env := envelopeWithHistory(t, &envelopeHistory{err: errors.New("meta db locked")})
	if env.Partial || env.Names == nil || !env.Names.Unavailable || env.Names.Familiar == nil || len(env.Names.Familiar) != 0 {
		t.Fatalf("names = %+v partial = %v, want unavailable with an empty array, not partial", env.Names, env.Partial)
	}
	raw, _ := json.Marshal(env)
	if !strings.Contains(string(raw), `"names":{"familiar":[],"unavailable":true}`) {
		t.Fatalf("envelope json = %s, want names with familiar [] and unavailable true", raw)
	}
	// and a readable one never carries the flag
	ok, _ := json.Marshal(envelopeWithHistory(t, &envelopeHistory{codes: []string{"hiro"}}))
	if strings.Contains(string(ok), "unavailable") {
		t.Fatalf("a readable history carries the flag: %s", ok)
	}
}
