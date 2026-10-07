package teammod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/team"
)

// getPrompts is GET /api/relay/prompts on the fixture, decoded.
func (f *fixture) getPrompts() team.RelayPrompts {
	f.t.Helper()
	code, body := f.do(http.MethodGet, "/api/relay/prompts", "")
	if code != http.StatusOK {
		f.t.Fatalf("GET /api/relay/prompts: %d %s", code, body)
	}
	var p team.RelayPrompts
	if err := json.Unmarshal(body, &p); err != nil {
		f.t.Fatalf("decode: %v; body=%s", err, body)
	}
	return p
}

// Spec §8.8: with nothing stored, each body is the built-in default, and
// the answer lists the defaults, the fixed parts and the variables
// (plan v3 P9a deviation 1).
func TestRelayPrompts_UnsetAnswersTheDefaults(t *testing.T) {
	f := newFixture(t)
	got := f.getPrompts()
	d := team.DefaultRelayPromptBodies
	want := team.RelayPrompts{Write: d.Write, Fix: d.Fix, Seed: d.Seed, Defaults: d,
		Fixed: team.RelayPromptFixedParts, Variables: team.RelayPromptVariables}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// A stored body is the effective one; the defaults are still listed (the
// settings page's 還原預設), and the unset bodies stay the defaults.
func TestRelayPrompts_StoredBodyIsEffectiveDefaultsStillListed(t *testing.T) {
	f := newFixture(t)
	f.switches.mu.Lock()
	f.switches.prompts = team.RelayPromptBodies{Write: "寫 {{path}}", Seed: "接 {{old_ref}}"}
	f.switches.mu.Unlock()
	got := f.getPrompts()
	if got.Write != "寫 {{path}}" || got.Seed != "接 {{old_ref}}" || got.Fix != team.DefaultRelayPromptBodies.Fix {
		t.Fatalf("effective bodies = %q / %q / %q", got.Write, got.Fix, got.Seed)
	}
	if got.Defaults != team.DefaultRelayPromptBodies {
		t.Fatalf("defaults = %+v", got.Defaults)
	}
}

// Behaviour rule 1 (spec §8.8, U21 (b)): nothing is cached. With the real
// hostconfig module behind the route, a PUT is served on the next GET,
// and 還原預設 ("") brings the default back.
func TestRelayPrompts_EditServedOnTheNextGet(t *testing.T) {
	f := newFixture(t)
	hc := hostconfig.New()
	if err := hc.Init(core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hc.Stop(context.Background()) })
	f.m.prompts = hc
	hcMux := http.NewServeMux()
	hc.RegisterRoutes(hcMux)
	put := func(items string, rev int) {
		t.Helper()
		rec := httptest.NewRecorder()
		hcMux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/hostconfig/relay",
			strings.NewReader(`{"items":`+items+`,"baseRevision":`+strconv.Itoa(rev)+`}`)))
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT %s: %d %s", items, rec.Code, rec.Body.String())
		}
	}
	if got := f.getPrompts(); got.Fix != team.DefaultRelayPromptBodies.Fix {
		t.Fatalf("before the edit: fix = %q", got.Fix)
	}
	put(`{"prompt_fix":"補齊 {{path}}"}`, 0)
	if got := f.getPrompts(); got.Fix != "補齊 {{path}}" {
		t.Fatalf("after the edit: fix = %q", got.Fix)
	}
	put(`{"prompt_fix":""}`, 1)
	if got := f.getPrompts(); got.Fix != team.DefaultRelayPromptBodies.Fix {
		t.Fatalf("after 還原預設: fix = %q", got.Fix)
	}
}

// A reader failure (host_config.db, or a stored body that no longer
// validates) is 500 storage_error, the module's code for a store failure.
func TestRelayPrompts_ReaderErrorIs500(t *testing.T) {
	f := newFixture(t)
	f.switches.mu.Lock()
	f.switches.promptsErr = errors.New("prompt_fix: a relay prompt may not contain [pdx-relay")
	f.switches.mu.Unlock()
	code, body := f.do(http.MethodGet, "/api/relay/prompts", "")
	if code != http.StatusInternalServerError || decodeErr(t, body).Error != errStorage {
		t.Fatalf("got %d %s, want 500 %s", code, body, errStorage)
	}
}
