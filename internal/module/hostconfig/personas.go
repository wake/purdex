package hostconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/personas"
)

// KeyPersonas is the host_config row of the persona collection (name pool v2 spec §4). It has its own routes and is left out
// of GET /api/hostconfig: the pictures make it large, and the full read is frequent.
const KeyPersonas = "personas"

// personasBodyCap is the body cap of the personas route only (64 personas with a picture of up to 32 KB each); every
// other hostconfig route keeps bodyCap.
const personasBodyCap = 4 << 20

// personasEvent is the host event a change of the collection goes out as ({"revision": n}); the App reads the route again.
const personasEvent = "hostconfig.personas"

const (
	maxPersonaNameRunes = 24
	maxPersonaTextUnits = 50 // personality, method: Chinese runes / English words
	maxInterestUnits    = 10
	maxPersonaRecipe    = 2 * 1024
	maxPersonaShortText = 200
)

var personaCode = regexp.MustCompile(`^[a-z]{3,12}$`)

// personaError is a refused item: its index, the field, and why. The route answers it as 400 {error, index, field}.
type personaError struct {
	Index int
	Field string
	Msg   string
}

func (e *personaError) Error() string { return fmt.Sprintf("item %d: %s: %s", e.Index, e.Field, e.Msg) }

// textUnits counts a text as the limits do: runes for Chinese, words for English.
func runeCount(s string) int { return utf8.RuneCountInString(s) }
func wordCount(s string) int { return len(strings.Fields(s)) }
func printable(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return !unicode.IsPrint(r) }) < 0
}

// normalizePersonas checks a PUT body's items (spec §4.1) and returns them as they are stored: names trimmed, the picture
// and recipe sizes bounded. A retired persona keeps its code reserved, so uniqueness spans active and retired alike.
func normalizePersonas(raw json.RawMessage, stored []personas.Persona) ([]personas.Persona, error) {
	if err := rejectDuplicateKeys(raw); err != nil {
		return nil, err
	}
	var rows []personas.Persona
	if firstByte(raw) != '[' || json.Unmarshal(raw, &rows) != nil {
		return nil, errors.New("items must be an array of personas")
	}
	if len(rows) > personas.MaxPersonas {
		return nil, &personaError{Index: len(rows) - 1, Field: "items", Msg: fmt.Sprintf("at most %d personas, retired ones included", personas.MaxPersonas)}
	}
	seen := make(map[string]bool, len(rows))
	out := make([]personas.Persona, 0, len(rows))
	for i, p := range rows {
		bad := func(field, msg string) error { return &personaError{Index: i, Field: field, Msg: msg} }
		if !personaCode.MatchString(p.Code) {
			return nil, bad("code", "must be 3 to 12 lowercase letters a-z")
		}
		if seen[p.Code] {
			return nil, bad("code", "is already used by another persona (a retired one keeps its code)")
		}
		seen[p.Code] = true
		p.Name = strings.TrimSpace(p.Name)
		if n := runeCount(p.Name); n < 1 || n > maxPersonaNameRunes || !printable(p.Name) {
			return nil, bad("name", fmt.Sprintf("must be 1 to %d printable characters", maxPersonaNameRunes))
		}
		if p.Sex != "" && p.Sex != "male" && p.Sex != "female" {
			return nil, bad("sex", `must be "male", "female" or ""`)
		}
		for _, f := range []struct {
			field string
			b     personas.Bilingual
			limit int
		}{{"personality", p.Personality, maxPersonaTextUnits}, {"method", p.Method, maxPersonaTextUnits}, {"interests", p.Interests, maxInterestUnits}} {
			if runeCount(f.b.ZH) > f.limit || wordCount(f.b.EN) > f.limit {
				return nil, bad(f.field, fmt.Sprintf("at most %d characters (zh) and %d words (en)", f.limit, f.limit))
			}
		}
		for _, s := range []string{p.Nationality.Code, p.Nationality.ZH, p.Nationality.EN, p.Template.Name, p.Template.NameZH, p.Template.FieldZH, p.Template.FieldEN} {
			if runeCount(s) > maxPersonaShortText {
				return nil, bad("nationality", fmt.Sprintf("a text is over %d characters", maxPersonaShortText))
			}
		}
		if len(p.Avatar) > maxPersonaRecipe {
			return nil, bad("avatar", "the recipe is over 2 KB")
		}
		if len(p.Avatar) > 0 && !json.Valid(p.Avatar) {
			return nil, bad("avatar", "the recipe is not JSON")
		}
		if err := checkAvatarData(p.AvatarData); err != nil {
			return nil, bad("avatar_data", err.Error())
		}
		out = append(out, p)
	}
	// Deleting retires (spec L5): a stored code never leaves the collection, so no other persona can ever take it.
	for _, old := range stored {
		if !seen[old.Code] {
			return nil, &personaError{Index: -1, Field: "code", Msg: old.Code + " is stored and cannot be removed; retire it instead"}
		}
	}
	return out, nil
}

