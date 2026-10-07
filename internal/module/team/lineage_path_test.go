package teammod

import (
	"errors"
	"fmt"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// Spec §15 "Lineage", end to end: twelve chained relays sid-0 → … → sid-12
// written by the store at cleared; the live head row built by ipeers.Build
// from the store's PreviousRefs() (as the peers module feeds it, Task
// 5a.5); Resolve of the OLDEST ref, bare and in the combined form, answers
// the live row. Mutation gate: cap previous_refs at 10 anywhere on the path
// (the store's query, the module's map copy, Build's copy, resolveRefHead's
// scan) → _r00000 and _r00001 are ErrNotFound → red.
func TestLineagePath_TwelveHopsOldestRefResolvesToTheLiveRow(t *testing.T) {
	s := openTestStore(t)
	const n = 12
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("op-%d", i)
		old, next := fmt.Sprintf("sid-%d", i), fmt.Sprintf("sid-%d", i+1)
		oldRef, nextRef := fmt.Sprintf("_r%05d", i), fmt.Sprintf("_r%05d", i+1)
		op := selfOp(id, old, oldRef, int64(1000*(i+1)))
		op.State = team.RelayClaimed
		if err := s.CreateRelayOp(op); err != nil {
			t.Fatal(err)
		}
		mustReport(t, s, id, RelayReport{State: team.RelayCleared, NewSessionID: next, NewRef: nextRef, At: int64(1000*(i+1) + 500)})
	}
	var reader team.LineageReader = s // the registry value the peers module reads
	refs, err := reader.PreviousRefs()
	if err != nil {
		t.Fatal(err)
	}
	head := fmt.Sprintf("sid-%d", n)
	records := ipeers.Build(ipeers.BuildInput{
		HostID: "h:1", Alias: "mlab",
		// The live conversation is the chain's head; only it is alive.
		Entries:      []ipeers.Entry{{PID: 4242, SessionID: head, Name: "purdex-x", Inbox: "/s/4242", ProcStart: "Sun Sep 13 15:22:36 2026"}},
		PreviousRefs: refs,
	})
	var live ipeers.PeerRecord
	for _, r := range records {
		if r.Agent != nil && r.Agent.SessionID == head {
			live = r
		}
	}
	if live.Agent == nil || len(live.PreviousRefs) != n || live.PreviousRefs[0] != fmt.Sprintf("_r%05d", n-1) || live.PreviousRefs[n-1] != "_r00000" {
		t.Fatalf("live row previous_refs = %v (want %d, newest first)", live.PreviousRefs, n)
	}
	for _, in := range []string{"_r00000", "r00000", "_r00001", "purdex-x [r00000]", fmt.Sprintf("_r%05d", n-1)} {
		rec, err := ipeers.Resolve(records, in, ipeers.ResolveSnapshot{})
		if err != nil || rec.Agent == nil || rec.Agent.PID != 4242 || rec.Agent.SessionID != head {
			t.Fatalf("Resolve(%q) = %+v err=%v; want the live head row (pid 4242)", in, rec, err)
		}
	}
	// A ref that was never in the chain stays not found.
	if _, err := ipeers.Resolve(records, "_r99999", ipeers.ResolveSnapshot{}); !errors.Is(err, ipeers.ErrNotFound) {
		t.Fatalf("unknown ref: err=%v, want ErrNotFound", err)
	}
}
