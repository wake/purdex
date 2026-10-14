package workbook

import (
	"strings"
	"unicode"

	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/redact"
)

// Limits of a journal job's output, in runes (spec §9 Validation, plan T-1b1.3).
const (
	maxEventText      = 80   // request / comm / confirm text
	maxConfirmItems   = 10   // items of one confirm
	maxConfirmItem    = 30   // runes of one item
	maxDetail         = 4000 // runes of a confirm's detail
	fallbackDetail    = 1500 // runes of the reply kept when detail_start is not found
	maxActorName      = 40   // runes of one side of "A → B"
	teamNoticePrefix  = "[pdx team]"
	maxOpenConfirmsIn = 30 // open items sent to the model
)

// OpenItem is one still-open item of a unit's confirm entries. N is the number this job handed the model (0: not sent,
// only used to skip a re-asked question); Asked counts the unit's confirm entries that still have open items from 1 =
// the oldest; Idx is the item's 1-based place in its entry (what the model sees and what the user's numbers mean) and
// ItemIndex the 0-based one the journal package ticks by.
type OpenItem struct {
	N         int
	Asked     int
	Idx       int
	EntryID   string
	ItemIndex int
	Text      string
}

// RoadmapRow is one row of the project's roadmap as the job sent it.
type RoadmapRow struct {
	Feature, Stage, Desc, Status string
}

// JournalContext is what a journal job's output is checked against: the turn text the job was built from and the
// open items and roadmap rows it sent.
type JournalContext struct {
	UserPrompt    string // as sent (redacted)
	AssistantText string // the whole last reply, redacted (the input sent only its first 3000 runes)
	Open          []OpenItem
	Rows          []RoadmapRow
}

// JournalEvent is a validated event: ready to become a request / comm / confirm entry (J-1b2 gives it an id and a time).
type JournalEvent struct {
	Kind   journal.Kind
	By     journal.By // request only
	Actor  string     // "A → B"
	Text   string
	Items  []string // confirm only
	Detail string   // confirm only
}

// ConfirmTick says: item ItemIndex (0-based) of the confirm entry EntryID was answered.
type ConfirmTick struct {
	EntryID   string
	ItemIndex int
}

// RoadmapChange is a status the daemon may apply: the feature and stage are rows the job sent and the status is one of
// journal.Statuses.
type RoadmapChange struct{ Feature, Stage, Status string }

// JournalResult is a journal job's output after the model's words went through the daemon's rules.
type JournalResult struct {
	Events  []JournalEvent
	Ticks   []ConfirmTick
	Roadmap []RoadmapChange
	Dropped int // items refused by the parse or by these rules
}

// ValidateJournal applies the limits and the daemon rules to what the model said (spec §9, plan T-1b1.3 / T-1b1.4):
//   - a turn whose user_prompt starts with `[pdx team]` writes no events, whatever the model says;
//   - request / comm / confirm text is redacted and cut to 80 at the last punctuation; confirm items are redacted, at
//     most 10 of at most 30 runes, and an item whose normalised text equals an open item of the unit (or one written
//     a moment ago by this same answer) is skipped - a confirm left with no items is not written;
//   - detail is cut from the reply at detail_start, markdown emphasis ignored on both sides;
//   - unknown confirm_done numbers and roadmap changes to rows the job did not send, or to a status outside the five,
//     are ignored.
func ValidateJournal(out JournalOutput, c JournalContext) JournalResult {
	res := JournalResult{Dropped: out.Dropped}
	byN := map[int]OpenItem{}
	for _, o := range c.Open {
		if o.N > 0 {
			byN[o.N] = o
		}
	}
	ticked := map[int]bool{}
	for _, n := range out.ConfirmDone {
		o, ok := byN[n]
		if !ok || ticked[n] {
			continue
		}
		ticked[n] = true
		res.Ticks = append(res.Ticks, ConfirmTick{EntryID: o.EntryID, ItemIndex: o.ItemIndex})
	}

	// the questions still open after this answer: a ticked item is no longer a reason to skip the same question again
	seen := map[string]bool{}
	for _, o := range c.Open {
		if o.N > 0 && ticked[o.N] {
			continue
		}
		seen[normalizeConfirm(o.Text)] = true
	}

	if strings.HasPrefix(strings.TrimSpace(c.UserPrompt), teamNoticePrefix) {
		res.Dropped += len(out.Events)
	} else {
		for _, ev := range out.Events {
			if je, ok := cleanEvent(ev, c, seen); ok {
				res.Events = append(res.Events, je)
			} else {
				res.Dropped++
			}
		}
	}

	rows := map[[2]string]bool{}
	for _, r := range c.Rows {
		rows[[2]string{r.Feature, r.Stage}] = true
	}
	for _, rc := range out.Roadmap {
		f, s, st := strings.TrimSpace(rc.Feature), strings.TrimSpace(rc.Stage), strings.TrimSpace(rc.Status)
		if !rows[[2]string{f, s}] || !knownStatus(st) {
			res.Dropped++
			continue
		}
		res.Roadmap = append(res.Roadmap, RoadmapChange{Feature: f, Stage: s, Status: st})
	}
	return res
}

