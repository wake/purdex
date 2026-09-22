package session

import (
	"crypto/rand"
	"encoding/hex"
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

// newEpoch draws a random 64-bit process identity as 16 lowercase hex chars.
// crypto/rand.Read never returns an error (Go ≥1.24), so there is no fallback.
func newEpoch() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// versionedList performs a fresh tmux read and stamps it with the next seq.
//
// The seq is taken and the read performed under one lock, so within an epoch
// a larger seq always means a read that started after the smaller one's read
// finished (spec §3.3 rule 1), and every seq belongs to exactly the read that
// produced the list it is sent with (rule 5). A failed read does not advance
// the counter. Every versioned path — ?fresh=1, the subscribe snapshot, the
// wait-for and ticker pushes — goes through here and never re-uses a list.
func (m *SessionModule) versionedList() (VersionedSessions, error) {
	m.snapMu.Lock()
	defer m.snapMu.Unlock()

	if m.snapSeq >= maxSeq {
		m.epoch = newEpoch()
		m.snapSeq = 0
	}
	seq := m.snapSeq + 1

	sessions, err := m.ListSessions()
	if err != nil {
		return VersionedSessions{}, err
	}
	if sessions == nil {
		sessions = []SessionInfo{}
	}
	m.snapSeq = seq
	return VersionedSessions{Epoch: m.epoch, Seq: seq, Sessions: sessions}, nil
}
