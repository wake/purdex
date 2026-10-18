package conversation

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/convmodel/ccnorm"
	"github.com/wake/purdex/internal/promptq"
)

// Message stages (docs/plans/2026-10-19-message-stages-step2-plan.md §1.1, spec docs/specs/2026-10-16-message-stages-spec.md):
// where each client_msg_id of a session is on its way from the App to Claude Code's transcript. The ledger is in memory
// only, like promptq's; after a daemon restart the clients fall back to matching by text and time.
//
// Per id the stages run received < filled < entered < queued < read. read, failed and recalled are terminal, a stage never
// moves back, and unknown (the outcome is not known) may still advance to queued / read by matching. The HTTP handlers write
// received .. entered, failed and unknown; observe() writes queued and read by comparing the header's queue and the
// conversation's user items with the ids still in flight; recallAll() writes recalled.

const (
	stageReceived = "received"
	stageFilled   = "filled"
	stageEntered  = "entered"
	stageQueued   = "queued"
	stageRead     = "read"
	stageFailed   = "failed"
	stageUnknown  = "unknown"
	stageRecalled = "recalled"
)

const (
	// maxStageText is the largest text kept for matching; a longer message is still followed, but only the clients' own
	// text-and-time matching can find it in the transcript.
	maxStageText = promptq.MaxTextBytes
	// maxStageTotal caps the texts of all in-flight ids of the daemon; past it the oldest texts are dropped first.
	maxStageTotal = 2 << 20
	// stageTextTTL: how long after the Enter a text is kept (a busy session reads its queue late, but not for ever).
	stageTextTTL = 30 * time.Minute
	// stageTerminalTTL: how long a finished id stays in the ledger, so a reconnect's snapshot still shows it.
	stageTerminalTTL = promptq.LedgerTTL
	// stageIdleTTL: an id that never finished is forgotten this long after its last change.
	stageIdleTTL = time.Hour
	// maxStageRows bounds the ids of one session (and so the work of one observe): past it the oldest finished id goes first.
	maxStageRows = 512
	// An id that never reached Claude Code's queue is read by an item stamped just before its Enter up to this long after.
	stageItemBefore = 2 * time.Second
	stageItemAfter  = 30 * time.Second
	// A queue row without a timestamp is dated by the Enter; an item may be stamped a hair before the daemon noted the Enter.
	stageNoStampSlack = 500 * time.Millisecond
	// maxStageUsed bounds the used-item map by count, whatever the traffic; the oldest go first.
	maxStageUsed = 4096
)

type stageRow struct {
	id           string
	arrival      uint64 // order the ids first appeared in
	stage        string
	at           time.Time
	reason, mode string
	seq          uint64 // the ledger's seq at this row's last change

	text     string // in flight only: what matching looks for; "" once dropped or over the cap
	textFrom time.Time
	claim    *stageClaim // set while the id is queued: the header row that is this message
	doneAt   time.Time   // terminal since
	changed  time.Time
}

// stageClaim names a header queue row: Claude Code gives the rows no id, so a row is its text (the id's own, kept once in
// stageRow.text) and its enqueue time.
type stageClaim struct{ at int64 }

type sessionStages struct {
	seq     uint64
	arrival uint64
	rows    map[string]*stageRow
	// lossy: the feed's queue tracker once overflowed, so what it shows is a subset of Claude Code's queue and a row that is not
	// there proves nothing. It stays set until the queue is seen empty with no id queued (the next time the two must agree), or
	// stageTextTTL after the last overflow was seen (an id older than that has lost its text and is no longer judged anyway).
	lossy      bool
	lossySince time.Time
	// usedFloor: items stamped at or before it are never matched again (their used marker was trimmed by count).
	usedFloor int64
	used      map[string]int64 // user item ids already given to an id, with the item's time (pruned with the ids that could still match)
}

type stageLedger struct {
	mu        sync.Mutex
	sessions  map[string]*sessionStages
	textBytes int
}

// stageJSON is one stage on the wire (a conversation.stages frame and the snapshot's stages).
type stageJSON struct {
	ClientMsgID string `json:"client_msg_id"`
	Stage       string `json:"stage"`
	At          int64  `json:"at"`
	Reason      string `json:"reason,omitempty"`
	Mode        string `json:"mode,omitempty"`
}

// stageItem is a user message of the transcript (source user, or queued).
type stageItem struct {
	ID   string
	Text string
	At   int64 // unix ms
}

func terminalStage(s string) bool { return s == stageRead || s == stageFailed || s == stageRecalled }

var stageRank = map[string]int{stageReceived: 1, stageFilled: 2, stageEntered: 3, stageQueued: 4, stageRead: 5}

