package peers

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	ipeers "github.com/wake/purdex/internal/peers"
)

// nameRewriteAfter is how long an unchanged name is trusted before it is
// written again (refreshing seen_at).
const nameRewriteAfter = time.Hour

// NameSink persists the registry name seen for a conversation
// (store.ConversationNameStore).
type NameSink interface {
	Upsert(ctx context.Context, sessionID, name string, nowMs int64) error
}

type nameState struct {
	name      string
	writtenAt time.Time
}

// nameWriter records "live session id -> registry name" into a NameSink, so
// the conversation list can show a name for a conversation after it ended.
// Inventory passes run concurrently, so mu guards the throttle state; the
// store call itself runs outside the lock. A sid whose write is in flight is
// skipped by other passes, and a failed write leaves the throttle state
// untouched so the next pass retries.
type nameWriter struct {
	sink NameSink
	now  func() time.Time

	mu       sync.Mutex
	state    map[string]nameState
	inflight map[string]bool
}

func newNameWriter() *nameWriter {
	return &nameWriter{
		now:      time.Now,
		state:    make(map[string]nameState),
		inflight: make(map[string]bool),
	}
}

// WithNameSink wires the sink that records registry names; nil (the default)
// records nothing. Returns m for chaining.
func (m *Module) WithNameSink(s NameSink) *Module {
	if m.names == nil {
		m.names = newNameWriter()
	}
	m.names.sink = s
	return m
}

// observeNames is called once per inventory pass with that pass's registry
// entries.
func (m *Module) observeNames(entries []ipeers.Entry) {
	if m.names == nil { // a Module built without New (test literals)
		return
	}
	m.names.observe(entries, m.logf)
}

type pendingName struct {
	sid, name string
}

func (w *nameWriter) observe(entries []ipeers.Entry, logf func(string, ...any)) {
	if w.sink == nil {
		return
	}
	now := w.now()

	// Phase 1 (locked): decide what to write and mark it in flight.
	var todo []pendingName
	w.mu.Lock()
	// A resumed session can have several live entries with one session id and
	// different names. One name per session per pass: the recorded one while
	// it is still live (no flapping), else the smallest (deterministic).
	chosen := make(map[string]string)
	var order []string
	for _, e := range entries {
		if len(e.SessionID) != 36 {
			continue
		}
		if _, err := uuid.Parse(e.SessionID); err != nil {
			continue
		}
		if !ipeers.RoutableName(e.Name) {
			continue
		}
		cur, seen := chosen[e.SessionID]
		switch {
		case !seen:
			order = append(order, e.SessionID)
			chosen[e.SessionID] = e.Name
		case w.state[e.SessionID].name == cur:
			// keep the recorded name
		case w.state[e.SessionID].name == e.Name || e.Name < cur:
			chosen[e.SessionID] = e.Name
		}
	}
	for _, sid := range order {
		name := chosen[sid]
		if w.inflight[sid] {
			continue
		}
		if st, ok := w.state[sid]; ok && st.name == name && now.Sub(st.writtenAt) <= nameRewriteAfter {
			continue
		}
		w.inflight[sid] = true
		todo = append(todo, pendingName{sid, name})
	}
	w.mu.Unlock()

	// Phase 2 (unlocked): write; Phase 3 (locked): record successes only.
	for _, p := range todo {
		err := w.sink.Upsert(context.Background(), p.sid, p.name, now.UnixMilli())
		w.mu.Lock()
		delete(w.inflight, p.sid)
		if err == nil {
			w.state[p.sid] = nameState{name: p.name, writtenAt: now}
		}
		w.mu.Unlock()
		if err != nil {
			logf("peers: record conversation name: %v", err)
		}
	}
}
