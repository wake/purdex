package gate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// Holds of the local wwd (spec §13.4, WWD-8 8b-3 PR-C). While a hold is in force on this host, a command that asks the gate for a slot
// waits — the gate's would-wait says so, with the reason hold:<name>, and a waiting gate does not start the command — and a hold with the
// release flag takes the light slots down to min(setting, 1). The gate is a short-lived process, so what it learned from the wwd on this host
// is kept in a small file in its own directory for holdTTL, and every process reads that file before it asks.
//
// The gate must never get stuck because of the wwd: the read is bounded by holdReadTimeout, and one that fails or is late means "no hold"
// (fail-open) — which is also what is kept for holdTTL, so a wwd that is down costs one failed dial per holdTTL, not one per command.
const (
	holdTTL         = 5 * time.Second
	holdReadTimeout = 300 * time.Millisecond
	holdCacheFile   = "wwd-holds.json"
)

// HoldFetch asks the local wwd for the exceptions in force (GET /v1/exceptions).
type HoldFetch func(ctx context.Context) ([]wire.Exception, error)

// WwdHoldFetch is the fetch of the wwd socket of this host.
func WwdHoldFetch(ctx context.Context) ([]wire.Exception, error) {
	list, _, err := client.New(client.DefaultSocket()).Exceptions(ctx, "")
	return list, err
}

// HoldInfo is one hold as the gate needs it.
type HoldInfo struct {
	Name    string `json:"name"`
	Light   bool   `json:"light,omitempty"`   // it holds light work too
	Release bool   `json:"release,omitempty"` // it has the release flag
}

// HoldView is the holds in force on this host.
type HoldView struct {
	Holds []HoldInfo `json:"holds"`
}

// Waits says whether a command of the class must wait, and for which hold: heavy work waits for any hold, light work for a hold that
// lists light.
func (v HoldView) Waits(c Class) (name string, wait bool) {
	for _, h := range v.Holds {
		if c == ClassHeavy || h.Light {
			return h.Name, true
		}
	}
	return "", false
}

// Release says whether a hold with the release flag is in force.
func (v HoldView) Release() bool {
	return slices.ContainsFunc(v.Holds, func(h HoldInfo) bool { return h.Release })
}

func holdViewOf(list []wire.Exception) HoldView {
	v := HoldView{}
	for _, e := range list {
		// Every exception counts: "a hold is an Exception" (spec §13.4), and a plain `pdx wwd hold` is the same on the wire as an ordinary
		// exception (no allow, no flags). The gate waits as the headroom is decided (spec §3): any exception takes the heavy room to 0, one
		// that lists light takes the light room too.
		v.Holds = append(v.Holds, HoldInfo{Name: e.Name, Light: slices.Contains(e.Holds, wire.HoldLight), Release: slices.Contains(e.Flags, wire.HoldFlagRelease)})
	}
	return v
}

type holdFile struct {
	At    int64      `json:"at"` // unix ms
	Holds []HoldInfo `json:"holds"`
}

// holdView is the holds now: this process's last reading, else the file the other gates share, else one read of the wwd. dir is the
// gate directory. Without a fetch (a gate that was not told to ask) there are no holds.
func (g *Gate) holdView(dir string) HoldView {
	if g.HoldFetch == nil || dir == "" {
		return HoldView{}
	}
	g.st.holdMu.Lock()
	defer g.st.holdMu.Unlock()
	now := g.Now()
	if !g.st.holdAt.IsZero() && now.Sub(g.st.holdAt) >= 0 && now.Sub(g.st.holdAt) < holdTTL {
		return g.st.holdMemo
	}
	path := filepath.Join(dir, holdCacheFile)
	if b, err := os.ReadFile(path); err == nil {
		var f holdFile
		if json.Unmarshal(b, &f) == nil {
			at := time.UnixMilli(f.At)
			if age := now.Sub(at); age >= 0 && age < holdTTL {
				g.st.holdMemo, g.st.holdAt = HoldView{Holds: f.Holds}, at
				return g.st.holdMemo
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), holdReadTimeout)
	defer cancel()
	v := HoldView{}
	if list, err := g.HoldFetch(ctx); err == nil && ctx.Err() == nil {
		v = holdViewOf(list)
	} // else: fail-open, and the "no hold" is kept for holdTTL like any other answer
	g.st.holdMemo, g.st.holdAt = v, now
	if b, err := json.Marshal(holdFile{At: now.UnixMilli(), Holds: v.Holds}); err == nil {
		tmp := path + "." + strconv.Itoa(os.Getpid()) + "." + strconv.FormatInt(now.UnixNano(), 36) + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil {
			if os.Rename(tmp, path) != nil {
				os.Remove(tmp)
			}
		}
	}
	return v
}

// effective is cfg as the holds make it: a release hold leaves one light slot at most. The stored settings are not changed.
func (g *Gate) effective(cfg Config) Config {
	if g.HoldFetch != nil && cfg.Enabled && g.holdView(cfg.Dir).Release() {
		cfg.Slots.Light = min(cfg.Slots.Light, 1)
	}
	return cfg
}
