package peers

import (
	"encoding/json"
	"testing"
)

// TestEnvelope_JSON_MatchesP1Shape pins Envelope's wire format to the exact
// bytes P1's private response/peersResponse types produced (see P1's
// internal/module/peers/module.go and cmd/pdx/peers.go), extended by Peer
// Address v2 (Task 5) with daemon_version and unknown_registry_files:
// host_id, ok, error (omitted when empty), partial, peers (never null),
// daemon_version, unknown_registry_files (never null). Any change to this
// literal is a wire-format break for the SPA/CLI.
func TestEnvelope_JSON_MatchesP1Shape(t *testing.T) {
	env := Envelope{
		HostID:               "h",
		OK:                   true,
		Partial:              false,
		Peers:                []PeerRecord{},
		DaemonVersion:        "1.2.3",
		UnknownRegistryFiles: []string{},
	}

	got, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	want := `{"host_id":"h","ok":true,"partial":false,"peers":[],"daemon_version":"1.2.3","unknown_registry_files":[],"titles_unavailable":false}`
	if string(got) != want {
		t.Errorf("Envelope JSON = %s, want %s", got, want)
	}
}

// TestHostResult_JSON_Shape pins HostResult's wire format — Envelope's
// fields behind alias/host_id, including titles_unavailable (always
// present), which pdx peers --all renders per host.
func TestHostResult_JSON_Shape(t *testing.T) {
	h := HostResult{
		Alias:                "air",
		HostID:               "air:1",
		OK:                   true,
		Partial:              true,
		Peers:                []PeerRecord{},
		DaemonVersion:        "1.2.3",
		UnknownRegistryFiles: []string{},
		TitlesUnavailable:    true,
	}
	got, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"alias":"air","host_id":"air:1","ok":true,"partial":true,"peers":[],"daemon_version":"1.2.3","unknown_registry_files":[],"titles_unavailable":true}`
	if string(got) != want {
		t.Errorf("HostResult JSON = %s, want %s", got, want)
	}
}

// TestEnvelope_JSON_ErrorIncludedWhenSet is the companion case: when Error
// is non-empty, it is present in the JSON (omitempty only elides the empty
// case).
func TestEnvelope_JSON_ErrorIncludedWhenSet(t *testing.T) {
	env := Envelope{
		HostID:               "h",
		OK:                   false,
		Error:                "boom",
		Peers:                []PeerRecord{},
		UnknownRegistryFiles: []string{},
	}

	got, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	want := `{"host_id":"h","ok":false,"error":"boom","partial":false,"peers":[],"daemon_version":"","unknown_registry_files":[],"titles_unavailable":false}`
	if string(got) != want {
		t.Errorf("Envelope JSON = %s, want %s", got, want)
	}
}
