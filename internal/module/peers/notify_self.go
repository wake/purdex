package peers

import (
	"context"
	"errors"

	"github.com/wake/purdex/internal/core"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// originSource is the part of the OriginResolver a notice from a session's own inbox needs: the session's inbox, and its address by the
// ref of its conversation (which names that session and no other, unlike the tmux session name that the next session reuses).
type originSource interface {
	InboxOf(sessionID string) (inbox string, ok bool, err error)
	ResolveOriginBySession(sessionID string) (team.Origin, bool, error)
}

// NotifySelf delivers text to the live session sessionID from that session's own inbox (a notice about itself, so the self-notice
// sender, which alone skips the self_target refusal), to its own address by ref. A session that restarted since has another
// session id and another ref, so a notice for the old one cannot reach the new one. The watch module's notices to the watcher go this way.
func NotifySelf(ctx context.Context, c *core.Core, logf func(format string, args ...any), sessionID, text string) error {
	reg := c.Registry
	osvc, ok := reg.Get(OriginResolverKey)
	if !ok {
		return errors.New("the peers origin resolver is not available")
	}
	origins, ok := osvc.(originSource)
	if !ok {
		return errors.New("the peers origin resolver cannot name an inbox")
	}
	ssvc, ok := reg.Get(SelfNoticeSenderKey)
	if !ok {
		return errors.New("the peers self-notice sender is not available")
	}
	sender, ok := ssvc.(SelfNoticeSender)
	if !ok {
		return errors.New("the peers self-notice sender is not a sender")
	}
	inbox, found, err := origins.InboxOf(sessionID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("the watcher has no live inbox")
	}
	origin, found, err := origins.ResolveOriginBySession(sessionID)
	if err != nil {
		return err
	}
	if !found || origin.Ref == "" {
		return errors.New("the watcher has no live address")
	}
	c.CfgMu.RLock()
	alias := c.Cfg.PeerAlias()
	c.CfgMu.RUnlock()
	to := alias + "/" + origin.Ref
	resp, err := sender.SendSelfNotice(ctx, ipeers.SendRequest{To: to, Text: text, OriginInbox: inbox})
	if err != nil {
		return err
	}
	if resp.Result == ipeers.ResultDeliveryUncertain {
		logf("[watch] notice to %s: delivery uncertain; not resent", to)
	}
	return nil
}
