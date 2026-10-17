package assistmod

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/gate"
)

func newOpID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// switchHost is contract 7: refuse while an Op is set; write On = asked, the Op and a new Gen; run the script; clear the Op (a
// failed on leaves On false). Off is therefore on disk before its script runs. guard, when set, is checked under the lock
// (the reconciler uses it so a late decision never overrides a newer one). The returned line is the script's detail.
func (m *Module) switchHost(alias string, on bool, by, reason string, guard func(gate.AssistHost) bool) (string, error) {
	mu := m.runner(alias)
	if !mu.TryLock() {
		return "", errBusy
	}
	defer mu.Unlock()
	kind := "off"
	if on {
		kind = "on"
	}
	var h gate.AssistHost
	var op gate.AssistOp
	var skipped, was bool
	_, err := gate.EditAssist(m.dir, func(f *gate.AssistFile) (bool, string, error) {
		cur, ok := f.Hosts[alias]
		if !ok {
			return false, "", errNoHost
		}
		if cur.Op != nil {
			return false, "", errBusy
		}
		if guard != nil && !guard(cur) {
			skipped = true
			return false, "", nil
		}
		was = cur.On
		op = gate.AssistOp{Kind: kind, ID: newOpID(), At: m.now()}
		cur.On, cur.Op, cur.Gen = on, &op, f.TakeGen()
		cur.By, cur.Reason, cur.At = by, reason, op.At
		f.Hosts[alias] = cur
		h = cur
		return true, "", nil
	})
	if err != nil || skipped {
		return "", err
	}
	detail, err := m.finishOp(alias, h, op)
	if now := on && err == nil; now != was { // the host's state changed: tell the watcher (it prepares or confirms)
		m.wakeWatcher("assist_changed", fmt.Sprintf("%s 輔測 %s→%s by %s：%s", alias, onOff(was), onOff(now), by, reason))
	}
	m.kickNotify()
	return detail, err
}

// finishOp runs the script of op and clears the Op under the lock, if it is still the one in the file. The caller holds the
// host's runner.
func (m *Module) finishOp(alias string, h gate.AssistHost, op gate.AssistOp) (string, error) {
	detail, serr := m.runSwitch(h, op.Kind)
	_, err := gate.EditAssist(m.dir, func(f *gate.AssistFile) (bool, string, error) {
		cur, ok := f.Hosts[alias]
		if !ok || cur.Op == nil || cur.Op.ID != op.ID {
			return false, "", nil
		}
		cur.Op = nil
		if op.Kind == "on" && serr != nil {
			cur.On = false
		}
		cur.Gen = f.TakeGen() // a check that began during the switch no longer counts
		f.Hosts[alias] = cur
		return true, "", nil
	})
	m.kickPoll()
	if serr != nil {
		if detail == "" {
			detail = serr.Error()
		}
		if err != nil {
			// the Op stays in the file and the reconciler will run the script again: say so
			return detail, fmt.Errorf("%s; and the Op could not be cleared: %w", detail, err)
		}
		return detail, &scriptError{detail}
	}
	return detail, err
}

type scriptError struct{ detail string }

func (e *scriptError) Error() string { return e.detail }

// reconcile runs at start and at every poll (contract 7): an Op nobody here is running is run again once, and a host asked off
// that the last check of its current generation says is still on or closing gets a new off.
func (m *Module) reconcile() {
	f, err := gate.ReadAssist(m.dir)
	if err != nil {
		m.logf("[assist] reconcile: %v", err)
		return
	}
	ready := m.readyCopy()
	for alias, h := range f.Hosts {
		switch {
		case h.Op != nil:
			m.resumeOp(alias, h.Op.ID)
		case !h.On:
			r, ok := ready[alias]
			if ok && r.Gen == h.Gen && r.Err == "" && (r.Answer.Switch == "on" || r.Answer.Switch == "closing") {
				_, err := m.switchHost(alias, false, "daemon", "the host still says "+r.Answer.Switch+" after being asked off",
					func(cur gate.AssistHost) bool { return !cur.On && cur.Gen == h.Gen })
				if err != nil && !errors.Is(err, errBusy) {
					m.logf("[assist] off again on %s: %v", alias, err)
				}
			}
		}
	}
}

// resumeOp runs the Op id of alias, left behind by a daemon that died, once. A host whose runner is taken is being switched here.
func (m *Module) resumeOp(alias, id string) {
	mu := m.runner(alias)
	if !mu.TryLock() {
		return
	}
	defer mu.Unlock()
	var h gate.AssistHost
	found := false
	if _, err := gate.EditAssist(m.dir, func(f *gate.AssistFile) (bool, string, error) {
		if cur, ok := f.Hosts[alias]; ok && cur.Op != nil && cur.Op.ID == id {
			h, found = cur, true
		}
		return false, "", nil
	}); err != nil || !found {
		return
	}
	if _, err := m.finishOp(alias, h, *h.Op); err != nil {
		m.logf("[assist] resumed %s %s: %v", h.Op.Kind, alias, err)
	}
}
