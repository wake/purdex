package peers

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
)

func familiarKeys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	// small sets: a fixed order for the comparison
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// The familiar set each other host published, unioned (name pool v2 spec §6.1, L6): a host's answer replaces its own set, and the
// union is every code any host named. Mutation gate: union only the last host → red; keep a replaced set → red.
func TestRemoteNameSnapshot_FamiliarIsTheUnionOfTheHostsLatestSets(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	s := newRemoteNameSnapshot()
	s.recordFamiliar("h1", &ipeers.NamesInfo{Familiar: []string{"freya", "hiro"}}, s.ticket(), t0)
	s.recordFamiliar("h2", &ipeers.NamesInfo{Familiar: []string{"hiro", "kenji"}}, s.ticket(), t0)
	if got, want := familiarKeys(s.familiarUnion(t0)), []string{"freya", "hiro", "kenji"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("union = %v, want %v", got, want)
	}
	s.recordFamiliar("h1", &ipeers.NamesInfo{Familiar: []string{"emma"}}, s.ticket(), t0.Add(time.Second))
	if got, want := familiarKeys(s.familiarUnion(t0.Add(time.Second))), []string{"emma", "hiro", "kenji"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after h1 replaced its set: union = %v, want %v", got, want)
	}
}

// A daemon from before names.familiar (the answer has no names) is an empty set, and that replaces what it published before.
// No data at all is nil, not an empty map (the rules read nil as "no remote data").
func TestRemoteNameSnapshot_AnOldDaemonIsAnEmptySetAndNothingIsNil(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	s := newRemoteNameSnapshot()
	if s.familiarUnion(t0) != nil {
		t.Fatal("a snapshot with no host must read nil")
	}
	s.recordFamiliar("h1", &ipeers.NamesInfo{Familiar: []string{"freya"}}, s.ticket(), t0)
	s.recordFamiliar("h1", nil, s.ticket(), t0.Add(time.Second))
	if got := s.familiarUnion(t0.Add(time.Second)); len(got) != 0 {
		t.Fatalf("union = %v, want nothing after the host answered without names", got)
	}
}

// A remote is attacker-controlled: only codes of the shape a name takes count, each once, at most 64 per host.
func TestRemoteNameSnapshot_FamiliarKeepsOnlyWellFormedCodesUpToSixtyFour(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	s := newRemoteNameSnapshot()
	codes := []string{"freya", "Freya", "fr eya", "", "a", "../etc", "freya", "toolongtoolongtoolong"}
	for i := 0; i < 100; i++ {
		codes = append(codes, string(rune('a'+i%26))+string(rune('a'+(i/26)%26))+"x")
	}
	s.recordFamiliar("h1", &ipeers.NamesInfo{Familiar: codes}, s.ticket(), t0)
	got := s.familiarUnion(t0)
	if !got["freya"] || got["Freya"] || got["fr eya"] || got[""] || got["a"] || got["../etc"] || got["toolongtoolongtoolong"] {
		t.Fatalf("union = %v, want freya and no malformed code", got)
	}
	if len(got) != ipeers.MaxFamiliar {
		t.Fatalf("a host contributes %d codes, want the first %d well-formed ones", len(got), ipeers.MaxFamiliar)
	}
}

// An OK answer files its names.familiar, and RemoteFamiliar is the union the naming rules read; a failed answer keeps the
// previous set, and an answer from a daemon without names empties it. Mutation gate: do not file the set at fetch → red.
func TestFetchHostResult_AnOKAnswerFeedsRemoteFamiliar(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	var env ipeers.Envelope
	var fail bool
	m := poolFetchModule(func(context.Context, *http.Client, string, string) (ipeers.Envelope, error) {
		if fail {
			return ipeers.Envelope{}, context.DeadlineExceeded
		}
		return env, nil
	}, func() time.Time { return now })
	if m.RemoteFamiliar() != nil {
		t.Fatal("before any answer RemoteFamiliar must be nil")
	}
	env = okEnvelope()
	env.Names = &ipeers.NamesInfo{Familiar: []string{"freya", "hiro"}}
	if res := m.fetchHostResult(context.Background(), poolHost()); !res.OK {
		t.Fatalf("result not OK: %+v", res)
	}
	if got, want := familiarKeys(m.RemoteFamiliar()), []string{"freya", "hiro"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RemoteFamiliar = %v, want %v", got, want)
	}
	fail = true
	m.fetchHostResult(context.Background(), poolHost())
	if len(m.RemoteFamiliar()) != 2 {
		t.Fatal("a failed answer dropped the previous set")
	}
	fail = false
	env = okEnvelope() // an older daemon: no names
	m.fetchHostResult(context.Background(), poolHost())
	if got := m.RemoteFamiliar(); len(got) != 0 {
		t.Fatalf("RemoteFamiliar = %v, want nothing from a daemon without names", got)
	}
	if (&Module{}).RemoteFamiliar() != nil {
		t.Fatal("a Module without a pool must read nil")
	}
}

// A host that stops answering keeps its last set for the name TTL, then nothing; a host no longer configured is forgotten;
// an answer to a request older than the stored set is dropped.
func TestRemoteNameSnapshot_FamiliarFollowsTheSameTTLPruneAndOrderingAsTheNames(t *testing.T) {
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	s := newRemoteNameSnapshot()
	old, newer := s.ticket(), s.ticket()
	s.recordFamiliar("h1", &ipeers.NamesInfo{Familiar: []string{"hiro"}}, newer, t0)
	s.recordFamiliar("h1", &ipeers.NamesInfo{Familiar: []string{"stale"}}, old, t0)
	if got := familiarKeys(s.familiarUnion(t0)); !reflect.DeepEqual(got, []string{"hiro"}) {
		t.Fatalf("a slow older answer overwrote a newer one: %v", got)
	}
	if len(s.familiarUnion(t0.Add(remoteNamesTTL-time.Second))) != 1 {
		t.Fatal("the set vanished before the TTL")
	}
	if len(s.familiarUnion(t0.Add(remoteNamesTTL))) != 0 {
		t.Fatal("the set outlived the TTL")
	}
	s.prune(map[string]bool{"h2": true})
	if len(s.familiarUnion(t0)) != 0 {
		t.Fatal("prune kept a host that is no longer configured")
	}
	s.recordFamiliar("h1", &ipeers.NamesInfo{Familiar: []string{"late"}}, newer, t0)
	if len(s.familiarUnion(t0)) != 0 {
		t.Fatal("an answer sent before the prune brought a removed host back")
	}
}
