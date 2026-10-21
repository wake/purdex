package wire

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ---- holds (spec §13.4): the new fields of an Exception ----

// HoldAllow lists what a hold still admits: a job whose class or whose repo is listed. Both lists empty: none.
type HoldAllow struct {
	Classes []JobClass `json:"classes"`
	Repos   []string   `json:"repos"`
}

// HoldFlag marks a hold's purpose.
type HoldFlag string

const (
	// HoldFlagRelease is a release hold: the host's light slots drop to 1 while it lasts (§13.3).
	HoldFlagRelease HoldFlag = "release"
	// HoldFlagExpectPressure is for a deliberate stress test: it suspends only the PSI part of the "mserv service" push.
	HoldFlagExpectPressure HoldFlag = "expect_pressure"
)

func (f HoldFlag) Valid() bool { return f == HoldFlagRelease || f == HoldFlagExpectPressure }

var reHoldRepo = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func validateHoldFields(a *HoldAllow, flags []HoldFlag) error {
	if a != nil {
		for _, c := range a.Classes {
			if !c.Valid() {
				return fmt.Errorf("allow: invalid class %q", c)
			}
		}
		for _, r := range a.Repos {
			if !reHoldRepo.MatchString(r) {
				return fmt.Errorf("allow: %q is not a repo name", r)
			}
		}
	}
	seen := map[HoldFlag]bool{}
	for _, f := range flags {
		if !f.Valid() {
			return fmt.Errorf("invalid flag %q", f)
		}
		if seen[f] {
			return fmt.Errorf("flag %q repeated", f)
		}
		seen[f] = true
	}
	return nil
}

// ---- alerts (spec §13.3) ----

// AlertKind names the rule that raised an alert (the table in §13.3).
type AlertKind string

const (
	AlertDiskLow        AlertKind = "disk_low"
	AlertDiskFalling    AlertKind = "disk_falling"
	AlertPressureHeld   AlertKind = "pressure_held"
	AlertMservService   AlertKind = "mserv_service"
	AlertWorkerCrash    AlertKind = "worker_crash_loop"
	AlertSilence        AlertKind = "silence"
	AlertThrash         AlertKind = "thrash"
	AlertResidentGrowth AlertKind = "resident_growth"
	AlertOrphans        AlertKind = "orphans"
	// The watcher's three pressure rules (spec §13.3, WWD switch S3-2). They are written only where [advisor] watch_rules is on, after every
	// reader of the alert list knows these values: a reader that does not refuses the whole list.
	AlertPressureWatch  AlertKind = "pressure_watch"
	AlertPressureDanger AlertKind = "pressure_danger"
	AlertSwapGrowth     AlertKind = "swap_growth"
	// The queue's four rules (spec §13.3, WWD-9 S3-3b), level watch, written by the host the queue is on only where [advisor] queue_rules is
	// on. This build reads them (S3-3a). A reader from before S3-3a refuses the whole list that holds one, so the rules are switched on after
	// every reader has this build; from S3-3a on a reader skips what it does not know (ErrUnknownAlertKind). The four spellings are wire contract.
	AlertQueueStalled     AlertKind = "queue_stalled"     // a slot a queued job could take is free and the job has not been taken for 5 minutes
	AlertJobRequeued      AlertKind = "job_requeued"      // one job has been put back (attempts + lapses) 3 times
	AlertQueueBacklog     AlertKind = "queue_backlog"     // the oldest queued job has waited 20 minutes
	AlertQueueUnclaimable AlertKind = "queue_unclaimable" // a queued job asks for labels no host with its worker on has, for 10 minutes
)

// The two kinds of silence about a peer (key silence:<alias>) are told apart by their words, not by a field of the alert: GET /v1/alerts is
// read strictly, and one key keeps the alert one failure that clears when the peer answers. A reader that wants to know whether the
// host itself is gone asks SilenceHostDown.
const silenceHostDownText = "主機離線"

const silenceWwdDownText = "wwd 沒有回應（主機還在）"

// SilenceDetailWwdDown is the alert text for a peer whose wwd does not answer while its host does.
func SilenceDetailWwdDown(alias string) string {
	return alias + "：" + silenceWwdDownText
}

// The kinds of silence SilenceKind tells apart.
const (
	SilenceWwdDownKind  = "wwd_down"
	SilenceHostDownKind = "host_down"
	SilenceUnknownKind  = "unknown"
)

// SilenceKind says which silence a silence alert is, from its words (the alert has no field for it, see above): SilenceHostDownKind when
// the host itself is gone, SilenceWwdDownKind when only its wwd is, SilenceUnknownKind for any other text (a writer of another release).
// An alert that is not a silence has none: "". Wwd and the Purdex daemon share this so both name the two the same way.
func SilenceKind(a Alert) string {
	if a.Kind != AlertSilence {
		return ""
	}
	switch {
	case SilenceHostDown(a.Detail):
		return SilenceHostDownKind
	case strings.HasSuffix(a.Detail, silenceWwdDownText):
		return SilenceWwdDownKind
	}
	return SilenceUnknownKind
}

// SilenceDetailHostDown is the alert text for a peer whose host does not answer either.
func SilenceDetailHostDown(alias string) string { return alias + "：" + silenceHostDownText }

