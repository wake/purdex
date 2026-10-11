package agent

import (
	"context"
	"path/filepath"
	"testing"
)

// A POST that completes after Stop (the HTTP server drains after StopModules) is written by Close.
func TestStatusSnapshotPersist_PostAfterStopIsFlushedByClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	a := moduleOn(t, path)
	a.sessions = sessionsOf("tm1", "c1")
	a.startContextUsageFlush()
	if err := a.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	postSnap(a, "c1", `{"cwd":"/late"}`) // drained handler finishing after Stop
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := moduleOn(t, path)
	b.sessions = sessionsOf("tm1", "c1")
	b.restoreStatusSnapshots(context.Background())
	if got := replayOf(t, b); got["c1"] == "" {
		t.Fatalf("late snapshot lost: %v", got)
	}
}
