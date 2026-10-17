package workbook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/redact"
	"github.com/wake/purdex/internal/team"
)

const maxRoadmapRows = 40

// JournalFiles is where a unit's journal lives: the folder of its month files and the project's roadmap.md. Either may
// be empty or missing; the job then sees an empty list.
type JournalFiles struct {
	JournalDir string
	Roadmap    string
}

// JournalSource is what the engine needs to build a journal job and nothing more: J-1b2 implements it over the unit
// registry. A nil Deps.Journal means no journal job is queued at all.
type JournalSource interface {
	// UnitFiles resolves the files of the unit a turn entry belongs to; ok is false when there are none yet.
	UnitFiles(e Entry) (files JournalFiles, ok bool, err error)
	// SelfName is the session's virtual name (its pdx address name).
	SelfName(sessionID string) string
}

// journalInput is the prompt JSON of a journal job (prompt document §2).
type journalInput struct {
	UnitRole     string        `json:"unit_role"`
	PersonName   string        `json:"person_name"`
	SelfName     string        `json:"self_name"`
	Turn         turnPart      `json:"turn"`
	OpenConfirms []openConfirm `json:"open_confirms"`
	RoadmapRows  []roadmapRow  `json:"roadmap_rows"`
}

type openConfirm struct {
	N     int    `json:"n"`
	Asked int    `json:"asked"`
	Idx   int    `json:"idx"`
	Text  string `json:"text"`
}

type roadmapRow struct {
	Feature string `json:"feature"`
	Stage   string `json:"stage"`
	Desc    string `json:"desc"`
	Status  string `json:"status"`
}

// unitRoleOf maps a seat to the prompt's unit_role; ok is false for a seat that has no journal job (a member, a remote
// member, or a seat that was not recorded).
func unitRoleOf(seatRole string) (role string, ok bool) {
	switch seatRole {
	case team.SeatLead:
		return "leader", true
	case team.SeatNone:
		return "solo", true
	}
	return "", false
}

// JournalSeed is everything BuildJournalInput needs besides the turn: read at hand-out from the unit's files.
type JournalSeed struct {
	UnitRole   string
	PersonName string       // the person the journal is for (config [journal] person_name); empty: the default
	SelfName   string       // the session as the journal writes it (journal.DisplayName), not its raw virtual name
	Open       []OpenItem   // every open item of the unit, oldest first (N set on the newest 30 only)
	Rows       []RoadmapRow // at most 40, the features the unit touched first
}

