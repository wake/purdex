package agent

import (
	"sync"
	"time"
)

const PathHintSchemaVersion = 1

const (
	PathHintKindRead  = "read"
	PathHintKindWrite = "write"
	PathHintKindEdit  = "edit"
)

// PathHint v1 schema (7 fields). Dir-level only — no full file path, no
// basename. HostId travels in the broadcast envelope, never in payload.
//
// Cwd is the agent's working directory and acts as the SPA cache scope key;
// SessionCode is a per-session tag the SPA uses for lookup priority (entries
// from the current session sort first when more than one session shares a
// cwd, e.g. CC restarts in the same repo).
type PathHint struct {
	SchemaVersion int       `json:"schemaVersion"`
	AgentID       string    `json:"agentId"`
	SessionCode   string    `json:"sessionCode"`
	Cwd           string    `json:"cwd"`
	Dir           string    `json:"dir"`
	Kind          string    `json:"kind"`
	Timestamp     time.Time `json:"timestamp"`
}

// PathHintRingBuffer is a fixed-capacity FIFO of recent hints. In-memory only;
// dropped on daemon restart.
type PathHintRingBuffer struct {
	mu    sync.Mutex
	cap   int
	items []PathHint
}

func NewPathHintRingBuffer(capacity int) *PathHintRingBuffer {
	if capacity <= 0 {
		capacity = 1
	}
	return &PathHintRingBuffer{cap: capacity, items: make([]PathHint, 0, capacity)}
}

func (r *PathHintRingBuffer) Push(h PathHint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, h)
	if len(r.items) > r.cap {
		r.items = r.items[len(r.items)-r.cap:]
	}
}

func (r *PathHintRingBuffer) Snapshot() []PathHint {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PathHint, len(r.items))
	copy(out, r.items)
	return out
}
