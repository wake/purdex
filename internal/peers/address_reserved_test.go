package peers

import (
	"errors"
	"testing"
)

// TestResolve_ReservedBareNamesAreOrdinaryNames pins that "cc" and "tmux"
// without a colon are names, not the retired prefixed forms.
//
// RoutableName accepts both — it asks only for two or more characters of
// [a-z0-9-] — so applyIdentity mints "<host>/cc" for a conversation whose
// registry name is exactly that, and the peers table prints it. A resolver
// that switched on the head alone answered that address with "cc: addresses
// were removed", so this daemon printed an address it then refused to deliver
// to. The colon is what makes the explicit forms explicit.
func TestResolve_ReservedBareNamesAreOrdinaryNames(t *testing.T) {
	recs := []PeerRecord{
		liveRow("_aaaaaa", "cc", "", "s-cc", 1),
		liveRow("_bbbbbb", "tmux", "", "s-tmux", 2),
	}
	for _, c := range []struct{ session, wantRef string }{
		{"cc", "_aaaaaa"},
		{"tmux", "_bbbbbb"},
	} {
		got, err := Resolve(recs, c.session, ResolveSnapshot{})
		if err != nil {
			t.Errorf("Resolve(%q): %v, want the name tier to decide it", c.session, err)
			continue
		}
		if got.Ref != c.wantRef {
			t.Errorf("Resolve(%q) = %q, want %q", c.session, got.Ref, c.wantRef)
		}
	}

	// The prefixed forms keep their meaning: a colon says "I mean the place,
	// not the conversation".
	if _, err := Resolve(recs, "cc:anything", ResolveSnapshot{}); !errors.Is(err, ErrLegacyCC) {
		t.Errorf(`Resolve("cc:anything") = %v, want ErrLegacyCC`, err)
	}
	if got, err := Resolve(recs, "tmux:s-cc", ResolveSnapshot{}); err != nil || got.Ref != "_aaaaaa" {
		t.Errorf(`Resolve("tmux:s-cc") = (%q, %v), want the row whose SessionName is s-cc`, got.Ref, err)
	}
	if _, err := Resolve(recs, "tmux:", ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Errorf(`Resolve("tmux:") = %v, want ErrNotFound`, err)
	}
}

// TestRoutableName_AcceptsReservedWords is the other half of the same
// contract, stated where someone tempted to "fix" this by adding reserved
// words to the grammar will meet it.
//
// Rejecting "cc" and "tmux" there would work too, but it would cost a real
// conversation its readable address to avoid a collision the resolver can
// simply not have. A name is not a wire prefix; only a colon makes one.
func TestRoutableName_AcceptsReservedWords(t *testing.T) {
	for _, s := range []string{"cc", "tmux"} {
		if !RoutableName(s) {
			t.Errorf("RoutableName(%q) = false; see TestResolve_ReservedBareNamesAreOrdinaryNames", s)
		}
	}
}
