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
