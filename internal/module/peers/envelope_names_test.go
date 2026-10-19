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
}

func (h *envelopeHistory) Leads(context.Context) ([]store.NameLead, error) { return nil, nil }
func (h *envelopeHistory) Pairs(context.Context) ([]store.NamePair, error) { return nil, nil }
func (h *envelopeHistory) Familiar(_ context.Context, limit int) ([]string, error) {
	h.limit = limit
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

// No history wired, or one that cannot be read, publishes no names (the readers take that as an empty set), and costs the
// envelope nothing else: it is still ok and not partial.
func TestLocalEnvelope_NoNamesWithoutAReadableHistory(t *testing.T) {
	for name, h := range map[string]NameHistoryReader{
		"none":       nil,
		"read error": &envelopeHistory{err: errors.New("meta db locked")},
	} {
		env := envelopeWithHistory(t, h)
		if env.Names != nil || env.Partial {
			t.Errorf("%s: names = %+v partial = %v, want no names and not partial", name, env.Names, env.Partial)
		}
		raw, _ := json.Marshal(env)
		if strings.Contains(string(raw), `"names"`) {
			t.Errorf("%s: envelope json carries names: %s", name, raw)
		}
	}
}
