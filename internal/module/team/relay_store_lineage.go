package teammod

import (
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// The store is the team.LineageReader the module publishes (P5a-1b).
var _ team.LineageReader = (*Store)(nil)

// lineageRow is one session_lineage row.
type lineageRow struct {
	predecessorSessionID, predecessorRef string
}

// PreviousRefs implements team.LineageReader: for every session id that
// appears as the head of a lineage row, the predecessor refs walking back
// the whole chain, newest first, uncapped (spec §8.4). A cycle (impossible
// by construction, guarded anyway) stops the walk.
func (s *Store) PreviousRefs() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT session_id, predecessor_session_id, predecessor_ref FROM session_lineage`)
	if err != nil {
		return nil, fmt.Errorf("read lineage: %w", err)
	}
	defer rows.Close()
	back := map[string]lineageRow{}
	for rows.Next() {
		var sid string
		var lr lineageRow
		if err := rows.Scan(&sid, &lr.predecessorSessionID, &lr.predecessorRef); err != nil {
			return nil, fmt.Errorf("read lineage: %w", err)
		}
		back[sid] = lr
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read lineage: %w", err)
	}
	// Each session's chain is its predecessor's ref followed by the
	// predecessor's own chain, so chains are memoised: every row's
	// predecessor is looked up once. The output itself is Θ(sum of chain
	// lengths) — a single chain of N relays yields N chains of 1..N refs —
	// because the contract hands every head its whole chain (U3: uncapped)
	// without knowing which heads are live; N is the number of relays one
	// conversation has been through, tens at most, 7 bytes a ref. The
	// lineage is acyclic (checkLineage), and `visiting` makes a cycle in a
	// hand-edited database terminate instead of recursing forever.
	out := make(map[string][]string, len(back))
	visiting := map[string]bool{}
	var chain func(sid string) []string
	chain = func(sid string) []string {
		if refs, done := out[sid]; done {
			return refs
		}
		lr, ok := back[sid]
		if !ok || visiting[sid] {
			return nil
		}
		visiting[sid] = true
		refs := append([]string{lr.predecessorRef}, chain(lr.predecessorSessionID)...)
		visiting[sid] = false
		out[sid] = refs
		return refs
	}
	for head := range back {
		chain(head)
	}
	return out, nil
}

// ChainRoots maps every session id that appears in session_lineage (as a
// head or a predecessor) to the root of its chain — the one session with
// no predecessor. Two ops whose sessions share a root are in one chain.
func (s *Store) ChainRoots() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT session_id, predecessor_session_id FROM session_lineage`)
	if err != nil {
		return nil, fmt.Errorf("read lineage: %w", err)
	}
	defer rows.Close()
	pred := map[string]string{}
	for rows.Next() {
		var sid, p string
		if err := rows.Scan(&sid, &p); err != nil {
			return nil, fmt.Errorf("read lineage: %w", err)
		}
		pred[sid] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read lineage: %w", err)
	}
	roots := make(map[string]string, len(pred)*2)
	rootOf := func(sid string) string {
		root, _ := chainRoot(sid, func(s string) (string, bool, error) { p, ok := pred[s]; return p, ok, nil })
		return root
	}
	for sid, p := range pred {
		roots[sid] = rootOf(sid)
		roots[p] = rootOf(p)
	}
	return roots, nil
}
