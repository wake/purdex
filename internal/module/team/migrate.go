package teammod

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// identPattern is a table or column name ensureColumn may interpolate.
var identPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// afterColumnCheck, when set, runs in ensureColumn between the check that
// found the column absent and its ALTER; tests race a second connection
// there. nil in production.
var afterColumnCheck func()

// ensureColumn adds column (declared decl) to table unless table_info
// already lists it (the pattern of internal/store/frames.go), so a column
// added after a table shipped reaches a team.db written before it. Table
// and column are interpolated, so each must be a plain identifier; decl is
// the caller's constant. Used by P4-6, P6-2a and P7-1.
//
// The check and the ALTER are two statements, so a second connection (two
// daemons opening one team.db) may add the column in between; this ALTER
// then fails "duplicate column". The schema is read again: the column there
// with decl's type is success (P4-6 review), anything else the ALTER's error.
func ensureColumn(db *sql.DB, table, column, decl string) error {
	if !identPattern.MatchString(table) || !identPattern.MatchString(column) || strings.TrimSpace(decl) == "" {
		return fmt.Errorf("ensure column %q.%q: not a plain identifier, or no declaration", table, column)
	}
	if _, found, err := columnType(db, table, column); err != nil || found {
		return err
	}
	if afterColumnCheck != nil {
		afterColumnCheck()
	}
	_, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + decl)
	if err == nil {
		return nil
	}
	if typ, found, qerr := columnType(db, table, column); qerr == nil && found && strings.EqualFold(typ, strings.Fields(decl)[0]) {
		return nil
	}
	return fmt.Errorf("add column %s.%s: %w", table, column, err)
}

