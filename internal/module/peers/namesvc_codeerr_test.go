package peers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CodeOfErr tells "this session has no persona" from "the read failed": a caller that keeps what it knew across a failure
// (the notification labeler) needs the difference.
func TestNameService_CodeOfErrSeparatesAFailedReadFromNoPersona(t *testing.T) {
	e := svcEnv(t)
	code, ok, err := e.m.CodeOfErr(context.Background(), poolSidA)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "anna", code)
	_, ok, err = e.m.CodeOfErr(context.Background(), "00000000-0000-4000-8000-0000000000ff")
	assert.NoError(t, err, "an unknown session is not a failure")
	assert.False(t, ok)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, ok, err = e.m.CodeOfErr(cancelled, poolSidA)
	assert.Error(t, err, "a read that could not run is a failure")
	assert.False(t, ok)
	_, ok = e.m.CodeOf(cancelled, poolSidA)
	assert.False(t, ok, "CodeOf keeps its two-value shape")
}
