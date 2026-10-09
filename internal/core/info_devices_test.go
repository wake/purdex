package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wake/purdex/internal/config"
)

// devicesStub is a module named "devices" that reports a status, like the real one.
type devicesStub struct {
	stubModule
	status map[string]any
}

func (d *devicesStub) Status() map[string]any { return d.status }

func devicesCore(stub *devicesStub) *Core {
	c := New(CoreDeps{Config: &config.Config{}})
	if stub != nil {
		c.AddModule(stub)
	}
	return c
}

// devices.v1 is announced only while the devices module is mounted AND ready: a store that could not be opened leaves it
// off (QR pairing spec §3.4), and the static list is unchanged either way.
func TestHandleInfo_DevicesCapabilityFollowsReadiness(t *testing.T) {
	static := infoOf(t, devicesCore(nil))["capabilities"].([]any)
	assert.NotContains(t, static, "devices.v1", "no devices module, no capability")

	ready := &devicesStub{stubModule{name: "devices"}, map[string]any{"ready": true, "init_error": ""}}
	with := infoOf(t, devicesCore(ready))["capabilities"].([]any)
	assert.Equal(t, append(append([]any{}, static...), "devices.v1"), with)

	broken := &devicesStub{stubModule{name: "devices"}, map[string]any{"ready": false, "init_error": "open devices db: cannot create the file"}}
	assert.NotContains(t, infoOf(t, devicesCore(broken))["capabilities"], "devices.v1")
}
