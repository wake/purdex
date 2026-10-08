package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// KeyUnattended is the host_config row of the unattended switch
// (unattended spec D-U23-1, D-U23-7): per host, on disk, not synced. It has
// no generic route — it is not in the GET's map, has no reader and no
// PUT /api/hostconfig/* — because its one writer, the team module's
// PUT /api/team/unattended, must sweep the open requests in the same
// critical section as the write (D-U23-3) and only the daemon may set
// since.
const KeyUnattended = "unattended"

// UnattendedKey is the registry key of the module as an UnattendedStore.
const UnattendedKey = "hostconfig.unattended"

// unattendedRetries bounds SetUnattended's CAS: a write that lost the race
// re-reads and tries again at most this many times, then errors.
const unattendedRetries = 3

// UnattendedStore is what the team module type-asserts on the registry value.
type UnattendedStore interface {
	// Unattended reads the switch: off for a never-written key, an error
	// for a stored value that does not decode as the state.
	Unattended() (team.UnattendedState, error)
	// SetUnattended turns the switch on or off as by, at now (unix ms).
	// The same value again writes nothing and answers changed=false.
	SetUnattended(on bool, by team.Client, now int64) (state team.UnattendedState, changed bool, err error)
}

// unattendedFields are the stored value's fields, every one written by
// every SetUnattended:
//
//	{"on":bool, "since":ms, "changed_at":ms, "changed_by":{"kind","label"[,"addr"]}}
//
// The off state of a host that never wrote the switch is the absent row,
// never a stored value, so a stored value always has changed_by.
var unattendedFields = []string{"on", "since", "changed_at", "changed_by"}

// decodeUnattended reads a stored value strictly. A never-written key (nil)
// is off. Anything else must be exactly the shape SetUnattended writes:
// every field present, not null and of its type, no other field, a
// changed_by with a kind and a label, a changed_at, and — when on — a since.
// Anything less is an error, never off with no error and never on: a bare
// {"on":true} would otherwise read as on with since 0 (auto-approving while
// the list is empty). The caller decides how to fail (the team module fails
// closed; SetUnattended overwrites).
func decodeUnattended(raw json.RawMessage) (team.UnattendedState, error) {
	if raw == nil {
		return team.UnattendedState{}, nil
	}
	if err := rejectDuplicateKeys(raw); err != nil {
		return team.UnattendedState{}, fmt.Errorf("stored unattended value: %w", err)
	}
	var fields map[string]json.RawMessage
	if firstByte(raw) != '{' || json.Unmarshal(raw, &fields) != nil {
		return team.UnattendedState{}, errors.New("stored unattended value is not a JSON object")
	}
	for _, k := range unattendedFields {
		if v, ok := fields[k]; !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return team.UnattendedState{}, fmt.Errorf("stored unattended value has no %s", k)
		}
	}
	var st team.UnattendedState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&st); err != nil {
		return team.UnattendedState{}, fmt.Errorf("stored unattended value: %w", err)
	}
	switch {
	case st.ChangedBy == nil || blank(st.ChangedBy.Kind) || blank(st.ChangedBy.Label):
		return team.UnattendedState{}, errors.New("stored unattended value: changed_by needs a kind and a label")
	case st.ChangedAt <= 0:
		return team.UnattendedState{}, errors.New("stored unattended value: changed_at must be a unix ms")
	case st.Since < 0:
		return team.UnattendedState{}, errors.New("stored unattended value: since is negative")
	case st.On && st.Since <= 0:
		return team.UnattendedState{}, errors.New("stored unattended value: on without a since")
	}
	return st, nil
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// Unattended implements UnattendedStore.
func (m *Module) Unattended() (team.UnattendedState, error) {
	e, err := m.store.Get(KeyUnattended)
	if err != nil {
		return team.UnattendedState{}, err
	}
	return decodeUnattended(e.Value)
}

// SetUnattended implements UnattendedStore. Off→on sets since and
// changed_at to now; on→off keeps since (the list of D-U23-6 outlives the
// switch-off) and moves changed_at; changed_by is by on every write. The
// write is a CAS on the row's revision: a lost race re-reads, recomputes and
// retries. A stored value nobody can read is overwritten, even with the
// value it seemed to hold: this is the person's explicit choice, and
// refusing it would leave the switch stuck until the row is edited by hand.
// A by without a kind or a label, or a now that is not a unix ms, is
// refused: the write would not read back.
func (m *Module) SetUnattended(on bool, by team.Client, now int64) (team.UnattendedState, bool, error) {
	if blank(by.Kind) || blank(by.Label) || now <= 0 {
		return team.UnattendedState{}, false, errors.New("set unattended: needs a client kind, a client label and a time")
	}
	e, err := m.store.Get(KeyUnattended)
	if err != nil {
		return team.UnattendedState{}, false, err
	}
	for retry := 0; ; retry++ {
		cur, bad := decodeUnattended(e.Value)
		if bad == nil && cur.On == on {
			return cur, false, nil
		}
		if bad != nil {
			log.Printf("[hostconfig] unattended: overwriting a stored value that does not read: %s", clipReason(bad.Error()))
			cur = team.UnattendedState{}
		}
		next := team.UnattendedState{On: on, Since: cur.Since, ChangedAt: now, ChangedBy: &by}
		if on {
			next.Since = now
		}
		value, err := json.Marshal(next)
		if err != nil {
			return team.UnattendedState{}, false, err
		}
		if m.beforeUnattendedPut != nil {
			m.beforeUnattendedPut() // test seam: another writer between the read and the CAS
		}
		stored, ok, err := m.store.Put(KeyUnattended, e.Revision, func() (json.RawMessage, error) { return value, nil })
		if err != nil {
			return team.UnattendedState{}, false, err
		}
		if ok {
			return next, true, nil
		}
		if retry == unattendedRetries {
			return team.UnattendedState{}, false, fmt.Errorf("set unattended: lost the revision race %d times", retry+1)
		}
		e = stored // Put answers the current entry on a lost race
	}
}
