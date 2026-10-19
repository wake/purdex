// Package gateheld reads, from the same host, whether the pdx gate's heavy slots are all held (#3447). During the migration to wwd
// (until WWD-6) a job the worker takes from the queue runs outside the gate's slots, so the worker has to count the gate itself, or it
// would run one more heavy job on a host whose gate is already full.
//
// Nothing here goes over the network: the gate's own slot records and locks are read from its directory (gate.HeldSlots), with the
// settings loaded the way the gate loads them (gate.Load of the config file, on every look: a file of a few lines, every few seconds).
package gateheld

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/wake/purdex/internal/gate"
)

// heavyLock is the name of a heavy slot's lock file in the gate directory (gate's Pool.slotPath).
var heavyLock = regexp.MustCompile(`^heavy-[0-9]+\.lock$`)

// Reader answers HeavyFull for one host's gate.
type Reader struct {
	path string
	logf func(format string, args ...any)

	mu      sync.Mutex
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
//   - A gate that is there but cannot be read — the settings cannot be parsed, the directory cannot be looked at or listed, or a heavy
//     lock file in it cannot be opened — counts as full: better one job fewer than one more on a host whose state is not known. That is
//     logged once per change of the problem.
//   - Otherwise: the number of held heavy slots has reached the heavy slot count.
func (r *Reader) HeavyFull() bool {
	cfg, st := r.look()
	switch st {
	case gateAbsent:
		return false
	case gateUnreadable:
		return true
	}
	heavy, _ := gate.HeldSlots(cfg)
	r.clearLog()
	return len(heavy) >= cfg.Slots.Size(gate.ClassHeavy)
}

// state is what a look at the gate found.
type state int

const (
	gateAbsent     state = iota // this host has no gate (or it is turned off)
	gateReadable                // there is a gate and its directory can be read
	gateUnreadable              // there is a gate and it cannot be read (logged once per change)
)

// look finds the gate: its settings, and whether it is there and can be read. It does not look at what is held.
func (r *Reader) look() (gate.Config, state) {
	cfg, cfgErr := gate.Load(r.path)
	// A config file that cannot be parsed comes first: the directory the defaults then point at says nothing about a gate the file
	// named another directory for. A host that has such a file has pdx, and so a gate; one without a gate has no config file at all.
	if cfgErr != nil {
		return cfg, r.unreadable(cfgErr.Error())
	}
	st, err := os.Stat(cfg.Dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.clearLog()
		return cfg, gateAbsent // no gate directory: this host has no gate
	case err != nil:
		return cfg, r.unreadable("the gate directory " + cfg.Dir + " cannot be read: " + err.Error())
	case !cfg.Enabled:
		r.clearLog()
		return cfg, gateAbsent
	case !st.IsDir():
		return cfg, r.unreadable("the gate directory " + cfg.Dir + " is not a directory")
	}
	// gate.HeldSlots skips every slot file it cannot open and would report fewer held slots, or an empty gate: so what it would skip is
	// looked at first. A gate with nothing held is not the same as one that cannot be looked at.
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		return cfg, r.unreadable("the gate directory " + cfg.Dir + " cannot be listed: " + err.Error())
	}
	for _, e := range entries {
		if !heavyLock.MatchString(e.Name()) {
			continue
		}
		f, err := os.OpenFile(filepath.Join(cfg.Dir, e.Name()), os.O_RDWR, 0)
		if err != nil {
			return cfg, r.unreadable("the gate's lock file " + e.Name() + " cannot be opened: " + err.Error())
		}
		_ = f.Close()
	}
	return cfg, gateReadable
}

func (r *Reader) unreadable(why string) state {
	r.mu.Lock()
	again := r.lastLog == why
	r.lastLog = why
	r.mu.Unlock()
	if !again {
		r.logf("wwd: the gate cannot be read, its heavy slots count as full: %s", why)
	}
	return gateUnreadable
}

func (r *Reader) clearLog() {
	r.mu.Lock()
	r.lastLog = ""
	r.mu.Unlock()
}
