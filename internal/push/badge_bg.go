package push

import (
	"encoding/json"
	"errors"
)

// maxClosedIDs caps purdex.closed_approval_ids of a background badge push (badge spec §3.1). The rest are left out: the phone
// clears their delivered notifications at its next foreground, as it did before background pushes existed.
const maxClosedIDs = 16

// backgroundPayload is the body of a background badge push: `content-available` and the purdex block, no alert, sound,
// mutable-content or badge. It says nothing without the open set (a failed read is "unknown", which is all this push is
// about), so a Content whose OpenApprovals is nil is refused here, not sent.
//
// It stays inside maxPayload: if it would not, ids are dropped before keys, and keys only when no id is left.
func (c Content) backgroundPayload(hostID string) ([]byte, error) {
	if c.OpenApprovals == nil || c.OpenApprovalKeys == nil {
		return nil, errors.New("background badge push: the open set is unknown")
	}
	ids := c.ClosedApprovalIDs
	if len(ids) > maxClosedIDs {
		ids = ids[:maxClosedIDs]
	}
	keys := c.OpenApprovalKeys
	for {
		raw, err := json.Marshal(map[string]any{
			"aps": map[string]any{"content-available": 1},
			"purdex": map[string]any{
				"kind": "badge", "host_id": hostID, "open_approvals": *c.OpenApprovals,
				"open_approval_keys": keys, "closed_approval_ids": nonNil(ids),
			},
		})
		if err != nil {
			return nil, err
		}
		if len(raw) <= maxPayload {
			return raw, nil
		}
		switch {
		case len(ids) > 0:
			ids = ids[:len(ids)-1]
		case len(keys) > 0:
			keys = keys[:len(keys)-1]
		default:
			return nil, errors.New("push payload exceeds 4096 bytes")
		}
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