// stageAllowed says whether an id in stage cur may take stage next.
func stageAllowed(cur, next string) bool {
	switch {
	case terminalStage(cur) || cur == next:
		return false
	case next == stageUnknown || next == stageFailed:
		return cur == stageReceived || cur == stageFilled || cur == stageEntered
	case next == stageRecalled:
		return cur == stageQueued
	case cur == stageUnknown: // the outcome is not known, but matching may still find the message
		return next == stageQueued || next == stageRead
	}
	rn, ok := stageRank[next]
	return ok && rn > stageRank[cur]
}

func (l *stageLedger) session(sid string) *sessionStages {
	if l.sessions == nil {
		l.sessions = map[string]*sessionStages{}
	}
	s := l.sessions[sid]
	if s == nil {
		s = &sessionStages{rows: map[string]*stageRow{}, used: map[string]int64{}}
		l.sessions[sid] = s
	}
	return s
}

// bump records a change of row r.
func (s *sessionStages) bump(r *stageRow, now time.Time) {
	s.seq++
	r.seq = s.seq
	r.changed = now
}

func (l *stageLedger) dropText(r *stageRow) {
	l.textBytes -= len(r.text)
	r.text = ""
}

// begin starts following an id: its text is kept for matching (within the caps). A known live id changes nothing; an id that
// failed because nothing was sent (a lock-wait busy) starts again.
func (l *stageLedger) begin(sid, id, text string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now)
	s := l.session(sid)
	if r, ok := s.rows[id]; ok {
		if r.stage != stageFailed {
			return
		}
		l.dropText(r)
		delete(s.rows, id)
	}
	if len(s.rows) >= maxStageRows {
		l.evictRowLocked(s)
	}
	s.arrival++
	r := &stageRow{id: id, arrival: s.arrival, stage: stageReceived, at: now, textFrom: now}
	s.rows[id] = r
	if len(text) <= maxStageText {
		r.text = text
		l.textBytes += len(text)
		l.evictTextsLocked(r)
	}
	s.bump(r, now)
}

// evictRowLocked drops the session's oldest finished id, or its oldest id when none has finished.
func (l *stageLedger) evictRowLocked(s *sessionStages) {
	var victim *stageRow
	for _, r := range s.sorted() {
		if terminalStage(r.stage) {
			victim = r
			break
		}
		if victim == nil {
			victim = r
		}
	}
	if victim != nil {
		l.dropText(victim)
		delete(s.rows, victim.id)
	}
}

// evictTextsLocked drops the oldest in-flight texts (never keep's) until the total is under the cap.
func (l *stageLedger) evictTextsLocked(keep *stageRow) {
	for l.textBytes > maxStageTotal {
		var oldest *stageRow
		for _, s := range l.sessions {
			for _, r := range s.rows {
				if r == keep || r.text == "" {
					continue
				}
				if oldest == nil || r.textFrom.Before(oldest.textFrom) {
					oldest = r
				}
			}
		}
		if oldest == nil {
			l.dropText(keep)
			return
		}
		l.dropText(oldest)
	}
}

// set moves an id to a stage if that is allowed. at is when it happened (a stage's own time, not the write's).
func (l *stageLedger) set(sid, id, stage, reason, mode string, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.sessions[sid]; s != nil {
		if r := s.rows[id]; r != nil {
			l.advanceLocked(s, r, stage, reason, mode, at)
		}
	}
}

func (l *stageLedger) advanceLocked(s *sessionStages, r *stageRow, stage, reason, mode string, at time.Time) bool {
	if !stageAllowed(r.stage, stage) {
		return false
	}
	r.stage, r.at, r.reason = stage, at, reason
	if mode != "" {
		r.mode = mode
	}
	if stage == stageEntered {
		r.textFrom = at
	}
	if terminalStage(stage) {
		l.dropText(r)
		r.claim = nil
		r.doneAt = at
	}
	s.bump(r, at)
	return true
}

// recallAll marks every id that is queued as taken back: a successful recall pops Claude Code's whole queue. The recall
// request's own id is not an id of the ledger.
func (l *stageLedger) recallAll(sid string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.sessions[sid]
	if s == nil {
		return
	}
	for _, r := range s.sorted() {
		if r.stage == stageQueued {
			l.advanceLocked(s, r, stageRecalled, "", "", now)
		}
	}
}

func (s *sessionStages) sorted() []*stageRow {
	out := make([]*stageRow, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].arrival < out[j].arrival })
	return out
}

func normText(s string) string { return strings.Join(strings.Fields(s), " ") }

