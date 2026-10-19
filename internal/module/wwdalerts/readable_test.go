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
