package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

// #2062 R1: no pdx command and no mod call writes a relay quota. Not a security boundary (the App and pdx share one
// host token, as for the unattended switch) but an agent gets no tool for it. Mutation gate: name the route in a pdx
// source file → red.
func TestPdx_NothingWritesARelayQuota(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := string(b)
		slash := filepath.ToSlash(path)
		switch {
		case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
			if strings.Contains(s, team.RelayQuotaRoute) || strings.Contains(s, "RelayQuotaRoute") || strings.Contains(s, "RelayQuotaPutRequest") ||
				strings.Contains(s, "/api/hostconfig/relay_quota") {
				t.Errorf("%s names the relay quota route: no pdx code may write a quota", path)
			}
		case strings.HasPrefix(slash, "plugin/purdex/hooks/") && strings.HasSuffix(path, ".js"):
			if strings.Contains(s, "relay-quota") || strings.Contains(s, "relay_quota") {
				t.Errorf("%s mentions the relay quota: the mod never writes it", path)
			}
		case slash == "plugin/purdex/skills/pdx-team/SKILL.md":
			if strings.Contains(s, team.RelayQuotaRoute) || strings.Contains(s, "hostconfig/relay_quota") {
				t.Errorf("%s teaches a relay quota route", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The relay quota route is on the general chain, admin token only: the peer host's token, a wrong one and none are
// 401 and never reach the handler. Mutation gate: move the route under /api/peers/ → red.
func TestNewOuterHandler_RelayQuotaIsAdminOnly(t *testing.T) {
	c := newTestCore(&config.Config{Token: "admin-secret", DataDir: t.TempDir(), Peers: config.PeersConfig{
		Hosts: []config.PeerHost{{Alias: "host-a", HostID: "hostid-a", InboundToken: "host-a-token"}}}})
	ran := 0
	stub := func(w http.ResponseWriter, _ *http.Request) { ran++; w.WriteHeader(http.StatusOK) }
	mux := http.NewServeMux()
	mux.HandleFunc("PUT "+team.RelayQuotaRoute, stub)
	outer := newOuterHandler(c, mux, nil)
	for _, tc := range []struct {
		bearer string
		want   int
	}{{"host-a-token", 401}, {"wrong-token", 401}, {"", 401}, {"admin-secret", 200}} {
		before := ran
		req := httptest.NewRequest(http.MethodPut, team.RelayQuotaRoute, strings.NewReader(`{}`))
		if tc.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+tc.bearer)
		}
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, req)
		if reached := ran > before; rec.Code != tc.want || reached != (tc.want == 200) {
			t.Errorf("bearer %q: got %d (stub ran %v), want %d", tc.bearer, rec.Code, reached, tc.want)
		}
	}
}
