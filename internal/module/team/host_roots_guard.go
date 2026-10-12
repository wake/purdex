// internal/module/team/host_roots_guard.go — a removal of a directory on a member host is refused while a member of this lead runs under it,
// and a spawn / adopt under a directory whose removal is pending is refused (cross-host roots spec 2026-10-12 §3.2, XR-2b). Both sides take
// the writer lock FIRST, in their own write transaction, so SQLite serializes them: either the removal is refused (a member exists) or the
// spawn / adopt is (a removal is pending). Lexical, by path components: this host cannot resolve the member host's symlinks (the member
// host's own pre-check, XR-1, covers the other spellings).
package teammod

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/wake/purdex/internal/team"
)

var (
	// ErrNotARoot: a remove of a path that is neither a root the host last said it has nor the path of a pending add.
	ErrNotARoot = errors.New("the path is not a root of that host")
	// ErrRootBeingRemoved: a spawn / adopt whose cwd is under a directory whose removal is pending.
	ErrRootBeingRemoved = errors.New("the directory is being removed from that host's grant")
)

// RootsInUseError is a removal refused because members of this lead run under the directory.
type RootsInUseError struct{ Uses []team.HostRootUse }

func (e *RootsInUseError) Error() string {
	return fmt.Sprintf("%d use(s) under the directory", len(e.Uses))
}

// pathWithin says whether child is root or lies under it, by path components (never by string prefix: /w/ab is not under /w/a).
func pathWithin(child, root string) bool {
	if root == "" || child == "" {
		return false
	}
	if child == root {
		return true
	}
	if root == "/" {
		return strings.HasPrefix(child, "/")
	}
	return strings.HasPrefix(child, root+"/")
}

// keptRootsIn are the roots this lead believes the host keeps when `without` goes: the cache's roots and the paths of its pending adds,
// minus `without`. known is whether there is a cache; without one the answer is the stricter one (fewer kept roots, more uses).
func keptRootsIn(q dbtxq, hostID, without string) (kept []string, known bool, err error) {
	var raw string
	switch err := q.QueryRow(`SELECT roots_json FROM host_roots_seen WHERE host_id = ?`, hostID).Scan(&raw); {
	case err == nil:
		known = true
		roots, perr := parseRoots(raw)
		if perr != nil {
			return nil, false, perr
		}
		kept = append(kept, roots...)
	case isNoRows(err):
	default:
		return nil, false, err
	}
	rows, err := q.Query(`SELECT path FROM host_root_changes WHERE host_id = ? AND op = 'add' AND state = 'pending' ORDER BY rowid`, hostID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, false, err
		}
		kept = append(kept, p)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	kept = slices.DeleteFunc(kept, func(k string) bool { return k == without })
	return kept, known, nil
}

// underRemoval says whether cwd runs under `path` and not under another root that stays.
func underRemoval(cwd, path string, kept []string) bool {
	return pathWithin(cwd, path) && !slices.ContainsFunc(kept, func(k string) bool { return pathWithin(cwd, k) })
}

