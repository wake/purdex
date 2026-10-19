package wire

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ---- outcome disagreements (spec §7): GET /v1/outcomes/disagreements ----

// The origin records how declared tasks ended on the hosts that hold their label (or are their reference host), and offers, to the host
// a disagreement concerns, the fact that its result differs from another host's for the same work. The owner of the label reads it and
// quarantines its own label: there is no route by which one host writes to another.

// Disagreement is one pair of different outcomes for the same snapshot, task, arguments and definition, of which Host is the side whose
// label is to be quarantined (the non-reference one; both sides when neither is the reference). OtherHost is the other side.
type Disagreement struct {
	ID        int64     `json:"id"` // the origin's own number: it only goes up, and the reader's cursor is the highest it has handled
	Label     string    `json:"label"`
	Host      string    `json:"host"`
	OtherHost string    `json:"other_host"`
	Tree      string    `json:"tree"`
	Task      string    `json:"task"`
	Jobs      [2]string `json:"jobs"` // the earlier job, then the later one (the one whose end made the pair)
	At        time.Time `json:"at"`   // when the later job ended, on the origin's clock
}

var reJobIDWire = regexp.MustCompile(`^j_[0-9A-Za-z_-]{1,64}$`)

// maxTreeBytes bounds the snapshot tree name in a disagreement (a git tree id is 40 or 64 hex digits).
const maxTreeBytes = 128

func validText(s string, max int) bool {
	if s == "" || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	return !strings.ContainsFunc(s, unicode.IsControl)
}

// Validate checks one disagreement as a reader must before it acts on it.
func (d Disagreement) Validate() error {
	switch {
	case d.ID <= 0:
		return fmt.Errorf("disagreement: id %d", d.ID)
	case !ValidTaskLabel(d.Label):
		return fmt.Errorf("disagreement: label %q is not task:<repo>/<task>", d.Label)
	case !ValidAlias(d.Host) || !ValidAlias(d.OtherHost) || d.Host == d.OtherHost:
		return fmt.Errorf("disagreement: hosts %q and %q", d.Host, d.OtherHost)
	case !validText(d.Tree, maxTreeBytes) || !validText(d.Task, 128):
		return fmt.Errorf("disagreement: tree or task")
	case !reJobIDWire.MatchString(d.Jobs[0]) || !reJobIDWire.MatchString(d.Jobs[1]) || d.Jobs[0] == d.Jobs[1]:
		return fmt.Errorf("disagreement: jobs %q", d.Jobs)
	case d.At.IsZero():
		return fmt.Errorf("disagreement: no time")
	}
	return nil
}

// MaxDisagreements is the most one GET returns, and the most a request may ask for.
const MaxDisagreements = 200

// DisagreementList is the body of GET /v1/outcomes/disagreements?subject=<alias>&after=<id>&limit=<n>: the disagreements that concern Host,
// oldest first, those after the cursor the reader gave. Cursor is the highest id returned (the one asked with when there is none).
type DisagreementList struct {
	Host   string         `json:"host"`
	Cursor int64          `json:"cursor"`
	Items  []Disagreement `json:"items"`
	Origin string         `json:"origin"` // the alias of the host that recorded them
	More   bool           `json:"more"`   // true when more were waiting than the limit let through: read again from Cursor
	Now    time.Time      `json:"now"`    // the origin's clock when it answered: the reader sets it against its own, so it can date the items by its own clock (the zero value from a build that did not say)
}

// Validate checks a list as a reader must before it acts on it.
func (l DisagreementList) Validate() error {
	if !ValidAlias(l.Host) || !ValidAlias(l.Origin) {
		return fmt.Errorf("disagreements: host %q or origin %q", l.Host, l.Origin)
	}
	if l.Cursor < 0 || len(l.Items) > MaxDisagreements {
		return fmt.Errorf("disagreements: cursor %d with %d items", l.Cursor, len(l.Items))
	}
	prev := int64(0)
	for i, d := range l.Items {
		if err := d.Validate(); err != nil {
			return fmt.Errorf("disagreements[%d]: %w", i, err)
		}
		if d.Host != l.Host {
			return fmt.Errorf("disagreements[%d]: concerns %q, not %q", i, d.Host, l.Host)
		}
		if d.ID <= prev {
			return fmt.Errorf("disagreements[%d]: id %d is not above %d", i, d.ID, prev)
		}
		prev = d.ID
	}
	if n := len(l.Items); n > 0 && l.Cursor != l.Items[n-1].ID {
		return fmt.Errorf("disagreements: cursor %d is not the last id %d", l.Cursor, l.Items[n-1].ID)
	}
	return nil
}
