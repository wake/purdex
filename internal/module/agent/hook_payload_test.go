package agent

import (
	"encoding/json"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
)

// TestEventRequest_UnmarshalsTmuxSessionID pins the wire-level contract: the
// daemon must read `tmux_session_id` off the hook payload. Without this, the
// fast path can't engage and every hook still racing through the name cache
// is exposed to the kill-then-recreate name-reuse window.
func TestEventRequest_UnmarshalsTmuxSessionID(t *testing.T) {
	var req EventRequest
	body := `{"tmux_session_id":"$7","tmux_session":"alpha"}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.TmuxSessionID != "$7" {
		t.Errorf("TmuxSessionID = %q, want $7", req.TmuxSessionID)
	}
	if req.TmuxSession != "alpha" {
		t.Errorf("TmuxSession = %q, want alpha", req.TmuxSession)
	}
}

// TestResolveSessionCodeFromHook_BypassesCacheWhenIDPresent is the primary
// acceptance test for option A: when the hook payload carries the immutable
// tmux session ID, the daemon resolves the session code via a pure O(1)
// EncodeSessionID call — neither LookupCodeByName nor ListSessions runs.
//
// This is the structural fix for the rename-race finding that all three
// codex round-2 reviewers flagged: the name cache TTL window leaves room
// for `kill-session foo && new-session -s foo` to alias the old code, and
// only side-stepping the cache entirely closes that window.
func TestResolveSessionCodeFromHook_BypassesCacheWhenIDPresent(t *testing.T) {
	m := &Module{}
	fake := &fakeFastSessionProvider{
		// ListSessions/lookup deliberately seeded with NO entry for $5 so a
		// regression that falls through to either path returns "" and the
		// counter assertion below catches it.
		sessions: []session.SessionInfo{{Name: "alpha", Code: "stale-code"}},
		lookup:   map[string]string{"alpha": "stale-code"},
	}
	m.sessions = fake

	req := EventRequest{TmuxSessionID: "$5", TmuxSession: "alpha"}
	got, path := m.resolveSessionCodeFromHook(req)

	want, err := session.EncodeSessionID("$5")
	if err != nil {
		t.Fatalf("EncodeSessionID: %v", err)
	}
	if got != want {
		t.Errorf("resolveSessionCodeFromHook = %q, want %q (pure ID path)", got, want)
	}
	if path != hookCodePathID {
		t.Errorf("resolution path = %q, want %q", path, hookCodePathID)
	}
	if fake.lookupCalls != 0 {
		t.Errorf("LookupCodeByName calls = %d, want 0 (ID path must skip cache)", fake.lookupCalls)
	}
	if fake.listCalls != 0 {
		t.Errorf("ListSessions calls = %d, want 0 (ID path must skip slow path)", fake.listCalls)
	}
}

// TestResolveSessionCodeFromHook_FallsBackToNamePathOnEmptyID covers
// backward compat: when the pdx hook binary is older than the daemon and
// omits tmux_session_id, the daemon falls through to the existing
// LookupCodeByName fast path. Required so a partial rollout (daemon
// upgraded before hooks) doesn't break the hot path.
func TestResolveSessionCodeFromHook_FallsBackToNamePathOnEmptyID(t *testing.T) {
	m := &Module{}
	fake := &fakeFastSessionProvider{
		sessions: []session.SessionInfo{{Name: "alpha", Code: "alpha-code"}},
		lookup:   map[string]string{"alpha": "alpha-code"},
	}
	m.sessions = fake

	req := EventRequest{TmuxSessionID: "", TmuxSession: "alpha"}
	got, path := m.resolveSessionCodeFromHook(req)

	if got != "alpha-code" {
		t.Errorf("resolveSessionCodeFromHook = %q, want alpha-code", got)
	}
	if path != hookCodePathIDEmpty {
		t.Errorf("resolution path = %q, want %q", path, hookCodePathIDEmpty)
	}
	if fake.lookupCalls != 1 {
		t.Errorf("LookupCodeByName calls = %d, want 1 (fallback engaged)", fake.lookupCalls)
	}
	if fake.listCalls != 0 {
		t.Errorf("ListSessions calls = %d, want 0 (cache hit on fallback)", fake.listCalls)
	}
}

// TestResolveSessionCodeFromHook_FallsBackOnMalformedID covers the corruption
// path: a payload reaching us with TmuxSessionID="not-a-tmux-id" must not
// silently drop. EncodeSessionID errors → we fall through to the name path
// and the existing LookupCodeByName cache covers the resolution.
func TestResolveSessionCodeFromHook_FallsBackOnMalformedID(t *testing.T) {
	m := &Module{}
	fake := &fakeFastSessionProvider{
		sessions: []session.SessionInfo{{Name: "beta", Code: "beta-code"}},
		lookup:   map[string]string{"beta": "beta-code"},
	}
	m.sessions = fake

	req := EventRequest{TmuxSessionID: "not-a-tmux-id", TmuxSession: "beta"}
	got, path := m.resolveSessionCodeFromHook(req)

	if got != "beta-code" {
		t.Errorf("resolveSessionCodeFromHook = %q, want beta-code", got)
	}
	if path != hookCodePathMalformedID {
		t.Errorf("resolution path = %q, want %q", path, hookCodePathMalformedID)
	}
	if fake.lookupCalls != 1 {
		t.Errorf("LookupCodeByName calls = %d, want 1 (fallback after malformed ID)", fake.lookupCalls)
	}
}

// TestEmitHookToSession_DirectUsesSessionIDPath is the focused unit-level
// proof: emitHookToSession with an EventRequest carrying tmux_session_id
// broadcasts to the ID-derived code WITHOUT touching the name cache. We
// drive the helper directly (skipping handleEvent's frame_ops/projection
// machinery, which has its own name→code lookups for frame storage that
// are explicitly out of scope per the SOT). This test pins the cache-bypass
// behaviour where the fix lives.
func TestEmitHookToSession_DirectUsesSessionIDPath(t *testing.T) {
	m := newTestModule(t)
	expectedCode, err := session.EncodeSessionID("$9")
	if err != nil {
		t.Fatalf("EncodeSessionID: %v", err)
	}

	fake := &fakeFastSessionProvider{
		// Empty stores: any name-keyed call would return "" and skip the
		// broadcast. Green broadcast → ID path fired. Counter == 0 → ID
		// path is the ONLY route taken.
		sessions: []session.SessionInfo{},
		lookup:   map[string]string{},
	}
	m.sessions = fake
	m.core = &core.Core{Events: core.NewEventsBroadcaster()}

	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	req := EventRequest{TmuxSessionID: "$9", TmuxSession: "work"}
	normalized := agentpkg.NormalizedEvent{
		AgentType:    "cc",
		RawEventName: "PdxStop",
		BroadcastTs:  time.Now().UnixNano(),
	}

	decision, reason := m.emitHookToSession(req, normalized)
	if decision != "broadcasted" {
		t.Fatalf("decision = %q (reason=%q), want broadcasted", decision, reason)
	}
	if reason != string(hookCodePathID) {
		t.Errorf("reason = %q, want %q (id_path label exposes fast-path activation)", reason, hookCodePathID)
	}

	select {
	case msg := <-sub.SendCh():
		var env struct {
			Type    string `json:"type"`
			Session string `json:"session"`
		}
		if err := json.Unmarshal(msg, &env); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if env.Type != "hook" {
			t.Errorf("type = %q, want hook", env.Type)
		}
		if env.Session != expectedCode {
			t.Errorf("session = %q, want %q (ID-derived code)", env.Session, expectedCode)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for broadcast — ID path likely not engaged")
	}

	if fake.lookupCalls != 0 {
		t.Errorf("LookupCodeByName calls = %d, want 0 (ID path must skip name cache)", fake.lookupCalls)
	}
	if fake.listCalls != 0 {
		t.Errorf("ListSessions calls = %d, want 0 (ID path must skip slow path)", fake.listCalls)
	}
}

// TestEmitHookToSession_LegacyPayloadUsesNamePath covers the backward-compat
// branch: when a pre-upgrade pdx hook binary sends a payload without
// tmux_session_id, emitHookToSession must still resolve via the name cache.
// Pins that the new ID branch hasn't broken the fallback path.
func TestEmitHookToSession_LegacyPayloadUsesNamePath(t *testing.T) {
	m := newTestModule(t)
	fake := &fakeFastSessionProvider{
		sessions: []session.SessionInfo{{Name: "work", Code: "code-work"}},
		lookup:   map[string]string{"work": "code-work"},
	}
	m.sessions = fake
	m.core = &core.Core{Events: core.NewEventsBroadcaster()}

	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	req := EventRequest{TmuxSession: "work"} // legacy: no TmuxSessionID
	normalized := agentpkg.NormalizedEvent{AgentType: "cc", RawEventName: "PdxStop"}

	decision, reason := m.emitHookToSession(req, normalized)
	if decision != "broadcasted" {
		t.Fatalf("decision = %q, want broadcasted (legacy fallback)", decision)
	}
	if reason != string(hookCodePathIDEmpty) {
		t.Errorf("reason = %q, want %q (id_empty label flags un-migrated hook clients)", reason, hookCodePathIDEmpty)
	}
	if fake.lookupCalls != 1 {
		t.Errorf("LookupCodeByName calls = %d, want 1 (name path engaged)", fake.lookupCalls)
	}
}

// TestEmitHookToSession_MalformedIDReason pins the third path label: when a
// hook payload reaches us with a corrupt TmuxSessionID, the reason carries
// the malformed_id label so log greps can spot the pathological case
// distinct from a benign legacy (id_empty) fallback.
func TestEmitHookToSession_MalformedIDReason(t *testing.T) {
	m := newTestModule(t)
	fake := &fakeFastSessionProvider{
		sessions: []session.SessionInfo{{Name: "work", Code: "code-work"}},
		lookup:   map[string]string{"work": "code-work"},
	}
	m.sessions = fake
	m.core = &core.Core{Events: core.NewEventsBroadcaster()}

	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	req := EventRequest{TmuxSessionID: "not-a-tmux-id", TmuxSession: "work"}
	normalized := agentpkg.NormalizedEvent{AgentType: "cc", RawEventName: "PdxStop"}

	decision, reason := m.emitHookToSession(req, normalized)
	if decision != "broadcasted" {
		t.Fatalf("decision = %q, want broadcasted (fallback after malformed ID)", decision)
	}
	if reason != string(hookCodePathMalformedID) {
		t.Errorf("reason = %q, want %q (malformed_id label distinguishes corruption from legacy)", reason, hookCodePathMalformedID)
	}
}
