package dialogue

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convturns"
)

const (
	dirMode  = 0o700
	fileMode = 0o600
)

// errNeedAll is the protocol for a rebuild that the turns in hand cannot serve: Write was given the turns from the
// cursor on (res.Found) but the session must be rebuilt (the segment file is gone or shorter than recorded, or the
// cursor turn changed under us). Write returns it BEFORE touching the files or the store. The caller re-reads the whole
// conversation (TurnsFrom with fromTurnID "") and calls Write again; that call rebuilds.
var errNeedAll = errors.New("dialogue: rebuild needs the whole conversation")

// Hooks are the seams PR 2 fills (turn summaries and recaps); PR 1 passes the zero value.
type Hooks struct {
	// Carried returns the full text, marker line included, of the newest recap of session sid whose to_no is not greater
	// than beforeNo, to be written into a new segment's header; "" for none. beforeNo is the number of the turn in which
	// the compaction boundary happens. PR 2 implements it.
	Carried func(sid string, beforeNo int) string
	// Placed returns blocks to put right after a turn when a session is rebuilt (its turn summary, a recap).
	Placed func(sid, turnID string) []string
}

// WrittenTurn is a turn written to the file for the first time.
type WrittenTurn struct {
	ID      string
	No      int
	EndedAt int64
}

// WriteResult is what one Write did.
type WriteResult struct {
	First   bool // the session had no state: Turns holds every historical turn, and the caller (PR 2) decides to take only the event's turn and later ones
	Rebuilt bool // the files were (re)written from the whole conversation; says nothing about Turns
	// Turns are the turns that got their number in this call, in turn order (so also in a rebuild, if the conversation
	// grew); not the cursor turn's second half, and not turns that had a number already.
	Turns       []WrittenTurn
	WaitingTool bool // stopped at a turn that still has a running tool (not force); try again later
}

// Writer writes dialogue files under root: <root>/<session id>/001.md, 002.md, …
//
// Everything that touches a session's files or state runs under that session's lock (lockFor). PR 2's AppendMarker
// takes the same lock.
type Writer struct {
	root   string
	store  *Store
	prevOf func(sid string) (string, bool) // the session this one continues, if any (a handoff)
	hooks  Hooks
	logf   func(format string, args ...any) // nil: no logging

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// Option configures a Writer.
type Option func(*Writer)

// WithLogf sets where the writer reports things it skipped (a previous segment it could not link). nil: silent.
func WithLogf(f func(format string, args ...any)) Option { return func(w *Writer) { w.logf = f } }

func NewWriter(root string, store *Store, prevOf func(sid string) (string, bool), hooks Hooks, opts ...Option) *Writer {
	w := &Writer{root: root, store: store, prevOf: prevOf, hooks: hooks, locks: map[string]*sync.Mutex{}}
	for _, o := range opts {
		o(w)
	}
	return w
}

func (w *Writer) lockFor(sid string) *sync.Mutex {
	w.mu.Lock()
	defer w.mu.Unlock()
	l := w.locks[sid]
	if l == nil {
		l = &sync.Mutex{}
		w.locks[sid] = l
	}
	return l
}

// sidRe is the shape of a session id (a lower-case UUID), the same rule as internal/modevents/wire.go. A session id
// becomes a directory name under root, so nothing else may get through.
var sidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validSessionID(s string) bool { return sidRe.MatchString(s) }

// sessionDir is <root>/<sid>, refused unless sid is valid and the cleaned path still lies under root.
func (w *Writer) sessionDir(sid string) (string, error) {
	if !validSessionID(sid) {
		return "", fmt.Errorf("dialogue: invalid session id %q", sid)
	}
	root := filepath.Clean(w.root)
	dir := filepath.Clean(filepath.Join(root, sid))
	if !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		return "", fmt.Errorf("dialogue: session dir %q is outside the dialogue root", dir)
	}
	return dir, nil
}

func segName(n int) string { return fmt.Sprintf("%03d.md", n) }

func (w *Writer) segPath(sid string, n int) string { return filepath.Join(w.root, sid, segName(n)) }