// observe compares the header's queue (queue: Claude Code's queued rows, oldest first) and the conversation's user items with
// the ids still in flight:
//   - an id that was entered (or whose outcome is unknown) and has an unclaimed queue row with its text takes the oldest such
//     row (FIFO, as the SPA's header claim does): queued;
//   - a queued id whose own row left the queue is read by the oldest unused item with its text stamped after the row (queue
//     order, never time distance, so the same text sent twice cannot swap);
//   - an id that never reached the queue is read by the unused item with its text nearest to its Enter, inside a window.
//
// lossy says the queue may be missing rows (the feed keeps at most ccnorm.MaxQueueEntries): a row that is not there then proves
// nothing, so no queued id is judged to have left. It also drops texts that outlived stageTextTTL and finished or idle ids
// that outlived theirs.
func (l *stageLedger) observe(sid string, queue []ccnorm.Queued, lossy bool, items []stageItem, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now)
	s := l.sessions[sid]
	if s == nil {
		return
	}
	rows := s.sorted()
	queuedNow := false
	for _, r := range rows {
		queuedNow = queuedNow || r.stage == stageQueued
	}
	switch {
	case lossy:
		s.lossy, s.lossySince = true, now
	case s.lossy && ((len(queue) == 0 && !queuedNow) || now.Sub(s.lossySince) > stageTextTTL):
		s.lossy = false
	}
	items = l.pruneUsedLocked(s, rows, items)

	var qs []ccnorm.Queued
	for _, q := range queue {
		if q.Source == ccnorm.QueuedUser {
			qs = append(qs, q)
		}
	}
	consumed := make([]bool, len(qs))
	take := func(text string, at int64, minAt int64) int {
		for i, q := range qs {
			if !consumed[i] && normText(q.Text) == text && (at < 0 || q.At == at) && (q.At == 0 || q.At >= minAt) {
				return i
			}
		}
		return -1
	}

	// 1. queued ids: is their row still there?
	var left []*stageRow
	for _, r := range rows {
		if r.stage != stageQueued || r.claim == nil || r.text == "" {
			continue
		}
		if i := take(normText(ccnorm.ShownQueued(r.text)), r.claim.at, 0); i >= 0 {
			consumed[i] = true
		} else if !s.lossy {
			left = append(left, r)
		}
	}
	// 2. ids that left the queue are read by their items, oldest first
	for _, r := range left {
		text := normText(r.text)
		// Claude Code writes the item after the row, so nothing older than the row is ours; a row with no timestamp is dated by
		// the Enter, with a hair of slack.
		from := r.claim.at
		if from <= 0 {
			from = r.textFrom.UnixMilli() - stageNoStampSlack.Milliseconds()
		}
		for _, it := range items {
			if _, taken := s.used[it.ID]; taken || it.At < from || normText(it.Text) != text {
				continue
			}
			s.used[it.ID] = it.At
			l.advanceLocked(s, r, stageRead, "", "", now)
			break
		}
	}
	// 3. entered / unknown ids claim a queue row, or are read straight away
	var unclaimed []*stageRow
	for _, r := range rows {
		if (r.stage != stageEntered && r.stage != stageUnknown) || r.text == "" {
			continue
		}
		if i := take(normText(ccnorm.ShownQueued(r.text)), -1, r.textFrom.UnixMilli()-stageItemBefore.Milliseconds()); i >= 0 {
			consumed[i] = true
			r.claim = &stageClaim{at: qs[i].At}
			l.advanceLocked(s, r, stageQueued, "", "", now)
			continue
		}
		unclaimed = append(unclaimed, r)
	}
	l.readIdleLocked(s, unclaimed, items, now)
	s.trimUsed()
}

// pruneUsedLocked forgets the used items no id in flight could still be given (they are older than every window), and returns
// the items that still could be: the matching below scans only those.
func (l *stageLedger) pruneUsedLocked(s *sessionStages, rows []*stageRow, items []stageItem) []stageItem {
	var minFrom int64
	found := false
	for _, r := range rows {
		if terminalStage(r.stage) || r.text == "" {
			continue
		}
		from := r.textFrom.UnixMilli()
		if r.claim != nil && r.claim.at > 0 {
			from = r.claim.at
		}
		if !found || from < minFrom {
			minFrom, found = from, true
		}
	}
	if !found {
		clear(s.used)
		return nil
	}
	minFrom -= stageItemBefore.Milliseconds()
	for id, at := range s.used {
		if at < minFrom {
			delete(s.used, id)
		}
	}
	s.trimUsed()
	kept := items[:0:0]
	for _, it := range items {
		if it.At >= minFrom && it.At > s.usedFloor {
			kept = append(kept, it)
		}
	}
	return kept
}

