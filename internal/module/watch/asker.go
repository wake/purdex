package watchmod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/watch"
)

// Peer routes the locator calls on other hosts. holder is served by this module (W2a-2); wake and start are the session part's
// (W3b): an older peer answers 404, which reads as "no watch support".
const (
	pathHolder = "/api/peers/watch/holder"
	pathWake   = "/api/peers/watch/wake"
	pathStart  = "/api/peers/watch/start"
)

// hostCaller is *peersmod.HostCaller as the module uses it (a test seam).
type hostCaller interface {
	Call(ctx context.Context, targetHostID, path string, body any) peersmod.CallResult
}

// SessionControl is what the session part (W3) gives this module so the host it runs on can wake or start the watcher itself; it
// answers the holder state after. Nil until W3: the host then cannot start or wake one.
type SessionControl interface {
	Wake(ctx context.Context) (watch.HolderAnswer, error)
	Start(ctx context.Context) (watch.HolderAnswer, error)
}

// asker answers the locator's questions: about this host from its own state, about another from the peer routes.
type asker struct{ m *Module }

func (m *Module) isSelf(alias string) bool {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	return strings.EqualFold(m.core.Cfg.PeerAlias(), alias) // the alias this host goes by, also when none is set (the host id's name)
}

func answerOf(v HolderView) watch.HolderAnswer {
	return watch.HolderAnswer{HostID: v.HostID, State: watch.HolderState(v.State), SessionID: v.SessionID, Targets: v.Targets}
}

func (a asker) Holder(ctx context.Context, alias string) (watch.HolderAnswer, error) {
	if a.m.isSelf(alias) {
		return answerOf(a.m.view()), nil
	}
	return a.call(ctx, alias, pathHolder)
}

func (a asker) Wake(ctx context.Context, alias string) (watch.HolderAnswer, error) {
	if a.m.isSelf(alias) {
		if a.m.session == nil {
			return watch.HolderAnswer{}, watch.ErrNoSession
		}
		return a.m.session.Wake(ctx)
	}
	return a.call(ctx, alias, pathWake)
}

func (a asker) Start(ctx context.Context, alias string) (watch.HolderAnswer, error) {
	if a.m.isSelf(alias) {
		if a.m.session == nil {
			return watch.HolderAnswer{}, watch.ErrNoSession
		}
		return a.m.session.Start(ctx)
	}
	return a.call(ctx, alias, pathStart)
}

// call POSTs the question to the paired host named alias and reads its holder view.
func (a asker) call(ctx context.Context, alias, path string) (watch.HolderAnswer, error) {
	if a.m.caller == nil {
		return watch.HolderAnswer{}, errors.New("no host caller")
	}
	entry, _, ok := a.m.peerEntry(alias) // the paired hosts, by alias without regard to case (as targets are validated)
	hostID := entry.HostID
	if !ok || hostID == "" {
		return watch.HolderAnswer{}, fmt.Errorf("no paired host %q", alias)
	}
	res := a.m.caller.Call(ctx, hostID, path, map[string]string{"to_host_id": hostID})
	switch res.Class {
	case peersmod.ClassDone:
		var v HolderView
		if err := json.Unmarshal(res.Body, &v); err != nil {
			return watch.HolderAnswer{}, fmt.Errorf("%s: unreadable answer", alias)
		}
		return answerOf(v), nil
	case peersmod.ClassUnsupported:
		if path == pathHolder {
			return watch.HolderAnswer{}, watch.ErrNoWatchSupport
		}
		return watch.HolderAnswer{}, watch.ErrNoSession // wake / start are not there on that host
	case peersmod.ClassRefused:
		if path != pathHolder {
			return watch.HolderAnswer{}, watch.ErrNoSession // the host said no (no consent, nothing to wake)
		}
	}
	return watch.HolderAnswer{}, fmt.Errorf("%s: %s %s", alias, res.Class, res.Detail)
}
