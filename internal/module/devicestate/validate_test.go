package devicestate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateClientID(t *testing.T) {
	assert.NoError(t, validateClientID("c_0123456789ab"))
	for _, bad := range []string{
		"",
		"c_0123456789AB",  // uppercase hex
		"c_0123456789a",   // too short
		"c_0123456789abc", // too long
		"0123456789abcd",  // missing prefix
		"d_0123456789ab",  // wrong prefix
		"c_0123456789ag",  // non-hex
	} {
		assert.Error(t, validateClientID(bad), "clientId %q", bad)
	}
}

func TestValidateDeviceName(t *testing.T) {
	got, err := validateDeviceName("  MacBook  ")
	require.NoError(t, err)
	assert.Equal(t, "MacBook", got)

	ok64 := strings.Repeat("機", 64)
	got, err = validateDeviceName(ok64)
	require.NoError(t, err)
	assert.Equal(t, ok64, got)

	for _, bad := range []string{"", "   ", "\t\n", strings.Repeat("機", 65)} {
		_, err := validateDeviceName(bad)
		assert.Error(t, err, "deviceName %q", bad)
	}
}

func TestValidateAppVersion(t *testing.T) {
	assert.NoError(t, validateAppVersion(""))
	assert.NoError(t, validateAppVersion(strings.Repeat("a", 64)))
	assert.Error(t, validateAppVersion(strings.Repeat("a", 65)))
}

func TestValidateCapturedAt(t *testing.T) {
	assert.NoError(t, validateCapturedAt(1))
	assert.Error(t, validateCapturedAt(0))
	assert.Error(t, validateCapturedAt(-1))
}

func TestParsePayloadCounts(t *testing.T) {
	ws, tabs, err := parsePayload(json.RawMessage(
		`{"version":1,"workspaces":[{"id":"a"},{"id":"b"}],"tabs":{"t1":{},"t2":{},"t3":{}},"tabOrder":["t1"]}`))
	require.NoError(t, err)
	assert.Equal(t, 2, ws)
	assert.Equal(t, 3, tabs)

	ws, tabs, err = parsePayload(json.RawMessage(`{"version":1,"workspaces":[],"tabs":{},"tabOrder":[]}`))
	require.NoError(t, err)
	assert.Equal(t, 0, ws)
	assert.Equal(t, 0, tabs)
}

func TestParsePayloadInvalid(t *testing.T) {
	cases := map[string]string{
		"empty":              ``,
		"null":               `null`,
		"string":             `"x"`,
		"number":             `1`,
		"array":              `[]`,
		"malformed":          `{"version":1,`,
		"version 2":          `{"version":2,"workspaces":[],"tabs":{},"tabOrder":[]}`,
		"version string":     `{"version":"1","workspaces":[],"tabs":{},"tabOrder":[]}`,
		"version missing":    `{"workspaces":[],"tabs":{},"tabOrder":[]}`,
		"version null":       `{"version":null,"workspaces":[],"tabs":{},"tabOrder":[]}`,
		"workspaces missing": `{"version":1,"tabs":{},"tabOrder":[]}`,
		"workspaces null":    `{"version":1,"workspaces":null,"tabs":{},"tabOrder":[]}`,
		"workspaces object":  `{"version":1,"workspaces":{},"tabs":{},"tabOrder":[]}`,
		"tabs missing":       `{"version":1,"workspaces":[],"tabOrder":[]}`,
		"tabs null":          `{"version":1,"workspaces":[],"tabs":null,"tabOrder":[]}`,
		"tabs array":         `{"version":1,"workspaces":[],"tabs":[],"tabOrder":[]}`,
		"tabOrder missing":   `{"version":1,"workspaces":[],"tabs":{}}`,
		"tabOrder null":      `{"version":1,"workspaces":[],"tabs":{},"tabOrder":null}`,
		"tabOrder string":    `{"version":1,"workspaces":[],"tabs":{},"tabOrder":"t1"}`,
	}
	for name, raw := range cases {
		_, _, err := parsePayload(json.RawMessage(raw))
		assert.Error(t, err, name)
	}
}
