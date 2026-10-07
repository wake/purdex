package main

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/peers"
)

// Lead-team-relay spec §8.4 display: a row that relayed shows its newest
// previous ref after the address; a row without lineage is unchanged.
func TestDisplayAddress_WasPreviousRef(t *testing.T) {
	rec := peers.PeerRecord{Address: "mlab/purdex-b0", Ref: "_b3xxxx", PreviousRefs: []string{"_b1xxxx", "_a0xxxx"}}
	if got, want := displayAddress(rec), "mlab/purdex-b0 [b3xxxx] (was _b1xxxx)"; got != want {
		t.Fatalf("displayAddress = %q, want %q", got, want)
	}
	if got := displayAddress(peers.PeerRecord{Address: "mlab/purdex-b0", Ref: "_b3xxxx"}); strings.Contains(got, "was") {
		t.Fatalf("no lineage must print no (was …): %q", got)
	}
	if got := addressField(peers.PeerRecord{RowKind: "entry", Address: "mlab/_b3xxxx", Ref: "_b3xxxx", PreviousRefs: []string{"_b1xxxx"}}); got != "  mlab/_b3xxxx (was _b1xxxx)" {
		t.Fatalf("addressField = %q", got)
	}
}
