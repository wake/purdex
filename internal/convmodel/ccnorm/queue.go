package ccnorm

import (
	"crypto/sha256"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The queue mirrors Claude Code's own prompt queue (the messages typed or sent
// while a turn runs, shown grey under the terminal). The rows are
// {"type":"queue-operation","operation":enqueue|dequeue|remove|popAll,"content":…}.
// dequeue carries no content, so the FIFO must hold every source (system and
// peer entries too) or it would pair a dequeue with the wrong entry.

const (
	maxQueueEntries = 50   // internal entries kept; the oldest is dropped past this
	maxQueuedText   = 2000 // runes of text kept and shown
)

// MaxQueuedText is the cap (in runes) of a queued text the feed keeps: a text at the cap may have been cut.
const MaxQueuedText = maxQueuedText

// Queued is one message waiting in Claude Code's queue.
type Queued struct {
	Text   string `json:"text"`
	At     int64  `json:"at"` // ms since the epoch, 0 when the row had no timestamp
	Source string `json:"source"`
	From   string `json:"from,omitempty"` // a peer entry's sender
}

// Queue sources.
const (
	QueuedUser   = "user"
	QueuedPeer   = "peer"
	QueuedSystem = "system"
)

// qEntry is what the queue keeps: the shown text already cut, and hashes of the full content for remove matching.
type qEntry struct {
	Queued
	full [sha256.Size]byte // whitespace-normalised full content
	body [sha256.Size]byte // a peer entry's normalised body (the wrapper attributes differ between enqueue and remove)
}

// queueSource: a trimmed content that opens like an XML-ish tag (`<` and a letter) is the harness's, not the person's;
// the cross-session wrapper is a peer's message.
func queueSource(content string) string {
	t := strings.TrimLeftFunc(content, unicode.IsSpace)
	if strings.HasPrefix(t, peerOpen) {
		return QueuedPeer
	}
	if len(t) > 1 && t[0] == '<' {
		for _, tag := range harnessTags {
			if rest, ok := strings.CutPrefix(t[1:], tag); ok && (rest == "" || rest[0] == '>' || rest[0] == '/' || unicode.IsSpace(rune(rest[0]))) {
				return QueuedSystem
			}
		}
	}
	return QueuedUser
}

// harnessTags are the tags Claude Code itself writes at the start of a queued command. user.go already reads bash-input,
// bash-stdout/stderr, local-command-stdout/caveat, command-name/message/args and task-notification (humanTags,
// isSlashCommand); system-reminder, local-command-stderr and user-prompt-submit-hook are the other harness wrappers.
var harnessTags = [...]string{"task-notification", "system-reminder", "command-name", "command-message", "command-args",
	"local-command-stdout", "local-command-stderr", "local-command-caveat", "bash-input", "bash-stdout", "bash-stderr",
	"user-prompt-submit-hook"}

func normSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// ShownQueued is the text the header shows for a queued user message: the message cut to maxQueuedText runes. A caller that
// looks for its own message in the header's queue compares against this.
func ShownQueued(text string) string {
	if utf8.RuneCountInString(text) > maxQueuedText {
		return string([]rune(text)[:maxQueuedText])
	}
	return text
}

func newQEntry(text string, at int64) qEntry {
	e := qEntry{Queued: Queued{At: at, Source: queueSource(text)}, full: sha256.Sum256([]byte(normSpace(text)))}
	shown := text
	if e.Source == QueuedPeer {
		b, name := peerBody(text)
		shown, e.From = strings.TrimSpace(b), cleanName(name)
		e.body = sha256.Sum256([]byte(normSpace(b)))
	}
	if utf8.RuneCountInString(shown) > maxQueuedText {
		shown = string([]rune(shown)[:maxQueuedText])
	}
	e.Text = shown
	return e
}

// removeAt drops entry i.
func (n *Normalizer) removeAt(i int) { n.queue = append(n.queue[:i:i], n.queue[i+1:]...) }

// queueRemove pairs a remove row with its enqueue: by normalised content, then (peers) by body, then the oldest entry of
// the same source class — Claude Code absorbs queued commands in FIFO order. Measured on the 60 largest local
// transcripts: 1413 of 1428 removes match exactly, 14 of the other 15 match by peer body (the wrapper is re-serialised,
// e.g. the enqueued one carries a hop-chain attribute), the last one needs the fallback.
func (n *Normalizer) queueRemove(text string) {
	want := newQEntry(text, 0)
	for i, q := range n.queue {
		if q.full == want.full {
			n.removeAt(i)
			return
		}
	}
	if want.Source == QueuedPeer {
		for i, q := range n.queue {
			if q.Source == QueuedPeer && q.body == want.body {
				n.removeAt(i)
				return
			}
		}
	}
	// A user remove with no match falls back only when exactly one user entry is queued: hiding a message that is still
	// queued is worse than briefly showing a delivered one.
	idx, count := -1, 0
	for i, q := range n.queue {
		if q.Source == want.Source {
			count++
			if idx < 0 {
				idx = i
			}
		}
	}
	if idx >= 0 && (want.Source != QueuedUser || count == 1) {
		n.removeAt(idx)
		n.skip("queue:remove-fifo")
		return
	}
	n.skip("queue:remove-unmatched")
}

func (n *Normalizer) queueRow(l *rawLine, op string) {
	switch op {
	case "enqueue", "remove":
		text, ok := jsonString(l.Content)
		if !ok {
			n.skip("queue:content")
			return
		}
		if op == "remove" {
			n.queueRemove(text)
			return
		}
		if len(n.queue) >= maxQueueEntries {
			n.queue = n.queue[1:]
			n.skip("queue:overflow")
		}
		n.queue = append(n.queue, newQEntry(text, l.at))
	case "dequeue":
		if len(n.queue) == 0 {
			n.skip("queue:dequeue-empty")
			return
		}
		n.queue = n.queue[1:]
	case "popAll":
		n.queue = nil
	default:
		if len(op) > 64 {
			op = strings.ToValidUTF8(op[:64], "")
		}
		n.skipDyn("queue:op:" + op)
	}
}

// queuedOut is what the header shows: user and peer entries, copied.
func (n *Normalizer) queuedOut() []Queued {
	var out []Queued
	for _, q := range n.queue {
		if q.Source != QueuedSystem {
			out = append(out, q.Queued)
		}
	}
	return out
}

// HiddenQueued is how many queued entries the header hides (the harness's own: task notifications, reminders…). Recall
// (K-1b) needs it: Up pops everything the queue holds, so "only user messages are queued" means this is zero.
func (n *Normalizer) HiddenQueued() int {
	c := 0
	for _, q := range n.queue {
		if q.Source == QueuedSystem {
			c++
		}
	}
	return c
}