// Write records the turns of res into sid's dialogue files. res is TurnsFrom(sid, cursor turn): when the cursor turn is
// found, res.Turns[0] is that turn and res.Found is true; otherwise res.Turns is the whole conversation.
// target is the turn id the stop event is about: it may be written while still running.
//
// A non-empty target is also where Write stops: turns are written up to and including the target and no further, so each
// Stop's own turn is still unwritten when that Stop is handled (a later Stop, or PR 2's pairing of a Stop with its turn,
// relies on it). The cursor turn (res.Turns[0] when res.Found) always gets its late items first, even when the target is a
// later turn; when the target is the cursor turn itself (a Stop sent again) only those late items are written. An empty
// target (no turn could be matched to the event, or a forced write after the transcript never caught up) writes up to the
// first turn that cannot be written yet. A rebuild stopped at the target stays marked as rebuilding until a later Write
// reaches the end of the conversation.
//
// force writes a turn even when a tool in it is still running. That is a trade-off (plan §1-3): once written, a late
// in-place update of the turn (an AskUserQuestion answer arriving) is not written again.
//
// There is no fsync: after a power loss a segment file can be shorter than its seg_bytes; the next Write then
// rebuilds the whole session.
func (w *Writer) Write(sid string, res convturns.TurnsFromResult, target string, force bool) (WriteResult, error) {
	if _, err := w.sessionDir(sid); err != nil {
		return WriteResult{}, err
	}
	l := w.lockFor(sid)
	l.Lock()
	defer l.Unlock()

	st, has, err := w.store.Session(sid)
	if err != nil {
		return WriteResult{}, err
	}
	if !has {
		if res.Found {
			return WriteResult{}, errNeedAll
		}
		return w.run(sid, res, target, force, nil)
	}

	rebuild, err := w.needsRebuild(sid, st, res)
	if err != nil {
		return WriteResult{}, err
	}
	if rebuild {
		if res.Found {
			return WriteResult{}, errNeedAll
		}
		return w.rebuild(sid, st, res, target, force)
	}

	// A rebuild that was reset but not started (or whose start crashed): nothing of the old files may stay.
	if st.Rebuilding && st.Segment == 1 && st.SegBytes == 0 && st.CursorTurn == "" {
		if res.Found {
			return WriteResult{}, errNeedAll
		}
		if err := w.removeSegments(sid); err != nil {
			return WriteResult{}, err
		}
	}

	// A file longer than recorded holds bytes written but never committed (a crash): cut them off.
	if st.SegBytes > 0 {
		if fi, err := os.Stat(w.segPath(sid, st.Segment)); err == nil && fi.Size() > st.SegBytes {
			if err := os.Truncate(w.segPath(sid, st.Segment), st.SegBytes); err != nil {
				return WriteResult{}, fmt.Errorf("repair dialogue segment: %w", err)
			}
		}
	}
	// st.Rebuilding: an earlier rebuild did not reach the end; this call carries on as a rebuild.
	return w.run(sid, res, target, force, &st)
}

func (w *Writer) removeSegments(sid string) error {
	matches, _ := filepath.Glob(filepath.Join(w.root, sid, "*.md"))
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove dialogue segment: %w", err)
		}
	}
	return nil
}

// needsRebuild says whether the files and cursor can no longer be trusted. It reads, never writes.
func (w *Writer) needsRebuild(sid string, st SessionState, res convturns.TurnsFromResult) (bool, error) {
	if st.SegBytes > 0 {
		fi, err := os.Stat(w.segPath(sid, st.Segment))
		if err != nil {
			if os.IsNotExist(err) {
				return true, nil
			}
			return false, fmt.Errorf("stat dialogue segment: %w", err)
		}
		if fi.Size() < st.SegBytes {
			return true, nil
		}
	}
	if st.CursorTurn == "" {
		return false, nil
	}
	if !res.Found || len(res.Turns) == 0 || res.Turns[0].ID != st.CursorTurn {
		return true, nil
	}
	items := res.Turns[0].Items
	if len(items) < st.CursorItems {
		return true, nil
	}
	if st.CursorItems > 0 && itemID(items[st.CursorItems-1]) != st.CursorItemID {
		return true, nil
	}
	return false, nil
}

func (w *Writer) rebuild(sid string, old SessionState, res convturns.TurnsFromResult, target string, force bool) (WriteResult, error) {
	// Reset first (this sets the flag, so a crash from here on is still a rebuild), then the files, then the rewrite.
	if err := w.store.ResetForRebuild(sid); err != nil {
		return WriteResult{}, err
	}
	if err := w.removeSegments(sid); err != nil {
		return WriteResult{}, err
	}
	fresh := SessionState{Segment: 1, NextTurnNo: old.NextTurnNo, SourcePath: old.SourcePath, Rebuilding: true}
	return w.run(sid, res, target, force, &fresh)
}