// columnType is the declared type table_info lists for table.column.
func columnType(db *sql.DB, table, column string) (string, bool, error) {
	var typ string
	err := db.QueryRow(`SELECT type FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&typ)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("ensure column %s.%s: %w", table, column, err)
	}
	return typ, true, nil
}

// usageColumns are the persisted statusline reading (spec §8.5 "Persist it
// for teams only"): a member's on its row, a lead's on its team's row
// (prefixed lead_). usage_at = 0 means none was ever stored; usage_pct is
// NULL when Claude Code reported none.
var usageColumns = [][2]string{
	{"usage_pct", "REAL"},
	{"usage_window", "INTEGER NOT NULL DEFAULT 0"},
	{"usage_model", "TEXT NOT NULL DEFAULT ''"},
	{"usage_effort", "TEXT NOT NULL DEFAULT ''"},
	{"usage_at", "INTEGER NOT NULL DEFAULT 0"},
}

// migrateTeamLabel gives teams the team's short label (team-label spec D-L5):
// the final one, explicit or derived. A row written before it reads "".
func migrateTeamLabel(db *sql.DB) error {
	return ensureColumn(db, "teams", "team_label", "TEXT NOT NULL DEFAULT ''")
}

// migrateTeamName gives teams the team's current name (team-name spec D-N5);
// a row written before it reads "" (no name).
func migrateTeamName(db *sql.DB) error {
	return ensureColumn(db, "teams", "team_name", "TEXT NOT NULL DEFAULT ''")
}

// migrateSpawnTask gives spawn_ops the task a spawn creates with its member
// (plan T-2): a row written before it carries none.
func migrateSpawnTask(db *sql.DB) error {
	for _, c := range [][2]string{
		{"task_subject", "TEXT NOT NULL DEFAULT ''"},
		{"task_description", "TEXT NOT NULL DEFAULT ''"},
		{"task_done_json", "TEXT NOT NULL DEFAULT '[]'"},
	} {
		if err := ensureColumn(db, "spawn_ops", c[0], c[1]); err != nil {
			return err
		}
	}
	return nil
}

// migrateMemberLastTurn gives team_members the member's last turn (plan T-3a2): the
// summary and its (at, seq) stamp, written when the member has no in_progress task. A row
// written before it reads no turn. Never reset on a relay (resetMemberUsage leaves it).
func migrateMemberLastTurn(db *sql.DB) error {
	for _, c := range [][2]string{
		{"last_turn_summary", "TEXT NOT NULL DEFAULT ''"},
		{"last_turn_at", "INTEGER NOT NULL DEFAULT 0"},
		{"last_turn_seq", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := ensureColumn(db, "team_members", c[0], c[1]); err != nil {
			return err
		}
	}
	return nil
}

// migrateAdopt gives the store what adopt and release write (adopt plan PL-1b, 2026-10-09 alignment): the code a
// request closed cancelled with at approve, and on team_members how a member joined, when it left `active`, and
// the notice the session is owed (the outbox PL-1d1 drains). Every column has a default, so a row written before
// reads as it was (spawned, not ended, nothing owed) and an older daemon's INSERTs, which name their columns, still work.
func migrateAdopt(db *sql.DB) error {
	if err := ensureColumn(db, "approval_requests", "close_reason", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	for _, c := range [][2]string{
		{"origin", "TEXT NOT NULL DEFAULT 'spawned'"},
		{"ended_at", "INTEGER NOT NULL DEFAULT 0"},
		{"notice_pending", "TEXT NOT NULL DEFAULT ''"},
		{"notice_since", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := ensureColumn(db, "team_members", c[0], c[1]); err != nil {
			return err
		}
	}
	return nil
}

// afterReportsPKRead, when set, runs in migrateReportsPK between the unlocked
// check that found the old key and the write lock; tests let a second opener
// migrate there. nil in production.
var afterReportsPKRead func()

// reportsPK is the primary key columns of reports in key order, empty when the
// table does not exist.
func reportsPK(db interface {
	Query(query string, args ...any) (*sql.Rows, error)
}) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info('reports') WHERE pk > 0 ORDER BY pk`)
	if err != nil {
		return nil, fmt.Errorf("read reports key: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("read reports key: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// migrateReportsPK gives reports the primary key (team_id, member_key, id).
// alpha.608 created the table with (team_id, id), which CREATE TABLE IF NOT
// EXISTS leaves alone, and SQLite cannot change a key in place, so a table of
// any other key is rebuilt: copied into reports_new, dropped, renamed, its
// index recreated, all in ONE immediate transaction (a failure anywhere rolls
// back to the old table untouched). The key is read again under the write
// lock, so a second daemon opening the same team.db finds the work done and
// leaves it. A missing table is simply created.
func migrateReportsPK(db *sql.DB) error {
	pk, err := reportsPK(db)
	if err != nil {
		return err
	}
	if slices.Equal(pk, reportPKColumns) {
		return nil
	}
	if afterReportsPKRead != nil {
		afterReportsPKRead()
	}
	cols := reportCols // the same list in both tables, in this order
	return (&Store{db: db}).immediateTx(func(ctx context.Context, conn *sql.Conn) error {
		tx := connQuerier{ctx, conn}
		pk, err := reportsPK(tx)
		if err != nil {
			return err
		}
		switch {
		case slices.Equal(pk, reportPKColumns):
			return nil // somebody else did it
		case len(pk) == 0:
			_, err := conn.ExecContext(ctx, reportSchema)
			return wrapReportsMigration("create", err)
		}
		for _, step := range []struct{ what, sql string }{
			{"create reports_new", `CREATE TABLE reports_new (` + reportColumnsDDL + `)`},
			{"copy", `INSERT INTO reports_new (` + cols + `) SELECT ` + cols + ` FROM reports ORDER BY rowid`},
			{"drop", `DROP TABLE reports`},
			{"rename", `ALTER TABLE reports_new RENAME TO reports`},
			{"index", reportIndexDDL},
		} {
			if _, err := conn.ExecContext(ctx, step.sql); err != nil {
				return wrapReportsMigration(step.what, err)
			}
		}
		return nil
	})
}

func wrapReportsMigration(what string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("rebuild reports (%s): %w", what, err)
}

// connQuerier is a dedicated connection as the Query-only interface reportsPK
// takes, bound to its context.
type connQuerier struct {
	ctx  context.Context
	conn *sql.Conn
}

func (c connQuerier) Query(query string, args ...any) (*sql.Rows, error) {
	return c.conn.QueryContext(c.ctx, query, args...)
}

// migrateUsage gives team_members and teams their usage columns (P4-6).
func migrateUsage(db *sql.DB) error {
	for _, c := range usageColumns {
		if err := ensureColumn(db, "team_members", c[0], c[1]); err != nil {
			return err
		}
		if err := ensureColumn(db, "teams", "lead_"+c[0], c[1]); err != nil {
			return err
		}
	}
	return nil
}

// migrateRelayQuotaRev gives relay_quotas its row version (#2062 RQ-1a2). The table is deployed without it (RQ-1a,
// alpha.634), so it is a column migration: a row written before it reads rev 0.
func migrateRelayQuotaRev(db *sql.DB) error {
	return ensureColumn(db, "relay_quotas", "rev", "INTEGER NOT NULL DEFAULT 0")
}
