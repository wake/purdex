package teammod

import (
	"database/sql"
	"fmt"
	"regexp"
)

// identPattern is a table or column name ensureColumn may interpolate.
var identPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// ensureColumn adds column (declared decl) to table unless table_info
// already lists it (the pattern of internal/store/frames.go), so a column
// added after a table shipped reaches a team.db written before it. Table
// and column are interpolated, so each must be a plain identifier; decl is
// the caller's constant. Used by P4-6, P6-2a and P7-1.
func ensureColumn(db *sql.DB, table, column, decl string) error {
	if !identPattern.MatchString(table) || !identPattern.MatchString(column) {
		return fmt.Errorf("ensure column %q.%q: not a plain identifier", table, column)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n); err != nil {
		return fmt.Errorf("ensure column %s.%s: %w", table, column, err)
	}
	if n > 0 {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + decl); err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, column, err)
	}
	return nil
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
