package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// workbook.v1 is announced only while the workbook module is mounted AND ready (a store that could not be opened leaves it
// off), independently of the devices capability.
func TestHandleInfo_WorkbookCapabilityFollowsReadiness(t *testing.T) {
	none := infoOf(t, devicesCore(nil))["capabilities"].([]any)
	assert.NotContains(t, none, "workbook.v1", "no workbook module, no capability")

	ready := &devicesStub{stubModule{name: "workbook"}, map[string]any{"ready": true, "init_error": ""}}
	with := infoOf(t, devicesCore(ready))["capabilities"].([]any)
	assert.Equal(t, append(append([]any{}, none...), "workbook.v1", "workbook.v2"), with)
	assert.NotContains(t, with, "devices.v1")

	broken := &devicesStub{stubModule{name: "workbook"}, map[string]any{"ready": false, "init_error": "open workbook db: cannot create the file"}}
	assert.NotContains(t, infoOf(t, devicesCore(broken))["capabilities"], "workbook.v1")
	assert.NotContains(t, infoOf(t, devicesCore(broken))["capabilities"], "workbook.v2")
}

// journal.v1 is announced only while the workbook module is ready AND its status says the journal runs: the journal switched
// off ([journal] enabled = false), or not wired, leaves it out, and the App stays on the old workbook interface.
func TestHandleInfo_JournalCapabilityFollowsTheJournalFlag(t *testing.T) {
	off := &devicesStub{stubModule{name: "workbook"}, map[string]any{"ready": true, "init_error": "", "journal": false}}
	assert.NotContains(t, infoOf(t, devicesCore(off))["capabilities"], "journal.v1", "journal off")
	assert.NotContains(t, infoOf(t, devicesCore(off))["capabilities"], "journal.confirm.v1", "journal off")
	assert.Contains(t, infoOf(t, devicesCore(off))["capabilities"], "workbook.v1", "the workbook itself is still there")

	on := &devicesStub{stubModule{name: "workbook"}, map[string]any{"ready": true, "init_error": "", "journal": true}}
	assert.Contains(t, infoOf(t, devicesCore(on))["capabilities"], "journal.v1")
	assert.Contains(t, infoOf(t, devicesCore(on))["capabilities"], "journal.confirm.v1")

	notReady := &devicesStub{stubModule{name: "workbook"}, map[string]any{"ready": false, "init_error": "x", "journal": true}}
	assert.NotContains(t, infoOf(t, devicesCore(notReady))["capabilities"], "journal.v1", "a module that is not ready announces nothing")

	assert.NotContains(t, infoOf(t, devicesCore(nil))["capabilities"], "journal.v1", "no workbook module")
}
