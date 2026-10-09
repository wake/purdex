// cmd/pdx/pairing_chain_test.go
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// QP-2: the pairing claim is the one route without a bearer. Through the daemon's real outer chain it needs no token, only a
// tailnet / loopback source, whatever `allow` says; everything else on the module still needs the admin token.

func pairingEnv(t *testing.T, allow []string) (http.Handler, *core.Core) {
	t.Helper()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Token: "t"}})
	require.NoError(t, registerServeModules(c, nil, nil))
	require.NoError(t, c.InitModules())
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	c.RegisterRoutes(mux)
	return newOuterHandler(c, mux, mux, allow), c
}

func send(h http.Handler, method, path, bearer, remote string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.RemoteAddr = remote
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func pairBody() map[string]any {
	return map[string]any{"rows": []any{map[string]any{
		"v": 1, "kind": "pair", "name": "mlab", "ip": "100.64.0.2", "port": 7860, "daemonId": "hostid", "look": map[string]any{},
		"token": "pdxd_0123456789abcdef0123456789abcdef", "deviceId": "d_0123456789ab", "pairingId": "00000000-0000-4000-8000-00000000000a",
		"profile": map[string]any{"hostDaemonId": "sot", "profileId": "p_0123456789ab", "name": "Main"},
	}}}
}

func TestPairingClaim_NeedsNoTokenButTheSourceMustBeTheTailnetEvenWithAnEmptyAllowList(t *testing.T) {
	h, _ := pairingEnv(t, nil) // allow = []
	create := send(h, "POST", "/api/host-transfer/pairings", "t", "127.0.0.1:1", pairBody())
	require.Equal(t, http.StatusOK, create.Code, create.Body.String())
	var b struct{ Code string }
	require.NoError(t, json.Unmarshal(create.Body.Bytes(), &b))

	rec := send(h, "POST", "/api/host-transfer/pairings/claim", "", "8.8.8.8:4000", map[string]string{"code": b.Code})
	assert.Equal(t, http.StatusForbidden, rec.Code, "a non-tailnet source is refused by the route itself")
	assert.Contains(t, rec.Body.String(), "forbidden_source")

	rec = send(h, "POST", "/api/host-transfer/pairings/claim", "", "100.64.0.9:4000", map[string]string{"code": b.Code})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, strings.Contains(rec.Body.String(), "pdxd_"))
}

// The other pairing routes still need the admin token (and a device token is not one).
func TestPairingRoutes_StillNeedTheAdminToken(t *testing.T) {
	h, c := pairingEnv(t, []string{"100.64.0.0/10", "127.0.0.1"})
	mint := callWith(t, c, http.MethodPost, "/api/devices", "t", map[string]any{
		"pairing_id": "00000000-0000-4000-8000-00000000000c", "label": "iPhone", "client": map[string]any{"kind": "app", "label": "Purdex.app"}})
	require.Equal(t, http.StatusCreated, mint.Code)
	var m struct{ ID, Token string }
	require.NoError(t, json.Unmarshal(mint.Body.Bytes(), &m))
	require.Equal(t, http.StatusOK, callWith(t, c, http.MethodGet, "/api/info", m.Token, nil).Code) // first use

	for _, bearer := range []string{"", "wrong", m.Token} {
		for _, rt := range [][2]string{
			{"POST", "/api/host-transfer/pairings"}, {"GET", "/api/host-transfer/pairings/ABCD2345"}, {"DELETE", "/api/host-transfer/pairings/ABCD2345"},
			{"POST", "/api/host-transfer/redeem"}, {"POST", "/api/host-transfer"},
		} {
			rec := send(h, rt[0], rt[1], bearer, "100.64.0.9:1", pairBody())
			assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, rec.Code, "%s %s bearer %q: %d", rt[0], rt[1], bearer, rec.Code)
		}
	}
}

// The exemption is one exact method and path: a claim-shaped path with another method, or a sibling path, is not exempt.
func TestPairingClaim_ExemptionIsOneExactRoute(t *testing.T) {
	h, _ := pairingEnv(t, nil)
	for _, rt := range [][2]string{
		{"GET", "/api/host-transfer/pairings/claim"}, {"DELETE", "/api/host-transfer/pairings/claim"},
		{"POST", "/api/host-transfer/pairings/claim/x"}, {"POST", "/api/host-transfer/pairings/claim2"}, {"POST", "/api/host-transfer/redeem"},
	} {
		rec := send(h, rt[0], rt[1], "", "100.64.0.9:1", map[string]string{"code": "ABCD2345"})
		assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed}, rec.Code, "%s %s: %d", rt[0], rt[1], rec.Code)
		assert.NotEqual(t, http.StatusOK, rec.Code, "%s %s", rt[0], rt[1])
	}
	// and a device token on the claim route changes nothing: still the code that decides
	rec := send(h, "POST", "/api/host-transfer/pairings/claim", "pdxd_0123456789abcdef0123456789abcdef", "100.64.0.9:1", map[string]string{"code": "ABCD2345"})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
