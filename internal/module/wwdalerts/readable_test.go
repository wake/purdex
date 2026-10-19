package wwdalerts

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// AlertsReadable follows the last pass's read of this host's own wwd: true before any pass (a restart does not hand delivery over for a
// moment), false when the wwd cannot be asked or its list cannot be read, true again as soon as it can; a peer's list failing is not this
// host's wwd failing.
func TestAlertsReadableFollowsTheLocalWWD(t *testing.T) {
	e := newEnv(t)
	e.src.peers = []string{"air26"}
	assert.True(t, e.m.AlertsReadable(), "no pass yet")

	e.pass(t)
	assert.True(t, e.m.AlertsReadable())

	e.src.errs = map[string]error{"air26": errors.New("unreachable")}
	e.pass(t)
	assert.True(t, e.m.AlertsReadable(), "a peer's list not read is not this host's wwd failing")

	e.src.errs = map[string]error{"mlab": errors.New("socket refused")}
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	assert.False(t, e.m.AlertsReadable(), "this host's own list cannot be read")

	e.src.errs = nil
	e.pass(t)
	assert.True(t, e.m.AlertsReadable(), "read again: the next answer hands back")
}

// A local wwd that does not serve the alerts route (older, or no store) answers 404: its list cannot be read, and neither can the peers'
// through it, so this daemon delivers nothing and must not say its push is ready (R1).
func TestAlertsReadableIsFalseWhenTheLocalWWDHasNoAlertsRoute(t *testing.T) {
	e := newEnv(t)
	e.src.known = map[string]bool{"mlab": false}
	e.pass(t)
	assert.False(t, e.m.AlertsReadable())
	e.src.known = nil
	e.pass(t)
	assert.True(t, e.m.AlertsReadable())
}

// The local wwd not answering at all (the first question, who am I, fails) is the same.
func TestAlertsReadableIsFalseWhenTheWWDDoesNotAnswer(t *testing.T) {
	e := newEnv(t)
	e.src.infoErr = errors.New("connection refused")
	e.pass(t)
	assert.False(t, e.m.AlertsReadable())
	e.src.infoErr = nil
	e.pass(t)
	assert.True(t, e.m.AlertsReadable())
}
