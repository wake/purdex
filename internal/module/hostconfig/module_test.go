package hostconfig

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestModule(t *testing.T) *Module {
	t.Helper()
	s, err := OpenStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	home := t.TempDir() // stable for the whole test so ~/x resolves consistently
	return &Module{store: s, home: func() (string, error) { return home, nil }}
}

func serve(m *Module, method, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestModuleNameAndDependencies(t *testing.T) {
	m := New()
	require.Equal(t, "hostconfig", m.Name())
	require.Nil(t, m.Dependencies())
}
