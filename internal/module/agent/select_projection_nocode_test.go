package agent

import (
	"fmt"
	"testing"
)

// -----------------------------------------------------------------------------
// T4: selectSessionProjection only needs the pane's session NAME; it must not
// pay resolveSessionCode (LookupCodeByName / ListSessions) for a code it drops.
// -----------------------------------------------------------------------------

// referenceSelect is the oracle for the name step: the same selection rule, but it
// resolves names through resolvePaneSession (which still returns name+code).
func referenceSelect(m *Module, sessionName string, projections []SessionProjection) *SessionProjection {
	var selected *SessionProjection
	for i := range projections {
		name, _ := m.resolvePaneSession(projections[i].PaneID)
		if name != sessionName {
			continue
		}
		if selected == nil || projectionRankGreater(projections[i], *selected) {
			projection := projections[i]
			selected = &projection
		}
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
