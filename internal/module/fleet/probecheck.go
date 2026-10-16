package fleetmod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	leaf "github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/middleware"
)

const (
	// probeCheckTimeout bounds one connection check (the Runner's own timeout and the route's context).
	probeCheckTimeout = 15 * time.Second
	// probeCheckBodyCap is the most the request body may be: it carries one alias.
	probeCheckBodyCap = 1 << 10
)

// probeCheck is the state of POST /api/fleet/probe-check (spec §6): one shot of the fleet Runner per alias at a time.
// Embedded in Measure; the seams are for tests (nil / zero = the real thing).
type probeCheck struct {
	probeRun     func(ctx context.Context, alias string) leaf.Result // nil = leaf.Runner (execstat tag fleet.ssh-run)
	probeValid   func(home, alias string) bool                       // nil = Session.Valid, else leaf.ValidAlias
	probeTimeout time.Duration                                       // zero = probeCheckTimeout

	pmu    sync.Mutex
	flight map[string]*probeCall
}

type probeCall struct {
	done    chan struct{}
	ans     probeAnswer
	waiters int
}

// probeReading is the figures of one probe. level is null: the one-shot probe does not read the memory pressure level.
type probeReading struct {
	CPU   float64 `json:"cpu"`
	Mem   float64 `json:"mem"`
	Level *int    `json:"level"`
	Disk  float64 `json:"disk"`
}

// probeAnswer is the body of the route. It never carries ssh's stderr.
type probeAnswer struct {
	SSH     string        `json:"ssh"`
	Measure string        `json:"measure"`
	Error   *string       `json:"error"`
	Reading *probeReading `json:"reading"`
}

func (p *probeCheck) probeWaiters(alias string) int {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	if c := p.flight[alias]; c != nil {
		return c.waiters
	}
	return 0
}

// mapProbe turns a Runner result into the answer: ok -> both ok; auth / host_key / alias_gone -> ssh failed; timeout /
// unreachable -> both unknown (the host was not reached); remote_exit / parse -> ssh worked, the measure failed.
func mapProbe(res leaf.Result) probeAnswer {
	a := probeAnswer{SSH: "unknown", Measure: "unknown"}
	if res.Status == leaf.StatusOK {
		a.SSH, a.Measure = "ok", "ok"
		a.Reading = &probeReading{CPU: res.Reading.CPU, Mem: res.Reading.Mem, Disk: res.Reading.Disk}
		return a
	}
	code := string(res.Code)
	a.Error = &code
	switch res.Code {
	case leaf.CodeAuth, leaf.CodeHostKey, leaf.CodeAliasGone:
		a.SSH = "failed"
	case leaf.CodeRemoteExit, leaf.CodeParse:
		a.SSH, a.Measure = "ok", "failed"
	}
	return a
}

func (m *Measure) handleProbeCheck(w http.ResponseWriter, r *http.Request) {
	// admin only: a phone is refused by the device allow-list, a peer by being no peer route; this is the third line.
	if p, ok := middleware.PrincipalFrom(r.Context()); ok && p.Kind != middleware.PrincipalAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req struct {
		SSHAlias string `json:"ssh_alias"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, probeCheckBodyCap))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.SSHAlias == "" {
		http.Error(w, "body must be {\"ssh_alias\": \"<alias>\"}", http.StatusBadRequest)
		return
	}
	ans, err := m.checkProbe(r.Context(), req.SSHAlias)
	if err != nil {
		http.Error(w, "the request ended before the check did", http.StatusRequestTimeout)
		return
	}
	writeJSON(w, http.StatusOK, ans)
}

// checkProbe runs (or joins) the one check of alias. The run outlives a caller that goes away: the next one joins it.
func (m *Measure) checkProbe(ctx context.Context, alias string) (probeAnswer, error) {
	valid := m.probeValid
	if valid == nil {
		if valid = m.d.Session.Valid; valid == nil {
			valid = leaf.ValidAlias
		}
	}
	if !valid(m.d.Home, alias) {
		return mapProbe(leaf.Result{Status: leaf.StatusError, Code: leaf.CodeAliasGone}), nil
	}
	m.pmu.Lock()
	if c := m.flight[alias]; c != nil {
		c.waiters++
		m.pmu.Unlock()
		select {
		case <-c.done:
			return c.ans, nil
		case <-ctx.Done():
			return probeAnswer{}, ctx.Err()
		}
	}
	if m.flight == nil {
		m.flight = map[string]*probeCall{}
	}
	c := &probeCall{done: make(chan struct{})}
	m.flight[alias] = c
	m.pmu.Unlock()

	c.ans = m.runProbe(alias)
	m.pmu.Lock()
	delete(m.flight, alias)
	m.pmu.Unlock()
	close(c.done)
	return c.ans, nil
}

func (m *Measure) runProbe(alias string) probeAnswer {
	timeout := m.probeTimeout
	if timeout <= 0 {
		timeout = probeCheckTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	run := m.probeRun
	if run == nil {
		run = leaf.Runner{LookPath: m.d.Session.LookPath, Timeout: timeout}.Run
	}
	res := run(ctx, alias)
	if res.Stderr != "" {
		m.d.Logf("[fleet] probe-check %s: %s: %s", alias, res.Code, res.Stderr) // the log only, never the answer
	}
	if res.Status != leaf.StatusOK && errors.Is(ctx.Err(), context.DeadlineExceeded) && res.Code != leaf.CodeTimeout {
		res = leaf.Result{Status: leaf.StatusOffline, Code: leaf.CodeTimeout}
	}
	return mapProbe(res)
}