// personasOf reads a stored value as the set it holds; a never-written key is the empty set.
func personasOf(e Entry) (personas.Set, error) {
	if e.Value == nil {
		return personas.Set{Items: []personas.Persona{}}, nil
	}
	var s personas.Set
	if err := json.Unmarshal(e.Value, &s); err != nil {
		return personas.Set{Items: []personas.Persona{}}, fmt.Errorf("stored personas do not read: %w", err)
	}
	if s.Items == nil {
		s.Items = []personas.Persona{}
	}
	return s, nil
}

// personasAnswer is the collection as the GET and a 409 answer it: items, stamp, revision. A stored value that does not read
// is an error, never an empty set: the codes in it are reserved for good, and a client shown an empty list would save over
// them.
func personasAnswer(e Entry) (any, error) {
	s, err := personasOf(e)
	if err != nil {
		return nil, err
	}
	return struct {
		Items    []personas.Persona `json:"items"`
		Stamp    personas.Stamp     `json:"stamp"`
		Revision int64              `json:"revision"`
	}{s.Items, s.Stamp, e.Revision}, nil
}

func (m *Module) handleGetPersonas(w http.ResponseWriter, _ *http.Request) {
	e, err := m.store.Get(KeyPersonas)
	if err != nil {
		log.Printf("[hostconfig] get %s: %v", KeyPersonas, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	answer, err := personasAnswer(e)
	if err != nil {
		log.Printf("[hostconfig] get %s: %v", KeyPersonas, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

func writePersonaError(w http.ResponseWriter, e *personaError) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": e.Error(), "index": e.Index, "field": e.Field})
}

func (m *Module) handlePutPersonas(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, personasBodyCap+1))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if len(body) > personasBodyCap {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	if err := rejectDuplicateKeys(body); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var req struct {
		Items        json.RawMessage `json:"items"`
		BaseRevision *int64          `json:"baseRevision"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Items == nil || req.BaseRevision == nil || *req.BaseRevision < 0 {
		http.Error(w, "items and baseRevision (>= 0) are required", http.StatusBadRequest)
		return
	}
	var internalErr error // not the caller's fault: the stored value does not read, or the set does not marshal
	entry, stored, err := m.store.Put(KeyPersonas, *req.BaseRevision, func(cur Entry) (json.RawMessage, error) {
		have, err := personasOf(cur)
		if err != nil {
			internalErr = err // never overwrite what cannot be read: its codes are reserved
			return nil, err
		}
		items, err := normalizePersonas(req.Items, have.Items)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(personas.Set{Items: items, Stamp: personas.Stamp{At: m.personaClock(), Host: m.hostID()}})
		if err != nil {
			internalErr = err
			return nil, err
		}
		return value, nil
	})
	var pe *personaError
	var ve *ValidationError
	switch {
	case err != nil && internalErr == nil && errors.As(err, &pe):
		writePersonaError(w, pe)
		return
	case err != nil && internalErr == nil && errors.As(err, &ve):
		http.Error(w, ve.Error(), http.StatusBadRequest)
		return
	case err != nil:
		log.Printf("[hostconfig] put %s: %v", KeyPersonas, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	case !stored:
		answer, err := personasAnswer(entry)
		if err != nil {
			log.Printf("[hostconfig] put %s: %v", KeyPersonas, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusConflict, answer)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"revision": entry.Revision})
	m.personasChanged(entry.Revision)
}

func (m *Module) personaClock() int64 {
	if m.personaNow != nil {
		return m.personaNow()
	}
	return time.Now().UnixMilli()
}

// hostID is this daemon's host id, the stamp's host; "" without a core (a test build).
func (m *Module) hostID() string {
	if m.core == nil {
		return ""
	}
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	return m.core.Cfg.HostID
}

// personasChanged sends the event and tells the readers (the peers module rebuilds its pool). The readers run off the
// request: the PUT is already stored and answered, so one that blocks must not hold it, and one that panics must not take the
// daemon down or keep the others from hearing. A reader reads the set again when told, so the order of the calls does not
// matter.
func (m *Module) personasChanged(revision int64) {
	if m.core != nil && m.core.Events != nil {
		m.core.Events.BroadcastEvent(core.HostEvent{Type: personasEvent, Value: fmt.Sprintf(`{"revision":%d}`, revision)})
	}
	m.personaMu.Lock()
	fns := make([]func(), 0, len(m.personaSubs))
	for _, fn := range m.personaSubs {
		fns = append(fns, fn)
	}
	m.personaMu.Unlock()
	for _, fn := range fns {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[hostconfig] a personas reader panicked: %v", r)
				}
			}()
			fn()
		}()
	}
}

// Personas is the stored set (personas.Reader).
func (m *Module) Personas() (personas.Set, error) {
	e, err := m.store.Get(KeyPersonas)
	if err != nil {
		return personas.Set{}, err
	}
	return personasOf(e)
}

// OnPersonasChange registers fn for every stored change of the set (personas.Reader).
func (m *Module) OnPersonasChange(fn func()) func() {
	m.personaMu.Lock()
	defer m.personaMu.Unlock()
	if m.personaSubs == nil {
		m.personaSubs = map[int]func(){}
	}
	m.personaSeq++
	id := m.personaSeq
	m.personaSubs[id] = fn
	return func() {
		m.personaMu.Lock()
		delete(m.personaSubs, id)
		m.personaMu.Unlock()
	}
}