// SilenceHostDown says whether a silence alert's text is the host-down one.
func SilenceHostDown(detail string) bool { return strings.HasSuffix(detail, silenceHostDownText) }

func (k AlertKind) Valid() bool {
	switch k {
	case AlertDiskLow, AlertDiskFalling, AlertPressureHeld, AlertMservService, AlertWorkerCrash, AlertSilence,
		AlertThrash, AlertResidentGrowth, AlertOrphans, AlertPressureWatch, AlertPressureDanger, AlertSwapGrowth,
		AlertQueueStalled, AlertJobRequeued, AlertQueueBacklog, AlertQueueUnclaimable:
		return true
	}
	return false
}

// AlertLevel is how an alert is delivered: pushed to the person at once, gathered into the daily digest, or (watch) told only to the
// watcher: the Purdex daemon wakes it and neither pushes it nor puts it in the digest.
type AlertLevel string

const (
	AlertPush   AlertLevel = "push"
	AlertDigest AlertLevel = "digest"
	AlertWatch  AlertLevel = "watch"
)

func (l AlertLevel) Valid() bool { return l == AlertPush || l == AlertDigest || l == AlertWatch }

// MaxAlertKeyBytes bounds a key (it is a row key and appears in a push).
const MaxAlertKeyBytes = 200

// MaxAlertDetailBytes bounds an alert's detail, the one line a person reads in the push ("mlab 剩 11.2 GB"). A writer cuts a longer
// text with TruncateDetail; a reader refuses one over the limit.
const MaxAlertDetailBytes = 200

// TruncateDetail makes s fit a detail: control characters (a newline, a tab) are dropped, and the text is cut to at most
// MaxAlertDetailBytes bytes without splitting a character.
func TruncateDetail(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) <= MaxAlertDetailBytes {
		return s
	}
	cut := MaxAlertDetailBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// Alert is one row of a host's wwd.db `alerts` table. Key identifies it (the same key is one alert across time); Since is when it
// began, Last the latest time its rule still held, ClearedAt when it stopped (null: active).
type Alert struct {
	Key       string     `json:"key"`
	Kind      AlertKind  `json:"kind"`
	Level     AlertLevel `json:"level"`
	Since     time.Time  `json:"since"`
	Last      time.Time  `json:"last"`
	ClearedAt *time.Time `json:"cleared_at"`
	// Detail is the one human-readable line for the push; omitted when empty, at most MaxAlertDetailBytes, no control characters.
	Detail string `json:"detail,omitempty"`
}

// ErrUnknownAlertKind and ErrUnknownAlertLevel are what Validate wraps (errors.Is) for a kind or a level this build does not know: the one
// failure a reader of the alert list may take as "an alert of a later release" and skip, where any other is a broken list. Only the two
// alert-specific readers (client.Alerts, store.ActiveAlerts) do; every other payload is still refused whole.
var (
	ErrUnknownAlertKind  = errors.New("unknown alert kind")
	ErrUnknownAlertLevel = errors.New("unknown alert level")
)

func (a Alert) Validate() error {
	if a.Key == "" || len(a.Key) > MaxAlertKeyBytes {
		return fmt.Errorf("alert: the key must be 1 to %d bytes", MaxAlertKeyBytes)
	}
	if !a.Kind.Valid() {
		return fmt.Errorf("alert: invalid kind %q: %w", a.Kind, ErrUnknownAlertKind)
	}
	if !a.Level.Valid() {
		return fmt.Errorf("alert: invalid level %q: %w", a.Level, ErrUnknownAlertLevel)
	}
	if len(a.Detail) > MaxAlertDetailBytes || strings.ContainsFunc(a.Detail, unicode.IsControl) || !utf8.ValidString(a.Detail) {
		return fmt.Errorf("alert: the detail must be valid text without control characters, at most %d bytes", MaxAlertDetailBytes)
	}
	if a.Since.IsZero() || a.Last.IsZero() {
		return fmt.Errorf("alert: since and last are required")
	}
	if a.Last.Before(a.Since) {
		return fmt.Errorf("alert: last is before since")
	}
	if a.ClearedAt != nil && a.ClearedAt.Before(a.Last) {
		return fmt.Errorf("alert: cleared_at is before last, the latest time its rule still held")
	}
	return nil
}

// AlertList is GET /v1/alerts: the host's alerts that are active (and recently cleared ones the reader needs to see clear).
type AlertList struct {
	Host   string  `json:"host"`
	Alerts []Alert `json:"alerts"`
}

func (l AlertList) Validate() error {
	if l.Host == "" {
		return fmt.Errorf("alerts: missing host")
	}
	for i, a := range l.Alerts {
		if err := a.Validate(); err != nil {
			return fmt.Errorf("alerts[%d]: %w", i, err)
		}
	}
	return nil
}

// AlertEvent is the `alert` SSE event: only a hint that Host's list changed. The reader reads the list again.
type AlertEvent struct {
	Host string `json:"host"`
}

func (e AlertEvent) Validate() error {
	if e.Host == "" {
		return fmt.Errorf("alert event: missing host")
	}
	return nil
}
