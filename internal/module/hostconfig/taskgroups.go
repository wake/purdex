package hostconfig

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	ipeers "github.com/wake/purdex/internal/peers"
)

// KeyTaskGroups is the host_config row of the sidebar's manual task groups (name pool v2 spec §7, U11): a solo session is put
// into a group by hand. Each host keeps its own groups, so there is nothing to sync (L9). A team is a task group by itself
// and is not stored here (L10).
const KeyTaskGroups = "task_groups"

const (
	maxTaskGroups     = 32
	maxTaskGroupRoots = 64
)

var (
	// taskGroupIDPattern is the id a group is given by its writer (the App), and taskGroupRootPattern a lineage root's
	// session id: neither is looked up, so only their shape is checked.
	taskGroupIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	taskGroupRootPattern = taskGroupIDPattern
)

// TaskGroup is one group. Roots are lineage root session ids (a relay keeps a session in its group, L10); a root is in at
// most one group. Color is optional, #rrggbb.
type TaskGroup struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Color     string   `json:"color,omitempty"`
	Roots     []string `json:"roots"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
}

// taskGroupIn is a row as it arrives. The times are pointers: a missing or null time must not read as the time 0.
type taskGroupIn struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Color     string   `json:"color"`
	Roots     []string `json:"roots"`
	CreatedAt *int64   `json:"created_at"`
	UpdatedAt *int64   `json:"updated_at"`
}

// taskGroupState is what the rows kept so far hold: the rules that span rows (an id once, a root in one group).
type taskGroupState struct {
	ids   map[string]bool
	owner map[string]string // root -> the id of the group that holds it
}

func newTaskGroupState() *taskGroupState {
	return &taskGroupState{ids: map[string]bool{}, owner: map[string]string{}}
}

// checkTaskGroup applies the PUT's rules to one row, in their order, and answers it as stored. A row takes its id and its
// roots only once the whole row passes. An empty id is generated (8 hex, avoiding the ids kept and reserved) when generate is
// set, else the row is refused: the lenient read must answer the same id every time.
func checkTaskGroup(in json.RawMessage, st *taskGroupState, reserved map[string]bool, generate bool) (TaskGroup, error) {
	// the same rule as the PUT body's (the shared handler checks every object in it): the last of two equal keys must not win
	if err := rejectDuplicateKeys(in); err != nil {
		return TaskGroup{}, fmt.Errorf("invalid task group: %v", err)
	}
	var r taskGroupIn
	if err := strictDecode(in, &r); err != nil {
		return TaskGroup{}, fmt.Errorf("invalid task group: %v", err)
	}
	id := r.ID
	if id != "" {
		if !taskGroupIDPattern.MatchString(id) {
			return TaskGroup{}, fmt.Errorf("invalid id %q", id)
		}
		if st.ids[id] {
			return TaskGroup{}, fmt.Errorf("duplicate id %q", id)
		}
	} else if !generate {
		return TaskGroup{}, errors.New("id is required")
	}
	name := strings.TrimSpace(r.Name)
	if err := ipeers.ValidateTitle(name); err != nil {
		return TaskGroup{}, fmt.Errorf("name: %v", err)
	}
	if r.Color != "" && !hexColorPattern.MatchString(r.Color) {
		return TaskGroup{}, fmt.Errorf("color must be #rrggbb, got %q", r.Color)
	}
	if r.CreatedAt == nil || r.UpdatedAt == nil || *r.CreatedAt < 0 || *r.UpdatedAt < 0 {
		return TaskGroup{}, errors.New("created_at and updated_at are required: integers, not negative")
	}
	if len(r.Roots) > maxTaskGroupRoots {
		return TaskGroup{}, fmt.Errorf("at most %d roots in a group", maxTaskGroupRoots)
	}
	roots := make([]string, 0, len(r.Roots))
	inGroup := make(map[string]bool, len(r.Roots))
	for _, root := range r.Roots {
		switch {
		case !taskGroupRootPattern.MatchString(root):
			return TaskGroup{}, fmt.Errorf("invalid root %q", root)
		case inGroup[root]:
			return TaskGroup{}, fmt.Errorf("root %q is twice in the group", root)
		case st.owner[root] != "":
			return TaskGroup{}, fmt.Errorf("root %q is already in group %q (a root is in at most one group)", root, st.owner[root])
		}
		inGroup[root] = true
		roots = append(roots, root)
	}
	for id == "" {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return TaskGroup{}, fmt.Errorf("generate id: %v", err)
		}
		if c := hex.EncodeToString(b[:]); !st.ids[c] && !reserved[c] {
			id = c
		}
	}
	st.ids[id] = true
	for _, root := range roots {
		st.owner[root] = id
	}
	return TaskGroup{ID: id, Name: name, Color: r.Color, Roots: roots, CreatedAt: *r.CreatedAt, UpdatedAt: *r.UpdatedAt}, nil
}

// normalizeTaskGroups validates a PUT body: an array of at most 32 groups, all passing. A group keeps the id it sends and gets
// a generated one when it sends none.
func normalizeTaskGroups(raw json.RawMessage, _ Entry) ([]TaskGroup, error) {
	var in []json.RawMessage
	if err := decodeArray(raw, &in); err != nil {
		return nil, err
	}
	if len(in) > maxTaskGroups {
		return nil, fmt.Errorf("at most %d task groups", maxTaskGroups)
	}
	reserved := map[string]bool{}
	for _, e := range in {
		var peek struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(e, &peek) == nil && peek.ID != "" {
			reserved[peek.ID] = true
		}
	}
	out := make([]TaskGroup, 0, len(in))
	st := newTaskGroupState()
	for i, e := range in {
		g, err := checkTaskGroup(e, st, reserved, true)
		if err != nil {
			return nil, fmt.Errorf("task group %d: %v", i, err)
		}
		out = append(out, g)
	}
	return out, nil
}

// readTaskGroups is normalizeTaskGroups' lenient twin: a group that fails (a second owner of a root included) is left out
// with its reason.
func readTaskGroups(raw json.RawMessage) readout {
	st := newTaskGroupState()
	return readRows(raw, "task group", "task groups", maxTaskGroups, func(e json.RawMessage) (TaskGroup, error) {
		return checkTaskGroup(e, st, nil, false)
	})
}
