package nex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"lab.protype.tw/wake/nexen"
)

// c933b0-122: through the REAL nexen.Assemble (not a fake engine) — pdx assembles through nexen.Assemble and never builds
// execution.Deps itself, so Deps.ConfinedDir (<data_dir>/confined) is nexen's wiring; these tests prove the host's extra_profiles
// reaches that path, i.e. the opt-in profile is advertised only when listed and refused (400 profile_not_allowed) when not.
func realNexMux(t *testing.T, extra []string) *http.ServeMux {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	cfg.Nex.Sandbox.ExtraProfiles = extra
	m := New()
	m.logf = discardLogf
	realAssemble := m.assemble
	m.assemble = func(ctx context.Context, opts nexen.Options) (engine, error) {
		opts.DisableHostKeychain = true
		return realAssemble(ctx, opts)
	}
	require.NoError(t, m.Init(newTestCore(&cfg)))
	require.NoError(t, m.initErr)
	t.Cleanup(func() {
		_ = m.Stop(context.Background())
		_ = m.Close()
	})
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	return mux
}

func TestExtraProfilesAdvertisedOnlyWhenListed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra []string
		want  bool
	}{{"listed", []string{"confined"}, true}, {"not listed", nil, false}} {
		t.Run(tc.name, func(t *testing.T) {
			mux := realNexMux(t, tc.extra)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("GET", RoutePrefix+"/v1/capabilities", nil))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var body struct {
				Delegate struct {
					WritablePaths bool `json:"writable_paths"`
				} `json:"delegate"`
				SandboxProfiles []string `json:"sandbox_profiles"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, tc.want, contains(body.SandboxProfiles, "confined"), "sandbox_profiles: %v", body.SandboxProfiles)
			assert.Equal(t, tc.want, body.Delegate.WritablePaths, "delegate.writable_paths follows the listing: %s", rec.Body.String())
		})
	}
}

func TestConfinedRequestRefusedWhenNotListed(t *testing.T) {
	mux := realNexMux(t, nil)
	body := `{"provider":"claude","brief":"b","sandbox_profile":"confined","mounts":[{"path":"` + t.TempDir() + `","role":"cwd","writable":true}]}`
	req := httptest.NewRequest("POST", RoutePrefix+"/v1/executions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "profile_not_allowed")
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
