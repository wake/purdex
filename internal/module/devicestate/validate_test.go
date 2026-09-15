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

// payloadTail is the remaining required top-level fields of a well-formed v1
// snapshot (mirrors isWellFormedSnapshotV1 in spa/src/lib/snapshot/storage.ts).
const payloadTail = `"activeTabId":null,"activeWorkspaceId":null,"sessionMeta":{}`

func TestParsePayloadCounts(t *testing.T) {
	ws, tabs, err := parsePayload(json.RawMessage(
		`{"version":1,"workspaces":[{"id":"a"},{"id":"b"}],"tabs":{"t1":{},"t2":{},"t3":{}},"tabOrder":["t1"],` + payloadTail + `}`))
	require.NoError(t, err)
	assert.Equal(t, 2, ws)
	assert.Equal(t, 3, tabs)

	ws, tabs, err = parsePayload(json.RawMessage(`{"version":1,"workspaces":[],"tabs":{},"tabOrder":[],` + payloadTail + `}`))
	require.NoError(t, err)
	assert.Equal(t, 0, ws)
	assert.Equal(t, 0, tabs)
}

func TestParsePayloadActiveIDsAndSessionMetaValid(t *testing.T) {
	base := `{"version":1,"workspaces":[],"tabs":{},"tabOrder":[],`
	for name, tail := range map[string]string{
		"null ids":        `"activeTabId":null,"activeWorkspaceId":null,"sessionMeta":{}`,
		"string ids":      `"activeTabId":"t1","activeWorkspaceId":"w1","sessionMeta":{"h":{}}`,
		"empty string id": `"activeTabId":"","activeWorkspaceId":null,"sessionMeta":{}`,
	} {
		_, _, err := parsePayload(json.RawMessage(base + tail + `}`))
		assert.NoError(t, err, name)
	}
}

func TestParsePayloadInvalid(t *testing.T) {
	const core = `"workspaces":[],"tabs":{},"tabOrder":[]`
	cases := map[string]string{
		"empty":              ``,
		"null":               `null`,
		"string":             `"x"`,
		"number":             `1`,
		"array":              `[]`,
		"malformed":          `{"version":1,`,
		"version 2":          `{"version":2,` + core + `,` + payloadTail + `}`,
		"version string":     `{"version":"1",` + core + `,` + payloadTail + `}`,
		"version missing":    `{` + core + `,` + payloadTail + `}`,
		"version null":       `{"version":null,` + core + `,` + payloadTail + `}`,
		"workspaces missing": `{"version":1,"tabs":{},"tabOrder":[],` + payloadTail + `}`,
		"workspaces null":    `{"version":1,"workspaces":null,"tabs":{},"tabOrder":[],` + payloadTail + `}`,
		"workspaces object":  `{"version":1,"workspaces":{},"tabs":{},"tabOrder":[],` + payloadTail + `}`,
		"tabs missing":       `{"version":1,"workspaces":[],"tabOrder":[],` + payloadTail + `}`,
		"tabs null":          `{"version":1,"workspaces":[],"tabs":null,"tabOrder":[],` + payloadTail + `}`,
		"tabs array":         `{"version":1,"workspaces":[],"tabs":[],"tabOrder":[],` + payloadTail + `}`,
		"tabOrder missing":   `{"version":1,"workspaces":[],"tabs":{},` + payloadTail + `}`,
		"tabOrder null":      `{"version":1,"workspaces":[],"tabs":{},"tabOrder":null,` + payloadTail + `}`,
		"tabOrder string":    `{"version":1,"workspaces":[],"tabs":{},"tabOrder":"t1",` + payloadTail + `}`,

		"activeTabId missing": `{"version":1,` + core + `,"activeWorkspaceId":null,"sessionMeta":{}}`,
		"activeTabId number":  `{"version":1,` + core + `,"activeTabId":1,"activeWorkspaceId":null,"sessionMeta":{}}`,
		"activeTabId object":  `{"version":1,` + core + `,"activeTabId":{},"activeWorkspaceId":null,"sessionMeta":{}}`,
		"activeTabId array":   `{"version":1,` + core + `,"activeTabId":[],"activeWorkspaceId":null,"sessionMeta":{}}`,
		"activeTabId bool":    `{"version":1,` + core + `,"activeTabId":true,"activeWorkspaceId":null,"sessionMeta":{}}`,

		"activeWorkspaceId missing": `{"version":1,` + core + `,"activeTabId":null,"sessionMeta":{}}`,
		"activeWorkspaceId number":  `{"version":1,` + core + `,"activeTabId":null,"activeWorkspaceId":1,"sessionMeta":{}}`,
		"activeWorkspaceId object":  `{"version":1,` + core + `,"activeTabId":null,"activeWorkspaceId":{},"sessionMeta":{}}`,
		"activeWorkspaceId array":   `{"version":1,` + core + `,"activeTabId":null,"activeWorkspaceId":[],"sessionMeta":{}}`,

		"sessionMeta missing": `{"version":1,` + core + `,"activeTabId":null,"activeWorkspaceId":null}`,
		"sessionMeta null":    `{"version":1,` + core + `,"activeTabId":null,"activeWorkspaceId":null,"sessionMeta":null}`,
		"sessionMeta array":   `{"version":1,` + core + `,"activeTabId":null,"activeWorkspaceId":null,"sessionMeta":[]}`,
		"sessionMeta number":  `{"version":1,` + core + `,"activeTabId":null,"activeWorkspaceId":null,"sessionMeta":1}`,
		"sessionMeta string":  `{"version":1,` + core + `,"activeTabId":null,"activeWorkspaceId":null,"sessionMeta":"x"}`,
	}
	for name, raw := range cases {
		_, _, err := parsePayload(json.RawMessage(raw))
		assert.Error(t, err, name)
	}
}
