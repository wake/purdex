package journal

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Statuses are the values of the 狀態 column.
var Statuses = []string{"未開始", "進行中", "審查中", "完成", "暫停"}

var (
	// ErrUnparsable is returned when a feature's table is not a 階段 | 說明 | 狀態 table; the file is left untouched.
	ErrUnparsable = errors.New("journal: roadmap table not parsable")
	// ErrUnknownStatus is returned for a status outside Statuses; the file is left untouched.
	ErrUnknownStatus = errors.New("journal: unknown roadmap status")
	// ErrConcurrentEdit is returned when roadmap.md kept changing under SetStatus for three attempts; nothing was written.
	ErrConcurrentEdit = errors.New("journal: roadmap changed while being updated")
)

// Stage is one row of a feature's table.
type Stage struct {
	Name, Desc, Status string
}

// Feature is a `## <feature>` section with its table.
type Feature struct {
	Name   string
	Stages []Stage
}

type span struct{ start, end int }

// cells splits a table line into the byte spans between its pipes (a `\|` does not split). ok is false when the line
// is not a table row.
func cells(line string) (out []span, ok bool) {
	line = strings.TrimSuffix(line, "\r")
	first := strings.IndexFunc(line, func(r rune) bool { return r != ' ' && r != '\t' })
	if first < 0 || line[first] != '|' {
		return nil, false
	}
	start := first + 1
	for i := start; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '|':
			out = append(out, span{start, i})
			start = i + 1
		}
	}
	if strings.TrimSpace(line[start:]) != "" {
		out = append(out, span{start, len(line)})
	}
	return out, true
}

func isSeparator(line string, cs []span) bool {
	if len(cs) == 0 {
		return false
	}
	for _, c := range cs {
		t := strings.Trim(strings.TrimSpace(line[c.start:c.end]), ":")
		if t == "" || strings.Trim(t, "-") != "" {
			return false
		}
	}
	return true
}

type rowRef struct {
	line   int
	stage  Stage
	status span // byte range of the status cell inside the line
}

type featureRef struct {
	Feature
	rows []rowRef
}

// scanRoadmap reads every `## ` section's table. Any section whose table lacks the 階段 / 說明 / 狀態 header or its
// separator row makes the whole file unparsable.
func scanRoadmap(lines []string) ([]featureRef, error) {
	var out []featureRef
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "## ") {
			continue
		}
		f := featureRef{Feature: Feature{Name: strings.TrimSpace(lines[i][3:])}}
		j := i + 1
		for ; j < len(lines) && !strings.HasPrefix(lines[j], "## "); j++ {
			hc, isRow := cells(lines[j])
			if !isRow {
				continue
			}
			ci := map[string]int{}
			for k, c := range hc {
				ci[strings.TrimSpace(lines[j][c.start:c.end])] = k + 1
			}
			ia, id, is := ci["階段"]-1, ci["說明"]-1, ci["狀態"]-1
			if ia < 0 || id < 0 || is < 0 || j+1 >= len(lines) {
				return nil, fmt.Errorf("%w: feature %q line %d", ErrUnparsable, f.Name, j+1)
			}
			if sc, _ := cells(lines[j+1]); !isSeparator(lines[j+1], sc) {
				return nil, fmt.Errorf("%w: feature %q line %d has no separator row", ErrUnparsable, f.Name, j+1)
			}
			for j += 2; j < len(lines); j++ {
				rc, isRow := cells(lines[j])
				if !isRow {
					break
				}
				if len(rc) <= max(ia, id, is) {
					continue
				}
				get := func(k int) string { return strings.TrimSpace(lines[j][rc[k].start:rc[k].end]) }
				st := Stage{Name: get(ia), Desc: get(id), Status: get(is)}
				f.Stages = append(f.Stages, st)
				f.rows = append(f.rows, rowRef{line: j, stage: st, status: rc[is]})
			}
			break
		}
		out = append(out, f)
		i = j - 1
	}
	return out, nil
}

func readRoadmap(file string) ([]string, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(b), "\n"), nil
}

// ParseRoadmap reads the features of a roadmap file. A missing file is an error (the lead agent creates it).
func ParseRoadmap(file string) ([]Feature, error) {
	lines, err := readRoadmap(file)
	if err != nil {
		return nil, err
	}
	fs, err := scanRoadmap(lines)
	if err != nil {
		return nil, err
	}
	out := make([]Feature, len(fs))
	for i, f := range fs {
		out[i] = f.Feature
	}
	return out, nil
}

// SetStatus rewrites the 狀態 cell of one stage and nothing else (every other byte of the file stays). changed is
// false, with a nil error, for an unknown feature or stage or a status that is already set; an unknown status is
// ErrUnknownStatus and an unparsable table is ErrUnparsable, both leaving the file untouched.
//
// The lead agent edits roadmap.md directly, outside the in-process lock, so the new content is replaced only if the
// file still holds the bytes it was computed from; otherwise it starts over, three attempts at most, then
// ErrConcurrentEdit. Known limitation: a write landing between that last compare and the rename (microseconds) is
// still lost; closing it needs a lock protocol the lead agent also follows, which plain file edits cannot offer.
func SetStatus(file, feature, stage, status string) (changed bool, err error) {
	known := false
	for _, s := range Statuses {
		known = known || s == status
	}
	if !known {
		return false, fmt.Errorf("%w: %q", ErrUnknownStatus, status)
	}
	defer lockFile(file)()
	for attempt := 0; attempt < 3; attempt++ {
		changed, retry, err := setStatusOnce(file, feature, stage, status)
		if !retry {
			return changed, err
		}
	}
	return false, fmt.Errorf("%w: %s", ErrConcurrentEdit, file)
}

// beforeReplace is a test seam between computing the new content and the compare-and-replace.
var beforeReplace func()

// setStatusOnce makes one attempt; retry is true when the file changed between the read and the replace.
func setStatusOnce(file, feature, stage, status string) (changed, retry bool, err error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return false, false, err
	}
	lines := strings.Split(string(raw), "\n")
	fs, err := scanRoadmap(lines)
	if err != nil {
		return false, false, err
	}
	var hits []rowRef
	for _, f := range fs {
		if f.Name != feature {
			continue
		}
		for _, r := range f.rows {
			if r.stage.Name == stage {
				hits = append(hits, r)
			}
		}
	}
	switch {
	case len(hits) == 0:
		return false, false, nil
	case len(hits) > 1:
		return false, false, fmt.Errorf("%w: %d rows for %s / %s", ErrUnparsable, len(hits), feature, stage)
	}
	r := hits[0]
	if r.stage.Status == status {
		return false, false, nil
	}
	l := lines[r.line]
	old := l[r.status.start:r.status.end]
	trimmed := strings.TrimSpace(old)
	lead, trail := " ", " "
	if trimmed != "" {
		lead = old[:strings.Index(old, trimmed)]
		trail = old[strings.Index(old, trimmed)+len(trimmed):]
	}
	lines[r.line] = l[:r.status.start] + lead + status + trail + l[r.status.end:]
	if beforeReplace != nil {
		beforeReplace()
	}
	if now, rerr := os.ReadFile(file); rerr != nil {
		return false, false, rerr
	} else if !bytes.Equal(now, raw) {
		return false, true, nil
	}
	if err := atomicWrite(file, []byte(strings.Join(lines, "\n"))); err != nil {
		return false, false, err
	}
	return true, false, nil
}
