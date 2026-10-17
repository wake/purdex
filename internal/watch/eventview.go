package watch

import (
	"strings"
	"time"
)

// EventView is an event as the events API and the CLI show it: the record's fields, the command as one line, and the ack's status,
// note and who handled it.
type EventView struct {
	ID              string     `json:"id"`
	Host            string     `json:"host"`
	Kind            Kind       `json:"kind"`
	At              time.Time  `json:"at"`
	Label           Label      `json:"label,omitempty"`
	Command         string     `json:"command,omitempty"`
	Cwd             string     `json:"cwd,omitempty"`
	Session         string     `json:"session,omitempty"`
	PeakCores       float64    `json:"peak_cores,omitempty"`
	PeakMB          uint64     `json:"peak_mb,omitempty"`
	DurationS       float64    `json:"duration_s,omitempty"`
	New             bool       `json:"new,omitempty"`
	Pressure        int        `json:"pressure,omitempty"`
	PageRate        float64    `json:"page_rate,omitempty"`
	Detail          string     `json:"detail,omitempty"`
	Slot            string     `json:"slot,omitempty"`
	HolderPID       int        `json:"holder_pid,omitempty"`
	LockPIDs        []int      `json:"lock_pids,omitempty"`
	Status          string     `json:"status"` // pending | handled
	Note            string     `json:"note,omitempty"`
	HandledAt       *time.Time `json:"handled_at,omitempty"`
	HandledBy       string     `json:"handled_by,omitempty"`
	HandledFromHost string     `json:"handled_from_host,omitempty"`
}

// Event statuses.
const (
	StatusPending = "pending"
	StatusHandled = "handled"
)

// ViewOf is the record r with its ack (nil: pending).
func ViewOf(r Record, a *Ack) EventView {
	v := EventView{ID: r.ID, Host: r.Host, Kind: r.Kind, At: r.At, Label: r.Label, Command: strings.Join(r.Argv, " "), Cwd: r.Cwd,
		Session: r.Session, PeakCores: r.PeakCores, PeakMB: r.PeakMB, DurationS: r.DurationS, New: r.New, Pressure: r.Pressure,
		PageRate: r.PageRate, Detail: r.Detail, Slot: r.Slot, HolderPID: r.HolderPID, LockPIDs: r.Holders, Status: StatusPending}
	if a != nil {
		at := a.At
		v.Status, v.Note, v.HandledAt, v.HandledBy, v.HandledFromHost = StatusHandled, a.Note, &at, a.By, a.FromHost
	}
	return v
}

// CountUnhandled is how many of recs have no ack.
func CountUnhandled(recs []Record, acks map[string]Ack) int {
	n := 0
	for _, r := range recs {
		if _, ok := acks[r.ID]; !ok {
			n++
		}
	}
	return n
}

// LoggedRecord is a record as `pdx watch log --json` prints it: the record's own fields, flat, with handled set and the ack's note,
// time and who.
type LoggedRecord struct {
	Record
	Note      string     `json:"note,omitempty"`
	HandledAt *time.Time `json:"handled_at,omitempty"`
	HandledBy string     `json:"handled_by,omitempty"`
}

// HandledRecord is r with its ack (nil: pending).
func HandledRecord(r Record, a *Ack) LoggedRecord {
	out := LoggedRecord{Record: r}
	if a != nil {
		at := a.At
		out.Handled, out.Note, out.HandledAt, out.HandledBy = true, a.Note, &at, a.By
	}
	return out
}
