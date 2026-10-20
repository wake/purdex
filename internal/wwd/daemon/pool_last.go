package daemon

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The last known info of the peers is kept in a small file in the data directory (peers-last.json), apart from wwd.db. A peer that is
// off counts by the proto it last gave (wire.PoolAtLeast), and without the file a restart of this wwd (every deploy) would turn every
// host that is off at that moment into one never seen, and shut the gate that waits for every host. The file only ever holds what a peer
// itself answered under its own alias: one that is missing, broken or unreadable means "never seen", which holds a gate shut and breaks
// nothing else.

// lastKnown is the file: alias → the last info that peer answered and when.
type lastKnown struct {
	Peers map[string]lastKnownPeer `json:"peers"`
}

type lastKnownPeer struct {
	Info wire.Info `json:"info"`
	At   time.Time `json:"at"`
}

// UseLastKnownFile starts the view of each peer from the file and keeps the file up to date from now on. Call it before the first poll.
func (p *PoolPoller) UseLastKnownFile(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastFile = path
	p.last = map[string]lastKnownPeer{}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	k, err := wire.Decode[lastKnown](b)
	if err != nil {
		return // a broken file is the same as no file
	}
	for i, a := range p.peers {
		e, ok := k.Peers[a.Alias]
		if !ok || e.Info.Alias != a.Alias || e.Info.Validate() != nil {
			continue
		}
		if e.Info.Labels == nil {
			e.Info.Labels = []string{}
		}
		if e.Info.Fingerprint == nil {
			e.Info.Fingerprint = map[string]string{}
		}
		p.state[i].Info = e.Info
		p.last[a.Alias] = e
	}
}

// rememberInfo files the info a peer just answered when it differs from the one last filed; the caller holds mu. A failed write is not an
// error of the poll: the next poll that finds a change tries again.
func (p *PoolPoller) rememberInfo(info wire.Info) {
	if p.lastFile == "" {
		return
	}
	enc, err := json.Marshal(info)
	if err != nil {
		return
	}
	if old, ok := p.last[info.Alias]; ok {
		if prev, err := json.Marshal(old.Info); err == nil && bytes.Equal(prev, enc) {
			return
		}
	}
	next := lastKnown{Peers: map[string]lastKnownPeer{}}
	for alias, e := range p.last {
		next.Peers[alias] = e
	}
	next.Peers[info.Alias] = lastKnownPeer{Info: info, At: p.now()}
	out, err := json.Marshal(next)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p.lastFile), ".peers-last-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(out)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(tmp.Name(), 0o600) != nil || os.Rename(tmp.Name(), p.lastFile) != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	p.last[info.Alias] = next.Peers[info.Alias]
}
