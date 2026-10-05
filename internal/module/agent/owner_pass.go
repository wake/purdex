package agent

import (
	"context"
	"errors"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/module/session"
)

// takeProcSnapshotFn is the process source of a pass whose caller did not hand
// it one: one read of the whole process table, taken when the pass reaches the
// first pane it has to walk. It is a var only so tests can stand a fixture
// process tree in for the real table; production never changes it.
//
// The snapshot takes no context: on darwin it is a sysctl, and on Linux one
// `ps -A`, either of them milliseconds, against a deadline that is seconds.
var takeProcSnapshotFn ProcessSource = func() (agentpkg.ProcessView, error) {
	snap, err := agentpkg.SnapshotProcesses(context.Background())
	if err != nil {
		// Never return snap here: a nil *ProcessSnapshot in a ProcessView is
		// not a nil interface, and a caller checking the view would walk it.
		return nil, err
	}
	return snap, nil
}

// errNoProcessView is what the pass reports when a ProcessSource breaks its
// contract by returning neither a view nor an error.
var errNoProcessView = errors.New("process source returned no view")

// NewOwnerPass starts a pass that answers for many sessions with one process
// view and two pane listings (OwnerPass). src is the view the pass walks; nil
// means the pass takes its own through takeProcSnapshotFn.
func (m *Module) NewOwnerPass(src ProcessSource) OwnerPass {
	return &ownerPass{
		m:       m,
		src:     src,
		results: make(map[string]OwnerResult),
		pending: make(map[string][]paneCandidates),
	}
}

// ownerPass is the OwnerPass behind NewOwnerPass. Everything it fetches is
// fetched lazily and at most once: the frames store and the first pane listing
// on the first Resolve that needs them, the process view on the first pane it
// walks, the second listing in Confirm and only when there is a candidate to
// confirm. A failure of any of them is kept and is the answer for every
// session that needed it; nothing is fetched twice to retry.
//
// Why the membership is read twice. The first listing decides which panes a
// session has, and the walk that follows is the slow part of the pass. A
// `join-pane` inside that window moves a pane — and whatever agent is now
// running in it — into a DIFFERENT session. Neither generation sample the
// provenance handler takes notices: both sessions live on the same tmux
// server, so the stamp is identical on both sides and the answer would be
// reported as trustworthy. So a pane's owners are only candidates until the
// second listing, taken after every walk, places the pane in the same session
// again; an answer that can no longer be confirmed as that session's is
// dropped rather than reported. One listing re-checks every candidate of
// every session, where a re-read per pane would cost one tmux round trip each.
//
// Why session IDs, never names. Each pane is matched to a session by tmux
// session ID, encoded with session.EncodeSessionID. m.resolvePaneSession looks
// like the right tool and must not be used: it goes through LookupCodeByName,
// whose cache is deliberately stale for up to 250 ms after an external
// mutation. That is fine on the hook hot path it was built for, and wrong
// here — rename session1 away and session2 into its name inside that window
// and a query for session1's code can be answered with session2's agent, with
// the generation stamp matching and the pane-tree check passing too. A session
// ID is immutable for the life of the session and EncodeSessionID is a pure
// function of it, so this path has no such window. It is the same reason
// handler.go prefers TmuxSessionID over the name whenever a hook carries one.
type ownerPass struct {
	m   *Module
	src ProcessSource

	enumerated bool
	enumErr    error
	// panes are the framed panes of each session, by session code, as the
	// first listing placed them, in frames-store (pane id) order.
	panes map[string][]passPane

	viewTaken bool
	view      agentpkg.ProcessView
	viewErr   error

	// results holds every final answer; pending the sessions whose answer
	// waits on Confirm's re-check.
	results map[string]OwnerResult
	pending map[string][]paneCandidates
}

// passPane is one framed pane and its own process, as the first listing gave
// them.
type passPane struct {
	id  string
	pid int
}

// paneCandidates are the root frames one pane's walk produced, before the
// second listing has confirmed the pane is still the session's.
type paneCandidates struct {
	paneID string
	owners []PaneOwner
}

// Resolve walks the panes of the session behind code and records the outcome,
// final or pending (see OwnerPass).
//
// An expired context is "no answer" wherever it is noticed, never the owners
// found so far, and is read once more after the last pane: a pane with no
// owners, or a session with no panes at all, skips every check inside the walk,
// so the loop can finish in the same instant the deadline does and would
// otherwise report "no owner" (found=false, err=nil) for a walk that in fact
// ran out of time.
//
// A non-nil error from resolvePaneOwners discards the owners it returned
// alongside it, and the session's whole answer with them: a partial walk is
// not an answer, and half a pane's frames can name a root that the rest of
// the walk would have rejected.
func (p *ownerPass) Resolve(ctx context.Context, code string) {
	if _, done := p.results[code]; done {
		return
	}
	if _, waiting := p.pending[code]; waiting {
		return
	}
	if p.m.frames == nil || p.m.tmux == nil || code == "" {
		p.results[code] = OwnerResult{}
		return
	}
	// A request that is already out of time spends nothing, not even the
	// first listing.
	if err := ctx.Err(); err != nil {
		p.results[code] = OwnerResult{Err: err}
		return
	}
	if err := p.enumerate(ctx); err != nil {
		p.results[code] = OwnerResult{Err: err}
		return
	}

	var candidates []paneCandidates
	for _, pane := range p.panes[code] {
		if err := ctx.Err(); err != nil {
			p.results[code] = OwnerResult{Err: err}
			return
		}
		view, err := p.processView()
		if err != nil {
			p.results[code] = OwnerResult{Err: err}
			return
		}
		owners, err := p.m.resolvePaneOwners(ctx, pane.id, pane.pid, view)
		if err != nil {
			p.results[code] = OwnerResult{Err: err}
			return
		}
		if len(owners) > 0 {
			candidates = append(candidates, paneCandidates{paneID: pane.id, owners: owners})
		}
	}
	if err := ctx.Err(); err != nil {
		p.results[code] = OwnerResult{Err: err}
		return
	}
	// A session whose panes produced no owner has nothing for the re-check to
	// confirm, so "no owner" is final here (spec D6). Only candidates wait.
	if len(candidates) == 0 {
		p.results[code] = OwnerResult{}
		return
	}
	p.pending[code] = candidates
}

