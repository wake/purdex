// internal/module/team/adopt_remote_attack_test.go
package teammod

import (
	"context"
	"net/http"
	"strings"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// A session id is only meaningful together with its host: another paired host announcing the same id neither blocks nor is
// blocked by an open request for the first host's session.
func TestRemoteAdopt_SameSessionIdOnAnotherHostDoesNotCollide(t *testing.T) {
	f, fc := adoptFixture(t)
	fc.aliases["other"] = "hostN"
	f.m.peerRecords = func(_ context.Context, host string) ([]ipeers.PeerRecord, error) {
		return []ipeers.PeerRecord{remoteSession(rtSession, "_rt1234")}, nil
	}
	f.adoptOK(uid(40), "air26/_rt1234")
	f.adoptOK(uid(41), "other/_rt1234") // same id, another host: its own request
	// and the same host still gets request_open
	f.wantAdoptRefusal(uid(42), "air26/_rt1234", http.StatusConflict, team.ErrRequestOpen)
}

// A remote session is never "the lead itself": it is a session of another host, whatever id it announces.
func TestRemoteAdopt_RemoteSessionIsNeverTheLead(t *testing.T) {
	f, _ := adoptFixture(t)
	f.adoptOK(uid(43), remoteTarget)
	// the stored payload's target id is rewritten to the lead's own session id: the approve must not read it as adopt_self
	if _, err := f.m.store.db.Exec(`UPDATE approval_requests SET payload_json = json_set(payload_json, '$.target_session_id', 'sid-1') WHERE id = ?`, uid(43)); err != nil {
		t.Fatal(err)
	}
	var one int
	tx, err := f.m.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	a, _, _ := f.m.store.Get(uid(43))
	p, _ := team.AdoptPayloadOf(a)
	refused, err := adoptRefusal(tx, uid(43), a.HostID, p, adoptCheck{HostID: f.m.hostID(), TargetLive: true})
	_ = one
	if err != nil || refused == team.ErrAdoptSelf {
		t.Fatalf("refusal = %q err=%v: a remote session was taken for the lead", refused, err)
	}
}

// What a paired host reports as identity is checked, not trimmed: a session id that is not a UUID or a ref that is not
// "_xxxxxx" is no session this host can name.
func TestRemoteAdopt_MalformedIdentityFromTheHostIsNotATarget(t *testing.T) {
	f, _ := adoptFixture(t)
	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) {
		return []ipeers.PeerRecord{
			remoteSession("not-a-uuid", "_rt1234"),
			remoteSession(rtSession, "_rt\n1234"),
			remoteSession(strings.Repeat("a", 5000), "_rt1234"),
		}, nil
	}
	f.wantAdoptRefusal(uid(44), remoteTarget, http.StatusConflict, team.ErrAdoptTargetNotFound)
}