func knownStatus(s string) bool {
	for _, k := range journal.Statuses {
		if s == k {
			return true
		}
	}
	return false
}

// cleanEvent turns one parsed event into a JournalEvent; false when nothing is left of it. seen holds the normalised
// open questions and is extended with the items this event keeps.
func cleanEvent(ev ModelEvent, c JournalContext, seen map[string]bool) (JournalEvent, bool) {
	text := cutAtPunct(strings.TrimSpace(redact.String(ev.Text)), maxEventText)
	if text == "" {
		return JournalEvent{}, false
	}
	je := JournalEvent{Text: text, Actor: actorOf(ev.From, ev.To)}
	switch ev.Kind {
	case "comm":
		je.Kind = journal.KindComm
	case "request":
		je.Kind = journal.KindRequest
		je.By = journal.By(ev.By)
	case "confirm":
		je.Kind = journal.KindConfirm
		for _, it := range ev.Items {
			it = cutRunes(strings.TrimSpace(redact.String(it)), maxConfirmItem)
			key := normalizeConfirm(it)
			if key == "" || seen[key] {
				continue
			}
			if len(je.Items) >= maxConfirmItems {
				break
			}
			seen[key] = true
			je.Items = append(je.Items, it)
		}
		if len(je.Items) == 0 {
			return JournalEvent{}, false
		}
		je.Detail = cutDetail(c.AssistantText, ev.DetailStart)
	default:
		return JournalEvent{}, false
	}
	return je, true
}

func actorOf(from, to string) string {
	from = cutRunes(strings.TrimSpace(redact.String(from)), maxActorName)
	to = cutRunes(strings.TrimSpace(redact.String(to)), maxActorName)
	switch {
	case from != "" && to != "":
		return from + " → " + to
	case from != "":
		return from
	}
	return to
}

// cutAtPunct keeps s whole within n runes; otherwise it keeps up to the last punctuation within n (a sentence end stays,
// a comma-like mark goes), or the first n runes when there is none.
func cutAtPunct(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	for i := n - 1; i >= 0; i-- {
		switch {
		case isSentenceEndAt(r, i):
			return strings.TrimSpace(string(r[:i+1]))
		case isPunctAt(r, i):
			return strings.TrimSpace(string(r[:i]))
		}
	}
	return string(r[:n])
}

// normalizeConfirm is the comparison key of a confirm item: full-width characters folded to half-width, lower-cased,
// whitespace, punctuation and symbols dropped.
func normalizeConfirm(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == 0x3000:
			continue
		case r >= 0xFF01 && r <= 0xFF5E:
			r -= 0xFEE0
		}
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// cutDetail is the confirm's original text: the reply from the first place detail_start occurs to its end, capped at
// 4000 runes. The match ignores markdown emphasis (`*`, `_`, backticks and `#` at a line start) on both sides and
// is mapped back to the original text, taking the markup in front of the match along; not found (or nothing to look
// for) gives the last 1500 runes of the reply.
func cutDetail(reply, start string) string {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return ""
	}
	rs := []rune(reply)
	from := -1
	if hay, orig := stripMarkdown(rs); len(hay) > 0 {
		needle, _ := stripMarkdown([]rune(strings.TrimSpace(start)))
		if at := indexRunes(hay, needle); at >= 0 && len(needle) > 0 {
			from = 0
			if at > 0 {
				from = orig[at-1] + 1
			}
			from = headingStart(rs, from)
		}
	}
	if from < 0 {
		if len(rs) > fallbackDetail {
			from = len(rs) - fallbackDetail
		} else {
			from = 0
		}
	}
	return strings.TrimSpace(cutRunes(redact.String(string(rs[from:])), maxDetail))
}

// headingStart moves from back to the start of its line when only a heading's `#` marks and spaces stand in front of it.
func headingStart(rs []rune, from int) int {
	j := from
	for j > 0 && rs[j-1] == ' ' {
		j--
	}
	k := j
	for k > 0 && rs[k-1] == '#' {
		k--
	}
	if k < j && (k == 0 || rs[k-1] == '\n') {
		return k
	}
	return from
}

// stripMarkdown drops emphasis marks and a line's leading `#` run; orig[i] is the index in rs of kept rune i.
func stripMarkdown(rs []rune) (kept []rune, orig []int) {
	lineStart := true
	for i, r := range rs {
		switch {
		case r == '\n':
			lineStart = true
		case r == '#' && lineStart:
			continue
		case r == '*' || r == '_' || r == '`':
			continue
		default:
			lineStart = false
		}
		kept = append(kept, r)
		orig = append(orig, i)
	}
	return kept, orig
}

func indexRunes(hay, needle []rune) int {
	if len(needle) == 0 {
		return -1
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j, r := range needle {
			if hay[i+j] != r {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
