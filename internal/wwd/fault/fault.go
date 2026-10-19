// Package fault holds the test hooks the C2 matrix needs (docs/wwd-c2-matrix.md §7): a way to make one host misbehave in one known way.
//
// Every hook is off unless the config file says [fault] enabled = true AND names that hook. Nothing else turns one on: no route, no
// flag, no environment variable. A nil *Hooks, a disabled one and an enabled one with nothing named are all the same thing: every
// method returns what its caller passed in, touching nothing. Each trigger, and the start, writes a warning to the log.
package fault

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// MaxDelayS bounds fault.snapshot_delay_s.
const MaxDelayS = 3600

// Config is the [fault] table of config.toml.
type Config struct {
	Enabled bool `toml:"enabled"`
	// LogUploadFail (H1): the origin refuses a log upload. A result upload is not touched.
	LogUploadFail bool `toml:"log_upload_fail"`
	// OwnerActiveFile (H2): while this file exists the host's owner counts as active: the facts' owner block says active (reason
	// "fault: owner_active_file"), so the headroom empties and the worker yields. On a host with no owner rule the block says away
	// ("fault: owner_active_file absent") while the file is absent, so that the yield can end (daemon/sampler.go).
	OwnerActiveFile string `toml:"owner_active_file"`
	// Zone (H3): the zone peers score this host's jobs by (facts.derived.zone_override). The real zone is not changed.
	Zone string `toml:"zone"`
	// SnapshotDelayS (H4): the worker waits this long before it fetches a snapshot.
	SnapshotDelayS int `toml:"snapshot_delay_s"`
}

// Validate rejects a table that cannot mean what its writer meant.
func (c Config) Validate() error {
	named := c.LogUploadFail || c.OwnerActiveFile != "" || c.Zone != "" || c.SnapshotDelayS != 0
	if !c.Enabled {
		if named {
			return fmt.Errorf("fault: a hook is named but enabled is not true; the hook would do nothing, remove it or set enabled")
		}
		return nil
	}
	if c.OwnerActiveFile != "" && !filepath.IsAbs(c.OwnerActiveFile) {
		return fmt.Errorf("fault.owner_active_file %q must be an absolute path", c.OwnerActiveFile)
	}
	if c.Zone != "" && !wire.Zone(c.Zone).Valid() {
		return fmt.Errorf("fault.zone %q is not a zone", c.Zone)
	}
	if c.SnapshotDelayS < 0 || c.SnapshotDelayS > MaxDelayS {
		return fmt.Errorf("fault.snapshot_delay_s is %d, it must be 0 (off) to %d", c.SnapshotDelayS, MaxDelayS)
	}
	return nil
}

// Hooks is the set of hooks one wwd runs with. Its methods are safe on a nil receiver.
type Hooks struct {
	cfg   Config
	warn  func(format string, args ...any)
	sleep func(ctx context.Context, d time.Duration) error // a variable so that a test need not wait
}

// New is the hooks of cfg, or nil when none is on. warn takes every warning (nil: dropped).
func New(cfg Config, warn func(format string, args ...any)) *Hooks {
	if !cfg.Enabled {
		return nil
	}
	if warn == nil {
		warn = func(string, ...any) {}
	}
	h := &Hooks{cfg: cfg, warn: warn, sleep: sleepCtx}
	if d := h.Declared(); d == nil {
		return nil // enabled with nothing named: nothing to do, nothing to declare
	}
	h.warn("wwd: FAULT HOOKS ARE ON (test hosts only): %+v", *h.Declared())
	return h
}

// Declared is what /v1/info says is on; nil when no hook is.
func (h *Hooks) Declared() *wire.InfoFault {
	if h == nil || !h.cfg.Enabled {
		return nil
	}
	d := &wire.InfoFault{LogUploadFail: h.cfg.LogUploadFail, OwnerActiveFile: h.cfg.OwnerActiveFile != "", Zone: h.cfg.Zone,
		SnapshotDelayS: h.cfg.SnapshotDelayS}
	if !d.LogUploadFail && !d.OwnerActiveFile && d.Zone == "" && d.SnapshotDelayS == 0 {
		return nil
	}
	return d
}

// LogUploadFail is H1: true when the log upload in hand must be refused.
func (h *Hooks) LogUploadFail() bool {
	if h == nil || !h.cfg.Enabled || !h.cfg.LogUploadFail {
		return false
	}
	h.warn("wwd: fault log_upload_fail: refusing a log upload")
	return true
}

// OwnerActive is H2: real is the owner rule's answer; the file makes it true, never false.
func (h *Hooks) OwnerActive(real bool) bool {
	if real || h == nil || !h.cfg.Enabled || h.cfg.OwnerActiveFile == "" {
		return real
	}
	if _, err := os.Stat(h.cfg.OwnerActiveFile); err != nil {
		return false
	}
	h.warn("wwd: fault owner_active_file: %s exists, the owner counts as active for the worker", h.cfg.OwnerActiveFile)
	return true
}

// OwnerBlock is H2 as the whole owner reading: nil unless the hook is named, else active while the file exists and away while it is
// confirmed absent (the reasons name the hook, which jobusage ignores). Unlike OwnerActive it replaces the real rule instead of adding to
// it, so the host's keyboard cannot reach a test (#3561). A file that cannot be told about (permission, I/O) gives nil: the real rule answers.
func (h *Hooks) OwnerBlock() *wire.OwnerState {
	if h == nil || !h.cfg.Enabled || h.cfg.OwnerActiveFile == "" {
		return nil
	}
	if _, err := os.Stat(h.cfg.OwnerActiveFile); err == nil {
		return &wire.OwnerState{Active: true, Reason: "fault: owner_active_file"}
	} else if os.IsNotExist(err) {
		return &wire.OwnerState{Active: false, Reason: "fault: owner_active_file absent"}
	}
	return nil
}

// OwnerFileAbsent is the other half of H2: true only when the hook is named and the file is confirmed not to be there (ENOENT). A file
// that cannot be told about (permission, I/O) is neither there nor absent: false, so that nothing is made up. A host with no owner rule has
// a null owner block, and a worker that yielded stays yielded on a null block, so the sampler fills in an "away" block on this
// (daemon/sampler.go).
func (h *Hooks) OwnerFileAbsent() bool {
	if h == nil || !h.cfg.Enabled || h.cfg.OwnerActiveFile == "" {
		return false
	}
	_, err := os.Stat(h.cfg.OwnerActiveFile)
	return err != nil && os.IsNotExist(err)
}

// ZoneOverride is H3: the zone peers score by instead of the real one, or "" when none.
func (h *Hooks) ZoneOverride() wire.Zone {
	if h == nil || !h.cfg.Enabled || h.cfg.Zone == "" {
		return ""
	}
	return wire.Zone(h.cfg.Zone) // the start warning stands for every sample: this runs once a second
}

// SnapshotDelay is H4: waits before a snapshot fetch; a cancelled ctx ends it with ctx's error.
func (h *Hooks) SnapshotDelay(ctx context.Context) error {
	if h == nil || !h.cfg.Enabled || h.cfg.SnapshotDelayS <= 0 {
		return nil
	}
	h.warn("wwd: fault snapshot_delay_s: waiting %d s before a snapshot fetch", h.cfg.SnapshotDelayS)
	return h.sleep(ctx, time.Duration(h.cfg.SnapshotDelayS)*time.Second)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
