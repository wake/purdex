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
		Code: func(ctx context.Context, sessionID string) (string, bool, error) {
			svc, ok := c.Registry.Get(ipeers.NameServiceKey)
			if !ok {
				return "", false, nil
			}
			if names, ok := svc.(codeReader); ok {
				return names.CodeOfErr(ctx, sessionID)
			}
			names, ok := svc.(ipeers.NameService)
			if !ok {
				return "", false, nil
			}
			code, ok := names.CodeOf(ctx, sessionID)
			return code, ok, nil
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
		Root: func(_ context.Context, sessionID string) (string, error) {
			svc, ok := c.Registry.Get(team.LineageRootKey)
			if !ok {
				return "", nil // no lineage yet: the root is not known, and the overlay is not asked with a guess
			}
			res, ok := svc.(team.LineageRootResolver)
			if !ok {
				return "", nil
			}
			return res.RootSessionOf(sessionID)
		},
		Display: ipeers.PoolDisplayName,
	}, notifylabel.WithOverlay(registryOverlay{c}))
}

// registryOverlay is the groups the App wrote back (the notify module), looked up in the registry on every call so that it is
// there whichever of the two modules started first; without it a session has no group but its team's. A registry lookup and a
// map read: no I/O (the Overlay contract).
type registryOverlay struct{ c *core.Core }

func (o registryOverlay) GroupOf(rootSessionID string) string {
	svc, ok := o.c.Registry.Get(notifylabel.OverlayKey)
	if !ok {
		return ""
	}
	ov, ok := svc.(notifylabel.Overlay)
	if !ok {
		return ""
	}
	return ov.GroupOf(rootSessionID)
}

// codeReader is the name service that can say a failed read (the peers module); any other only has CodeOf.
type codeReader interface {
	CodeOfErr(ctx context.Context, sessionID string) (string, bool, error)
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
