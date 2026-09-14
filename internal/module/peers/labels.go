package peers

import (
	"time"

	"github.com/wake/purdex/internal/store"
)

// LabelStore is the peer_labels table (Peer Address v2 spec §3.3):
// *store.PeerLabelStore in production, a fake in tests. nil means "no
// store": every conversation has its default label and claims fail with
// store_unavailable.
type LabelStore interface {
	Snapshot() ([]store.PeerLabel, error)
	Claim(sessionID, label string, now time.Time) (store.PeerLabel, error)
	Release(sessionID string, now time.Time) (store.PeerLabel, bool, error)
}
