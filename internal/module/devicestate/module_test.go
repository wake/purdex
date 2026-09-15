package devicestate

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTestModule builds a Module backed by an in-memory store.
func newTestModule(t *testing.T) *Module {
	t.Helper()
	s, err := OpenStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return &Module{store: s}
}

// serve runs one request through a mux wired with m.RegisterRoutes.
func serve(m *Module, method, path string, body io.Reader) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(method, path, body)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func serveBytes(m *Module, method, path string, body []byte) *httptest.ResponseRecorder {
	return serve(m, method, path, bytes.NewReader(body))
}

func TestModule_NameAndDependencies(t *testing.T) {
	m := New()
	require.Equal(t, "devicestate", m.Name())
	require.Nil(t, m.Dependencies())
}
