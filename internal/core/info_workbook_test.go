package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The session workbook and the team journal are gone: a module named "workbook" that is ready announces neither the
// workbook nor any journal capability (the App hides both interfaces when they are absent).
func TestHandleInfo_NoWorkbookOrJournalCapability(t *testing.T) {
	ready := &devicesStub{stubModule{name: "workbook"}, map[string]any{"ready": true, "init_error": "", "journal": true}}
	caps := infoOf(t, devicesCore(ready))["capabilities"]
	for _, c := range []string{"workbook.v1", "workbook.v2", "journal.v1", "journal.sessions.v1", "journal.confirm.v1", "journal.members.v1"} {
		assert.NotContains(t, caps, c)
	}
}

// dialogue.v1 follows the dialogue module's readiness (the real module is exercised in cmd/pdx, which can import it).
func TestHandleInfo_DialogueCapabilityFollowsReadiness(t *testing.T) {
	assert.NotContains(t, infoOf(t, devicesCore(nil))["capabilities"], "dialogue.v1")
	ready := &devicesStub{stubModule{name: "dialogue"}, map[string]any{"ready": true, "init_error": ""}}
	assert.Contains(t, infoOf(t, devicesCore(ready))["capabilities"], "dialogue.v1")
	broken := &devicesStub{stubModule{name: "dialogue"}, map[string]any{"ready": false, "init_error": "x"}}
	assert.NotContains(t, infoOf(t, devicesCore(broken))["capabilities"], "dialogue.v1")
}
