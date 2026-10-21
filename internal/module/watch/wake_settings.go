package watchmod

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/watchwake"
)

// This file is the source side of the watcher's wake settings (docs/specs/2026-10-21-watcher-wake-settings-spec.md sections 4 and 5): the one
// document, kept in the host_config row `watch_wake` of the source host, read with a one-time migration from the old `watch` row and written
// by compare-and-swap. A host that is not the source does not follow it yet (WK-1b).

// WakeSettingsChangedEvent is the /ws/host-events type sent when the document has a new revision. The value carries no document: a client
// reads GET again.
const WakeSettingsChangedEvent = "watch.wake_settings.changed"

var (
	errNoWakeRows  = errors.New("the host config module does not offer the wake settings rows")
	errMigrateRead = errors.New("the old watch setting could not be read for the migration")
)

// wakeDoc is the document as stored, with what a reply needs to say about it.
type wakeDoc struct {
	settings json.RawMessage // canonical JSON; the stored bytes as they are when the document is of a newer version
	etag     string
	revision int64
	readonly bool // the stored document is of a version this build does not know
}

// wakeSourceAlias is the alias of the host that is the source, and whether this host is it. The source is [watch] wake_source (mlab by
// default); this host is the source when that is its own alias.
func (m *Module) wakeSourceAlias() (source string, isSource bool) {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	source = m.core.Cfg.WakeSource()
	return source, m.core.Cfg.Peers.Alias != "" && m.core.Cfg.Peers.Alias == source
}

func (m *Module) wakeHostID() string {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	return m.core.Cfg.HostID
}

func (m *Module) wakeRows() (hostconfig.WakeRowStore, error) {
	svc, ok := m.core.Registry.Get(hostconfig.WakeRowStoreKey)
	if !ok {
		return nil, errNoWakeRows
	}
	st, ok := svc.(hostconfig.WakeRowStore)
	if !ok {
		return nil, errNoWakeRows
	}
	return st, nil
}

// zone is the zone the fixed times are read in until someone sets it: this host's own, as an IANA name.
func (m *Module) zone() string {
	if m.localTZ != nil {
		return m.localTZ()
	}
	return localZoneName()
}

// localZoneName is this host's time zone as an IANA name: $TZ, then the /etc/localtime link, then what the runtime calls it; UTC when none of
// them is a name the document accepts.
func localZoneName() string {
	candidates := []string{os.Getenv("TZ")}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(target, "zoneinfo/"); i >= 0 {
			candidates = append(candidates, target[i+len("zoneinfo/"):])
		}
	}
	candidates = append(candidates, time.Now().Location().String())
	for _, c := range candidates {
		if watchwake.ValidZone(c) {
			return c
		}
	}
	return "UTC"
}

// readWakeSource reads the document of the source host: the stored one, or - when there is none yet - the one the old `watch` row makes, stored
// with a compare-and-swap against "no row" so that readers and the first PUT racing for it leave exactly one.
func (m *Module) readWakeSource() (wakeDoc, error) {
	rows, err := m.wakeRows()
	if err != nil {
		return wakeDoc{}, err
	}
	raw, rev, ok, err := rows.Get(hostconfig.KeyWatchWake)
	if err != nil {
		return wakeDoc{}, err
	}
	for tries := 0; !ok && tries < 3; tries++ {
		old, err := m.readSettings()
		if err != nil {
			return wakeDoc{}, fmt.Errorf("%w: %v", errMigrateRead, err)
		}
		body := watchwake.Marshal(watchwake.FromWatchRow(old, m.zone()))
		switch rev, err = rows.Put(hostconfig.KeyWatchWake, body, 0); {
		case err == nil:
			return m.decodeWake(body, rev), nil
		case !errors.Is(err, hostconfig.ErrConflict):
			return wakeDoc{}, err
		}
		if raw, rev, ok, err = rows.Get(hostconfig.KeyWatchWake); err != nil { // lost the race: the winner's document is the one
			return wakeDoc{}, err
		}
	}
	if !ok {
		return wakeDoc{}, errors.New("the wake settings row could not be created")
	}
	return m.decodeWake(raw, rev), nil
}

// decodeWake reads a stored document. A newer version is passed on as it is and marked read-only; a document that does not read (damage) is
// served as the defaults, so it can be replaced by a PUT.
func (m *Module) decodeWake(raw []byte, rev int64) wakeDoc {
	if v, ok := watchwake.PeekVersion(raw); ok && v > watchwake.Version {
		sum := sha256.Sum256(raw)
		return wakeDoc{settings: append(json.RawMessage(nil), raw...), etag: hex.EncodeToString(sum[:8]), revision: rev, readonly: true}
	}
	s, errs := watchwake.Normalize(raw)
	if len(errs) > 0 {
		m.logf("[watch] the stored wake settings do not read (%d problems, first: %s %s): serving the defaults", len(errs), errs[0].Path, errs[0].Code)
		s = watchwake.Default(m.zone())
	}
	return wakeDoc{settings: watchwake.Marshal(s), etag: watchwake.Etag(s), revision: rev}
}

// wakeChanged tells the clients of this host that the document has a new revision.
func (m *Module) wakeChanged(source string, d wakeDoc) {
	b, err := json.Marshal(struct {
		Revision int64  `json:"revision"`
		Etag     string `json:"etag"`
		Source   string `json:"source"`
	}{d.revision, d.etag, source})
	if err != nil {
		return
	}
	ev := core.HostEvent{Type: WakeSettingsChangedEvent, Value: string(b)}
	switch {
	case m.emit != nil:
		m.emit(ev)
	case m.core != nil && m.core.Events != nil:
		m.core.Events.BroadcastEvent(ev)
	}
}
