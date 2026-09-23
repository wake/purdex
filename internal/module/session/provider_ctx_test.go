package session

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ctxKey struct{}

// plainProvider has only the SessionProvider methods (GetSession through the
// embedded module), so GetSessionWithin must fall back to GetSession.
type plainProvider struct {
	SessionProvider
	calls int
}

func (p *plainProvider) GetSession(code string) (*SessionInfo, error) {
	p.calls++
	return &SessionInfo{Code: code}, nil
}

func TestGetSessionWithin_FallsBackToGetSession(t *testing.T) {
	p := &plainProvider{}
	info, err := GetSessionWithin(context.Background(), p, "abc")
	require.NoError(t, err)
	assert.Equal(t, "abc", info.Code)
	assert.Equal(t, 1, p.calls)
}

// ctxProvider records the context GetSessionContext was called with.
type ctxProvider struct {
	plainProvider
	got context.Context
}

func (p *ctxProvider) GetSessionContext(ctx context.Context, code string) (*SessionInfo, error) {
	p.got = ctx
	return &SessionInfo{Code: code}, nil
}

func TestGetSessionWithin_PassesContext(t *testing.T) {
	p := &ctxProvider{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "request")
	_, err := GetSessionWithin(ctx, p, "abc")
	require.NoError(t, err)
	require.NotNil(t, p.got)
	assert.Equal(t, "request", p.got.Value(ctxKey{}))
	assert.Zero(t, p.calls, "GetSession used although GetSessionContext exists")
}
