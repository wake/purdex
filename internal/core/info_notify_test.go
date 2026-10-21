package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// notifyStub is a module named "notify" that reports a status, like the real one.
type notifyStub struct {
	stubModule
	status map[string]any
}

func (n *notifyStub) Status() map[string]any { return n.status }

func notifyCore(stub *notifyStub) *Core {
	c := devicesCore(nil)
	if stub != nil {
		c.AddModule(stub)
	}
	return c
}

// notify.group_labels.v1 is announced only while the notify module is mounted AND ready: a store that could not be opened leaves it
// off (notify-title spec §3.2), and the static list is unchanged either way.
func TestHandleInfo_NotifyCapabilityFollowsReadiness(t *testing.T) {
	static := infoOf(t, notifyCore(nil))["capabilities"].([]any)
	assert.NotContains(t, static, "notify.group_labels.v1", "no notify module, no capability")

	ready := &notifyStub{stubModule{name: "notify"}, map[string]any{"ready": true, "init_error": ""}}
	with := infoOf(t, notifyCore(ready))["capabilities"].([]any)
	assert.Equal(t, append(append([]any{}, static...), "notify.group_labels.v1"), with)

	broken := &notifyStub{stubModule{name: "notify"}, map[string]any{"ready": false, "init_error": "open notify db: cannot create the file"}}
	assert.NotContains(t, infoOf(t, notifyCore(broken))["capabilities"], "notify.group_labels.v1")
}
