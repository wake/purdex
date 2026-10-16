package fleetmod

import (
	"errors"
	"fmt"
	"os"

	"github.com/wake/purdex/internal/config"
	peersmod "github.com/wake/purdex/internal/module/peers"
)

var (
	errNoConfig = errors.New("the fleet config reader is not available")
	errNoCaller = errors.New("no host caller")
	errNoPeer   = errors.New("the other daemon host is not paired")
)

type cfgPeer = config.PeerHost

// userConfigDir is ~/.config (the pdx state directory's parent, as the spec names ~/.config/pdx/fleet).
func userConfigDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return h + "/.config", nil
}

// callErr is a failed HostCaller attempt as an error (its class and the peer's code, never a token).
func callErr(res peersmod.CallResult) error {
	if res.Err != nil {
		return fmt.Errorf("%s: %w", res.Class, res.Err)
	}
	return fmt.Errorf("%s %s", res.Class, res.Code)
}

// peerEntry is the live peer entry of alias from one config snapshot.
func (m *Module) peerEntry(alias string) (config.PeerHost, bool) {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	if i := m.core.Cfg.Peers.FindPeerHostByAlias(alias); i >= 0 {
		return m.core.Cfg.Peers.Hosts[i], true
	}
	return config.PeerHost{}, false
}

// settingsSnapshot is the settings the engine last read.
func (e *Engine) settingsSnapshot() Settings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.set
}

// InOrder says whether alias is in the fleet order (settings refreshed by revision first).
func (e *Engine) InOrder(alias string) bool {
	e.refresh()
	e.mu.Lock()
	defer e.mu.Unlock()
	return indexOf(e.set.Order, alias) >= 0
}
