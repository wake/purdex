package ccnorm

import (
	"encoding/json"
	"strings"

	"github.com/wake/purdex/internal/convmodel"
)

// maxDiffLine bounds one diff line (the lines kept are capped in number by
// MaxDiffLines; this keeps their size bounded too). It cuts silently: the
// model has no per-line flag.
const maxDiffLine = convmodel.MaxInputString

// diffBuilder fills a Diff with at most MaxDiffLines hunk lines in total,
// counting every added and removed line of the whole change.
type diffBuilder struct {
	d    convmodel.Diff
	kept int
}

func newDiffBuilder(path string, exact bool) *diffBuilder {
	p, _ := capText(path, maxDiffLine)
	return &diffBuilder{d: convmodel.Diff{Path: p, Exact: exact}}
}

// room is how many more hunk lines may be kept.
func (b *diffBuilder) room() int { return convmodel.MaxDiffLines - b.kept }

// add records a hunk. total is the number of lines it has, kept the lines
// that fit (a prefix of them); added and removed are of the whole hunk.
func (b *diffBuilder) add(h convmodel.Hunk, total, added, removed int, kept []string) {
	b.d.Added += added
	b.d.Removed += removed
	if total > len(kept) {
		b.d.Truncated = true
	}
	if len(kept) == 0 {
		return
	}
	h.Lines = kept
	b.kept += len(kept)
	b.d.Hunks = append(b.d.Hunks, h)
}

// result is the finished diff; nil when it holds nothing.
func (b *diffBuilder) result() *convmodel.Diff {
	if b.d.Added == 0 && b.d.Removed == 0 && len(b.d.Hunks) == 0 {
		return nil
	}
	d := b.d
	return &d
}

// patchHunk is one entry of toolUseResult.structuredPatch as Claude Code
// writes it.
type patchHunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}

// patchDiff is the diff of a result that carries toolUseResult.structuredPatch
// (exact). nil when there is none, or it has no hunks (a Write that creates a
// file reports an empty patch).
func patchDiff(path string, tur object) *convmodel.Diff {
	var hunks []patchHunk
	if json.Unmarshal(tur.get("structuredPatch"), &hunks) != nil || len(hunks) == 0 {
		return nil
	}
	if path == "" {
		path = tur.str("filePath")
	}
	b := newDiffBuilder(path, true)
	for _, h := range hunks {
		var added, removed int
		for _, l := range h.Lines {
			switch {
			case strings.HasPrefix(l, "+"):
				added++
			case strings.HasPrefix(l, "-"):
				removed++
			}
		}
		keep := h.Lines[:min(len(h.Lines), max(b.room(), 0))]
		kept := make([]string, len(keep))
		for i, l := range keep {
			kept[i], _ = capText(l, maxDiffLine)
		}
		b.add(convmodel.Hunk{OldStart: h.OldStart, OldLines: h.OldLines, NewStart: h.NewStart, NewLines: h.NewLines},
			len(h.Lines), added, removed, kept)
	}
	return b.result()
}

// inputDiff builds the diff of an edit step from its tool input, for a step
// with no patch (running, denied, failed, older rows): Edit's
// old_string / new_string, Write's content, MultiEdit's edits (one hunk each,
// in order). It is not exact: the real line numbers and context are unknown.
func inputDiff(tool string, in object) *convmodel.Diff {
	path := in.str("file_path")
	if path == "" {
		path = in.str("notebook_path")
	}
	b := newDiffBuilder(path, false)
	switch tool {
	case "Edit":
		b.replace(in.str("old_string"), in.str("new_string"))
	case "Write":
		b.replace("", in.str("content"))
	case "MultiEdit":
		edits, _ := contentBlocks(in.get("edits"))
		for _, e := range edits {
			b.replace(e.obj.str("old_string"), e.obj.str("new_string"))
		}
	}
	return b.result()
}

// replace adds the hunk of one old → new replacement: the old lines as
// removals, then the new lines as additions.
func (b *diffBuilder) replace(oldS, newS string) {
	oldN, oldKept := scanLines(oldS, "-", b.room())
	newN, newKept := scanLines(newS, "+", b.room()-len(oldKept))
	if oldN+newN == 0 {
		return
	}
	h := convmodel.Hunk{OldLines: oldN, NewLines: newN}
	if oldN > 0 {
		h.OldStart = 1
	}
	if newN > 0 {
		h.NewStart = 1
	}
	b.add(h, oldN+newN, newN, oldN, append(oldKept, newKept...))
}

// scanLines counts the "\n"-separated lines of s (a trailing "\n" does not
// start another) and returns the first room of them with prefix put in front
// and each cut to maxDiffLine. It allocates for the lines kept only.
func scanLines(s, prefix string, room int) (n int, kept []string) {
	for rest := s; rest != ""; {
		line, tail, _ := strings.Cut(rest, "\n")
		rest = tail
		n++
		if len(kept) < room {
			line, _ = capText(line, maxDiffLine)
			kept = append(kept, prefix+line)
		}
	}
	return n, kept
}
