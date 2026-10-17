package gatemod

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A rotation between the two opens leaves the older name on the file already open as the current one: it is read once, not twice.
func TestDecisions_TheSameFileUnderBothNamesIsReadOnce(t *testing.T) {
	m, dir := rig(t)
	writeFile(t, dir, "decisions.jsonl", line(0, "heavy"), line(1, "heavy"))
	require.NoError(t, os.Link(filepath.Join(dir, "decisions.jsonl"), filepath.Join(dir, "decisions.jsonl.1")))
	_, r := get(t, m, "", true)
	assert.Len(t, r.Records, 2)
}

// A file that exists but cannot be read is an error, not an empty history.
func TestDecisions_AnUnreadableFileIsAnError(t *testing.T) {
	m, dir := rig(t)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "decisions.jsonl"), 0o755)) // opens, but reading it fails
	code, _ := get(t, m, "", true)
	assert.Equal(t, 500, code)
}
