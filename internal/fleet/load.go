package fleet

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/resources"
)

// Composite-load and memory-latch lines (spec §5).
const (
	diskScoreFrom   = 85.0 // disk counts toward the composite only from here
	pageRateCap     = 100.0
	memWarnLevel    = 2
	memDangerLevel  = 4
	memWarnRate     = 50.0 // pages/s: warning is entered at this rate
	memWarnLeave    = 40.0 // and left only under this one
	memLeaveHold    = 10 * time.Second
	maxQueueShown   = 20
	maxCommandRunes = 80
)

// Composite is the ring's length, 0–100: the largest of the CPU %, the memory %, the level (1 -> 30, 2 -> 75, 4 -> 100; 0,
// unknown, counts for nothing), the page rate (pages/s, capped at 100) and the disk % only when it is 85 or more. The ring's
// colour is the zone, not this score: a host at CPU 95 % without memory pressure shows a long neutral ring.
func Composite(f LocalFast, diskPct float64, diskOK bool) float64 {
	score := clamp100(f.CPU)
	score = max(score, clamp100(f.Mem))
	score = max(score, levelScore(f.Level))
	score = max(score, min(max(f.PageRate, 0), pageRateCap))
	if diskOK && diskPct >= diskScoreFrom {
		score = max(score, clamp100(diskPct))
	}
	return score
}

func clamp100(v float64) float64 { return min(max(v, 0), 100) }

func levelScore(level int) float64 {
	switch {
	case level >= memDangerLevel:
		return 100
	case level >= memWarnLevel:
		return 75
	case level == 1:
		return 30
	}
	return 0
}

// MemLatch is the memory part of a host's zone with hysteresis, so the ring does not flicker (spec §5). Warning is entered at
// level >= 2 or a page rate >= 50 and left after 10 s with level 1 (or unknown) and a rate under 40; danger is entered at
// level 4 and left after 10 s below 4. Entering is at once and may skip a level; leaving is one level at a time, each step
// with its own 10 s. The zero value starts from normal. Not safe for concurrent use.
type MemLatch struct {
	z     resources.Zone
	since time.Time // when the current leave condition began to hold; zero = it does not hold
}

// held reports whether cond has held for memLeaveHold up to now.
func (l *MemLatch) held(cond bool, now time.Time) bool {
	if !cond {
		l.since = time.Time{}
		return false
	}
	if l.since.IsZero() {
		l.since = now
	}
	return now.Sub(l.since) >= memLeaveHold
}

// Update takes the newest level (0 = unknown) and page rate (0 = none known: the zone then runs on the level alone).
func (l *MemLatch) Update(now time.Time, level int, rate float64) resources.Zone {
	cur := l.z
	if cur == resources.ZoneUnknown {
		cur = resources.ZoneNormal
	}
	next := cur
	switch {
	case level >= memDangerLevel:
		next = resources.ZoneDanger
		l.since = time.Time{}
	case cur == resources.ZoneDanger:
		if l.held(true, now) {
			next = resources.ZoneWarning
			l.since = time.Time{}
		}
	case level >= memWarnLevel || rate >= memWarnRate:
		next = resources.ZoneWarning
		l.since = time.Time{}
	case cur == resources.ZoneWarning:
		if l.held(level <= 1 && rate < memWarnLeave, now) {
			next = resources.ZoneNormal
			l.since = time.Time{}
		}
	default:
		next = resources.ZoneNormal
		l.since = time.Time{}
	}
	l.z = next
	return next
}

// Reset is for a host with no reading (offline): the zone is unknown and the latch starts again from normal.
func (l *MemLatch) Reset() resources.Zone {
	l.z, l.since = resources.ZoneUnknown, time.Time{}
	return l.z
}

// HostZone is a host's whole zone: the memory latch and F-1a's disk latch, the worse of the two. One per host; not safe for
// concurrent use.
type HostZone struct {
	Mem  MemLatch
	Disk DiskLatch
}

// Update takes one good reading and the disk figure (diskOK false: none).
func (h *HostZone) Update(now time.Time, f LocalFast, diskPct float64, diskOK bool) resources.Zone {
	return Combine(h.Mem.Update(now, f.Level, f.PageRate), h.Disk.Update(diskPct, diskOK))
}

// Reset is for a host that is offline or in error: unknown.
func (h *HostZone) Reset() resources.Zone {
	h.Disk.Update(0, false)
	return h.Mem.Reset()
}

// QueueWaiter is one command waiting in the gate.
type QueueWaiter struct {
	Class   string    `json:"class"`
	Place   int       `json:"place"`
	Command string    `json:"command"`
	Project string    `json:"project"`
	Session string    `json:"session"`
	Since   time.Time `json:"since"`
}

// QueueHolder is one command that holds a slot. It carries no token on purpose: the slot's Token is the secret of the
// command's PDX_GATE_HELD, which lets a command below the holder pass through the gate, and must never leave the host.
type QueueHolder struct {
	Class   string    `json:"class"`
	Slot    int       `json:"slot"`
	Command string    `json:"command"`
	Project string    `json:"project"`
	Session string    `json:"session"`
	Since   time.Time `json:"since"` // when the command started
	PID     int       `json:"pid"`   // the gate's pid: the process that holds the slot's lock (the 名額鎖 column)
}

