package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/modevents"
)

// TestRegisterServeModules_MountsModEvents: the mod event channel is
// mounted and its Init publishes the stream registry for other modules.
func TestRegisterServeModules_MountsModEvents(t *testing.T) {
	c := newTestCore(&config.Config{DataDir: t.TempDir(), Token: "t"})
	require.NoError(t, registerServeModules(c, nil, nil))
	assert.True(t, c.Mounted("modevents"))
	require.NoError(t, c.InitModules())
	v, ok := c.Registry.Get("modevents")
	require.True(t, ok, "the registry must be in the ServiceRegistry")
	_, ok = v.(*modevents.Registry)
	assert.True(t, ok, "ServiceRegistry modevents holds %T", v)
	require.NoError(t, c.CloseModules())
}
