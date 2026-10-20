package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The last known info of the peers is kept in a small file in the data directory (peers-last.json), apart from wwd.db. A peer that is
// off counts by the proto it last gave (wire.PoolAtLeast), and without the file a restart of this wwd (every deploy) would turn every
// host that is off at that moment into one never seen, and shut the gate that waits for every host. The file only ever holds what a peer
// itself answered under its own alias: one that is missing, broken, not plain and private, or too large means "never seen", which holds
// a gate shut and breaks nothing else.
//
// What the file is not: an authority. A peer that comes back is read again at once (the next poll) and replaces what the file said; the
// window of a host rolled back while it was away is the one wire.PoolAtLeast names. The data directory is this wwd's own (0700), so the
// file is trusted as far as wwd.db beside it is; a link or a file others can write is refused all the same.

// lastKnownMax bounds the file: a few peers' info is a few KiB.
const lastKnownMax = 1 << 20

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
	p.saveLast = writeFileAtomic
	b, ok := readLastKnown(path)
	if !ok {
		return
	}
	k, err := wire.DecodeLenient[lastKnown](b)
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

// readLastKnown reads the file when it is a plain file, not writable by group or others, and no larger than lastKnownMax.
func readLastKnown(path string) ([]byte, bool) {
	st, err := os.Lstat(path) // Lstat: a link is not a plain file
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o022 != 0 || st.Size() > lastKnownMax {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, lastKnownMax+1))
	if err != nil || len(b) > lastKnownMax {
		return nil, false
	}
	return b, true
}

// rememberInfo notes the info a peer just answered when it differs from the one last filed, or when the last write failed; the caller holds
// mu. It returns what writes the file, to be run after mu is released: a disk that is slow must not hold the pool. Nil: nothing to write.
func (p *PoolPoller) rememberInfo(info wire.Info) func() {
	if p.lastFile == "" {
		return nil
	}
	enc, err := json.Marshal(info)
	if err != nil {
		return nil
	}
	if old, ok := p.last[info.Alias]; ok && !p.lastRetry {
		if prev, err := json.Marshal(old.Info); err == nil && bytes.Equal(prev, enc) {
			return nil
		}
	}
	p.last[info.Alias] = lastKnownPeer{Info: info, At: p.now()}
	out, err := json.Marshal(lastKnown{Peers: p.last})
	if err != nil {
		return nil
	}
	p.lastRetry = false
	p.lastGen++
	gen, path, save := p.lastGen, p.lastFile, p.saveLast
	return func() {
		// one write at a time, and an older snapshot never replaces a newer one that was written first
		p.saveMu.Lock()
		defer p.saveMu.Unlock()
		if gen <= p.savedGen {
			return
		}
		if save(path, out) != nil {
			p.mu.Lock()
			p.lastRetry = true // the next poll writes again, though its info is the same
			p.mu.Unlock()
			return
		}
		p.savedGen = gen
	}
}

// writeFileAtomic writes b to a temporary file beside path and renames it over path (mode 0600).
func writeFileAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".peers-last-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		if werr != nil {
			return werr
		}
		return cerr
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}
