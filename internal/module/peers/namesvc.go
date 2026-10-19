package peers

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// The name service (ipeers.NameService, name pool v2 plan §1.3): the team module's door to the lead name card, the rename at a
// lead's approval with its undo, and the name history. The reservations of a member's name join it in NP-4.

var _ ipeers.NameService = (*Module)(nil)

// NameHistoryWriter is the write side of the name history (store.NameHistoryStore).
type NameHistoryWriter interface {
	RecordLead(ctx context.Context, code string, nowMs int64) error
	RecordPair(ctx context.Context, leadCode, memberCode string, nowMs int64) error
}

var errNoNameStore = errors.New("peers: names are not enabled")

// leadRenameLedger remembers, per session, the name a RenameForLead set, so the undo can be a compare-and-set on exactly that
// name. It is in memory: a daemon that restarted between the rename and its undo has nothing to undo, which is harmless (spec
// §5.3: on a retry, keeping the current name is the new name).
type leadRenameLedger struct {
	mu sync.Mutex
	to map[string]string // session id (lowercase) → the name the rename set
}

func (l *leadRenameLedger) set(sid, name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.to == nil {
		l.to = map[string]string{}
	}
	l.to[sid] = name
}

func (l *leadRenameLedger) get(sid string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n, ok := l.to[sid]
	return n, ok
}

func (l *leadRenameLedger) drop(sid string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.to, sid)
}

func nameSID(sessionID string) string { return strings.ToLower(strings.TrimSpace(sessionID)) }

// nameRow is the stored name of a session; ok is false when it has none.
func (m *Module) nameRow(ctx context.Context, sessionID string) (store.PeerNameEntry, bool, error) {
	if m.peerNames == nil {
		return store.PeerNameEntry{}, false, errNoNameStore
	}
	rows, err := m.peerNames.Lookup(ctx, []string{sessionID})
	if err != nil {
		return store.PeerNameEntry{}, false, err
	}
	r, ok := rows[nameSID(sessionID)]
	return r, ok, nil
}

// CodeOf is the persona code a session's name ends in.
func (m *Module) CodeOf(ctx context.Context, sessionID string) (string, bool) {
	row, ok, err := m.nameRow(ctx, sessionID)
	if err != nil || !ok {
		return "", false
	}
	return ipeers.PoolNameOf(row.Name)
}

// isPoolCode says whether code is a recognised pool name (a persona code, or a built-in name while there are none).
func isPoolCode(code string) bool {
	c, ok := ipeers.PoolNameOf("x-" + code)
	return ok && c == code
}

// LeadCandidates is rule 1 for the session.
func (m *Module) LeadCandidates(ctx context.Context, sessionID string) ([]ipeers.LeadCandidate, error) {
	row, _, err := m.nameRow(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	keep, _ := ipeers.PoolNameOf(row.Name)
	if err := m.awaitPoolReady(ctx); err != nil {
		return nil, err
	}
	unlock, err := m.lockPool(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	held, err := m.heldPoolNames(ctx, map[string]bool{})
	if err != nil {
		return nil, err
	}
	var out []ipeers.LeadCandidate
	for _, c := range leadCandidates(m.inputFor(ctx, held), keep) {
		out = append(out, ipeers.LeadCandidate{Code: c, Keep: c == keep})
	}
	return out, nil
}

// RenameForLead renames the session to <base>-<code>; see ipeers.NameService.
func (m *Module) RenameForLead(ctx context.Context, sessionID, code string) (string, error) {
	row, ok, err := m.nameRow(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ipeers.ErrNameLost
	}
	cur, curIsPool := ipeers.PoolNameOf(row.Name)
	if curIsPool && cur == code {
		return row.Name, nil
	}
	// the base is the name without its suffix: the pool name, or the two ref characters
	base := row.Name
	if curIsPool {
		base = strings.TrimSuffix(row.Name, "-"+cur)
	} else if ref := ipeers.RefID(sessionID); ipeers.IsRef(ref) {
		base = strings.TrimSuffix(row.Name, "-"+ref[1:3])
	}
	newName, ok := ipeers.VirtualNameWith(base, code)
	if !ok {
		return "", ipeers.ErrNameUnknown
	}
	if err := m.awaitPoolReady(ctx); err != nil {
		return "", err
	}
	unlock, err := m.lockPool(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	held, err := m.heldPoolNames(ctx, map[string]bool{})
	if err != nil {
		return "", err
	}
	if held[code] {
		return "", ipeers.ErrNameTaken
	}
	if m.beforeLeadRename != nil {
		m.beforeLeadRename()
	}
	// the code must still be pickable at the moment of the rename: a collection that retires it meanwhile waits for the read
	// lock (bindPersonas holds the write side), so the check and the write see one generation
	m.poolGen.RLock()
	defer m.poolGen.RUnlock()
	if !slices.Contains(ipeers.PickablePool(), code) {
		return "", ipeers.ErrNameUnknown
	}
	_, won, err := m.peerNames.RenamePool(ctx, sessionID, row.Name, newName)
	if err != nil {
		return "", err
	}
	if !won {
		return "", ipeers.ErrNameLost
	}
	m.leadRenames.set(nameSID(sessionID), newName)
	return row.Name, nil
}

// UndoRenameForLead reverses the last RenameForLead of the session; see ipeers.NameService.
func (m *Module) UndoRenameForLead(ctx context.Context, sessionID, oldName string) error {
	sid := nameSID(sessionID)
	to, ok := m.leadRenames.get(sid)
	if !ok {
		m.logf("peers: name: no rename of %s to undo", sessionID)
		return nil
	}
	if m.peerNames == nil {
		return errNoNameStore
	}
	unlock, err := m.lockPool(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = m.peerNames.UndoRename(ctx, sessionID, to, oldName)
	switch {
	case errors.Is(err, store.ErrOldNameReclaimed):
		return ipeers.ErrNameTaken
	case err != nil:
		return err // the record stays, so the undo can be tried again
	}
	m.leadRenames.drop(sid) // undone, or a later rename won: either way this record is spent
	return nil
}

// RecordLead writes the lead-history row of the session when its name is a persona code.
func (m *Module) RecordLead(ctx context.Context, sessionID string) error {
	code, ok := m.CodeOf(ctx, sessionID)
	if !ok || m.nameWriter == nil {
		return nil
	}
	return m.nameWriter.RecordLead(ctx, code, m.now().UnixMilli())
}

// RecordPairCodes writes one lead/member pair by code.
func (m *Module) RecordPairCodes(ctx context.Context, leadCode, memberCode string) error {
	if m.nameWriter == nil || !isPoolCode(leadCode) || !isPoolCode(memberCode) {
		return nil
	}
	return m.nameWriter.RecordPair(ctx, leadCode, memberCode, m.now().UnixMilli())
}
