package push

import (
	"encoding/json"
	"errors"

	"github.com/wake/purdex/internal/reads"
)

// maxClosedIDs caps purdex.closed_approval_ids of a background badge push (badge spec §3.1). The rest are left out: the phone
// clears their delivered notifications at its next foreground, as it did before background pushes existed.
const maxClosedIDs = 16

// backgroundBuild builds a background push: the badge push (kind "badge", BG-1b) or the reads flush (kind "reads", BG-4b).
func (c Content) backgroundBuild(hostID string) ([]byte, []reads.Read, error) {
	if c.Kind == "reads" {
		return c.readsFlushBuild(hostID)
	}
	return c.badgeBuild(hostID)
}

// badgeBuild is the body of a background badge push: `content-available` and the purdex block, no alert, sound,
// mutable-content or badge. It says nothing without the open set (a failed read is "unknown", which is all this push is
// about), so a Content whose OpenApprovals is nil is refused here, not sent. It also carries the reads of other devices
// (BG-4b) when the phone syncs them.
//
// It stays inside maxPayload: if it would not, reads are dropped first (from the newest end; they stay pending, nothing is
// lost), then ids (their notifications are cleared at the next foreground), then keys, and keys only when nothing else is
// left.
func (c Content) badgeBuild(hostID string) ([]byte, []reads.Read, error) {
	if c.OpenApprovals == nil || c.OpenApprovalKeys == nil {
		return nil, nil, errors.New("background badge push: the open set is unknown")
	}
	ids := c.ClosedApprovalIDs
	if len(ids) > maxClosedIDs {
		ids = ids[:maxClosedIDs]
	}
	keys := c.OpenApprovalKeys
	rs := firstReads(c.Reads)
	for {
		purdex := map[string]any{
			"kind": "badge", "host_id": hostID, "open_approvals": *c.OpenApprovals,
			"open_approval_keys": keys, "closed_approval_ids": nonNil(ids),
		}
		if len(rs) > 0 {
			purdex["reads"] = rs
		}
		raw, err := json.Marshal(map[string]any{"aps": map[string]any{"content-available": 1}, "purdex": purdex})
		if err != nil {
			return nil, nil, err
		}
		if len(raw) <= maxPayload {
			return raw, rs, nil
		}
		switch {
		case len(rs) > 0:
			rs = rs[:len(rs)-1]
		case len(ids) > 0:
			ids = ids[:len(ids)-1]
		case len(keys) > 0:
			keys = keys[:len(keys)-1]
		default:
			return nil, nil, errors.New("push payload exceeds 4096 bytes")
		}
	}
}

// readsFlushBuild is the push that carries pending reads when nothing else went to the phone for a while: kind "reads",
// background, the reads and nothing else. With no read it is not built.
func (c Content) readsFlushBuild(hostID string) ([]byte, []reads.Read, error) {
	rs := firstReads(c.Reads)
	for len(rs) > 0 {
		raw, err := json.Marshal(map[string]any{
			"aps":    map[string]any{"content-available": 1},
			"purdex": map[string]any{"kind": "reads", "host_id": hostID, "reads": rs},
		})
		if err != nil {
			return nil, nil, err
		}
		if len(raw) <= maxPayload {
			return raw, rs, nil
		}
		rs = rs[:len(rs)-1]
	}
	return nil, nil, errors.New("reads flush: nothing to carry")
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