// hostUsesIn are the members of this lead on the host, and the forwarded spawns still starting there, that run under `path` and
// not under a kept root. Members: joining, active, releasing or killing, of a team that has not ended. Failed, released, killed and
// gone ones, an ended team's, and another host's are not counted.
func hostUsesIn(q dbtxq, hostID, path string, kept []string) ([]team.HostRootUse, error) {
	var uses []team.HostRootUse
	rows, err := q.Query(`SELECT m.ref, m.title, m.cwd FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.host_id = ? AND m.state IN ('joining', 'active', 'releasing', 'killing') AND t.ended_at = 0 ORDER BY m.created_at, m.spawn_op`, hostID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var u team.HostRootUse
		if err := rows.Scan(&u.Ref, &u.Title, &u.Cwd); err != nil {
			rows.Close()
			return nil, err
		}
		if underRemoval(u.Cwd, path, kept) {
			uses = append(uses, u)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = q.Query(`SELECT id, title, cwd FROM remote_spawns WHERE host_id = ? AND state = 'running' ORDER BY created_at, id`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u team.HostRootUse
		if err := rows.Scan(&u.OpID, &u.Title, &u.Cwd); err != nil {
			return nil, err
		}
		if underRemoval(u.Cwd, path, kept) {
			uses = append(uses, u)
		}
	}
	return uses, rows.Err()
}

// rootBeingRemovedIn says whether a pending remove of the host covers cwd (under the removed path, not under a root that stays). The
// caller holds the writer lock (its own write transaction, after its own first write).
func rootBeingRemovedIn(q dbtxq, hostID, cwd string) (bool, error) {
	if hostID == "" || cwd == "" {
		return false, nil
	}
	rows, err := q.Query(`SELECT path FROM host_root_changes WHERE host_id = ? AND op = 'remove' AND state = 'pending' ORDER BY rowid`, hostID)
	if err != nil {
		return false, err
	}
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return false, err
		}
		paths = append(paths, p)
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, p := range paths {
		kept, _, err := keptRootsIn(q, hostID, p)
		if err != nil {
			return false, err
		}
		if underRemoval(cwd, p, kept) {
			return true, nil
		}
	}
	return false, nil
}

// AddHostRootChange records a change in ONE write transaction that takes the writer lock first. An add is just inserted. A remove
// must name a root the host last said it has or the path of a pending add (else ErrNotARoot), and is refused with *RootsInUseError
// while a member of this lead runs under the path (nothing is written). The reconciler is woken by the caller.
func (s *Store) AddHostRootChange(c HostRootChange) error {
	fail := func(err error) error { return fmt.Errorf("add root change %s: %w", c.ID, err) }
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	// The writer lock first (a write that touches no row): the in-use look below and the insert are then one atomic step against every
	// spawn accept and adopt approve, which take the same lock before their own look at the pending removals.
	if _, err := tx.Exec(`UPDATE host_root_changes SET id = id WHERE 0`); err != nil {
		return fail(err)
	}
	if s.afterRootsLock != nil {
		s.afterRootsLock()
	}
	if c.Op == team.RootsOpRemove {
		known, err := rootOrPendingAddIn(tx, c.HostID, c.Path)
		if err != nil {
			return fail(err)
		}
		if !known {
			return ErrNotARoot
		}
		kept, _, err := keptRootsIn(tx, c.HostID, c.Path)
		if err != nil {
			return fail(err)
		}
		uses, err := hostUsesIn(tx, c.HostID, c.Path, kept)
		if err != nil {
			return fail(err)
		}
		if len(uses) > 0 {
			return &RootsInUseError{Uses: uses}
		}
	}
	if _, err := tx.Exec(`INSERT INTO host_root_changes (id, host_id, op, path, state, created_at, updated_at) VALUES (?, ?, ?, ?, 'pending', ?, ?)`,
		c.ID, c.HostID, c.Op, c.Path, c.CreatedAt, c.CreatedAt); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	return nil
}

// rootOrPendingAddIn: path is one of the roots the host last said it has, or the path of one of its pending adds.
func rootOrPendingAddIn(q dbtxq, hostID, path string) (bool, error) {
	var raw string
	switch err := q.QueryRow(`SELECT roots_json FROM host_roots_seen WHERE host_id = ?`, hostID).Scan(&raw); {
	case err == nil:
		roots, perr := parseRoots(raw)
		if perr != nil {
			return false, perr
		}
		if slices.Contains(roots, path) {
			return true, nil
		}
	case !isNoRows(err):
		return false, err
	}
	var one int
	switch err := q.QueryRow(`SELECT 1 FROM host_root_changes WHERE host_id = ? AND op = 'add' AND state = 'pending' AND path = ? LIMIT 1`, hostID, path).Scan(&one); {
	case err == nil:
		return true, nil
	case isNoRows(err):
		return false, nil
	default:
		return false, err
	}
}
