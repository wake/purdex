package snapshot

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What the origin answers that the client cannot act on is the origin's, not this host's (#3379): a 5xx such as "the commit of this
// job is not pinned", an unexpected 4xx, a 422 it does not know. The caller sees the status and the body, and can tell it from a
// failure that is this host's own.
func TestClient_AnAnswerItCannotActOnIsAnOriginError(t *testing.T) {
	for name, code := range map[string]int{"500": http.StatusInternalServerError, "502": http.StatusBadGateway, "404": http.StatusNotFound, "400": http.StatusBadRequest} {
		t.Run(name, func(t *testing.T) {
			m := newMaterializeCase(t, "j_20")
			rec := &recorder{before: func(n int, w http.ResponseWriter) bool {
				http.Error(w, "the commit of this job is not pinned", code)
				return true
			}}
			c := serveRecorded(t, m.src, rec)
			_, err := c.Tar(context.Background(), "j_20", m.claim)
			var oe *OriginError
			require.True(t, errors.As(err, &oe), "%v", err)
			assert.Equal(t, code, oe.Status)
			assert.Equal(t, "the commit of this job is not pinned", oe.Body)
			assert.True(t, IsOriginSide(err))
			assert.Contains(t, err.Error(), "the origin answered", "the text callers and tests already read")
			assert.Equal(t, 1, len(rec.seen()), "not retried: only a 503 is")
		})
	}
}

func TestClient_An422ItDoesNotKnowIsAnOriginError(t *testing.T) {
	m := newMaterializeCase(t, "j_21")
	rec := &recorder{before: func(n int, w http.ResponseWriter) bool {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"something_new"}`))
		return true
	}}
	c := serveRecorded(t, m.src, rec)
	_, err := c.Tar(context.Background(), "j_21", m.claim)
	var oe *OriginError
	require.True(t, errors.As(err, &oe), "%v", err)
	assert.Equal(t, http.StatusUnprocessableEntity, oe.Status)
}

// Busy past the retries is the origin's too; the sentinel is the same as before.
func TestClient_BusyIsOriginSideAndStillErrBusy(t *testing.T) {
	m := newMaterializeCase(t, "j_22")
	rec := &recorder{before: func(n int, w http.ResponseWriter) bool {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return true
	}}
	c := serveRecorded(t, m.src, rec)
	_, err := c.Tar(context.Background(), "j_22", m.claim)
	assert.ErrorIs(t, err, ErrBusy)
	assert.True(t, IsOriginSide(err))
}

// What is this host's own, and what the client handles itself, is not the origin's.
func TestIsOriginSide_OnlyWhatTheOriginAnswered(t *testing.T) {
	for _, err := range []error{
		ErrStaleClaim, ErrBundleTooLarge, ErrHaveUnknown, ErrNothingToSend,
		errors.New("snapshot: reading the bundle: unexpected EOF"),
		context.DeadlineExceeded, context.Canceled,
		&netErr{}, // a connection that could not be made or broke: whose network it is cannot be told
		nil,
	} {
		assert.False(t, IsOriginSide(err), "%v", err)
	}
	assert.True(t, IsOriginSide(&OriginError{Status: 500}))
	assert.True(t, IsOriginSide(errors.Join(errors.New("wrapped"), &OriginError{Status: 500})))
}

type netErr struct{}

func (*netErr) Error() string   { return "dial tcp: connection refused" }
func (*netErr) Timeout() bool   { return false }
func (*netErr) Temporary() bool { return false }

func TestOriginError_MessageCarriesStatusAndClippedBody(t *testing.T) {
	e := &OriginError{Status: 500, Body: strings.Repeat("x", 300)}
	assert.Contains(t, e.Error(), "500")
	assert.LessOrEqual(t, len(e.Error()), 400, "a long body does not make a long message")
}
