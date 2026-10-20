package jobnotify

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The job events the module reads from wwd are read leniently (765): an ended job whose record carries started_at (a wwd on queue schema 9
// keeps the last one) is told like any other, once.
func TestAnEndedJobThatHasStartedAtIsTold(t *testing.T) {
	s := &sent{}
	m := newMod(t, t.TempDir(), &fakeSource{}, s)
	at := time.Date(2026, 10, 20, 8, 0, 3, 0, time.UTC)
	j := job("j_started", wire.StateFailed, t0)
	j.StartedAt = &at
	for i := 0; i < 2; i++ {
		m.ev(context.Background(), event(t, j))
	}
	got := s.texts()
	if len(got) != 1 || !strings.Contains(got[0], "j_started") {
		t.Fatalf("sent %q", got)
	}
}
