package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/execstat"
)

func eventCount(name string) int64 {
	for _, s := range execstat.Events() {
		if s.Name == name {
			return s.Total
		}
	}
	return 0
}

// The hook path's denominator is keyed by catalog names only: a name that came over the wire and is not in the catalog must
// land on "unknown", or the registry grows with whatever a client sends.
func TestHookEventLabel(t *testing.T) {
	var nilModule *Module
	if got := nilModule.hookEventLabel("cc", "PdxSessionStart"); got != "unknown" {
		t.Fatalf("nil module: got %q", got)
	}

	m := newTestModule(t)
	if got := m.hookEventLabel("cc", "PdxSessionStart"); got != "unknown" {
		t.Fatalf("no provider registered: got %q", got)
	}

	m.registry.Register(agentcc.NewProvider(nil, nil, nil, nil))
	for _, tc := range []struct{ agent, name, want string }{
		{"cc", "PdxSessionStart", "PdxSessionStart"},
		{"cc", "PdxUserPromptSubmit", "PdxUserPromptSubmit"},
		{"cc", "NotAnEvent", "unknown"},
		{"cc", "", "unknown"},
		{"nope", "PdxSessionStart", "unknown"},
	} {
		if got := m.hookEventLabel(tc.agent, tc.name); got != tc.want {
			t.Errorf("hookEventLabel(%q,%q) = %q, want %q", tc.agent, tc.name, got, tc.want)
		}
	}
}

// handleEvent counts one trigger per well-formed request, under the event's own name, and counts nothing for a body that
// does not parse.
func TestHandleEventCountsDenominator(t *testing.T) {
	execstat.ResetTags()
	t.Cleanup(execstat.ResetTags)
	m := newTestModule(t)
	m.registry.Register(agentcc.NewProvider(nil, nil, nil, nil))

	post := func(body string) int {
		req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		m.handleEvent(w, req)
		return w.Code
	}

	if code := post("not json"); code != http.StatusBadRequest {
		t.Fatalf("bad json: status %d", code)
	}
	if n := eventCount("hook.unknown") + eventCount("hook.PdxSessionStart"); n != 0 {
		t.Fatalf("a body that does not parse must not count, got %d", n)
	}

	body := func(name string) string {
		return `{"tmux_session":"work","tmux_pane_id":"%1","purdex_name":"` + name + `","raw_event":{},"agent_type":"cc","sender_pid":4242,"sender_start_time":"Sun Apr 20 01:30:00 2026"}`
	}
	// well-formed JSON that fails the schema checks is refused before it is counted
	post(`{"tmux_session":"work","purdex_name":"PdxSessionStart","raw_event":{},"agent_type":"cc"}`)
	if got := eventCount("hook.PdxSessionStart"); got != 0 {
		t.Fatalf("a request refused as schema_invalid must not count, got %d", got)
	}
	post(body("PdxSessionStart"))
	post(body("PdxSessionStart"))
	post(body("Bogus"))
	if got := eventCount("hook.PdxSessionStart"); got != 2 {
		t.Errorf("hook.PdxSessionStart = %d, want 2", got)
	}
	if got := eventCount("hook.unknown"); got != 1 {
		t.Errorf("hook.unknown = %d, want 1", got)
	}
}

func tagCount(name string) int64 {
	for _, s := range execstat.Tags() {
		if s.Name == name {
			return s.Total
		}
	}
	return 0
}

// The forks a pdx hook reports with its event are counted under "hook-cli", once per fork; an event without the field (an older
// hook) and a request refused as schema_invalid add none.
func TestHandleEventCountsTheForksTheHookReports(t *testing.T) {
	execstat.ResetTags()
	t.Cleanup(execstat.ResetTags)
	m := newTestModule(t)
	m.registry.Register(agentcc.NewProvider(nil, nil, nil, nil))
	post := func(body string) {
		req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		m.handleEvent(httptest.NewRecorder(), req)
	}
	body := func(extra string) string {
		return `{"tmux_session":"work","tmux_pane_id":"%1","purdex_name":"PdxSessionStart","raw_event":{},"agent_type":"cc","sender_pid":4242,"sender_start_time":"Sun Apr 20 01:30:00 2026"` + extra + `}`
	}
	post(body(""))
	if got := tagCount("hook-cli"); got != 0 {
		t.Fatalf("an event without forks: %d", got)
	}
	post(body(`,"forks":3`))
	if got := tagCount("hook-cli"); got != 3 {
		t.Fatalf("an event reporting 3 forks: %d", got)
	}
	post(`{"tmux_session":"work","purdex_name":"PdxSessionStart","raw_event":{},"agent_type":"cc","forks":5}`)
	if got := tagCount("hook-cli"); got != 3 {
		t.Fatalf("a request refused as schema_invalid must not count its forks: %d", got)
	}
}
