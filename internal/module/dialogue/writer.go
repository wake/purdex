package dialogue

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	// Carried is the last recap's full text, marker line included, written into a new segment's header. "" for none.
	Carried func(sid string) string
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
	First       bool          // the session had no state and this was not a rebuild
	Rebuilt     bool          // the files were deleted and rewritten from the whole conversation
	Turns       []WrittenTurn // turns that had no number before (not the cursor turn's second half; not a rebuild's turns)
	WaitingTool bool          // stopped at a turn that still has a running tool (not force); try again later
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

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func NewWriter(root string, store *Store, prevOf func(sid string) (string, bool), hooks Hooks) *Writer {
	return &Writer{root: root, store: store, prevOf: prevOf, hooks: hooks, locks: map[string]*sync.Mutex{}}
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

func segName(n int) string { return fmt.Sprintf("%03d.md", n) }

func (w *Writer) segPath(sid string, n int) string { return filepath.Join(w.root, sid, segName(n)) }

// Write records the turns of res into sid's dialogue files. res is TurnsFrom(sid, cursor turn): when the cursor turn is
// found, res.Turns[0] is that turn and res.Found is true; otherwise res.Turns is the whole conversation.
// target is the turn id the stop event is about: it may be written while still running.
// force writes a turn even when a tool in it is still running.
func (w *Writer) Write(sid string, res convturns.TurnsFromResult, target string, force bool) (WriteResult, error) {
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
		return w.run(sid, res, target, force, nil, false, 0)
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

	// A file longer than recorded holds bytes written but never committed (a crash): cut them off.
	if st.SegBytes > 0 {
		if fi, err := os.Stat(w.segPath(sid, st.Segment)); err == nil && fi.Size() > st.SegBytes {
			if err := os.Truncate(w.segPath(sid, st.Segment), st.SegBytes); err != nil {
				return WriteResult{}, fmt.Errorf("repair dialogue segment: %w", err)
			}
		}
	}
	return w.run(sid, res, target, force, &st, false, 0)
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
	if err := w.store.ResetForRebuild(sid); err != nil {
		return WriteResult{}, err
	}
	matches, _ := filepath.Glob(filepath.Join(w.root, sid, "*.md"))
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !os.IsNotExist(err) {
			return WriteResult{}, fmt.Errorf("remove dialogue segment: %w", err)
		}
	}
	return w.run(sid, res, target, force, nil, true, old.NextTurnNo)
}

// run writes res.Turns from the cursor (st) or from the start (st nil).
func (w *Writer) run(sid string, res convturns.TurnsFromResult, target string, force bool, st *SessionState, rebuilt bool, floorNext int) (WriteResult, error) {
	nos, err := w.store.TurnNos(sid)
	if err != nil {
		return WriteResult{}, err
	}
	r := &runner{w: w, sid: sid, source: res.Path, rebuilt: rebuilt, nos: nos, nextNo: 1, seg: 1}
	maxNo := 0
	for _, n := range nos {
		if n > maxNo {
			maxNo = n
		}
	}
	r.nextNo = max(maxNo+1, floorNext)
	if st != nil {
		r.seg, r.size, r.nextNo = st.Segment, st.SegBytes, st.NextTurnNo
		r.cursorTurn, r.cursorItems, r.cursorItemID = st.CursorTurn, st.CursorItems, st.CursorItemID
	}
	out := WriteResult{First: st == nil && !rebuilt, Rebuilt: rebuilt}

	for i, t := range res.Turns {
		from := 0
		if st != nil && res.Found && i == 0 {
			from = st.CursorItems
		}
		if t.Outcome == convmodel.OutcomeRunning && t.ID != target {
			break
		}
		items := t.Items[from:]
		if len(items) == 0 {
			continue
		}
		if !force && hasRunningStep(items) {
			out.WaitingTool = true
			break
		}
		wt, isNew, err := r.writeTurn(t, items)
		if err != nil {
			return out, err
		}
		if isNew && !rebuilt {
			out.Turns = append(out.Turns, wt)
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
	rebuilt bool
	nos     map[string]int
	nextNo  int

	seg  int
	size int64 // bytes of the current segment file; 0 means its header is not written yet

	cursorTurn   string
	cursorItems  int
	cursorItemID string
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
	dir := filepath.Join(r.w.root, r.sid)
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
		if !r.rebuilt {
			if err := r.w.linkNext(p, r.w.segPath(r.sid, 1)); err != nil {
				return err
			}
		}
	}
	var sb strings.Builder
	sb.WriteString("<prev: " + prev + ">\n<source: " + r.source + ">\n")
	if r.seg > 1 && r.w.hooks.Carried != nil {
		if c := r.w.hooks.Carried(r.sid); c != "" {
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
	err := r.w.store.Commit(r.sid, SessionState{
		Segment: r.seg, SegBytes: r.size, CursorTurn: r.cursorTurn, CursorItems: r.cursorItems,
		CursorItemID: r.cursorItemID, NextTurnNo: r.nextNo, SourcePath: r.source,
	}, []TurnRow{{TurnID: t.ID, TurnNo: no, Segment: startSeg, EndedAt: ended}})
	if err != nil {
		return WrittenTurn{}, false, err
	}
	return WrittenTurn{ID: t.ID, No: no, EndedAt: ended}, !known, nil
}

// prevSegment is the path of the last segment of the session sid continues, when that session has been recorded.
func (w *Writer) prevSegment(sid string) (string, bool, error) {
	if w.prevOf == nil {
		return "", false, nil
	}
	p, ok := w.prevOf(sid)
	if !ok || p == "" || p == sid {
		return "", false, nil
	}
	st, has, err := w.store.Session(p)
	if err != nil || !has {
		return "", false, err
	}
	return w.segPath(p, st.Segment), true, nil
}

// linkNext appends "<next: …>" to the previous session's last segment and records the new size, so that session's own
// next write does not take the line for crash residue. Idempotent: a line already there is not repeated.
func (w *Writer) linkNext(prevPath, next string) error {
	prevSid := filepath.Base(filepath.Dir(prevPath))
	l := w.lockFor(prevSid)
	l.Lock()
	defer l.Unlock()
	st, has, err := w.store.Session(prevSid)
	if err != nil || !has {
		return err
	}
	line := "<next: " + next + ">\n"
	b, err := os.ReadFile(prevPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read previous dialogue segment: %w", err)
	}
	if bytes.Contains(b, []byte(line)) {
		return nil
	}
	if int64(len(b)) != st.SegBytes {
		return nil // the previous file is not in a state we can extend safely; it will repair itself on its next write
	}
	f, err := os.OpenFile(prevPath, os.O_APPEND|os.O_WRONLY, fileMode)
	if err != nil {
		return fmt.Errorf("append previous dialogue segment: %w", err)
	}
	_, err = f.WriteString("\n" + line)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("append previous dialogue segment: %w", err)
	}
	st.SegBytes += int64(len(line)) + 1
	return w.store.Commit(prevSid, st, nil)
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
