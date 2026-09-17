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
// daemon_version, unknown_registry_files (never null), and Peer Address
// v4's alias (always present). Any change to this literal is a wire-format
// break for the SPA/CLI.
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

	want := `{"host_id":"h","alias":"","ok":true,"partial":false,"peers":[],"daemon_version":"1.2.3","unknown_registry_files":[],"titles_unavailable":false}`
	if string(got) != want {
		t.Errorf("Envelope JSON = %s, want %s", got, want)
	}
}

// TestHostResult_JSON_Shape pins HostResult's wire format — Envelope's
// fields behind alias/host_id, including titles_unavailable (always
// present), which pdx peers --all renders per host.
//
// self_alias sits beside alias and is likewise always present: "" is the
// meaningful "this peer never reported a name", so eliding it would make a
// silent peer indistinguishable from an absent field.
func TestHostResult_JSON_Shape(t *testing.T) {
	h := HostResult{
		Alias:                "air",
		SelfAlias:            "air26",
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
	want := `{"alias":"air","host_id":"air:1","self_alias":"air26","ok":true,"partial":true,"peers":[],"daemon_version":"1.2.3","unknown_registry_files":[],"titles_unavailable":true}`
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

	want := `{"host_id":"h","alias":"","ok":false,"error":"boom","partial":false,"peers":[],"daemon_version":"","unknown_registry_files":[],"titles_unavailable":false}`
	if string(got) != want {
		t.Errorf("Envelope JSON = %s, want %s", got, want)
	}
}

// TestEnvelope_CarriesAliasKey pins the Phase C field: an envelope reports
// what its host calls ITSELF, under the key `alias`, so a reader can name a
// newly paired peer the way that peer names itself (spec §7). The key is
// unconditional — no omitempty — so an older daemon that publishes nothing
// and a daemon that publishes an empty alias stay distinguishable only by
// the field's absence, never by a silently elided one.
func TestEnvelope_CarriesAliasKey(t *testing.T) {
	b, err := json.Marshal(Envelope{HostID: "mlab:278cbm", Alias: "mlab"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["alias"] != "mlab" {
		t.Errorf("alias key = %v, want %q", m["alias"], "mlab")
	}
}
