package agent

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/lights"
)

func TestAbortedAt_ReadsTheNewestStreamOfTheSession(t *testing.T) {
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	st := lights.NewStreamState("stream-a")
	st.AbortedAt = at
	quiet := lights.NewStreamState("stream-b")
	m := &Module{}
	m.modStreams = map[string]*lights.StreamState{"stream-a": st, "stream-b": quiet}
	m.modBySID = map[string]string{"sess-1": "stream-a", "sess-2": "stream-b"}
	if got, ok := m.AbortedAt("sess-1"); !ok || !got.Equal(at) {
		t.Errorf("sess-1: %v %v, want %v", got, ok, at)
	}
	for _, sid := range []string{"sess-2", "sess-none", ""} {
		if got, ok := m.AbortedAt(sid); ok {
			t.Errorf("%q: got %v, want none", sid, got)
		}
	}
	var nilMod *Module
	if _, ok := nilMod.AbortedAt("sess-1"); ok {
		t.Error("a nil module answers none")
	}
}
