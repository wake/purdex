// Package gateheld reads, from the same host, whether the pdx gate's heavy slots are all held (#3447). During the migration to wwd
// (until WWD-6) a job the worker takes from the queue runs outside the gate's slots, so the worker has to count the gate itself, or it
// would run one more heavy job on a host whose gate is already full.
//
// Nothing here goes over the network: the gate's own slot records and locks are read from its directory (gate.HeldSlots), with the
// settings loaded the way the gate loads them (gate.Load of the config file).
package gateheld

import (
	"errors"
	"io/fs"
	"os"
	"sync"
	"time"

	"github.com/wake/purdex/internal/gate"
)

// Reader answers HeavyFull for one host's gate.
type Reader struct {
	path string
	logf func(format string, args ...any)

	mu      sync.Mutex
	loaded  bool
	cfg     gate.Config
	cfgErr  error
	mtime   time.Time
	size    int64
	missing bool   // the config file is not there (the gate's defaults apply)
	lastLog string // the problem last logged; "" when the gate was readable at the last look
}

// New returns a reader of the gate whose settings are in the config file at path (gate.DefaultConfigPath() on a host). logf may be nil.
func New(path string, logf func(format string, args ...any)) *Reader {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Reader{path: path, logf: logf}
}

// HeavyFull reports whether the gate's heavy slots are all held (or it has none to give), so that a worker takes no new heavy job.
//
//   - A host without a gate — no gate directory, or the gate turned off — is not affected: false.
//   - A gate that is there but cannot be read — the directory cannot be looked at, or the settings cannot be parsed — counts as full:
//     better one job fewer than one more on a host whose state is not known. That is logged once per change of the problem.
//   - Otherwise: the number of held heavy slots has reached the heavy slot count.
func (r *Reader) HeavyFull() bool {
	cfg, cfgErr := r.config()
	if _, err := os.Stat(cfg.Dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			r.clearLog()
			return false // no gate directory: this host has no gate
		}
		return r.unreadable("the gate directory " + cfg.Dir + " cannot be read: " + err.Error())
	}
	if cfgErr != nil {
		return r.unreadable(cfgErr.Error())
	}
	if !cfg.Enabled {
		r.clearLog()
		return false
	}
	st, err := os.Stat(cfg.Dir)
	if err == nil && !st.IsDir() {
		return r.unreadable("the gate directory " + cfg.Dir + " is not a directory")
	}
	heavy, _ := gate.HeldSlots(cfg)
	r.clearLog()
	return len(heavy) >= cfg.Slots.Size(gate.ClassHeavy)
}

// config loads the gate's settings, again only when the file changed (the config is looked at every sample).
func (r *Reader) config() (gate.Config, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, err := os.Stat(r.path)
	switch {
	case err != nil && r.loaded && r.missing:
		return r.cfg, r.cfgErr // still not there
	case err == nil && r.loaded && !r.missing && st.ModTime().Equal(r.mtime) && st.Size() == r.size:
		return r.cfg, r.cfgErr
	}
	r.cfg, r.cfgErr = gate.Load(r.path)
	r.loaded, r.missing = true, err != nil
	if err == nil {
		r.mtime, r.size = st.ModTime(), st.Size()
	}
	return r.cfg, r.cfgErr
}

func (r *Reader) unreadable(why string) bool {
	r.mu.Lock()
	again := r.lastLog == why
	r.lastLog = why
	r.mu.Unlock()
	if !again {
		r.logf("wwd: the gate cannot be read, its heavy slots count as full: %s", why)
	}
	return true
}

func (r *Reader) clearLog() {
	r.mu.Lock()
	r.lastLog = ""
	r.mu.Unlock()
}
