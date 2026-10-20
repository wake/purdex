package agent

import (
	"context"
	"encoding/json"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/notifylabel"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The notification title's label (notify-title spec §3.1, §3.4). The agent module owns one notifylabel.Labeler; the frame
// carries what it says (`notify_label`), the push module reads it off the same event, so the phone's title and the Apps'
// are one answer. A Label call is memory only, so the emit slot may make it; what it needs from peers and the team module is
// read by the labeler's own goroutine.

// newLabeler is the labeler of this module, reading peers and the team module through the registry on every background
// read (the modules start in no fixed order; either may be absent, and then its part is empty).
func newLabeler(c *core.Core) *notifylabel.Labeler {
	return notifylabel.New(notifylabel.Sources{
		Code: func(ctx context.Context, sessionID string) (string, bool) {
			svc, ok := c.Registry.Get(ipeers.NameServiceKey)
			if !ok {
				return "", false
			}
			names, ok := svc.(ipeers.NameService)
			if !ok {
				return "", false
			}
			return names.CodeOf(ctx, sessionID)
		},
		Group: func(_ context.Context, sessionID string) (string, error) {
			svc, ok := c.Registry.Get(team.NotifyGroupKey)
			if !ok {
				return "", nil
			}
			g, ok := svc.(team.NotifyGrouper)
			if !ok {
				return "", nil
			}
			return g.NotifyGroupOf(sessionID)
		},
		Display: ipeers.PoolDisplayName,
	})
}

// notifyLabelFor is the label a frame of the session carries, nil when there is nothing to say about the session (a cold
// cache, no persona, no team): the title then stays as it always was. Memory only; safe under the emit slot.
func (m *Module) notifyLabelFor(sessionID, tmuxName string) *notifylabel.Label {
	if m.labeler == nil || sessionID == "" || tmuxName == "" {
		return nil
	}
	l := m.labeler.Label(sessionID, tmuxName)
	if l.Name == "" && l.Group == "" {
		return nil
	}
	return &l
}

// storedSessionID is the agent session id inside a stored hook event ("" when it has none).
func storedSessionID(raw json.RawMessage) string {
	var e struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &e)
	return e.SessionID
}