// run writes res.Turns from the cursor (st) or from the start (st nil). A state with Rebuilding set writes in rebuild
// mode: Placed blocks are put, and the flag is cleared once res.Turns has been written to the end.
func (w *Writer) run(sid string, res convturns.TurnsFromResult, target string, force bool, st *SessionState) (WriteResult, error) {
	nos, err := w.store.TurnNos(sid)
	if err != nil {
		return WriteResult{}, err
	}
	r := &runner{w: w, sid: sid, source: res.Path, nos: nos, nextNo: 1, seg: 1}
	maxNo := 0
	for _, n := range nos {
		if n > maxNo {
			maxNo = n
		}
	}
	r.nextNo = maxNo + 1
	if st != nil {
		r.seg, r.size, r.nextNo = st.Segment, st.SegBytes, st.NextTurnNo
		r.cursorTurn, r.cursorItems, r.cursorItemID = st.CursorTurn, st.CursorItems, st.CursorItemID
		r.rebuilt = st.Rebuilding
		r.rebuilding = st.Rebuilding
	}
	out := WriteResult{First: st == nil, Rebuilt: r.rebuilt}

	complete := true
	for i, t := range res.Turns {
		from := 0
		if st != nil && res.Found && i == 0 {
			from = st.CursorItems
		}
		// a forced write also takes the newest turn while it still runs (the transcript never caught up with the Stop)
		if t.Outcome == convmodel.OutcomeRunning && t.ID != target && !(force && i == len(res.Turns)-1) {
			complete = false
			break
		}
		items := t.Items[from:]
		if len(items) == 0 {
			if target != "" && t.ID == target {
				if i < len(res.Turns)-1 {
					complete = false
				}
				break
			}
			continue
		}
		// A tool still running in an older, finished turn is an orphan (its result never came) and holds nothing back;
		// only the turn the event is about and the newest turn can still be waiting for a result.
		if !force && (t.ID == target || i == len(res.Turns)-1) && hasRunningStep(items) {
			out.WaitingTool = true
			complete = false
			break
		}
		wt, isNew, err := r.writeTurn(t, items)
		if err != nil {
			return out, err
		}
		if isNew {
			out.Turns = append(out.Turns, wt)
		}
		if target != "" && t.ID == target {
			// the event's own turn is the last one this call writes; the rest belongs to the events after it
			if i < len(res.Turns)-1 {
				complete = false
			}
			break
		}
	}
	if complete && r.rebuilding {
		// the whole conversation is in the files: the rebuild is over
		r.rebuilding = false
		if err := r.w.store.Commit(sid, r.state(), nil); err != nil {
			return out, err
		}
	}
	return out, nil
}

func hasRunningStep(items []convmodel.Item) bool {
	for _, it := range items {
		if it.Step != nil && it.Step.Status == convmodel.StepRunning {
			return true
		}
	}
	return false
}

// runner is one Write call's position in the files.
type runner struct {
	w       *Writer
	sid     string
	source  string
	rebuilt bool // this run writes in rebuild mode (Placed blocks)
	// rebuilding is what the committed state says; it turns false with the commit that ends the rebuild.
	rebuilding bool
	nos        map[string]int
	nextNo     int
	curNo      int // number of the turn being written

	seg  int
	size int64 // bytes of the current segment file; 0 means its header is not written yet

	cursorTurn   string
	cursorItems  int
	cursorItemID string
}

func (r *runner) state() SessionState {
	return SessionState{
		Segment: r.seg, SegBytes: r.size, CursorTurn: r.cursorTurn, CursorItems: r.cursorItems,
		CursorItemID: r.cursorItemID, NextTurnNo: r.nextNo, SourcePath: r.source, Rebuilding: r.rebuilding,
	}
}

func (r *runner) append(s string) error {
	f, err := os.OpenFile(r.w.segPath(r.sid, r.seg), os.O_APPEND|os.O_WRONLY, fileMode)
	if err != nil {
		return fmt.Errorf("append dialogue segment: %w", err)
	}
	_, err = f.WriteString(s)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("append dialogue segment: %w", err)
	}
	r.size += int64(len(s))
	return nil
}

// ensureHeader creates the current segment file (truncating any leftover of a crashed attempt) when it is empty.
func (r *runner) ensureHeader() error {
	if r.size > 0 {
		return nil
	}
	dir, err := r.w.sessionDir(r.sid)
	if err != nil {
		return err
	}
	for _, d := range []string{r.w.root, dir} {
		if err := os.MkdirAll(d, dirMode); err != nil {
			return fmt.Errorf("create dialogue dir: %w", err)
		}
		if err := os.Chmod(d, dirMode); err != nil {
			return fmt.Errorf("restrict dialogue dir: %w", err)
		}
	}
	prev := "none"
	if r.seg > 1 {
		prev = r.w.segPath(r.sid, r.seg-1)
	} else if p, ok, err := r.w.prevSegment(r.sid); err != nil {
		return err
	} else if ok {
		prev = p
	}
	var sb strings.Builder
	sb.WriteString("<prev: " + prev + ">\n<source: " + r.source + ">\n")
	if r.seg > 1 && r.w.hooks.Carried != nil {
		if c := r.w.hooks.Carried(r.sid, r.curNo); c != "" {
			sb.WriteString(c)
			if !strings.HasSuffix(c, "\n") {
				sb.WriteByte('\n')
			}
		}
	}
	path := r.w.segPath(r.sid, r.seg)
	if err := os.WriteFile(path, []byte(sb.String()), fileMode); err != nil {
		return fmt.Errorf("write dialogue segment header: %w", err)
	}
	if err := os.Chmod(path, fileMode); err != nil {
		return fmt.Errorf("restrict dialogue segment: %w", err)
	}
	r.size = int64(sb.Len())
	return nil
}

