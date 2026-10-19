package queue

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A host is compared by its latest outcome of the work, not by an older one: B passed and then failed, so when A fails, A and B agree,
// and B's earlier pass is no reason to quarantine anything.
func TestAHostIsComparedByItsLatestOutcomeNotAnOlderOne(t *testing.T) {
	f := newFixture(t)
	f.record(t, outcome("j_b1", "mserv", StandingEarned, true))
	assert.Empty(t, f.record(t, outcome("j_b2", "mserv", StandingEarned, false)), "the same host differing from itself is not a disagreement")
	assert.Empty(t, f.record(t, outcome("j_a", "mini", StandingEarned, false)), "A fails as B's latest does")

	// and the other way: B failed and then passed; A passes, which B's latest does too
	g := newFixture(t)
	g.record(t, outcome("j_b1", "mserv", StandingEarned, false))
	g.record(t, outcome("j_b2", "mserv", StandingEarned, true))
	assert.Empty(t, g.record(t, outcome("j_a", "mini", StandingEarned, true)))

	// a real difference with the latest is still found
	h := newFixture(t)
	h.record(t, outcome("j_b1", "mserv", StandingEarned, true))
	h.record(t, outcome("j_b2", "mserv", StandingEarned, false))
	assert.Len(t, h.record(t, outcome("j_a", "mini", StandingEarned, true)), 2, "A passes, B's latest failed: both are quarantined")
}
