package wire

import (
	"fmt"
	"time"
	"unicode/utf8"
)

// How an observed tree came to be: started by a Claude Code session itself, or by codex (its broker and the processes it runs).
const (
	ObsViaSession = "session"
	ObsViaCodex   = "codex"
)

// MaxObservationCommand bounds the command line an observation keeps (already redacted).
const MaxObservationCommand = 200

// Observation is one process tree that belongs to no job and used more than a threshold (CPU seconds or memory) while it ran
// (spec section 8.4). It names whose it was and what it ran, so the list feeds manifest and hook rules; nothing is blocked or killed.
// The command is redacted (no tokens) and cut to MaxObservationCommand; Signature is the short form the undeclared list groups by.
type Observation struct {
	ID        string     `json:"id"` // o_<started unix ms>_<root pid>
	Host      string     `json:"host"`
	Session   string     `json:"session"` // the session's ref; "orphan" when none could be named
	Via       string     `json:"via"`     // ObsViaSession or ObsViaCodex
	Command   string     `json:"command"`
	Signature string     `json:"signature"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"` // null while the tree is still running
	// CPUSeconds is the CPU time the whole tree used (the sum over its processes of CPU share x time); CPUPeak the most it used at
	// once, in percent of one core; MemPeakMB the most memory it held at once; ProcsPeak the most processes it had.
	CPUSeconds float64 `json:"cpu_seconds"`
	CPUPeak    float64 `json:"cpu_peak"`
	MemPeakMB  int     `json:"mem_peak_mb"`
	ProcsPeak  int     `json:"procs_peak"`
}

// Validate checks the enumeration, the bounds and the order of the times.
func (o Observation) Validate() error {
	if o.ID == "" {
		return fmt.Errorf("observation: missing id")
	}
	if o.Via != ObsViaSession && o.Via != ObsViaCodex {
		return fmt.Errorf("observation: invalid via %q", o.Via)
	}
	if o.Signature == "" {
		return fmt.Errorf("observation: missing signature")
	}
	if utf8.RuneCountInString(o.Command) > MaxObservationCommand {
		return fmt.Errorf("observation: command is over %d characters", MaxObservationCommand)
	}
	if o.CPUSeconds < 0 || o.CPUPeak < 0 || o.MemPeakMB < 0 || o.ProcsPeak < 0 {
		return fmt.Errorf("observation: negative figure")
	}
	if o.EndedAt != nil && o.EndedAt.Before(o.StartedAt) {
		return fmt.Errorf("observation: ended before it began")
	}
	return nil
}