// text appends one block, set off from what is there by a blank line.
func (r *runner) text(s string) error {
	if err := r.ensureHeader(); err != nil {
		return err
	}
	return r.append("\n" + s)
}

// boundary closes the current segment with the compaction marker and opens the next one.
func (r *runner) boundary(trigger string) error {
	if err := r.ensureHeader(); err != nil {
		return err
	}
	next := r.w.segPath(r.sid, r.seg+1)
	if err := r.append("\n<compacted | " + trigger + ">\n<next: " + next + ">\n"); err != nil {
		return err
	}
	r.seg++
	r.size = 0
	return r.ensureHeader()
}

// writeTurn writes items (the turn's new items) and commits. isNew: the turn had no number before.
func (r *runner) writeTurn(t convmodel.Turn, items []convmodel.Item) (WrittenTurn, bool, error) {
	no, known := r.nos[t.ID]
	if !known {
		no = r.nextNo
		r.nextNo++
		r.nos[t.ID] = no
	}
	r.curNo = no
	startSeg := r.seg
	for _, b := range renderItems(items) {
		var err error
		if b.compact {
			err = r.boundary(b.trigger)
		} else {
			err = r.text(b.text)
		}
		if err != nil {
			return WrittenTurn{}, false, err
		}
	}
	if r.rebuilt && r.w.hooks.Placed != nil {
		for _, s := range r.w.hooks.Placed(r.sid, t.ID) {
			if !strings.HasSuffix(s, "\n") {
				s += "\n"
			}
			if err := r.text(s); err != nil {
				return WrittenTurn{}, false, err
			}
		}
	}
	ended := t.StartedAt
	if t.EndedAt != nil {
		ended = *t.EndedAt
	}
	r.cursorTurn, r.cursorItems, r.cursorItemID = t.ID, len(t.Items), itemID(t.Items[len(t.Items)-1])
	// the file is written first; only then is it recorded
	err := r.w.store.Commit(r.sid, r.state(), []TurnRow{{TurnID: t.ID, TurnNo: no, Segment: startSeg, EndedAt: ended}})
	if err != nil {
		return WrittenTurn{}, false, err
	}
	return WrittenTurn{ID: t.ID, No: no, EndedAt: ended}, !known, nil
}

func (w *Writer) log(format string, args ...any) {
	if w.logf != nil {
		w.logf(format, args...)
	}
}

// prevSegment finds the last segment of the session sid continues (a handoff), for the "<prev:>" line of sid's first
// segment. ok is false when there is no previous session, its id is not valid, or it has not been recorded.
//
// The previous session's files are never modified (no "<next:>" is added to it), so this is one store read: it needs
// neither that session's lock nor any lock order between sessions. If the previous session is written to later, the
// path still names its then-last segment as of this read, which is what "<prev:>" means.
func (w *Writer) prevSegment(sid string) (path string, ok bool, err error) {
	if w.prevOf == nil {
		return "", false, nil
	}
	prevSid, has := w.prevOf(sid)
	if !has || prevSid == "" || prevSid == sid {
		return "", false, nil
	}
	if !validSessionID(prevSid) {
		w.log("dialogue: %s continues an invalid session id; not linked", sid)
		return "", false, nil
	}
	st, has, err := w.store.Session(prevSid)
	if err != nil {
		return "", false, err
	}
	if !has {
		w.log("dialogue: %s continues %s, which has no dialogue state yet; not linked", sid, prevSid)
		return "", false, nil
	}
	return w.segPath(prevSid, st.Segment), true, nil
}

// itemID is the id of any item variant; "" for an unknown item.
func itemID(it convmodel.Item) string {
	switch {
	case it.User != nil:
		return it.User.ID
	case it.AgentText != nil:
		return it.AgentText.ID
	case it.Thinking != nil:
		return it.Thinking.ID
	case it.Step != nil:
		return it.Step.ID
	case it.System != nil:
		return it.System.ID
	}
	return ""
}
