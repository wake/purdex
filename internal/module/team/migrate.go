package teammod

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
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

// migrateTeamName gives teams the team's current name (team-name spec D-N5);
// a row written before it reads "" (no name).
func migrateTeamName(db *sql.DB) error {
	return ensureColumn(db, "teams", "team_name", "TEXT NOT NULL DEFAULT ''")
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