// Confirm re-checks every pending session's candidate panes with ONE listing
// and returns the pass's answers: every session Resolve was asked about, by
// code.
//
// The listing must have completed, and completed in time, to confirm anything.
// A listing that failed as a whole checked nothing, so every pending session
// reports its error (spec D5), never "no owner" (#988). And the clock is read
// once more after the listing returns, because it is the last thing done before
// an owner is adopted: a tmux round trip that completes as its context expires
// returns a perfectly good answer with no error (there is nothing left for
// CommandContext to kill), and adopting it would answer a request that is
// already out of time — exactly what the deadline is for. Either way the pending
// sessions are "no answer", not their candidates: a candidate is never an answer
// without a re-check that completed in time.
//
// Anything short of a confirmed match drops the pane: absent from the listing,
// a session ID that cannot be encoded, or one that encodes to another code are
// all "not confirmed as this session's", and none of them may be answered with.
// A pane merely absent from a successful listing is dropped, not failed: the
// listing is complete (tmux.Executor.ListAllPanes), so absent means gone.
func (p *ownerPass) Confirm(ctx context.Context) map[string]OwnerResult {
	if len(p.pending) == 0 {
		return p.results
	}
	listing, err := p.m.tmux.ListAllPanes(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		for code := range p.pending {
			p.results[code] = OwnerResult{Err: err}
		}
		p.pending = map[string][]paneCandidates{}
		return p.results
	}

	sessionOf := make(map[string]string, len(listing))
	for _, row := range listing {
		if code, err := session.EncodeSessionID(row.SessionID); err == nil {
			sessionOf[row.PaneID] = code
		}
	}
	for code, candidates := range p.pending {
		var best PaneOwner
		found := false
		for _, c := range candidates {
			if sessionOf[c.paneID] != code {
				continue
			}
			for _, owner := range c.owners {
				// The session-id filter lives HERE, not in resolvePaneOwners: a
				// root that never reported an identity is still a root, it just
				// cannot answer this question.
				if owner.SessionID == "" {
					continue
				}
				if !found || betterOwner(owner, best) {
					best, found = owner, true
				}
			}
		}
		p.results[code] = OwnerResult{Owner: best, Found: found}
	}
	p.pending = map[string][]paneCandidates{}
	return p.results
}

// enumerate decides, once per pass, which framed panes each session has. It
// starts from the frames because a pane with no frame has no agent to report,
// so with no frames at all it stops before asking tmux anything, and the pass
// costs no listing and no process view.
//
// A framed pane the listing does not place, places in a session whose ID does
// not encode, or places with a PID that does not parse is excluded, with no
// fallback to a name lookup: with no session there is no question to answer
// for it, and with no pane PID there is nothing to check a chain against.
// That is not an error — the session's other panes may still have answers.
//
// The listing runs under the caller's deadline like everything else: a tmux
// server that has stopped answering must not hold the request open past it.
// A listing that fails as a whole, the deadline included, means no session's
// panes are known, and every session reports it (spec D5).
func (p *ownerPass) enumerate(ctx context.Context) error {
	if p.enumerated {
		return p.enumErr
	}
	p.enumerated = true
	p.panes = make(map[string][]passPane)

	frames, err := p.m.frames.ListAll()
	if err != nil {
		p.enumErr = err
		return err
	}
	if len(frames) == 0 {
		return nil
	}
	listing, err := p.m.tmux.ListAllPanes(ctx)
	if err != nil {
		p.enumErr = err
		return err
	}
	byPane := make(map[string]passPane, len(listing))
	codeOfPane := make(map[string]string, len(listing))
	for _, row := range listing {
		code, err := session.EncodeSessionID(row.SessionID)
		if err != nil {
			continue
		}
		pid, err := parsePanePID(row.PanePID)
		if err != nil {
			continue
		}
		byPane[row.PaneID] = passPane{id: row.PaneID, pid: pid}
		codeOfPane[row.PaneID] = code
	}
	seen := make(map[string]bool, len(frames))
	for _, frame := range frames {
		if seen[frame.PaneID] {
			continue
		}
		seen[frame.PaneID] = true
		pane, ok := byPane[frame.PaneID]
		if !ok {
			continue
		}
		code := codeOfPane[frame.PaneID]
		p.panes[code] = append(p.panes[code], pane)
	}
	return nil
}

// processView returns the pass's one process view, calling the source the
// first time and never again: a failure is the answer for every session that
// needs a view, and is not retried (ProcessSource).
func (p *ownerPass) processView() (agentpkg.ProcessView, error) {
	if p.viewTaken {
		return p.view, p.viewErr
	}
	p.viewTaken = true
	src := p.src
	if src == nil {
		src = takeProcSnapshotFn
	}
	p.view, p.viewErr = src()
	if p.viewErr == nil && p.view == nil {
		p.viewErr = errNoProcessView
	}
	return p.view, p.viewErr
}
