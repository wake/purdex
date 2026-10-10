package teammod

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/wake/purdex/internal/team"
)

// RootsOutcome is what SetRoots did.
type RootsOutcome int

const (
	RootsSet      RootsOutcome = iota // written
	RootsNoTeam                       // no live team with that id (an ended one included)
	RootsStaleRev                     // if_rev is not the stored revision: nothing written
	RootsHeld                         // a removed root still has something under it: nothing written
)

// RootsResult is SetRoots' answer. Old and Rev are the grant's roots and revision as read under the write lock (Rev is the
// NEW revision after a RootsSet); InUse is set for RootsHeld.
type RootsResult struct {
	Outcome RootsOutcome
	Old     []string
	Rev     int
	InUse   []team.RootUse
}

// SetRoots replaces the roots of the live team teamID with roots (the caller made them canonical and collapsed) when ifRev is
// the stored roots_rev. One BEGIN IMMEDIATE transaction reads the grant, compares the revision (a stale one is answered
// before anything else), looks for what still has its cwd under a root being removed and writes roots, roots_canonical and
// the next revision together; a spawn op written after it is judged against the new roots in its own transaction
// (AcceptSpawnOp), so one that read the old grant cannot slip in under a removed root. hostID is this host's: only a
// member whose row names it has a local cwd.
func (s *Store) SetRoots(teamID string, ifRev int, roots []string, hostID string) (RootsResult, error) {
	var res RootsResult
	err := s.immediateTx(func(ctx context.Context, conn *sql.Conn) error {
		var raw string
		err := conn.QueryRowContext(ctx, `SELECT grant_json FROM teams WHERE id = ? AND ended_at = 0`, teamID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			res.Outcome = RootsNoTeam
			return nil
		}
		if err != nil {
			return err
		}
		var g team.Grant
		if err := json.Unmarshal([]byte(raw), &g); err != nil {
			return fmt.Errorf("grant of team %s: %w", teamID, err)
		}
		res.Old = g.Roots
		if ifRev != g.RootsRev {
			res.Outcome, res.Rev = RootsStaleRev, g.RootsRev
			return nil
		}
		if res.InUse, err = rootUses(ctx, conn, teamID, hostID, canonicalRoots(g.Roots), roots); err != nil {
			return err
		}
		if len(res.InUse) > 0 {
			res.Outcome = RootsHeld
			return nil
		}
		blob, err := json.Marshal(roots)
		if err != nil {
			return err
		}
		res.Rev = g.RootsRev + 1
		_, err = conn.ExecContext(ctx, `UPDATE teams SET grant_json = json_set(grant_json, '$.roots', json(?), '$.roots_canonical', json('true'), '$.roots_rev', ?)
			WHERE id = ? AND ended_at = 0`, string(blob), res.Rev, teamID)
		return err
	})
	if err != nil {
		return RootsResult{}, fmt.Errorf("set roots of team %s: %w", teamID, err)
	}
	return res, nil
}

// rootUses are the things of team teamID whose cwd is under a root of old that is not in next and under none of next:
// an active (or in-flight) member of this host, a spawn op still starting that has no member row yet, and an open adopt
// request for a session of this host. Read on the caller's write lock. A remote spawn or a member of another host has its
// cwd on that host and holds no local root.
func rootUses(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, teamID, hostID string, old, next []string) ([]team.RootUse, error) {
	removed := make([]string, 0, len(old))
	for _, r := range old {
		if !containsRoot(next, r) {
			removed = append(removed, r)
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	var uses []team.RootUse
	collect := func(kind, query string, args ...any) error {
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ref, cwd string
			if err := rows.Scan(&ref, &cwd); err != nil {
				return err
			}
			if cwd == "" {
				continue
			}
			// One resolution per row, used for every comparison. A cwd that cannot be resolved is judged as written against
			// the removed roots alone: a kept root cannot clear a path whose real place is unknown.
			real, ok := realCwd(cwd)
			if ok && cwdUnderAny(real, next) {
				continue
			}
			for _, r := range removed {
				if within(real, r) {
					uses = append(uses, team.RootUse{Root: r, Kind: kind, Ref: ref, Cwd: cwd})
					break
				}
			}
		}
		return rows.Err()
	}
	if err := collect(team.RootUseMember, `SELECT ref, cwd FROM team_members
		WHERE team_id = ? AND host_id = ? AND state IN ('active', 'joining', 'releasing', 'killing')`, teamID, hostID); err != nil {
		return nil, err
	}
	if err := collect(team.RootUseSpawn, `SELECT '', o.cwd FROM spawn_ops o WHERE o.team_id = ? AND o.state = 'running'
		AND NOT EXISTS (SELECT 1 FROM team_members m WHERE m.spawn_op = o.id AND m.state IN ('active', 'joining', 'releasing', 'killing'))`, teamID); err != nil {
		return nil, err
	}
	if err := collect(team.RootUseAdoption, `SELECT COALESCE(json_extract(payload_json, '$.target_ref'), ''), COALESCE(json_extract(payload_json, '$.target_cwd'), '')
		FROM approval_requests WHERE kind = ? AND state = 'open' AND json_extract(payload_json, '$.team_id') = ?
		AND COALESCE(json_extract(payload_json, '$.target_host_id'), '') = ''`, string(team.KindAdopt), teamID); err != nil {
		return nil, err
	}
	return uses, nil
}

func containsRoot(roots []string, r string) bool {
	for _, x := range roots {
		if x == r {
			return true
		}
	}
	return false
}

// realCwd is cwd with its symlinks evaluated; ok is false when it cannot be (gone, unreadable) and the cleaned path as
// written comes back.
func realCwd(cwd string) (string, bool) {
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		return real, true
	}
	return filepath.Clean(cwd), false
}

func cwdUnderAny(real string, roots []string) bool {
	for _, r := range roots {
		if within(real, r) {
			return true
		}
	}
	return false
}
