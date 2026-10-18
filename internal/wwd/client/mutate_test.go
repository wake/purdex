package client

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mutate hands back wwd's answer as it is, whatever the status, with the host in the query; only no answer is an error.
func TestMutateRelaysStatusAndBodyAndNamesTheHost(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "wwdc")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "w.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	var gotHost, gotBody, gotType string
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotHost, gotBody, gotType = r.URL.Query().Get("host"), string(b), r.Header.Get("Content-Type")
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error":"x"}`))
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	c := New(sock)
	status, body, err := c.Mutate(context.Background(), http.MethodPut, "/v1/worker", "air 26&x=1", []byte(`{"a":1}`))
	require.NoError(t, err)
	assert.Equal(t, 409, status)
	assert.JSONEq(t, `{"error":"x"}`, string(body))
	assert.Equal(t, "air 26&x=1", gotHost, "the host is query-escaped, so it stays one value")
	assert.Equal(t, `{"a":1}`, gotBody)
	assert.Equal(t, "application/json", gotType)

	// no body: no content type
	_, _, err = c.Mutate(context.Background(), http.MethodDelete, "/v1/exceptions/pkg", "mserv", nil)
	require.NoError(t, err)
	assert.Empty(t, gotType)
}

func TestMutateWithNoWWDIsUnavailable(t *testing.T) {
	_, _, err := New(filepath.Join(t.TempDir(), "none.sock")).Mutate(context.Background(), http.MethodPut, "/v1/worker", "mlab", []byte(`{}`))
	assert.True(t, errors.Is(err, ErrUnavailable), "%v", err)
}
