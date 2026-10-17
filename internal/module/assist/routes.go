package assistmod

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/gate"
	ipeers "github.com/wake/purdex/internal/peers"
)

const maxBody = 16 << 10

var (
	aliasRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	sshRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`)
)

func (m *Module) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		m.logf("[assist] encode response: %v", err)
	}
}

func (m *Module) writeErr(w http.ResponseWriter, status int, code, detail string) {
	m.writeJSON(w, status, ipeers.APIError{Error: code, Detail: detail})
}

// requireAdmin: the admin token only; a device token is refused (the watch module's rule).
func (m *Module) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if _, isDevice := devices.PrincipalFrom(r.Context()); isDevice || !devices.IsAdmin(r.Context()) {
		m.writeErr(w, http.StatusForbidden, "forbidden", "only the admin token may change or read the assist hosts")
		return false
	}
	return true
}

func (m *Module) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	b, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(b) > maxBody {
		m.writeErr(w, http.StatusBadRequest, "bad_body", "body unreadable or too large")
		return false
	}
	if err := json.Unmarshal(b, v); err != nil {
		m.writeErr(w, http.StatusBadRequest, "bad_body", err.Error())
		return false
	}
	return true
}

// validLine is one line, at most max bytes, no control characters.
func validLine(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, c := range s {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func byOf(s string) string {
	if validLine(s, 64) {
		return s
	}
	return "person"
}

// writeEditErr maps the refusals of an edit of assist.json.
func (m *Module) writeEditErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gate.ErrAssistNewer):
		m.writeErr(w, http.StatusConflict, "assist_version", err.Error())
	case errors.Is(err, errBusy):
		m.writeErr(w, http.StatusConflict, "busy", "a switch of this host is running")
	case errors.Is(err, errNoHost):
		m.writeErr(w, http.StatusNotFound, "no_such_host", "no such assist host")
	default:
		m.writeErr(w, http.StatusInternalServerError, "assist_error", err.Error())
	}
}

func (m *Module) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !m.requireAdmin(w, r) {
		return
	}
	out := map[string]any{"ready": m.readyCopy(), "now": m.now()}
	f, err := gate.ReadAssist(m.dir)
	if err != nil {
		out["assist_error"] = err.Error()
	}
	out["file"] = f
	m.writeJSON(w, http.StatusOK, out)
}

type hostBody struct {
	Alias       string    `json:"alias"`
	SSH         *string   `json:"ssh"`
	Scripts     *string   `json:"scripts"`
	Slots       *int      `json:"slots"`
	Can         *[]string `json:"can"`
	Cannot      *[]string `json:"cannot"`
	ClearCan    bool      `json:"clear_can"`
	ClearCannot bool      `json:"clear_cannot"`
	LoadLimit   *float64  `json:"load_limit"`
	IdleMin     *int      `json:"idle_min"`
	HoldMin     *int      `json:"hold_min"`
}

func validPrefixes(l []string) bool {
	for _, s := range l {
		if !validLine(s, 200) {
			return false
		}
	}
	return true
}

// handleHostSet adds a host or changes the fields it is given; the others keep what they have. Any change takes a new Gen.
func (m *Module) handleHostSet(w http.ResponseWriter, r *http.Request) {
	if !m.requireAdmin(w, r) {
		return
	}
	var b hostBody
	if !m.decode(w, r, &b) {
		return
	}
	bad := func(detail string) { m.writeErr(w, http.StatusBadRequest, "bad_host", detail) }
	switch {
	case !aliasRE.MatchString(b.Alias):
		bad("alias must be lowercase letters, digits and dashes")
		return
	case b.SSH != nil && !sshRE.MatchString(*b.SSH):
		bad("ssh must be an ssh alias or host name")
		return
	case b.Scripts != nil && (!validLine(*b.Scripts, 400) || !strings.HasPrefix(*b.Scripts, "/")):
		bad("scripts must be an absolute directory")
		return
	case b.Slots != nil && *b.Slots < 0, b.IdleMin != nil && *b.IdleMin < 0, b.HoldMin != nil && *b.HoldMin < 0,
		b.LoadLimit != nil && *b.LoadLimit < 0:
		bad("numbers must not be negative")
		return
	case b.Can != nil && !validPrefixes(*b.Can), b.Cannot != nil && !validPrefixes(*b.Cannot):
		bad("each can / cannot entry is one line of command words")
		return
	}
	line, err := gate.EditAssist(m.dir, func(f *gate.AssistFile) (bool, string, error) {
		cur, existed := f.Hosts[b.Alias]
		next := cur
		if !existed {
			if b.Scripts == nil {
				return false, "", errors.New("a new host needs scripts")
			}
			next.SSH = b.Alias
		}
		if b.SSH != nil {
			next.SSH = *b.SSH
		}
		if b.Scripts != nil {
			next.Scripts = *b.Scripts
		}
		if b.Slots != nil {
			next.Slots = *b.Slots
		}
		if b.LoadLimit != nil {
			next.LoadLimit = *b.LoadLimit
		}
		if b.IdleMin != nil {
			next.IdleMin = *b.IdleMin
		}
		if b.HoldMin != nil {
			next.HoldMin = *b.HoldMin
		}
		if b.ClearCan {
			next.Can = nil
		}
		if b.Can != nil {
			next.Can = append([]string(nil), *b.Can...)
		}
		if b.ClearCannot {
			next.Cannot = nil
		}
		if b.Cannot != nil {
			next.Cannot = append([]string(nil), *b.Cannot...)
		}
		if existed && reflect.DeepEqual(cur, next) {
			return false, "unchanged", nil
		}
		next.Gen = f.TakeGen()
		f.Hosts[b.Alias] = next
		if existed {
			return true, "changed", nil
		}
		return true, "added", nil
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "a new host") {
			bad(err.Error())
			return
		}
		m.writeEditErr(w, err)
		return
	}
	m.kickPoll()
	m.writeJSON(w, http.StatusOK, map[string]string{"result": line})
}

func (m *Module) handleHostRemove(w http.ResponseWriter, r *http.Request) {
	if !m.requireAdmin(w, r) {
		return
	}
	alias := r.PathValue("alias")
	_, err := gate.EditAssist(m.dir, func(f *gate.AssistFile) (bool, string, error) {
		h, ok := f.Hosts[alias]
		switch {
		case !ok:
			return false, "", errNoHost
		case h.Op != nil:
			return false, "", errBusy
		case h.On:
			return false, "", errSwitchOn
		}
		delete(f.Hosts, alias)
		return true, "removed", nil
	})
	if errors.Is(err, errSwitchOn) {
		m.writeErr(w, http.StatusConflict, "switch_on", "turn the host off before removing it")
		return
	}
	if err != nil {
		m.writeEditErr(w, err)
		return
	}
	m.writeJSON(w, http.StatusOK, map[string]string{"result": "removed"})
}

var errSwitchOn = errors.New("switch_on")

type switchBody struct {
	Host   string `json:"host"`
	On     bool   `json:"on"`
	Reason string `json:"reason"`
	By     string `json:"by"`
}

func (m *Module) handleSwitch(w http.ResponseWriter, r *http.Request) {
	if !m.requireAdmin(w, r) {
		return
	}
	var b switchBody
	if !m.decode(w, r, &b) {
		return
	}
	if !validLine(b.Reason, 200) {
		m.writeErr(w, http.StatusBadRequest, "bad_reason", "a reason is required: one line, at most 200 bytes")
		return
	}
	detail, err := m.switchHost(b.Host, b.On, byOf(b.By), b.Reason, nil)
	var se *scriptError
	switch {
	case errors.As(err, &se):
		m.writeErr(w, http.StatusBadGateway, "switch_failed", se.detail)
	case err != nil:
		m.writeEditErr(w, err)
	default:
		f, _ := gate.ReadAssist(m.dir)
		m.writeJSON(w, http.StatusOK, map[string]any{"host": b.Host, "on": b.On, "detail": detail, "gen": f.Hosts[b.Host].Gen})
	}
}

// sortedHosts are the aliases of f in order.
func sortedHosts(f gate.AssistFile) []string {
	out := make([]string, 0, len(f.Hosts))
	for a := range f.Hosts {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
