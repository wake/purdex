package workbook

import (
	"log"
)

// Event kinds.
const (
	EventEntry  = "entry"  // an entry was inserted, or reached a final state
	EventStatus = "status" // a conversation's status was written
	EventTodos  = "todos"  // a turn or a refresh changed the todo list (v2)
)

// Event is one change the store has just committed. It is about the store's rows, not about who wrote them: a daemon
// runner and a mod that writes through the same calls announce the same way.
type Event struct {
	Kind      string
	ConvKey   string
	SessionID string    // the session whose turn produced the change
	Entry     Entry     // EventEntry: the row as it is now
	Status    StatusRow // EventStatus: the row as it is now
	Todos     []Todo    // EventTodos: the todos that changed, closings first then the adds
}

// SetObserver registers fn to be told of every committed change (nil clears it). It runs on the writer's goroutine after
// the write, holding no store lock, so it may read the store back; it must be quick. A panic in it is recovered. What
// is announced: an insert (state pending), a Finish that moved a pending entry (ok / failed / skipped), a SetStatus.
// Not announced: SetPushLine (the entry is still pending), FailPending (a restart; clients refetch on reconnect), and
// anything that changed no row.
func (s *Store) SetObserver(fn func(Event)) {
	if fn == nil {
		s.obs.Store(nil)
		return
	}
	s.obs.Store(&fn)
}

func (s *Store) emit(e Event) {
	p := s.obs.Load()
	if p == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[workbook] an observer panicked: %v", r)
		}
	}()
	(*p)(e)
}

// emitTodos announces the todos a committed write changed; nothing changed, nothing is sent.
func (s *Store) emitTodos(conv, session string, changed []Todo) {
	if len(changed) == 0 {
		return
	}
	s.emit(Event{Kind: EventTodos, ConvKey: conv, SessionID: session, Todos: changed})
}

// emitEntry announces the row id as it is now.
func (s *Store) emitEntry(id int64) {
	if s.obs.Load() == nil {
		return
	}
	e, err := s.Entry(id)
	if err != nil {
		log.Printf("[workbook] event for entry %d: %v", id, err)
		return
	}
	s.emit(Event{Kind: EventEntry, ConvKey: e.ConvKey, SessionID: e.SessionID, Entry: e})
}
