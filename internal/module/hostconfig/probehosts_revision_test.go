package hostconfig

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ProbeHostsRevision is the stored revision alone: 0 for a never-written key, moving with each save, and it never reads the
// SSH config (a gone alias in the stored row does not matter to it).
func TestProbeHostsRevision(t *testing.T) {
	withSSHAliases(t, "eq14")
	m := newTestModule(t)
	rev, err := m.ProbeHostsRevision()
	require.NoError(t, err)
	assert.Equal(t, int64(0), rev)

	code, body := putProbe(m, `[`+probeRow+`]`, 0)
	require.Equal(t, http.StatusOK, code, body)
	rev, err = m.ProbeHostsRevision()
	require.NoError(t, err)
	assert.Equal(t, int64(1), rev)

	withSSHAliases(t) // the alias is gone: the rows read empty, the revision does not care
	rev, err = m.ProbeHostsRevision()
	require.NoError(t, err)
	assert.Equal(t, int64(1), rev)
}