// Queue is the heavy-command queue of a daemon host (spec §5).
type Queue struct {
	HeavyUsed       int           `json:"heavy_used"`
	HeavyTotal      int           `json:"heavy_total"`
	LightUsed       int           `json:"light_used"`
	LightTotal      int           `json:"light_total"`
	Holders         []QueueHolder `json:"holders"`
	Waiters         []QueueWaiter `json:"waiters"`
	LongestWaitS    int           `json:"longest_wait_s"`
	AdmissionHeld   bool          `json:"admission_held"`
	AdmissionReason string        `json:"admission_reason"`
	HeavyPaused     bool          `json:"heavy_paused"`
	LightPaused     bool          `json:"light_paused"`
}

// QueueSummary condenses a gate status. Slot totals and the admission rule come from the status' effective settings (an
// older gate without them leaves the totals 0 and admission open). A class whose slot setting is 0 is paused (the gate accepts
// 0 from W3c; no setting, or one that does not read, is not a pause).
//
// admissionKnown says the status carried an Admission object (the local gate always does; a remote `pdx gate status --json`
// from an older pdx does not, and an absent object must stay distinct from "not held", which a value struct cannot say). When
// it did, its Held and Reason are the answer. When it did not, held is worked out from the host's level and page rate against
// its own effective admission.* settings, with the gate's own rule (gate.AdmissionConfig.Admit), and the reason is empty.
//
// Holders are heavy then light, waiters first in line first, heavy then light, each at most maxQueueShown; the longest wait
// counts all of the waiters.
func QueueSummary(st gate.Status, admissionKnown bool, f LocalFast, now time.Time) Queue {
	set := make(map[string]string, len(st.Settings))
	for _, s := range st.Settings {
		set[s.Key] = s.Value
	}
	q := Queue{HeavyUsed: len(st.Heavy), LightUsed: len(st.Light), Holders: []QueueHolder{}, Waiters: []QueueWaiter{}}
	q.HeavyTotal, _ = strconv.Atoi(set["slots.heavy"])
	q.LightTotal, _ = strconv.Atoi(set["slots.light"])
	q.HeavyPaused, q.LightPaused = pausedSlots(set, "slots.heavy"), pausedSlots(set, "slots.light")
	for _, h := range append(append([]gate.Holder{}, st.Heavy...), st.Light...) {
		if len(q.Holders) >= maxQueueShown {
			break
		}
		q.Holders = append(q.Holders, QueueHolder{
			Class: string(h.Class), Slot: h.Slot, Command: shorten(strings.Join(h.Argv, " ")),
			Project: projectOf(h.Cwd), Session: h.Session, Since: h.Started, PID: h.PID,
		})
	}
	var longest time.Duration
	for _, w := range append(append([]gate.Waiter{}, st.QueueHeavy...), st.QueueLight...) {
		if !w.Since.IsZero() {
			longest = max(longest, now.Sub(w.Since))
		}
		if len(q.Waiters) < maxQueueShown {
			q.Waiters = append(q.Waiters, QueueWaiter{
				Class: string(w.Class), Place: w.Place, Command: shorten(strings.Join(w.Argv, " ")),
				Project: projectOf(w.Cwd), Session: w.Session, Since: w.Since,
			})
		}
	}
	q.LongestWaitS = int(longest.Seconds())
	if admissionKnown {
		q.AdmissionHeld, q.AdmissionReason = st.Admission.Held, st.Admission.Reason
	} else if cfg, ok := admissionOf(set); ok {
		q.AdmissionHeld = !cfg.Admit(gate.Pressure{Level: f.Level, PageRate: f.PageRate})
	}
	return q
}

// pausedSlots says the setting is there and reads as exactly 0.
func pausedSlots(set map[string]string, key string) bool {
	v, ok := set[key]
	if !ok {
		return false
	}
	n, err := strconv.Atoi(v)
	return err == nil && n == 0
}

func shorten(s string) string {
	if r := []rune(s); len(r) > maxCommandRunes {
		return string(r[:maxCommandRunes-1]) + "…"
	}
	return s
}

// projectOf is the last element of the waiter's directory ("" when it has none).
func projectOf(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

// admissionOf rebuilds the admission rule from the effective settings; ok is false when the status carries none of it.
func admissionOf(set map[string]string) (gate.AdmissionConfig, bool) {
	en, haveEn := set["admission.enabled"]
	lv, haveLv := set["admission.max_pressure_level"]
	rt, haveRt := set["admission.max_page_rate"]
	if !haveEn && !haveLv && !haveRt {
		return gate.AdmissionConfig{}, false
	}
	cfg := gate.AdmissionConfig{Enabled: !haveEn || en == "true", MaxPressureLevel: 1, PageRateAuto: true}
	if haveLv {
		if n, err := strconv.Atoi(lv); err == nil {
			cfg.MaxPressureLevel = n
		}
	}
	if haveRt && rt != "auto" {
		if v, err := strconv.ParseFloat(rt, 64); err == nil {
			cfg.MaxPageRate, cfg.PageRateAuto = v, false
		}
	}
	return cfg, true
}
