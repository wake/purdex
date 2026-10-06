package nex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/wake/purdex/internal/conversations"
	"lab.protype.tw/wake/nexen/store"
)

// GET /api/nex/conversations?state=ended|gone (spec §13.5, §13.6, §13.9):
// this host's ended or gone Claude Code conversations, from a snapshot.
//
// A snapshot is one scan of the projects root (conversations.Scan, which
// brings the index up to date), the index, every Nexen execution and the
// live terminal frames, joined by buildConversations. It is taken at Start,
// every convScanInterval, and on request; a snapshot younger than convReuse
// answers a request, and concurrent requests share one in-flight snapshot.
//
// A flight runs on the module's context (convCtx), never on a request's: a
// client that gives up does not cancel it, and Stop does. Stop waits for it
// boundedly; a flight still running after that stops at its next file or
// page boundary, and whatever it ends with is neither cached nor served.

// conversationRowCap bounds the rows of one state in a response (§13.6);
// total and truncated tell the client what was cut.
const conversationRowCap = 2000

const (
	convReuse         = 5 * time.Second  // a snapshot this young answers a request (§13.6)
	convFlightTimeout = 60 * time.Second // bounds one snapshot
	convStopWait      = 3 * time.Second  // how long Stop waits for a running snapshot
)

// convScanInterval is how often the daemon takes a snapshot on its own
// (§13.6: every 6 hours). A var only so tests can shorten it.
var convScanInterval = 6 * time.Hour

// errConversationsStopped: Stop began (or Init never ran), so no snapshot
// starts and none is served.
var errConversationsStopped = errors.New("the conversation listing is stopped (the daemon is stopping)")

// convScanFunc is conversations.Scan's signature (the convScan seam).
type convScanFunc func(ctx context.Context, root string, idx conversations.Index, now func() time.Time) (conversations.ScanResult, error)

// convSnapshot is one successful snapshot. It is shared by every request
// that reads it and never modified.
type convSnapshot struct {
	at        time.Time // convNow() when its flight began; convReuse counts from it
	scannedAt int64     // the scan's ScannedAt, Unix ms
	rootErr   string    // the scan's RootErr; "" when the root was listed
	res       conversationsResult
}

// convFlight is the snapshot in progress. done is closed once snap and err
// are set.
type convFlight struct {
	done chan struct{}
	snap *convSnapshot
	err  error
	// waiters counts the callers that waited on it (under convMu). It exists
	// only so tests can wait deterministically for N waiters to have joined;
	// production code never reads it.
	waiters int
}

// conversationsResponse is the 200 body.
type conversationsResponse struct {
	State         string            `json:"state"`
	ScannedAt     int64             `json:"scanned_at"`           // Unix ms of the scan this snapshot used
	RootError     string            `json:"root_error,omitempty"` // the projects root could not be listed (R-4-1)
	Home          string            `json:"home"`                 // for "~" display
	Total         int               `json:"total"`                // rows in this state before the cap
	Truncated     bool              `json:"truncated"`            // total > conversationRowCap
	UnknownOwner  int               `json:"unknown_owner"`        // R-4-9
	Conversations []conversationRow `json:"conversations"`        // ≤ conversationRowCap, newest first
}

// WithConversationIndex wires the conversation index; nil keeps the feature
// off (the endpoint answers 503). Returns m.
func (m *Module) WithConversationIndex(idx conversations.Index) *Module {
	m.convIdx = idx
	return m
}

// lstatIsRegular: p is a regular file, not followed when it is a symlink
// (R-4-8, §13.6).
func lstatIsRegular(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode().IsRegular()
}

// initConversations is Init's part (before anything that can soft-fail):
// the context every scan runs under, and the projects root and home from
// the user's home. Values a test set beforehand are kept. Without a home
// the feature stays off.
func (m *Module) initConversations(home string, homeErr error) {
	m.convMu.Lock()
	if m.convCtx == nil {
		m.convCtx, m.convCancel = context.WithCancel(context.Background())
	}
	m.convMu.Unlock()
	if home == "" {
		if m.convIdx != nil && m.convRoot == "" {
			m.logf("nex: conversations: no home directory (%v); GET %s/conversations answers 503", homeErr, RoutePrefix)
		}
		return
	}
	if m.convHome == "" {
		m.convHome = home
	}
	if m.convRoot == "" {
		m.convRoot = filepath.Join(home, ".claude", "projects")
	}
}

// conversationsUnavailable says why the listing cannot run ("" when it
// can). A stopped module is checked separately, under convMu.
func (m *Module) conversationsUnavailable() string {
	switch {
	case m.convIdx == nil:
		return "the conversation index is not wired"
	case m.initErr != nil:
		return m.initErr.Error()
	case m.convRoot == "":
		return "the projects root is unknown (no home directory)"
	case m.sys.store == nil:
		return "nex engine unavailable"
	case m.terminals == nil:
		return "terminal sessions unavailable"
	}
	return ""
}

