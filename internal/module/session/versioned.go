package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/wake/purdex/internal/core"
)

// maxSeq is the largest seq a JSON client can hold exactly (2^53−1). Reaching
// it rotates the epoch rather than wrapping (spec §3.3 "Rotation").
const maxSeq = 1<<53 - 1

// VersionedSessions is a session list stamped with the version that orders it
// against every other versioned list from this daemon process (spec §3.3).
// It is the body of GET /api/sessions?fresh=1; WS `sessions` frames carry the
// same Epoch/Seq as top-level keys.
type VersionedSessions struct {
	Epoch    string        `json:"epoch"`
	Seq      uint64        `json:"seq"`
	Sessions []SessionInfo `json:"sessions"`
}

// hostEvent renders v as a WS `sessions` frame carrying its version.
func (v VersionedSessions) hostEvent() core.HostEvent {
	return core.HostEvent{
		Type:  "sessions",
		Value: mustMarshal(v.Sessions),
		Epoch: v.Epoch,
		Seq:   v.Seq,
	}
}

// newEpoch draws a random 64-bit process identity as 16 lowercase hex chars.
// crypto/rand.Read never returns an error (Go ≥1.24), so there is no fallback.
func newEpoch() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// versionedList performs a fresh tmux read and stamps it with the next seq.
//
// The read is performed and the seq taken inside one slot (snapSlot), so
// within an epoch a larger seq always means a read that started after the
// smaller one's read finished (spec §3.3 rule 1), and every seq belongs to
// exactly the read that produced the list it is sent with (rule 5). Every
// versioned path — ?fresh=1, the subscribe snapshot, the wait-for and ticker
// pushes — goes through here and never re-uses a list.
//
// Bounded (#1293): the read runs under ctx capped at listReadTimeout, and a
// caller waiting for the slot gives up with ctx.Err() when ctx ends. The seq
// is assigned — and the epoch rotated at maxSeq — only AFTER a successful
// read, so a failed, timed-out or abandoned read never consumes a seq and
// never rotates the epoch.
func (m *SessionModule) versionedList(ctx context.Context) (VersionedSessions, error) {
	if err := m.snapSlot.acquire(ctx); err != nil {
		return VersionedSessions{}, fmt.Errorf("versioned session list: %w", err)
	}
	defer m.snapSlot.release()

	sessions, err := m.ListSessionsContext(ctx)
	if err != nil {
		return VersionedSessions{}, err
	}
	if sessions == nil {
		sessions = []SessionInfo{}
	}
	if m.snapSeq >= maxSeq {
		m.epoch = newEpoch()
		m.snapSeq = 0
	}
	m.snapSeq++
	return VersionedSessions{Epoch: m.epoch, Seq: m.snapSeq, Sessions: sessions}, nil
}
