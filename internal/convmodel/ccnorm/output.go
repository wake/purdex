package ccnorm

import (
	"strings"
	"unicode/utf8"

	"github.com/wake/purdex/internal/convmodel"
)

// imagePlaceholder is the line an image block of a tool result leaves in the
// output text (spec §8.1 "Steps"); the image itself is never kept.
const imagePlaceholder = "[image]"

// outputOf builds the output of a result: the text of its content (a string
// as is, a list's text blocks and `[image]` lines in block order, joined by
// "\n"), each text block unwrapped from <persisted-output> as Nexen prelude
// does, the totals of the whole text, and the text capped at 16 KiB on a line
// boundary — the tail for an execute step, the head otherwise.
func (n *Normalizer) outputOf(blocks []block, kind convmodel.StepKind) *convmodel.Output {
	var parts []string
	var images []convmodel.Image
	for _, b := range blocks {
		switch b.typ {
		case "text":
			parts = append(parts, unwrapPersisted(b.text))
		case "image":
			mt, size := imageSize(b)
			images = append(images, convmodel.Image{MediaType: mt, Bytes: size})
			parts = append(parts, imagePlaceholder)
		default:
			n.skipDyn("result_block:" + b.typ)
		}
	}
	whole := strings.Join(parts, "\n")
	o := &convmodel.Output{
		TotalLines: countLines(whole), TotalBytes: len(whole), Images: images,
	}
	keep := convmodel.KeepHead
	if kind == convmodel.StepExecute {
		keep = convmodel.KeepTail
	}
	o.Text, o.Truncated = capOutput(whole, keep)
	if o.Truncated {
		o.Keep = keep
	}
	return o
}

// countLines counts the "\n"-separated lines of s: none for "", and a
// trailing "\n" does not start a further line. "\r" is ordinary text.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// capOutput keeps at most MaxOutput bytes of s, on a line boundary: whole
// lines from the head or from the tail. A line that is longer than the cap
// itself cannot be kept whole, so there the cut is by bytes (on a UTF-8
// boundary), after the whole lines that precede it (head) or before the
// whole lines that follow it (tail).
func capOutput(s string, keep convmodel.Keep) (string, bool) {
	limit := convmodel.MaxOutput
	if len(s) <= limit {
		return s, false
	}
	if keep == convmodel.KeepTail {
		return s[tailStart(s, limit):], true
	}
	return s[:headEnd(s, limit)], true
}

// headEnd is where the kept head of s ends: before the last "\n" within the
// first limit+1 bytes, unless the line after it is too long to keep whole
// (or there is no such "\n"), in which case it is limit bytes, cut back to a
// rune start.
func headEnd(s string, limit int) int {
	if i := strings.LastIndexByte(s[:limit+1], '\n'); i > 0 {
		next := s[i+1:]
		end := strings.IndexByte(next, '\n')
		if end < 0 {
			end = len(next)
		}
		if end <= limit {
			return i
		}
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return cut
}

// tailStart is where the kept tail of s starts: right after the first "\n"
// in the last limit bytes, unless the line that holds the window's start is
// too long to keep whole (or no "\n" follows), in which case it is the
// window's start, moved forward to a rune start. A final "\n" ends the last
// line; it is not a line break to cut at.
func tailStart(s string, limit int) int {
	start := len(s) - limit
	if s[start-1] == '\n' {
		return start
	}
	body := strings.TrimSuffix(s, "\n")
	if j := strings.IndexByte(body[start:], '\n'); j >= 0 {
		lineStart := strings.LastIndexByte(s[:start], '\n') + 1
		if start+j-lineStart <= limit {
			return start + j + 1
		}
	}
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return start
}