// handleConversations serves GET RoutePrefix/conversations?state=ended|gone.
// 400 bad_state for any other state; 503 conversations_unavailable when the
// listing cannot run or its snapshot failed (R-4-2: never a partial list).
func (m *Module) handleConversations(w http.ResponseWriter, r *http.Request) {
	state := conversationState(r.URL.Query().Get("state"))
	if state != conversationEnded && state != conversationGone {
		writeHandoffError(w, http.StatusBadRequest, "bad_state", `state must be "ended" or "gone"`, nil)
		return
	}
	if why := m.conversationsUnavailable(); why != "" {
		writeHandoffError(w, http.StatusServiceUnavailable, "conversations_unavailable", why, nil)
		return
	}
	snap, err := m.conversationSnapshot(r.Context())
	if err != nil {
		writeHandoffError(w, http.StatusServiceUnavailable, "conversations_unavailable", err.Error(), nil)
		return
	}
	rows := snap.res.Ended
	if state == conversationGone {
		rows = snap.res.Gone
	}
	total := len(rows)
	if total > conversationRowCap {
		rows = rows[:conversationRowCap]
	}
	writeJSON(w, http.StatusOK, conversationsResponse{
		State:         string(state),
		ScannedAt:     snap.scannedAt,
		RootError:     snap.rootErr,
		Home:          m.convHome,
		Total:         total,
		Truncated:     total > conversationRowCap,
		UnknownOwner:  snap.res.UnknownOwner,
		Conversations: rows,
	})
}

// conversationSnapshot returns a snapshot younger than convReuse, else the
// in-flight one's result, else starts a flight and waits for it. A caller
// whose ctx ends stops waiting (ctx.Err()); the flight goes on. Once Stop
// began nothing is returned, cached or not.
func (m *Module) conversationSnapshot(ctx context.Context) (*convSnapshot, error) {
	m.convMu.Lock()
	if m.convCtx == nil || m.convCtx.Err() != nil {
		m.convMu.Unlock()
		return nil, errConversationsStopped
	}
	if c := m.convCached; c != nil && m.convNow().Sub(c.at) < convReuse {
		m.convMu.Unlock()
		return c, nil
	}
	f := m.convFlight
	if f == nil {
		// Under convMu while convCtx is live: Stop cancels it under convMu
		// before it waits, so this Add never races that Wait.
		f = &convFlight{done: make(chan struct{})}
		m.convFlight = f
		m.convWG.Add(1)
		go m.runConversationFlight(f)
	}
	f.waiters++
	m.convMu.Unlock()

	select {
	case <-f.done:
		return f.snap, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// runConversationFlight takes one snapshot on the module's context, bounded
// by convFlightTimeout, and publishes it to its waiters. Only a success
// taken while the module runs is cached; a failure is not (the next request
// retries), and nothing that ends after Stop began is cached or served.
//
// "After Stop began" is decided under convMu, where Stop cancels convCtx: a
// check before taking the lock could pass, Stop cancel, and the snapshot
// still be published. Either the publish precedes Stop's cancel, or the
// snapshot is dropped.
func (m *Module) runConversationFlight(f *convFlight) {
	defer m.convWG.Done()
	ctx, cancel := context.WithTimeout(m.convCtx, convFlightTimeout)
	defer cancel()
	start := time.Now()
	snap, err := m.collectConversations(ctx)

	if m.convBeforePublish != nil {
		m.convBeforePublish()
	}
	m.convMu.Lock()
	if err == nil && m.convCtx.Err() != nil {
		snap, err = nil, errConversationsStopped
	}
	m.convFlight = nil
	if err == nil {
		m.convCached = snap
	}
	f.snap, f.err = snap, err
	close(f.done)
	m.convMu.Unlock()

	if err != nil {
		m.logf("nex: conversations: snapshot failed after %v: %v", time.Since(start).Round(time.Millisecond), err)
	}
}

// collectConversations is one snapshot: scan the root (the index is brought
// up to date), read the index, list every execution, list the live terminal
// frames, join. A failure of the scan's index, the index, the List or
// LiveSessions fails it; a root error does not (R-4-1, the result carries
// it). ctx is checked at every boundary. It runs on a goroutine of its own,
// so a panic is recovered into an error rather than taking the daemon down;
// the stack goes to the log only, never into the error a client sees.
func (m *Module) collectConversations(ctx context.Context) (snap *convSnapshot, err error) {
	defer func() {
		if r := recover(); r != nil {
			m.logf("nex: conversations: snapshot panicked: %v\n%s", r, debug.Stack())
			snap, err = nil, fmt.Errorf("panic: %v", r)
		}
	}()
	at := m.convNow()
	start := time.Now()
	scan, err := m.convScan(ctx, m.convRoot, m.convIdx, m.convNow)
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", m.convRoot, err)
	}
	m.logConversationScan(scan, time.Since(start))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := m.convIdx.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the conversation index: %w", err)
	}
	execs, err := m.listAllExecutions(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	terms, err := m.terminals.LiveSessions(ctx, "cc")
	if err != nil {
		return nil, fmt.Errorf("listing terminal sessions: %w", err)
	}
	res := buildConversations(conversationInputs{
		IndexRows: rows,
		Scan:      scan,
		Execs:     execs,
		Terminals: terms,
		IsRegular: m.convIsRegular,
		DirExists: m.convDirExists,
	})
	if res.UnknownOwner > 0 {
		m.logf("nex: conversations: unknown_owner=%d (held only by terminal frames whose start time cannot be read; listed in neither state)", res.UnknownOwner)
	}
	snap = &convSnapshot{at: at, scannedAt: scan.ScannedAt, res: res}
	if scan.RootErr != nil {
		snap.rootErr = scan.RootErr.Error()
	}
	return snap, nil
}

// logConversationScan is the one line per scan (the P4-1c acceptance reads
// it): files listed; reread, the files whose head or tail was read and whose
// row was written; bytes_read, every byte read, those of a read that failed
// midway included; the unreadable slug dirs; the scan's duration; and the
// root error when the root could not be listed.
func (m *Module) logConversationScan(r conversations.ScanResult, d time.Duration) {
	dirs := ""
	if len(r.UnreadableDirs) > 0 {
		dirs = fmt.Sprintf(" %q", r.UnreadableDirs)
	}
	rootErr := ""
	if r.RootErr != nil {
		rootErr = fmt.Sprintf(" root_error=%q", r.RootErr.Error())
	}
	m.logf("nex: conversations: scan root=%q files=%d reread=%d bytes_read=%d unreadable_dirs=%d%s duration=%v%s",
		m.convRoot, r.Files, r.Reread, r.BytesRead, len(r.UnreadableDirs), dirs, d.Round(time.Millisecond), rootErr)
}

// listAllExecutions pages every execution, archived included, to the end
// (§13.6: no page cap; one missing row could show a live worker's
// conversation as ended). Each page runs under detachedContext, bounded by
// engineOpTimeout; ctx is checked before each page. A cursor the store
// already gave fails the walk (R-4-2, fail closed): the walk cannot reach
// every execution, so the rows read so far are never returned. The error
// names the cursor; the failed flight logs it.
func (m *Module) listAllExecutions(ctx context.Context) ([]store.Execution, error) {
	var out []store.Execution
	cursor := ""
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pctx, cancel := detachedContext(ctx, m.engineOpTimeout)
		page, err := m.sys.store.List(pctx, store.ListOptions{IncludeArchived: true, Limit: ownerScanPageSize, Cursor: cursor})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("listing executions: %w", err)
		}
		out = append(out, page.Items...)
		next := page.NextCursor
		if next == "" {
			return out, nil
		}
		if seen[next] {
			return nil, fmt.Errorf("listing executions: repeated cursor %q after %d rows; the listing cannot see every execution", next, len(out))
		}
		seen[next] = true
		cursor = next
	}
}

