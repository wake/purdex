package promptq

import (
	"sort"
	"time"
)

// Echo pairing (U3-2): the App shows its own message at once ("你 · 排隊中") and has to find the same message again when the
// transcript's user item appears. The transcript has no client_msg_id (a prompt submitted through the mod is written with the
// text and the time only), so the daemon, which knows which client_msg_id it handed to the mod, names it on the item.

// EchoItem is a user message of the transcript, as far as the pairing looks at it.
type EchoItem struct {
	Text string
	At   time.Time // the row's time
}

// Slack around the time a request ran in: a row is written when the turn starts, a little before the mod reports back.
const (
	echoBefore = 2 * time.Second  // a row may be stamped a little before the hand-out the daemon saw
	echoAfter  = 30 * time.Second // ... and for an unknown result, up to this long after it
)

// Match pairs the session's user items with the client_msg_ids of requests that ran (accepted) or may have (unknown): same
// text, the row's time inside the request's window. Each request names at most one item and each item gets at most one id;
// when several requests carry the same text the nearest in time pairs first, so two identical messages sent close together
// still end up with their own ids. out[i] is the id for items[i], "" for none. Nothing is stored: the same items and the same
// ledger always give the same pairing.
func (q *Queue) Match(sessionID string, items []EchoItem) []string {
	out := make([]string, len(items))
	q.mu.Lock()
	type cand struct {
		key  string
		text string
		from time.Time
		to   time.Time
		mid  time.Time
	}
	var cands []cand
	for key, e := range q.ledger {
		if e.job.SessionID != sessionID || e.state == stQueued || e.handedAt.IsZero() {
			continue
		}
		var end time.Time
		switch {
		case e.state == stDone && e.res.Status == Accepted:
			end = e.finishedAt.Add(echoBefore)
		case e.state == stHanded || (e.state == stDone && e.res.Status == Unknown):
			end = e.handedAt.Add(q.handTimeout() + echoAfter)
		default: // dropped, busy, timeout: it did not run
			continue
		}
		from := e.handedAt.Add(-echoBefore)
		cands = append(cands, cand{key: key, text: e.job.Text, from: from, to: end, mid: e.handedAt})
	}
	q.mu.Unlock()
	if len(cands) == 0 {
		return out
	}
	type pair struct {
		i, c int
		dist time.Duration
	}
	var pairs []pair
	for i, it := range items {
		for c, cd := range cands {
			if it.Text != cd.text || it.At.Before(cd.from) || it.At.After(cd.to) {
				continue
			}
			d := it.At.Sub(cd.mid)
			if d < 0 {
				d = -d
			}
			pairs = append(pairs, pair{i, c, d})
		}
	}
	sort.Slice(pairs, func(a, b int) bool {
		if pairs[a].dist != pairs[b].dist {
			return pairs[a].dist < pairs[b].dist
		}
		if pairs[a].i != pairs[b].i {
			return pairs[a].i < pairs[b].i
		}
		return cands[pairs[a].c].key < cands[pairs[b].c].key
	})
	usedItem := make([]bool, len(items))
	usedCand := make([]bool, len(cands))
	for _, p := range pairs {
		if usedItem[p.i] || usedCand[p.c] {
			continue
		}
		usedItem[p.i], usedCand[p.c] = true, true
		out[p.i] = cands[p.c].key
	}
	return out
}