// BuildJournalInput builds the prompt of a journal job and the context its answer is checked against (the turn text
// as sent, the whole reply for the detail cut, the open items and roadmap rows that carry the job's numbering). Every
// string goes through redact.String; the output is byte-stable for the same seed.
func BuildJournalInput(seed JournalSeed, turn convmodel.Turn) (prompt string, ctx JournalContext, err error) {
	part, reply := buildTurnPart(turn)
	// The journal job reads peers by their display names; the turn job built its own copy of the text and keeps the
	// addresses. The check context below carries the rewritten text, as sent.
	part.UserPrompt = journal.RewritePeerPrefixes(part.UserPrompt)
	person := strings.TrimSpace(seed.PersonName)
	if person == "" {
		person = journal.DefaultPersonName
	}
	in := journalInput{UnitRole: seed.UnitRole, PersonName: redact.String(person), SelfName: redact.String(seed.SelfName), Turn: part,
		OpenConfirms: []openConfirm{}, RoadmapRows: []roadmapRow{}}
	for _, o := range seed.Open {
		if o.N > 0 {
			in.OpenConfirms = append(in.OpenConfirms, openConfirm{N: o.N, Asked: o.Asked, Idx: o.Idx, Text: redact.String(o.Text)})
		}
	}
	for _, r := range seed.Rows {
		in.RoadmapRows = append(in.RoadmapRows, roadmapRow{Feature: redact.String(r.Feature), Stage: redact.String(r.Stage),
			Desc: redact.String(r.Desc), Status: r.Status})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(in); err != nil {
		return "", JournalContext{}, fmt.Errorf("build journal input: %w", err)
	}
	// the context carries the rows as sent (redacted), so a change the model copies back matches them
	rows := make([]RoadmapRow, 0, len(in.RoadmapRows))
	for _, r := range in.RoadmapRows {
		rows = append(rows, RoadmapRow{Feature: r.Feature, Stage: r.Stage, Desc: r.Desc, Status: r.Status})
	}
	// and the open items as sent (redacted text, the same map fields), so a re-asked question that was redacted in the
	// prompt still matches what the model copies back
	open := make([]OpenItem, len(seed.Open))
	for i, o := range seed.Open {
		o.Text = redact.String(o.Text)
		open[i] = o
	}
	return strings.TrimSuffix(buf.String(), "\n"),
		JournalContext{UserPrompt: part.UserPrompt, AssistantText: reply, Open: open, Rows: rows}, nil
}

// ReadOpenItems lists the still-open items of a unit's confirm entries, oldest first, from the month files in dir. Each
// confirm entry that still has an open item counts as one ask (Asked, from 1 = the oldest); Idx is the item's 1-based
// place in its entry. Only the newest maxOpenConfirmsIn items get a number (N, from 1 in the same order); the older
// ones are returned with N 0 so a re-asked question can still be recognised. A missing folder has none. A month file
// that cannot be read is logged by the caller's logf and gives no list at all: the numbering of a partial list would
// point a tick at the wrong question (plan T-1b1.2: unreadable file -> empty lists).
func ReadOpenItems(dir string, logf func(string, ...any)) []OpenItem {
	if dir == "" {
		return nil
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil
	}
	sort.Strings(files)
	var items []OpenItem
	asked := 0
	for _, f := range files {
		entries, err := journal.Parse(f)
		if err != nil {
			logf("[workbook] journal input: read %s: %v", filepath.Base(f), err)
			return nil
		}
		for _, e := range entries {
			if e.Kind != journal.KindConfirm {
				continue
			}
			counted := false
			for i, it := range e.Items {
				if it.Done || it.Manual { // a hand-set item is the person's, Haiku never sees it
					continue
				}
				if !counted {
					asked++
					counted = true
				}
				items = append(items, OpenItem{Asked: asked, Idx: i + 1, ItemIndex: i, EntryID: e.ID, Text: it.Text})
			}
		}
	}
	first := len(items) - maxOpenConfirmsIn
	if first < 0 {
		first = 0
	}
	for i := first; i < len(items); i++ {
		items[i].N = i - first + 1
	}
	return items
}

// ReadRoadmapRows reads the project's roadmap rows, at most 40, the features the unit touched first (a feature is
// touched when its name, or the name of one of its stages of three runes or more, occurs in hint - the turn text and
// the unit's recent entries). Features and stages keep the file's order within each group. An unreadable file gives no
// rows (logged); a missing one is not an error: the lead agent has not written it yet.
func ReadRoadmapRows(file, hint string, logf func(string, ...any)) []RoadmapRow {
	if file == "" {
		return nil
	}
	feats, err := journal.ParseRoadmap(file)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logf("[workbook] journal input: read the roadmap: %v", err)
		}
		return nil
	}
	touched := func(f journal.Feature) bool {
		if f.Name != "" && strings.Contains(hint, f.Name) {
			return true
		}
		for _, s := range f.Stages {
			if len([]rune(s.Name)) >= 3 && strings.Contains(hint, s.Name) {
				return true
			}
		}
		return false
	}
	var first, rest []RoadmapRow
	for _, f := range feats {
		dst := &rest
		if touched(f) {
			dst = &first
		}
		for _, s := range f.Stages {
			*dst = append(*dst, RoadmapRow{Feature: f.Name, Stage: s.Name, Desc: s.Desc, Status: s.Status})
		}
	}
	rows := append(first, rest...)
	if len(rows) > maxRoadmapRows {
		rows = rows[:maxRoadmapRows]
	}
	return rows
}

// recentEntryText is the text of the unit's newest journal entries (at most 30, across month files, newest file
// first), for the roadmap hint. A month file that cannot be read is skipped: the hint only orders rows.
func recentEntryText(dir string) string {
	if dir == "" {
		return ""
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	sort.Strings(files)
	var recent []journal.Entry
	for i := len(files) - 1; i >= 0 && len(recent) < maxHintEntries; i-- {
		entries, err := journal.Parse(files[i])
		if err != nil {
			continue
		}
		if need := maxHintEntries - len(recent); len(entries) > need {
			entries = entries[len(entries)-need:]
		}
		recent = append(recent, entries...)
	}
	var b strings.Builder
	for _, e := range recent {
		b.WriteString(e.Text)
		b.WriteByte(' ')
		b.WriteString(strings.Join(e.Refs, " "))
		b.WriteByte(' ')
	}
	return b.String()
}

const maxHintEntries = 30