// startConversationScan starts the schedule (Start): a snapshot at once,
// then one every convScanInterval, until Stop. A failed snapshot logs itself
// (runConversationFlight). Nothing starts while the listing is unavailable.
func (m *Module) startConversationScan() {
	if why := m.conversationsUnavailable(); why != "" {
		m.logf("nex: conversations: not scanning: %s", why)
		return
	}
	interval := convScanInterval
	m.convMu.Lock()
	ctx := m.convCtx
	if ctx == nil || ctx.Err() != nil {
		m.convMu.Unlock()
		return
	}
	m.convWG.Add(1)
	m.convMu.Unlock()
	go func() {
		defer m.convWG.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			_, _ = m.conversationSnapshot(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// stopConversations is Stop's step before the engine drains: cancel the
// scans' context (no snapshot starts; a running one stops at its next
// boundary), then wait for the schedule and any flight, bounded by ctx and
// convStopWait. A flight still running past that may reach a List against
// the closed engine; its error is discarded, never cached or served.
//
// The schedule goroutine exits as soon as the context is cancelled, so only
// a flight can outlive the bound; the line is logged only for one (a ctx
// that had already expired on entry must not report work that is not there).
func (m *Module) stopConversations(ctx context.Context) {
	m.convMu.Lock()
	if m.convCancel != nil {
		m.convCancel()
	}
	m.convMu.Unlock()

	done := make(chan struct{})
	go func() {
		m.convWG.Wait()
		close(done)
	}()
	limit := m.convStopCap
	if limit <= 0 {
		limit = convStopWait
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-done:
	case <-ctx.Done():
		if m.convFlightRunning() {
			m.logf("nex: stop: conversation scan still running (%v); not waiting for it", ctx.Err())
		}
	case <-timer.C:
		if m.convFlightRunning() {
			m.logf("nex: stop: conversation scan still running after %v; not waiting for it", limit)
		}
	}
}

// convFlightRunning: a snapshot is in progress.
func (m *Module) convFlightRunning() bool {
	m.convMu.Lock()
	defer m.convMu.Unlock()
	return m.convFlight != nil
}
