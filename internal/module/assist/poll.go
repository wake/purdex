package assistmod

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/gate"
)

// pass is one round of the poller: the reconciler, then a check of every host that is asked on, has an Op, or whose last check
// did not say off (contract 7).
func (m *Module) pass() {
	m.passMu.Lock()
	defer m.passMu.Unlock()
	m.reconcile()
	f, err := gate.ReadAssist(m.dir)
	if err != nil {
		return
	}
	ready := m.readyCopy()
	for alias, h := range f.Hosts {
		r, ok := ready[alias]
		saysOff := ok && r.Gen == h.Gen && r.Err == "" && r.Answer.Switch == "off"
		if h.On || h.Op != nil || !saysOff {
			m.checkHost(alias, h)
		}
	}
	m.dropGone(f)
}

// checkHost runs assist-check --json for the generation h had when it was read and records the answer under that generation.
func (m *Module) checkHost(alias string, h gate.AssistHost) {
	out, err := m.runScript(h, "assist-check", []string{"--json"}, m.timeouts.check)
	rec := gate.AssistReady{Gen: h.Gen, At: m.now()}
	if err == nil {
		var a gate.AssistAnswer
		if jerr := json.Unmarshal(bytes.TrimSpace(out), &a); jerr != nil {
			err = fmt.Errorf("bad JSON: %v", jerr)
		} else {
			rec.Answer = a
		}
	}
	if err != nil {
		rec.Err = "check failed: " + oneLine(err.Error())
	}
	m.setReady(alias, rec)
}

// setReady records alias's check (for rec.Gen) in memory and in assist-ready.json.
func (m *Module) setReady(alias string, rec gate.AssistReady) {
	m.readyMu.Lock()
	defer m.readyMu.Unlock()
	m.ready[alias] = rec
	if err := gate.WriteReady(m.dir, m.ready); err != nil {
		m.logf("[assist] write assist-ready.json: %v", err)
	}
}

// dropGone forgets the checks of hosts no longer in the file.
func (m *Module) dropGone(f gate.AssistFile) {
	m.readyMu.Lock()
	defer m.readyMu.Unlock()
	changed := false
	for alias := range m.ready {
		if _, ok := f.Hosts[alias]; !ok {
			delete(m.ready, alias)
			changed = true
		}
	}
	if changed {
		_ = gate.WriteReady(m.dir, m.ready)
	}
}

func (m *Module) readyCopy() map[string]gate.AssistReady {
	m.readyMu.Lock()
	defer m.readyMu.Unlock()
	out := make(map[string]gate.AssistReady, len(m.ready))
	for k, v := range m.ready {
		out[k] = v
	}
	return out
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
