package advisor

import (
	"testing"
	"time"
)

// Through the evaluator: a peer silent past the limit raises its own alert, one that answers again clears it, and the partner's alert is a
// separate key.
func TestEvaluatorRaisesAndClearsSilence(t *testing.T) {
	book := newBook()
	now := silT0
	downSince := silT0.Add(-4 * time.Minute)
	seen := silT0.Add(-time.Minute)
	input := func() (Input, bool) {
		return Input{
			Now:         now,
			Peers:       func() []PeerDown { return []PeerDown{{"mserv", downSince, ""}, {"air26", time.Time{}, ""}} },
			PartnerSeen: func() time.Time { return seen },
		}, true
	}
	e := NewEvaluator(book, input, nil, PeerSilence{Alias: "mserv"}, PeerSilence{Alias: "air26"}, PartnerDown{})
	e.Pass()
	if _, ok := book.active["silence:mserv"]; !ok || len(book.active) != 1 {
		t.Fatalf("only mserv is silent: %v", book.active)
	}
	// the partner stops reading
	now = now.Add(5 * time.Minute)
	e.Pass()
	if _, ok := book.active["partner_down"]; !ok {
		t.Fatalf("the partner has been silent for 6 min: %v", book.active)
	}
	// both come back
	downSince, seen = time.Time{}, now
	e.Pass()
	if len(book.active) != 0 {
		t.Fatalf("answers must clear: %v", book.active)
	}
}
