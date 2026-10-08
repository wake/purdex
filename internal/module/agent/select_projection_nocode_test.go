package agent

import (
	"fmt"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// -----------------------------------------------------------------------------
// T4: selectSessionProjection only needs the pane's session NAME; it must not
// pay resolveSessionCode (LookupCodeByName / ListSessions) for a code it drops.
// -----------------------------------------------------------------------------

// referenceSelect is the oracle for the name step: the same selection rule,
// but it resolves names through resolvePaneSession (which still returns
// name+code).
func referenceSelect(m *Module, sessionName string, projections []SessionProjection) *SessionProjection {
	var selected *SessionProjection
	background := ""
	for i := range projections {
		name, _ := m.resolvePaneSession(projections[i].PaneID)
		if name != sessionName {
			continue
		}
		background = higherBackground(background, projections[i].Background)
		if selected == nil || projectionRankGreater(projections[i], *selected) {
			projection := projections[i]
			selected = &projection
		}
	}
	if selected != nil {
		selected.Background = background
	}
	return selected
}

func TestSelectSessionProjection_SameResultAsReferenceWithoutCodeLookups(t *testing.T) {
	fx := newReplayFixture(t, 3, 3)
	m := fx.m
	projections, err := m.liveFrameProjections()
	if err != nil || len(projections) == 0 {
		t.Fatalf("liveFrameProjections: %v (n=%d)", err, len(projections))
	}
	fx.sess.mu.Lock()
	fx.sess.list, fx.sess.lookup = 0, 0
	fx.sess.mu.Unlock()

	for _, name := range []string{"s0", "s1", "s2", "ghost"} {
		got := m.selectSessionProjection(name, projections)
		want := referenceSelect(m, name, projections)
		if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
			t.Errorf("session %s: got %+v, want %+v", name, got, want)
		}
		if name != "ghost" && got == nil {
			t.Errorf("session %s: expected a projection", name)
		}
	}

	// The reference above DOES look codes up; measure the real path separately.
	fx.sess.mu.Lock()
	fx.sess.list, fx.sess.lookup = 0, 0
	fx.sess.mu.Unlock()
	for _, name := range []string{"s0", "s1", "s2"} {
		if _, err := m.projectionForSession(name); err != nil {
			t.Fatalf("projectionForSession(%s): %v", name, err)
		}
	}
	if list, lookup := fx.sess.counts(); list != 0 || lookup != 0 {
		t.Errorf("projectionForSession resolved session codes: ListSessions=%d LookupCodeByName=%d, want 0/0", list, lookup)
	}
}

func TestSelectSessionProjection_NilTmuxAndNameErrorsUnchanged(t *testing.T) {
	fx := newReplayFixture(t, 1, 2)
	m := fx.m
	projections, err := m.liveFrameProjections()
	if err != nil || len(projections) != 2 {
		t.Fatalf("liveFrameProjections: %v (n=%d)", err, len(projections))
	}

	// PaneSessionName errors for exactly one pane: the other still selects.
	fx.tmx.failOnce["%00"] = true
	got := m.selectSessionProjection("s0", projections)
	if got == nil || got.PaneID != "%01" {
		t.Fatalf("one pane erroring: got %+v, want the %%01 projection", got)
	}

	// Every pane errors -> nil (as before).
	fx.tmx.failOnce["%00"] = true
	fx.tmx.failOnce["%01"] = true
	if got := m.selectSessionProjection("s0", projections); got != nil {
		t.Fatalf("all panes erroring: got %+v, want nil", got)
	}

	// nil tmux -> nil (as before).
	m.tmux = nil
	if got := m.selectSessionProjection("s0", projections); got != nil {
		t.Fatalf("nil tmux: got %+v, want nil", got)
	}
}

// TestLiveSessionProjections_SameRuleAsSelect: the snapshot / sweep entry
// point and the per-session entry point pick the same representative pane and
// the same session background.
func TestLiveSessionProjections_SameRuleAsSelect(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	for pane, name := range map[string]string{"%5": "work", "%6": "work", "%7": "work", "%8": "solo"} {
		fakeTmux.SetPaneSessionName(pane, name)
	}
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "work-code", Name: "work"}, {Code: "solo-code", Name: "solo"}}}
	seedRankPane(t, m, "%5", 501, agentpkg.StatusWaiting, 10, "") // older, most urgent
	seedRankPane(t, m, "%6", 502, agentpkg.StatusRunning, 20, "")
	f7 := seedRankPane(t, m, "%7", 503, agentpkg.StatusIdle, 30, "") // newest, runs a workflow
	seedRankPane(t, m, "%8", 504, agentpkg.StatusIdle, 5, "")
	m.modMu.Lock()
	m.hookBackground[f7.FrameID] = "workflow"
	m.modMu.Unlock()

	live, err := m.liveSessionProjections()
	if err != nil {
		t.Fatalf("liveSessionProjections: %v", err)
	}
	projections, err := m.liveFrameProjections()
	if err != nil {
		t.Fatalf("liveFrameProjections: %v", err)
	}
	if len(live) != 2 {
		t.Fatalf("live sessions = %d, want 2: %+v", len(live), live)
	}
	for _, np := range live {
		want := m.selectSessionProjection(np.SessionName, projections)
		got := np.Projection
		if want == nil || got.PaneID != want.PaneID || got.TopFrame.FrameID != want.TopFrame.FrameID ||
			got.EffectiveStatus() != want.EffectiveStatus() || got.Source != want.Source || got.Background != want.Background {
			t.Errorf("session %s: live %+v, select %+v", np.SessionName, got, want)
		}
		if np.SessionName == "work" && (np.Projection.PaneID != "%5" || np.Projection.Background != "workflow") {
			t.Errorf("work: got pane %s background %q, want %%5 / workflow", np.Projection.PaneID, np.Projection.Background)
		}
	}
}
