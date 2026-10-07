package peers

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/team"
)

// fakeLineage is the team module's LineageReader as the peers inventory
// sees it through the registry.
type fakeLineage struct {
	refs map[string][]string
	err  error
}

func (f *fakeLineage) PreviousRefs() (map[string][]string, error) { return f.refs, f.err }

var _ team.LineageReader = (*fakeLineage)(nil)

// Lead-team-relay spec §8.4: the row whose CC session id heads a relay
// chain carries previous_refs (newest first); a reader error leaves every
// row without the field, the envelope NOT partial but lineage_unavailable
// (PR #1705 attacker A-1: a ref miss is then not-ready, see address_lineage_test.go).
func TestLocalEnvelope_AttachesPreviousRefsFromLineageReader(t *testing.T) {
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "76973.json", fixture76973)
	sessions := &fakeSessions{sessions: []session.SessionInfo{
		{Code: "mt1code", Name: "mt1", Cwd: "/Users/wake/Workspace/wake/purdex", TmuxInstance: "inst1"},
	}}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{
		"mt1code": {AgentType: "cc", SessionID: "fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c", Cwd: "/Users/wake/Workspace/wake/purdex", TmuxPaneID: "%10", LastSeenAt: 1789314156000, Status: "busy"},
	}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	lineage := &fakeLineage{refs: map[string][]string{"fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c": {"_b1xxxx", "_a0xxxx"}}}
	c.Registry.Register(team.LineageReaderKey, lineage)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	var lineageUnavailable bool
	read := func() (refs []string, partial bool) {
		t.Helper()
		rr := doGetPeers(t, m, "/api/peers")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
		}
		var got struct {
			Partial            bool `json:"partial"`
			LineageUnavailable bool `json:"lineage_unavailable"`
			Peers              []struct {
				SessionCode  string   `json:"session_code"`
				PreviousRefs []string `json:"previous_refs"`
			} `json:"peers"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
		}
		lineageUnavailable = got.LineageUnavailable
		for _, p := range got.Peers {
			if p.SessionCode == "mt1code" {
				return p.PreviousRefs, got.Partial
			}
		}
		t.Fatal("mt1code row missing")
		return nil, false
	}

	refs, partial := read()
	if len(refs) != 2 || refs[0] != "_b1xxxx" || refs[1] != "_a0xxxx" || partial || lineageUnavailable {
		t.Fatalf("previous_refs = %v partial=%v lineage_unavailable=%v", refs, partial, lineageUnavailable)
	}

	lineage.err = errors.New("team.db locked")
	refs, partial = read()
	if refs != nil || partial || !lineageUnavailable {
		t.Fatalf("after a reader error: previous_refs = %v partial=%v lineage_unavailable=%v (want absent, not partial, unavailable)", refs, partial, lineageUnavailable)
	}
}
