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
	assert.Equal(t, append(append([]any{}, none...), "workbook.v1"), with)
	assert.NotContains(t, with, "devices.v1")

	broken := &devicesStub{stubModule{name: "workbook"}, map[string]any{"ready": false, "init_error": "open workbook db: cannot create the file"}}
	assert.NotContains(t, infoOf(t, devicesCore(broken))["capabilities"], "workbook.v1")
}
