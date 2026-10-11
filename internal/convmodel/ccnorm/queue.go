package ccnorm

import (
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
	maxQueuedText   = 2000 // runes of text shown outward
)

// Queued is one message waiting in Claude Code's queue.
type Queued struct {
	Text   string `json:"text"`
	At     int64  `json:"at"` // ms since the epoch, 0 when the row had no timestamp
	Source string `json:"source"`
}

// Queue sources.
const (
	QueuedUser   = "user"
	QueuedPeer   = "peer"
	QueuedSystem = "system"
)

func queueSource(content string) string {
	t := strings.TrimLeftFunc(content, unicode.IsSpace)
	switch {
	case strings.HasPrefix(t, "<cross-session-message"):
		return QueuedPeer
	case strings.HasPrefix(t, "<task-notification"):
		return QueuedSystem
	}
	return QueuedUser
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
			for i, q := range n.queue {
				if q.Text == text {
					n.queue = append(n.queue[:i:i], n.queue[i+1:]...)
					return
				}
			}
			n.skip("queue:remove-unmatched")
			return
		}
		if len(n.queue) >= maxQueueEntries {
			n.queue = n.queue[1:]
			n.skip("queue:overflow")
		}
		n.queue = append(n.queue, Queued{Text: text, At: l.at, Source: queueSource(text)})
	case "dequeue":
		if len(n.queue) == 0 {
			n.skip("queue:dequeue-empty")
			return
		}
		n.queue = n.queue[1:]
	case "popAll":
		n.queue = nil
	default:
		n.skipDyn("queue:op:" + op)
	}
}

// queuedOut is what the header shows: user and peer entries, copied, text cut.
func (n *Normalizer) queuedOut() []Queued {
	var out []Queued
	for _, q := range n.queue {
		if q.Source == QueuedSystem {
			continue
		}
		if utf8.RuneCountInString(q.Text) > maxQueuedText {
			q.Text = string([]rune(q.Text)[:maxQueuedText])
		}
		out = append(out, q)
	}
	return out
}