// trimUsed keeps the used-item map within maxStageUsed entries, dropping the oldest items first.
func (s *sessionStages) trimUsed() {
	over := len(s.used) - maxStageUsed
	if over <= 0 {
		return
	}
	type usedAt struct {
		id string
		at int64
	}
	all := make([]usedAt, 0, len(s.used))
	for id, at := range s.used {
		all = append(all, usedAt{id, at})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at < all[j].at })
	for _, u := range all[:over] {
		delete(s.used, u.id)
	}
	// Nothing at or before the floor is matched again, so a trimmed item cannot be given out twice. When the cut falls inside
	// one millisecond the floor stays below it, so items of that millisecond that were never used still can be (more than
	// maxStageUsed matches inside one millisecond is not a real traffic shape).
	cut := all[over-1].at
	if over < len(all) && all[over].at == cut {
		cut--
	}
	s.usedFloor = max(s.usedFloor, cut)
}

// readIdleLocked pairs ids that never queued with the items that are their messages: same text, the item inside the window
// around the Enter, the nearest first, one to one.
func (l *stageLedger) readIdleLocked(s *sessionStages, rows []*stageRow, items []stageItem, now time.Time) {
	type pair struct {
		r    *stageRow
		it   int
		dist int64
	}
	var pairs []pair
	for _, r := range rows {
		text := normText(r.text)
		from := r.textFrom.UnixMilli() - stageItemBefore.Milliseconds()
		to := r.textFrom.UnixMilli() + stageItemAfter.Milliseconds()
		for i, it := range items {
			if _, taken := s.used[it.ID]; taken || it.At < from || it.At > to || normText(it.Text) != text {
				continue
			}
			d := it.At - r.textFrom.UnixMilli()
			if d < 0 {
				d = -d
			}
			pairs = append(pairs, pair{r, i, d})
		}
	}
	sort.Slice(pairs, func(a, b int) bool {
		if pairs[a].dist != pairs[b].dist {
			return pairs[a].dist < pairs[b].dist
		}
		if pairs[a].it != pairs[b].it {
			return pairs[a].it < pairs[b].it
		}
		return pairs[a].r.arrival < pairs[b].r.arrival
	})
	done := map[string]bool{}
	for _, p := range pairs {
		id := items[p.it].ID
		if done[p.r.id] {
			continue
		}
		if _, taken := s.used[id]; taken {
			continue
		}
		s.used[id] = items[p.it].At
		done[p.r.id] = true
		l.advanceLocked(s, p.r, stageRead, "", "", now)
	}
}

// sweepLocked drops texts and rows that outlived their time, over every session.
func (l *stageLedger) sweepLocked(now time.Time) {
	for sid, s := range l.sessions {
		for id, r := range s.rows {
			switch {
			case terminalStage(r.stage) && now.Sub(r.doneAt) > stageTerminalTTL,
				!terminalStage(r.stage) && now.Sub(r.changed) > stageIdleTTL:
				l.dropText(r)
				delete(s.rows, id)
			case r.text != "" && now.Sub(r.textFrom) > stageTextTTL:
				l.dropText(r)
			}
		}
		if len(s.rows) == 0 {
			delete(l.sessions, sid)
		}
	}
}

func (r *stageRow) json() stageJSON {
	return stageJSON{ClientMsgID: r.id, Stage: r.stage, At: r.at.UnixMilli(), Reason: r.reason, Mode: r.mode}
}

// snapshot is the latest stage of every id of the session, with the seq the ledger is at (the baseline of a connection).
func (l *stageLedger) snapshot(sid string, now time.Time) ([]stageJSON, uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now)
	s := l.sessions[sid]
	if s == nil {
		return []stageJSON{}, 0
	}
	out := make([]stageJSON, 0, len(s.rows))
	for _, r := range s.sorted() {
		out = append(out, r.json())
	}
	return out, s.seq
}

// since is the stages that changed after seq (one entry per id, in the order they changed) and the seq to remember.
func (l *stageLedger) since(sid string, after uint64, now time.Time) ([]stageJSON, uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.sessions[sid]
	if s == nil || s.seq == after {
		return nil, after
	}
	var rs []*stageRow
	for _, r := range s.rows {
		if r.seq > after {
			rs = append(rs, r)
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].seq < rs[j].seq })
	out := make([]stageJSON, len(rs))
	for i, r := range rs {
		out[i] = r.json()
	}
	return out, s.seq
}

// seqOf is the ledger's current seq for the session.
func (l *stageLedger) seqOf(sid string) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.sessions[sid]; s != nil {
		return s.seq
	}
	return 0
}
