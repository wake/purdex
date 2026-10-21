package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
)

// The two host_config rows of the watcher's wake settings (docs/specs/2026-10-21-watcher-wake-settings-spec.md section 4): the document on the
// source host, and the copy a host that is not the source keeps. Neither is in the generic /api/hostconfig routes: the watch module is the
// only writer, through WakeRowStore, so a write always goes through its validation and its compare-and-swap.
const (
	KeyWatchWake      = "watch_wake"
	KeyWatchWakeCache = "watch_wake_cache"
)

// WakeRowStoreKey is the service-registry key of the module as a WakeRowStore.
const WakeRowStoreKey = "hostconfig.watch-wake"

var (
	// ErrWakeKey: the key is not one of the two rows.
	ErrWakeKey = errors.New("hostconfig: not a watch wake row")
	// ErrConflict: the row is not at the revision the write was based on (a row that is not there is at revision 0).
	ErrConflict = errors.New("hostconfig: the row changed since it was read")
)

// WakeRowStore is the narrow door to the two rows. The value is a JSON object the caller has already validated; the store only keeps it.
type WakeRowStore interface {
	// Get reads a row: ok is false for a row that was never written (revision 0).
	Get(key string) (raw []byte, revision int64, ok bool, err error)
	// Put writes a row when it is at baseRevision (0 for a row that is not there); otherwise it changes nothing and returns ErrConflict.
	Put(key string, raw []byte, baseRevision int64) (revision int64, err error)
}

var _ WakeRowStore = (*Module)(nil)

func wakeKey(key string) error {
	if key != KeyWatchWake && key != KeyWatchWakeCache {
		return ErrWakeKey
	}
	return nil
}

// Get implements WakeRowStore.
func (m *Module) Get(key string) ([]byte, int64, bool, error) {
	if err := wakeKey(key); err != nil {
		return nil, 0, false, err
	}
	e, err := m.store.Get(key)
	if err != nil {
		return nil, 0, false, err
	}
	if e.Revision == 0 {
		return nil, 0, false, nil
	}
	return append([]byte(nil), e.Value...), e.Revision, true, nil
}

// Put implements WakeRowStore on the store's own compare-and-swap (Store.PutWith), so two writers that read the same revision cannot both win.
func (m *Module) Put(key string, raw []byte, baseRevision int64) (int64, error) {
	if err := wakeKey(key); err != nil {
		return 0, err
	}
	if t := bytes.TrimSpace(raw); len(t) == 0 || t[0] != '{' || !json.Valid(t) {
		return 0, errors.New("hostconfig: a watch wake row is a JSON object")
	}
	e, ok, err := m.store.PutWith(key, baseRevision, func(Entry) (json.RawMessage, error) { return json.RawMessage(raw), nil }, nil)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrConflict
	}
	return e.Revision, nil
}
