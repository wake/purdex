// internal/module/peers/hostcaller_records_test.go
package peers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/config"
)

// X5: a lead host reads a remote member's context and model from the member host's GET /api/peers.
func TestHostCaller_PeerRecords(t *testing.T) {
	var gotAuth string
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "host_id": "hostB", "peers": []any{map[string]any{
			"address": "b/_abc123", "row_kind": "session",
			"agent": map[string]any{"type": "cc", "session_id": "sid-1", "version": "",
				"context": map[string]any{"used_percentage": 41.0, "window": 200000, "at": 5, "model_id": "claude-opus-5-5", "effort": "high"}},
		}}})
	})
	h := newHolder(config.PeerHost{Alias: "b", URL: s.URL, HostID: "hostB", Token: "tok1"})
	rows, err := callerFor(h).PeerRecords(context.Background(), "hostB")
	if err != nil || gotAuth != "Bearer tok1" || len(rows) != 1 {
		t.Fatalf("rows = %+v err=%v auth=%q", rows, err, gotAuth)
	}
	if rows[0].Agent == nil || rows[0].Agent.Context == nil {
		t.Fatalf("no context in %+v", rows[0])
	}
	if c := rows[0].Agent.Context; c.ModelID != "claude-opus-5-5" || c.Effort != "high" || *c.UsedPercentage != 41 {
		t.Fatalf("context = %+v", c)
	}
	if _, err := callerFor(h).PeerRecords(context.Background(), "hostZ"); err == nil {
		t.Fatal("an unpaired host answered rows")
	}
	h.set(config.PeerHost{Alias: "b", URL: s.URL, HostID: "hostOther", Token: "tok1"})
	if _, err := callerFor(h).PeerRecords(context.Background(), "hostOther"); err == nil {
		t.Fatal("a host_id mismatch was accepted")
	}
}
