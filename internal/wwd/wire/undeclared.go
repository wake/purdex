package wire

import (
	"fmt"
	"sort"
	"time"
)

// ObservationsResponse is GET /v1/observations: the observations of one host that began at or after Since, running ones included
// (EndedAt null), oldest first.
type ObservationsResponse struct {
	Host         string        `json:"host"`
	Since        time.Time     `json:"since"`
	Observations []Observation `json:"observations"`
	// Truncated says the period held more than one answer carries: the newest are here, the oldest were left out.
	Truncated bool `json:"truncated"`
}

func (r ObservationsResponse) Validate() error {
	if r.Host == "" {
		return fmt.Errorf("observations: missing host")
	}
	for i, o := range r.Observations {
		if err := o.Validate(); err != nil {
			return fmt.Errorf("observations[%d]: %w", i, err)
		}
	}
	return nil
}

// UndeclaredGroup is every observation of one command signature in a period: what a person looks at to decide which commands need a
// manifest or a hook rule. LastSession and LastVia say whose the latest one was.
type UndeclaredGroup struct {
	Signature   string    `json:"signature"`
	Count       int       `json:"count"`
	CPUSeconds  float64   `json:"cpu_seconds"`  // the total over the group
	MaxMemMB    int       `json:"max_mem_mb"`   // the greatest peak in the group
	LastAt      time.Time `json:"last_at"`      // when the latest one began
	LastSession string    `json:"last_session"` // the latest one's session ref ("orphan" when none could be named)
	LastVia     string    `json:"last_via"`     // ObsViaSession or ObsViaCodex
	LastCommand string    `json:"last_command"` // the latest one's redacted command line
}

// UndeclaredResponse is GET /v1/observations/undeclared.
type UndeclaredResponse struct {
	Host   string            `json:"host"`
	Since  time.Time         `json:"since"`
	Groups []UndeclaredGroup `json:"groups"`
	// Truncated says there were more groups than one answer carries: the biggest by CPU are here.
	Truncated bool `json:"truncated"`
}

func (r UndeclaredResponse) Validate() error {
	if r.Host == "" {
		return fmt.Errorf("undeclared: missing host")
	}
	for i, g := range r.Groups {
		if g.Signature == "" || g.Count <= 0 {
			return fmt.Errorf("undeclared: groups[%d]: empty group", i)
		}
		if g.LastVia != ObsViaSession && g.LastVia != ObsViaCodex {
			return fmt.Errorf("undeclared: groups[%d]: invalid via %q", i, g.LastVia)
		}
	}
	return nil
}

// GroupUndeclared groups observations by command signature. The biggest total CPU comes first, then the greater memory peak, then
// the signature, so the same observations give the same list in any order. The result is never nil.
func GroupUndeclared(obs []Observation) []UndeclaredGroup {
	by := map[string]*UndeclaredGroup{}
	lastID := map[string]string{}
	for _, o := range obs {
		g := by[o.Signature]
		if g == nil {
			g = &UndeclaredGroup{Signature: o.Signature}
			by[o.Signature] = g
		}
		g.Count++
		g.CPUSeconds += o.CPUSeconds
		g.MaxMemMB = max(g.MaxMemMB, o.MemPeakMB)
		if g.Count == 1 || o.StartedAt.After(g.LastAt) || (o.StartedAt.Equal(g.LastAt) && o.ID > lastID[o.Signature]) {
			g.LastAt, g.LastSession, g.LastVia, g.LastCommand = o.StartedAt, o.Session, o.Via, o.Command
			lastID[o.Signature] = o.ID
		}
	}
	out := make([]UndeclaredGroup, 0, len(by))
	for _, g := range by {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.CPUSeconds != b.CPUSeconds {
			return a.CPUSeconds > b.CPUSeconds
		}
		if a.MaxMemMB != b.MaxMemMB {
			return a.MaxMemMB > b.MaxMemMB
		}
		return a.Signature < b.Signature
	})
	return out
}
