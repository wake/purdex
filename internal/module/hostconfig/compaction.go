package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
)

// KeyCompaction is the host_config row of the host's compaction defaults (compaction-control spec §5.1): whether Claude
// Code's context is compacted automatically, at what percent, and whether an idle compaction is allowed, for a solo
// session (`solo`) and for a team (`team`: a lead and its members). It is its OWN row, not a field of `relay`: an older
// App judges the whole `relay` row bad on an unknown field (#3970).
const KeyCompaction = "compaction"

// CompactionKey is the service-registry key of the module as a CompactionReader.
const CompactionKey = "hostconfig.compaction"

// compactionJSON is the row as the GET answers it for a never-written key (emptyFor): "development" for both sets.
const compactionJSON = `{"solo":{"auto":true,"threshold":70,"idle":true},"team":{"auto":true,"threshold":70,"idle":true}}`

// CompactionSet is one role's defaults.
type CompactionSet struct {
	Auto      bool `json:"auto"`
	Threshold int  `json:"threshold"`
	Idle      bool `json:"idle"`
}

// CompactionDefaults is the stored shape and the GET field `compaction.items`.
type CompactionDefaults struct {
	Solo CompactionSet `json:"solo"`
	Team CompactionSet `json:"team"`
}

// DefaultCompaction is what a never-written row means (today's behaviour).
var DefaultCompaction = CompactionDefaults{
	Solo: CompactionSet{Auto: true, Threshold: 70, Idle: true},
	Team: CompactionSet{Auto: true, Threshold: 70, Idle: true},
}

// CompactionReader is what the team module type-asserts on the registry value.
type CompactionReader interface {
	// Compaction reads the defaults: DefaultCompaction for a never-written row. A stored value that does not read is
	// an error and the caller falls back to the built-in defaults.
	Compaction() (CompactionDefaults, error)
}

// exactObject splits raw into its members; anything but an object holding exactly the wanted names is refused.
func exactObject(raw json.RawMessage, what string, names ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if firstByte(raw) != '{' || json.Unmarshal(raw, &fields) != nil {
		return nil, errors.New(what + " must be a JSON object")
	}
	for k := range fields {
		known := false
		for _, n := range names {
			known = known || k == n
		}
		if !known {
			return nil, errors.New("unknown " + what + " field " + k)
		}
	}
	for _, n := range names {
		if v, ok := fields[n]; !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, errors.New(what + "." + n + " is required")
		}
	}
	return fields, nil
}

func compactionSetOf(raw json.RawMessage, what string) (CompactionSet, error) {
	f, err := exactObject(raw, what, "auto", "threshold", "idle")
	if err != nil {
		return CompactionSet{}, err
	}
	var s CompactionSet
	if json.Unmarshal(f["auto"], &s.Auto) != nil {
		return s, errors.New(what + ".auto must be a boolean")
	}
	if json.Unmarshal(f["idle"], &s.Idle) != nil {
		return s, errors.New(what + ".idle must be a boolean")
	}
	// Atoi on the raw text: refuses 70.5, "70", 7e1 alike.
	n, err := strconv.Atoi(string(bytes.TrimSpace(f["threshold"])))
	if err != nil || n < 1 || n > 100 {
		return s, errors.New(what + ".threshold must be an integer 1-100")
	}
	s.Threshold = n
	return s, nil
}

// compactionOf reads exactly {"solo": set, "team": set}; a misspelt or missing field must not save as a default.
func compactionOf(raw json.RawMessage) (CompactionDefaults, error) {
	f, err := exactObject(raw, "compaction", "solo", "team")
	if err != nil {
		return CompactionDefaults{}, err
	}
	solo, err := compactionSetOf(f["solo"], "solo")
	if err != nil {
		return CompactionDefaults{}, err
	}
	team, err := compactionSetOf(f["team"], "team")
	if err != nil {
		return CompactionDefaults{}, err
	}
	return CompactionDefaults{Solo: solo, Team: team}, nil
}

// normalizeCompaction validates a PUT body.
func normalizeCompaction(raw json.RawMessage) (CompactionDefaults, error) { return compactionOf(raw) }

// readCompaction is normalizeCompaction's lenient twin (the GET's view): a value that does not read is invalid and
// answers the built-in defaults.
func readCompaction(raw json.RawMessage) readout {
	d, err := compactionOf(raw)
	if err != nil {
		return readout{items: DefaultCompaction, invalid: err}
	}
	return readout{items: d}
}

// Compaction reads the stored defaults, DefaultCompaction for a never-written row.
func (m *Module) Compaction() (CompactionDefaults, error) {
	e, err := m.store.Get(KeyCompaction)
	if err != nil {
		return CompactionDefaults{}, err
	}
	if e.Value == nil {
		return DefaultCompaction, nil
	}
	return compactionOf(e.Value)
}

// handleGetCompaction answers the one row like its slot in GET /api/hostconfig: {items, revision, invalid}.
func (m *Module) handleGetCompaction(w http.ResponseWriter, _ *http.Request) {
	e, err := m.store.Get(KeyCompaction)
	if err != nil {
		log.Printf("[hostconfig] get %s: %v", KeyCompaction, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, collectionOf("get", KeyCompaction, e))
}
